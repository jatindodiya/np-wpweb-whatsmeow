package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type SessionWebhookEvent struct {
	Event       string `json:"event"`
	SessionID   string `json:"sessionId"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
	QRCode      string `json:"qrCode,omitempty"`
	PairingCode string `json:"pairingCode,omitempty"`
	Timestamp   string `json:"timestamp"`
}

func (m *Manager) notifyWebhook(sessionID, phoneNumber, event string) {
	m.notifyWebhookEvent(SessionWebhookEvent{
		Event:       event,
		SessionID:   sessionID,
		PhoneNumber: phoneNumber,
	})
}

func (m *Manager) notifyWebhookEvent(payload SessionWebhookEvent) {
	if m.webhookURL == "" {
		return
	}
	payload.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if payload.Event == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		if err := postWebhookEvent(ctx, m.webhookURL, payload); err != nil {
			fmt.Printf("WhatsApp session %s webhook notification (%s) failed: %v\n", payload.SessionID, payload.Event, err)
		}
	}()
}

func postWebhookEvent(ctx context.Context, endpoint string, event SessionWebhookEvent) error {
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("WHATSAPP_WEBHOOK_URL must be an absolute HTTP(S) URL")
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("send webhook request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return nil
}
