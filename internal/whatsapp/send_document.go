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

func (m *Manager) SendDocumentMessage(ctx context.Context, id, mobileNumber, groupJID, caption, fileName string, data []byte) (SendDocumentMessageResult, error) {
	if !ValidSessionID(id) {
		return SendDocumentMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if _, _, err := ValidateRecipient(mobileNumber, groupJID); err != nil {
		return SendDocumentMessageResult{}, err
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

	service, client, err := m.getConnectedSession(id)
	if err != nil {
		return SendDocumentMessageResult{}, err
	}

	recipient, err := resolveMessageRecipient(ctx, client, mobileNumber, groupJID)
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

	historyStored := storeOutgoingMessage(id, service, client, recipient, response.ID, response.Timestamp, "document", message)
	return SendDocumentMessageResult{
		ID:            response.ID,
		Recipient:     recipient.String(),
		Timestamp:     response.Timestamp.UTC().Format(time.RFC3339Nano),
		HistoryStored: historyStored,
	}, nil
}
