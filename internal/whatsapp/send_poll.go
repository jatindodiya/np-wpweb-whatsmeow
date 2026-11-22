package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

var ErrInvalidPoll = errors.New("invalid poll")

type SendPollMessageResult struct {
	ID            string `json:"messageId"`
	Recipient     string `json:"recipient"`
	Timestamp     string `json:"timestamp"`
	HistoryStored bool   `json:"historyStored"`
}

func (m *Manager) SendPollMessage(ctx context.Context, id, groupJID, question string, options []string, selectableOptionsCount int) (SendPollMessageResult, error) {
	if !ValidSessionID(id) {
		return SendPollMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	target, err := types.ParseJID(strings.TrimSpace(groupJID))
	if err != nil || target.Server != types.GroupServer || target.User == "" {
		return SendPollMessageResult{}, fmt.Errorf("%w: groupJID must be a valid WhatsApp group JID", ErrInvalidPoll)
	}
	return m.sendPollToJID(ctx, id, target, question, options, selectableOptionsCount)
}

func (m *Manager) SendPollToUserMessage(ctx context.Context, id, phoneNumber, question string, options []string, selectableOptionsCount int) (SendPollMessageResult, error) {
	if !ValidSessionID(id) {
		return SendPollMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if len(phoneNumber) < 10 || len(phoneNumber) > 15 || !allPhoneNumberDigits(phoneNumber) {
		return SendPollMessageResult{}, fmt.Errorf("%w: mobileNumber must contain 10 to 15 digits", ErrInvalidPoll)
	}
	service, client, err := m.getConnectedSession(id)
	if err != nil {
		return SendPollMessageResult{}, err
	}
	target, err := resolveWhatsAppRecipient(ctx, client, phoneNumber)
	if err != nil {
		return SendPollMessageResult{}, err
	}
	return m.sendPollToJIDWithSession(ctx, id, service, client, target, question, options, selectableOptionsCount)
}

func (m *Manager) sendPollToJID(ctx context.Context, id string, target types.JID, question string, options []string, selectableOptionsCount int) (SendPollMessageResult, error) {
	service, client, err := m.getConnectedSession(id)
	if err != nil {
		return SendPollMessageResult{}, err
	}
	return m.sendPollToJIDWithSession(ctx, id, service, client, target, question, options, selectableOptionsCount)
}

func (m *Manager) sendPollToJIDWithSession(ctx context.Context, id string, service *Service, client *whatsmeow.Client, target types.JID, question string, options []string, selectableOptionsCount int) (SendPollMessageResult, error) {
	if len([]rune(strings.TrimSpace(question))) == 0 || len([]rune(question)) > 255 {
		return SendPollMessageResult{}, fmt.Errorf("%w: question must contain 1 to 255 characters", ErrInvalidPoll)
	}
	if len(options) < 2 || len(options) > 12 {
		return SendPollMessageResult{}, fmt.Errorf("%w: provide between 2 and 12 options", ErrInvalidPoll)
	}
	if selectableOptionsCount < 1 || selectableOptionsCount > len(options) {
		return SendPollMessageResult{}, fmt.Errorf("%w: selectableOptionsCount must be between 1 and the number of options", ErrInvalidPoll)
	}
	seenOptions := make(map[string]struct{}, len(options))
	trimmedOptions := make([]string, len(options))
	for i, option := range options {
		option = strings.TrimSpace(option)
		if len([]rune(option)) == 0 || len([]rune(option)) > 100 {
			return SendPollMessageResult{}, fmt.Errorf("%w: each option must contain 1 to 100 characters", ErrInvalidPoll)
		}
		if _, exists := seenOptions[strings.ToLower(option)]; exists {
			return SendPollMessageResult{}, fmt.Errorf("%w: poll options must be unique", ErrInvalidPoll)
		}
		seenOptions[strings.ToLower(option)] = struct{}{}
		trimmedOptions[i] = option
	}
	message := client.BuildPollCreation(strings.TrimSpace(question), trimmedOptions, selectableOptionsCount)
	response, err := client.SendMessage(ctx, target, message)
	if err != nil {
		return SendPollMessageResult{}, fmt.Errorf("send WhatsApp poll: %w", err)
	}
	historyStored := storeOutgoingMessage(id, service, client, target, response.ID, response.Timestamp, "poll", message)
	return SendPollMessageResult{
		ID:            response.ID,
		Recipient:     target.String(),
		Timestamp:     response.Timestamp.UTC().Format(time.RFC3339Nano),
		HistoryStored: historyStored,
	}, nil
}

func (m *Manager) getConnectedSession(id string) (*Service, *whatsmeow.Client, error) {
	m.mu.RLock()
	service, exists := m.sessions[id]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return nil, nil, ErrSessionNotFound
	}
	client := service.Client
	if !client.IsConnected() || !client.IsLoggedIn() {
		return nil, nil, ErrSessionNotConnected
	}
	return service, client, nil
}

func storeOutgoingMessage(id string, service *Service, client *whatsmeow.Client, recipient types.JID, messageID string, timestamp time.Time, messageType string, message *waE2E.Message) bool {
	if client.Store == nil || client.Store.ID == nil {
		return false
	}
	info := types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     recipient,
			Sender:   client.Store.ID.ToNonAD(),
			IsFromMe: true,
		},
		ID:        messageID,
		Type:      messageType,
		Timestamp: timestamp,
	}
	if err := service.storeChatMessage(info, message); err != nil {
		fmt.Printf("WhatsApp session %s sent %s %s but failed to store chat history: %v\n", id, messageType, messageID, err)
		return false
	}
	return true
}
