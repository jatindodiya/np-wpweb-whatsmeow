package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

var ErrWhatsAppNumberNotRegistered = errors.New("mobile number is not registered on WhatsApp")
var messageURLPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

type SendMessageResult struct {
	ID                   string `json:"messageId"`
	Recipient            string `json:"recipient"`
	Timestamp            string `json:"timestamp"`
	HistoryStored        bool   `json:"historyStored"`
	LinkPreviewGenerated bool   `json:"linkPreviewGenerated"`
	LinkPreviewError     string `json:"linkPreviewError,omitempty"`
}

func (m *Manager) SendTextMessage(ctx context.Context, id, mobileNumber, groupJID, text string) (SendMessageResult, error) {
	if !ValidSessionID(id) {
		return SendMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if _, _, err := ValidateRecipient(mobileNumber, groupJID); err != nil {
		return SendMessageResult{}, err
	}
	if strings.TrimSpace(text) == "" {
		return SendMessageResult{}, errors.New("message text must not be empty")
	}

	service, client, err := m.getConnectedSession(id)
	if err != nil {
		return SendMessageResult{}, err
	}

	recipient, err := resolveMessageRecipient(ctx, client, mobileNumber, groupJID)
	if err != nil {
		return SendMessageResult{}, err
	}

	message := &waE2E.Message{Conversation: new(text)}
	previewError := ""
	if rawURL := firstMessageURL(text); rawURL != "" {
		preview, previewErr := buildLinkPreview(ctx, rawURL)
		if previewErr != nil {
			previewError = previewErr.Error()
		} else {
			previewError = preview.ImageError
			message.Conversation = nil
			message.ExtendedTextMessage = &waE2E.ExtendedTextMessage{
				Text:                proto.String(text),
				MatchedText:         proto.String(rawURL),
				Description:         proto.String(preview.Description),
				Title:               proto.String(preview.Title),
				PreviewType:         previewType(false),
				LinkPreviewMetadata: &waE2E.LinkPreviewMetadata{},
			}
			if len(preview.Image) > 0 {
				thumbnail, uploadErr := client.Upload(ctx, preview.Image, whatsmeow.MediaLinkThumbnail)
				if uploadErr != nil {
					previewError = fmt.Sprintf("link preview thumbnail upload failed: %v", uploadErr)
				} else {
					message.ExtendedTextMessage.JPEGThumbnail = preview.Image
					message.ExtendedTextMessage.ThumbnailDirectPath = proto.String(thumbnail.DirectPath)
					message.ExtendedTextMessage.ThumbnailSHA256 = thumbnail.FileSHA256
					message.ExtendedTextMessage.ThumbnailEncSHA256 = thumbnail.FileEncSHA256
					message.ExtendedTextMessage.MediaKey = thumbnail.MediaKey
					message.ExtendedTextMessage.MediaKeyTimestamp = proto.Int64(time.Now().Unix())
					message.ExtendedTextMessage.PreviewType = previewType(true)
				}
			}
		}
	}

	response, err := client.SendMessage(ctx, recipient, message)
	if err != nil {
		return SendMessageResult{}, fmt.Errorf("send WhatsApp text message: %w", err)
	}

	historyStored := storeOutgoingMessage(id, service, client, recipient, response.ID, response.Timestamp, "text", message)

	return SendMessageResult{
		ID:                   response.ID,
		Recipient:            recipient.String(),
		Timestamp:            response.Timestamp.UTC().Format(time.RFC3339Nano),
		HistoryStored:         historyStored,
		LinkPreviewGenerated: message.ExtendedTextMessage != nil,
		LinkPreviewError:     previewError,
	}, nil
}

func resolveWhatsAppRecipient(ctx context.Context, client *whatsmeow.Client, phoneNumber string) (types.JID, error) {
	lookup, err := client.IsOnWhatsApp(ctx, []string{"+" + phoneNumber})
	if err != nil {
		return types.JID{}, fmt.Errorf("check WhatsApp recipient: %w", err)
	}
	for _, result := range lookup {
		if result.IsIn {
			return result.JID, nil
		}
	}
	return types.JID{}, ErrWhatsAppNumberNotRegistered
}

func firstMessageURL(text string) string {
	rawURL := messageURLPattern.FindString(text)
	return strings.TrimRight(rawURL, ".,!?;:)]}")
}

func previewType(hasImage bool) *waE2E.ExtendedTextMessage_PreviewType {
	preview := waE2E.ExtendedTextMessage_PLACEHOLDER
	if hasImage {
		preview = waE2E.ExtendedTextMessage_IMAGE
	}
	return &preview
}

func allPhoneNumberDigits(number string) bool {
	if number == "" {
		return false
	}
	for _, digit := range number {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
