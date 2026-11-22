package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

const maxDocumentSize = 64 << 20

var ErrInvalidDocument = errors.New("invalid document")

type SendDocumentMessageResult struct {
	ID            string `json:"messageId"`
	Recipient     string `json:"recipient"`
	Timestamp     string `json:"timestamp"`
	HistoryStored bool   `json:"historyStored"`
}

func (m *Manager) SendDocumentMessage(ctx context.Context, id, phoneNumber, caption, fileName string, data []byte) (SendDocumentMessageResult, error) {
	if !ValidSessionID(id) {
		return SendDocumentMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if len(phoneNumber) < 10 || len(phoneNumber) > 15 || !allPhoneNumberDigits(phoneNumber) {
		return SendDocumentMessageResult{}, errors.New("mobileNumber must contain 10 to 15 digits")
	}
	if len(data) == 0 || len(data) > maxDocumentSize {
		return SendDocumentMessageResult{}, fmt.Errorf("%w: document must be between 1 byte and 64 MiB", ErrInvalidDocument)
	}
	fileName = normalizedUploadName(strings.TrimSpace(fileName))
	if fileName == "" || fileName == "." {
		return SendDocumentMessageResult{}, fmt.Errorf("%w: a valid filename is required", ErrInvalidDocument)
	}
	if len([]rune(fileName)) > 255 {
		return SendDocumentMessageResult{}, fmt.Errorf("%w: filename must be 255 characters or fewer", ErrInvalidDocument)
	}
	if len([]rune(caption)) > 1024 {
		return SendDocumentMessageResult{}, errors.New("caption must be 1024 characters or fewer")
	}

	m.mu.RLock()
	service, exists := m.sessions[id]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return SendDocumentMessageResult{}, ErrSessionNotFound
	}
	client := service.Client
	if !client.IsConnected() || !client.IsLoggedIn() {
		return SendDocumentMessageResult{}, ErrSessionNotConnected
	}

	recipient, err := resolveWhatsAppRecipient(ctx, client, phoneNumber)
	if err != nil {
		return SendDocumentMessageResult{}, err
	}
	contentType := mime.TypeByExtension(filepath.Ext(fileName))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	upload, err := client.Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return SendDocumentMessageResult{}, fmt.Errorf("upload WhatsApp document: %w", err)
	}
	documentMessage := &waE2E.DocumentMessage{
		URL:               proto.String(upload.URL),
		DirectPath:        proto.String(upload.DirectPath),
		MediaKey:          upload.MediaKey,
		FileSHA256:        upload.FileSHA256,
		FileEncSHA256:     upload.FileEncSHA256,
		FileLength:        &upload.FileLength,
		Mimetype:          proto.String(contentType),
		FileName:          proto.String(fileName),
		MediaKeyTimestamp: proto.Int64(time.Now().Unix()),
	}
	if caption != "" {
		documentMessage.Caption = proto.String(caption)
	}
	message := &waE2E.Message{DocumentMessage: documentMessage}
	response, err := client.SendMessage(ctx, recipient, message)
	if err != nil {
		return SendDocumentMessageResult{}, fmt.Errorf("send WhatsApp document message: %w", err)
	}

	historyStored := false
	if client.Store != nil && client.Store.ID != nil {
		info := types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     recipient,
				Sender:   client.Store.ID.ToNonAD(),
				IsFromMe: true,
			},
			ID:        response.ID,
			Type:      "document",
			Timestamp: response.Timestamp,
		}
		if storeErr := service.storeChatMessage(info, message); storeErr != nil {
			fmt.Printf("WhatsApp session %s sent document %s but failed to store chat history: %v\n", id, response.ID, storeErr)
		} else {
			historyStored = true
		}
	}
	return SendDocumentMessageResult{
		ID:            response.ID,
		Recipient:     recipient.String(),
		Timestamp:     response.Timestamp.UTC().Format(time.RFC3339Nano),
		HistoryStored: historyStored,
	}, nil
}
