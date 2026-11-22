package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"np-npweb-whatsmeow/internal/whatsapp"
)

func SyncChatHistory(manager *whatsapp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "only POST is allowed",
			})
			return
		}

		sessionID := r.URL.Query().Get("id")
		if !whatsapp.ValidSessionID(sessionID) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "id must be a UUID",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := manager.RequestRecentChatHistory(ctx, strings.ToLower(sessionID)); err != nil {
			switch {
			case errors.Is(err, whatsapp.ErrSessionNotFound):
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrSessionNotConnected):
				WriteJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			default:
				WriteJSON(w, http.StatusBadGateway, map[string]string{
					"error": fmt.Sprintf("failed to request chat history sync: %v", err),
				})
			}
			return
		}

		WriteJSON(w, http.StatusAccepted, map[string]any{
			"id":                     strings.ToLower(sessionID),
			"status":                 "history_sync_requested",
			"conversationLimit":      20,
			"messagesPerConversation": 50,
			"message":                "History arrives asynchronously and is stored as WhatsApp delivers history-sync events.",
		})
	}
}
