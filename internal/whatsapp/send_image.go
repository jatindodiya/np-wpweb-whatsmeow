package whatsapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

const maxImagePixels = 20_000_000

var ErrInvalidImage = errors.New("invalid image")

type SendImageMessageResult struct {
	ID            string `json:"messageId"`
	Recipient     string `json:"recipient"`
	Timestamp     string `json:"timestamp"`
	HistoryStored bool `json:"historyStored"`
	ViewOnce      bool   `json:"viewOnce"`
}

func (m *Manager) SendImageMessage(ctx context.Context, id, phoneNumber, caption string, viewOnce bool, data []byte) (SendImageMessageResult, error) {
	if !ValidSessionID(id) {
		return SendImageMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if len(phoneNumber) < 10 || len(phoneNumber) > 15 || !allPhoneNumberDigits(phoneNumber) {
		return SendImageMessageResult{}, errors.New("mobileNumber must contain 10 to 15 digits")
	}
	if len(data) == 0 {
		return SendImageMessageResult{}, fmt.Errorf("%w: image must not be empty", ErrInvalidImage)
	}
	if len([]rune(caption)) > 1024 {
		return SendImageMessageResult{}, errors.New("caption must be 1024 characters or fewer")
	}

	m.mu.RLock()
	service, exists := m.sessions[id]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return SendImageMessageResult{}, ErrSessionNotFound
	}
	client := service.Client
	if !client.IsConnected() || !client.IsLoggedIn() {
		return SendImageMessageResult{}, ErrSessionNotConnected
	}

	contentType, width, height, thumbnail, err := prepareImage(data)
	if err != nil {
		return SendImageMessageResult{}, err
	}
	recipient, err := resolveWhatsAppRecipient(ctx, client, phoneNumber)
	if err != nil {
		return SendImageMessageResult{}, err
	}
	upload, err := client.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return SendImageMessageResult{}, fmt.Errorf("upload WhatsApp image: %w", err)
	}
	imageMessage := &waE2E.ImageMessage{
		URL:               proto.String(upload.URL),
		DirectPath:        proto.String(upload.DirectPath),
		MediaKey:          upload.MediaKey,
		FileSHA256:        upload.FileSHA256,
		FileEncSHA256:     upload.FileEncSHA256,
		FileLength:        &upload.FileLength,
		Mimetype:          proto.String(contentType),
		Width:             proto.Uint32(width),
		Height:            proto.Uint32(height),
		JPEGThumbnail:     thumbnail,
		MediaKeyTimestamp: proto.Int64(time.Now().Unix()),
	}
	if viewOnce {
		imageMessage.ViewOnce = proto.Bool(true)
	}
	if caption != "" {
		imageMessage.Caption = proto.String(caption)
	}
	message := &waE2E.Message{ImageMessage: imageMessage}
	if viewOnce {
		message = &waE2E.Message{
			ViewOnceMessageV2: &waE2E.FutureProofMessage{
				Message: message,
			},
		}
	}
	response, err := client.SendMessage(ctx, recipient, message)
	if err != nil {
		return SendImageMessageResult{}, fmt.Errorf("send WhatsApp image message: %w", err)
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
			Type:      "image",
			Timestamp: response.Timestamp,
		}
		if storeErr := service.storeChatMessage(info, message); storeErr != nil {
			fmt.Printf("WhatsApp session %s sent image %s but failed to store chat history: %v\n", id, response.ID, storeErr)
		} else {
			historyStored = true
		}
	}

	return SendImageMessageResult{
		ID:            response.ID,
		Recipient:     recipient.String(),
		Timestamp:     response.Timestamp.UTC().Format(time.RFC3339Nano),
		HistoryStored: historyStored,
		ViewOnce:      viewOnce,
	}, nil
}

func prepareImage(data []byte) (string, uint32, uint32, []byte, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, nil, fmt.Errorf("%w: image must be a valid JPEG or PNG: %v", ErrInvalidImage, err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return "", 0, 0, nil, fmt.Errorf("%w: image dimensions exceed the 20 megapixel limit", ErrInvalidImage)
	}
	var mimetype string
	switch format {
	case "jpeg":
		mimetype = "image/jpeg"
	case "png":
		mimetype = "image/png"
	default:
		return "", 0, 0, nil, fmt.Errorf("%w: only JPEG and PNG images are supported", ErrInvalidImage)
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, nil, fmt.Errorf("%w: decode image: %v", ErrInvalidImage, err)
	}
	thumbnail, err := makeImageThumbnail(decoded)
	if err != nil {
		return "", 0, 0, nil, err
	}
	return mimetype, uint32(config.Width), uint32(config.Height), thumbnail, nil
}

func makeImageThumbnail(img image.Image) ([]byte, error) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scale := min(1, float64(maxThumbnailSize)/float64(max(width, height)))
	thumbWidth := max(1, int(float64(width)*scale))
	thumbHeight := max(1, int(float64(height)*scale))
	thumbnail := image.NewRGBA(image.Rect(0, 0, thumbWidth, thumbHeight))
	for y := 0; y < thumbHeight; y++ {
		for x := 0; x < thumbWidth; x++ {
			sourceX := bounds.Min.X + x*width/thumbWidth
			sourceY := bounds.Min.Y + y*height/thumbHeight
			thumbnail.Set(x, y, img.At(sourceX, sourceY))
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, thumbnail, &jpeg.Options{Quality: 75}); err != nil {
		return nil, fmt.Errorf("encode image thumbnail: %w", err)
	}
	return output.Bytes(), nil
}
