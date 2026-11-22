package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"np-npweb-whatsmeow/internal/whatsapp"
)

type sendGroupInviteRequest struct {
	MobileNumber     string `json:"mobileNumber"`
	RecipientGroupJID string `json:"recipientGroupJid"`
	GroupJID          string `json:"groupJid"`
	Caption           string `json:"caption"`
}

func SendGroupInvite(manager *whatsapp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "only POST is allowed"})
			return
		}
		sessionID := r.URL.Query().Get("id")
		if !whatsapp.ValidSessionID(sessionID) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a UUID"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request sendGroupInviteRequest
		if err := decoder.Decode(&request); err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid request body: %v", err)})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain a single JSON object"})
			return
		}
		digits, recipientGroupJID, err := whatsapp.ValidateRecipient(request.MobileNumber, request.RecipientGroupJID)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		result, err := manager.SendGroupInviteMessage(ctx, strings.ToLower(sessionID), digits, recipientGroupJID, request.GroupJID, request.Caption)
		if err != nil {
			switch {
			case errors.Is(err, whatsapp.ErrSessionNotFound):
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrSessionNotConnected):
				WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrWhatsAppNumberNotRegistered):
				WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrInvalidRecipient):
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrInvalidGroupInvite):
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			default:
				WriteJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("failed to send WhatsApp group invite card: %v", err)})
			}
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
