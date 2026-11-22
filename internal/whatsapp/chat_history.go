package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const (
	recentHistoryConversationCount = 20
	recentHistoryMessagesPerChat  = 50
)

type ChatMessage struct {
	ID            string `json:"id"`
	ChatJID       string `json:"chatJid"`
	SenderJID     string `json:"senderJid"`
	FromMe        bool   `json:"fromMe"`
	Timestamp     string `json:"timestamp"`
	TimestampMS   int64  `json:"timestampMs"`
	Type          string `json:"type"`
	Text          string `json:"text,omitempty"`
}

func openChatHistoryStore(databasePath string) (*sql.DB, error) {
	dsn := "file:" + databasePath + "?_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open chat history store: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chat_messages (
			chat_jid TEXT NOT NULL,
			phone_number TEXT NOT NULL,
			message_id TEXT NOT NULL,
			sender_jid TEXT NOT NULL,
			from_me INTEGER NOT NULL,
			timestamp_ms INTEGER NOT NULL,
			message_type TEXT NOT NULL,
			text TEXT NOT NULL,
			PRIMARY KEY (chat_jid, message_id)
		);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize chat history store: %w", err)
	}
	if _, err = db.Exec(`CREATE INDEX IF NOT EXISTS chat_messages_phone_time ON chat_messages (phone_number, timestamp_ms DESC)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to index chat history store: %w", err)
	}
	return db, nil
}

func (s *Service) attachChatHistoryHandler() {
	s.Client.AddEventHandler(func(evt any) {
		switch event := evt.(type) {
		case *events.Message:
			if err := s.storeChatMessage(event.Info, event.Message); err != nil {
				fmt.Printf("WhatsApp session %s failed to store chat message %s: %v\n", s.id, event.Info.ID, err)
			}
		case *events.HistorySync:
			if event.Data == nil {
				fmt.Printf("WhatsApp session %s received an empty history sync event\n", s.id)
				return
			}
			messageCount := 0
			for _, conversation := range event.Data.GetConversations() {
				messageCount += len(conversation.GetMessages())
			}
			fmt.Printf(
				"WhatsApp session %s received history sync: type=%s conversations=%d messages=%d chunk=%d progress=%d\n",
				s.id,
				event.Data.GetSyncType().String(),
				len(event.Data.GetConversations()),
				messageCount,
				event.Data.GetChunkOrder(),
				event.Data.GetProgress(),
			)
			if err := s.storeHistorySync(event.Data); err != nil {
				fmt.Printf("WhatsApp session %s failed to store history sync: %v\n", s.id, err)
			} else {
				fmt.Printf("WhatsApp session %s stored history sync with %d messages\n", s.id, messageCount)
			}
		}
	})
}

func (s *Service) storeHistorySync(history *waHistorySync.HistorySync) error {
	if history == nil {
		return nil
	}
	phoneByLID := make(map[string]string, len(history.GetPhoneNumberToLidMappings()))
	for _, mapping := range history.GetPhoneNumberToLidMappings() {
		if mapping == nil {
			continue
		}
		pn, err := types.ParseJID(mapping.GetPnJID())
		if err != nil {
			return fmt.Errorf("parse phone number JID %q: %w", mapping.GetPnJID(), err)
		}
		lid, err := types.ParseJID(mapping.GetLidJID())
		if err != nil {
			return fmt.Errorf("parse LID JID %q: %w", mapping.GetLidJID(), err)
		}
		phoneByLID[lid.String()] = pn.User
	}
	for _, conversation := range history.GetConversations() {
		chatJID, err := types.ParseJID(conversation.GetID())
		if err != nil {
			return fmt.Errorf("parse history conversation JID %q: %w", conversation.GetID(), err)
		}
		phoneNumber := conversationPhoneNumber(conversation, chatJID, phoneByLID)
		if phoneNumber == "" {
			continue
		}
		for _, historyMessage := range conversation.GetMessages() {
			if historyMessage == nil || historyMessage.GetMessage() == nil {
				continue
			}
			event, err := s.Client.ParseWebMessage(chatJID, historyMessage.GetMessage())
			if err != nil {
				return fmt.Errorf("parse historical message in %s: %w", chatJID, err)
			}
			if err := s.storeChatMessageForPhone(event.Info, event.Message, phoneNumber); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) storeChatMessage(info types.MessageInfo, message *waE2E.Message) error {
	return s.storeChatMessageForPhone(info, message, "")
}

func (s *Service) storeChatMessageForPhone(info types.MessageInfo, message *waE2E.Message, phoneOverride string) error {
	if s.historyDB == nil || message == nil || info.ID == "" || info.IsGroup {
		return nil
	}
	phoneNumber := phoneOverride
	if phoneNumber == "" {
		switch info.Chat.Server {
		case types.DefaultUserServer, types.LegacyUserServer:
			phoneNumber = info.Chat.User
		case types.HiddenUserServer:
			pn, err := s.Client.Store.LIDs.GetPNForLID(s.ctx, info.Chat)
			if err != nil {
				phoneNumber = info.Chat.User
			} else {
				phoneNumber = pn.User
			}
		default:
			if info.IsFromMe {
				phoneNumber = info.RecipientAlt.User
			} else {
				phoneNumber = info.SenderAlt.User
			}
		}
	}
	phoneNumber = strings.TrimPrefix(phoneNumber, "+")
	if phoneNumber == "" {
		return nil
	}
	fromMe := 0
	if info.IsFromMe {
		fromMe = 1
	}

	_, err := s.historyDB.ExecContext(s.ctx, `
		INSERT INTO chat_messages (
			chat_jid, phone_number, message_id, sender_jid, from_me,
			timestamp_ms, message_type, text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_jid, message_id) DO UPDATE SET
			phone_number = excluded.phone_number,
			sender_jid = excluded.sender_jid,
			from_me = excluded.from_me,
			timestamp_ms = excluded.timestamp_ms,
			message_type = excluded.message_type,
			text = excluded.text
	`, info.Chat.String(), phoneNumber, info.ID, info.Sender.String(), fromMe,
		info.Timestamp.UnixMilli(), chatMessageType(info.Type, message), chatMessageText(message))
	if err != nil {
		return fmt.Errorf("insert chat message: %w", err)
	}
	return nil
}

func conversationPhoneNumber(conversation *waHistorySync.Conversation, chatJID types.JID, phoneByLID map[string]string) string {
	if conversation.GetPnJID() != "" {
		pn, err := types.ParseJID(conversation.GetPnJID())
		if err == nil {
			return pn.User
		}
	}
	if chatJID.Server == types.DefaultUserServer || chatJID.Server == types.LegacyUserServer {
		return chatJID.User
	}
	if phone := phoneByLID[chatJID.String()]; phone != "" {
		return phone
	}
	if conversation.GetLidJID() != "" {
		if phone := phoneByLID[conversation.GetLidJID()]; phone != "" {
			return phone
		}
	}
	return ""
}

func chatMessageType(messageType string, message *waE2E.Message) string {
	if messageType != "" {
		return messageType
	}
	switch {
	case message.GetConversation() != "":
		return "text"
	case message.GetExtendedTextMessage() != nil:
		return "text"
	case message.GetImageMessage() != nil:
		return "image"
	case message.GetVideoMessage() != nil:
		return "video"
	case message.GetAudioMessage() != nil:
		return "audio"
	case message.GetDocumentMessage() != nil:
		return "document"
	case message.GetStickerMessage() != nil:
		return "sticker"
	case message.GetLocationMessage() != nil:
		return "location"
	case message.GetContactMessage() != nil:
		return "contact"
	default:
		return "other"
	}
}

func chatMessageText(message *waE2E.Message) string {
	if text := message.GetConversation(); text != "" {
		return text
	}
	if text := message.GetExtendedTextMessage().GetText(); text != "" {
		return text
	}
	if text := message.GetImageMessage().GetCaption(); text != "" {
		return text
	}
	if text := message.GetVideoMessage().GetCaption(); text != "" {
		return text
	}
	if text := message.GetDocumentMessage().GetCaption(); text != "" {
		return text
	}
	if text := message.GetDocumentWithCaptionMessage().GetMessage().GetDocumentMessage().GetCaption(); text != "" {
		return text
	}
	return message.GetButtonsResponseMessage().GetSelectedDisplayText()
}

func (m *Manager) GetChatHistory(ctx context.Context, id, phoneNumber string, limit int, before int64, beforeID string) ([]ChatMessage, bool, error) {
	if !ValidSessionID(id) {
		return nil, false, fmt.Errorf("id must be a UUID")
	}
	id = strings.ToLower(id)

	sessionDSN, err := makeSessionDSN(m.baseDSN, id)
	if err != nil {
		return nil, false, err
	}
	if _, err := os.Stat(sqlitePath(sessionDSN)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, ErrSessionNotFound
		}
		return nil, false, fmt.Errorf("check WhatsApp session database: %w", err)
	}
	historyDB, err := openChatHistoryStore(sqlitePath(sessionDSN) + "-history.db")
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if err := historyDB.Close(); err != nil {
			fmt.Printf("Failed to close chat history query store for session %s: %v\n", id, err)
		}
	}()

	query := `
		SELECT chat_jid, message_id, sender_jid, from_me, timestamp_ms, message_type, text
		FROM chat_messages
		WHERE phone_number = ?
	`
	args := []any{phoneNumber}
	if before > 0 && beforeID != "" {
		query += " AND (timestamp_ms < ? OR (timestamp_ms = ? AND message_id < ?))"
		args = append(args, before, before, beforeID)
	} else if before > 0 {
		query += " AND timestamp_ms < ?"
		args = append(args, before)
	}
	query += " ORDER BY timestamp_ms DESC, message_id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := historyDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query chat history: %w", err)
	}
	defer rows.Close()

	messages := make([]ChatMessage, 0, limit)
	for rows.Next() {
		var message ChatMessage
		var fromMe int
		var timestamp int64
		if err := rows.Scan(&message.ChatJID, &message.ID, &message.SenderJID, &fromMe,
			&timestamp, &message.Type, &message.Text); err != nil {
			return nil, false, fmt.Errorf("read chat history: %w", err)
		}
		message.FromMe = fromMe != 0
		message.TimestampMS = timestamp
		message.Timestamp = time.UnixMilli(timestamp).UTC().Format(time.RFC3339Nano)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate chat history: %w", err)
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	return messages, hasMore, nil
}

func (m *Manager) RequestRecentChatHistory(ctx context.Context, id string) error {
	if !ValidSessionID(id) {
		return fmt.Errorf("id must be a UUID")
	}
	id = strings.ToLower(id)

	m.mu.RLock()
	service, exists := m.sessions[id]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return ErrSessionNotFound
	}
	if !service.Client.IsConnected() || !service.Client.IsLoggedIn() {
		return ErrSessionNotConnected
	}

	request := &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_MESSAGE.Enum(),
			PeerDataOperationRequestMessage: &waE2E.PeerDataOperationRequestMessage{
				PeerDataOperationRequestType: waE2E.PeerDataOperationRequestType_RECENT_CONVERSATIONS_ON_DEMAND_REQUEST.Enum(),
				RecentConversationsOnDemandRequest: &waE2E.PeerDataOperationRequestMessage_RecentConversationsOnDemandRequest{
					ConversationCount:       new(uint32(recentHistoryConversationCount)),
					MessagesPerConversation: new(uint32(recentHistoryMessagesPerChat)),
				},
			},
		},
	}
	if _, err := service.Client.SendPeerMessage(ctx, request); err != nil {
		return fmt.Errorf("send recent chat history request to linked phone: %w", err)
	}
	fmt.Printf("WhatsApp session %s sent recent history request: conversations=%d messagesPerConversation=%d\n",
		id, recentHistoryConversationCount, recentHistoryMessagesPerChat)
	return nil
}

func (m *Manager) RequestUserChatHistory(ctx context.Context, id, phoneNumber string, count int) error {
	if !ValidSessionID(id) {
		return fmt.Errorf("id must be a UUID")
	}
	if count < 1 || count > 50 {
		return fmt.Errorf("message count must be between 1 and 50")
	}
	id = strings.ToLower(id)

	m.mu.RLock()
	service, exists := m.sessions[id]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return ErrSessionNotFound
	}
	if !service.Client.IsConnected() || !service.Client.IsLoggedIn() {
		return ErrSessionNotConnected
	}

	service.syncMu.Lock()
	defer service.syncMu.Unlock()

	sessionDSN, err := makeSessionDSN(m.baseDSN, id)
	if err != nil {
		return err
	}
	historyDB, err := openChatHistoryStore(sqlitePath(sessionDSN) + "-history.db")
	if err != nil {
		return err
	}
	var chatJIDString, messageID string
	var fromMe int
	var timestamp int64
	err = historyDB.QueryRowContext(ctx, `
		SELECT chat_jid, message_id, from_me, timestamp_ms
		FROM chat_messages
		WHERE phone_number = ?
		ORDER BY timestamp_ms ASC, message_id ASC
		LIMIT 1
	`, phoneNumber).Scan(&chatJIDString, &messageID, &fromMe, &timestamp)
	closeErr := historyDB.Close()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrChatHistoryCursorNotFound
		}
		return fmt.Errorf("find oldest stored message for number: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close chat history store: %w", closeErr)
	}

	chatJID, err := types.ParseJID(chatJIDString)
	if err != nil {
		return fmt.Errorf("parse stored chat JID: %w", err)
	}
	lastMessage := &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chatJID,
			IsFromMe: fromMe != 0,
		},
		ID:        messageID,
		Timestamp: time.UnixMilli(timestamp),
	}
	request := service.Client.BuildHistorySyncRequest(lastMessage, count)
	if _, err := service.Client.SendPeerMessage(ctx, request); err != nil {
		return fmt.Errorf("send per-chat history request to linked phone: %w", err)
	}
	return nil
}
