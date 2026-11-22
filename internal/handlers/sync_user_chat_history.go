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

type syncUserChatHistoryRequest struct {
	MobileNumber string `json:"mobileNumber"`
	Limit        int    `json:"limit"`
}

func SyncUserChatHistory(manager *whatsapp.Manager) http.HandlerFunc {
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

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request syncUserChatHistoryRequest
		if err := decoder.Decode(&request); err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("invalid request body: %v", err),
			})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "request body must contain a single JSON object",
			})
			return
		}

		number := strings.TrimSpace(request.MobileNumber)
		digits := strings.TrimPrefix(number, "+")
		if len(digits) < 10 || len(digits) > 15 || !allDigits(digits) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "mobileNumber must contain 10 to 15 digits, optionally prefixed with +",
			})
			return
		}
		if request.Limit == 0 {
			request.Limit = 50
		}
		if request.Limit < 1 || request.Limit > 50 {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "limit must be between 1 and 50",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := manager.RequestUserChatHistory(ctx, strings.ToLower(sessionID), digits, request.Limit); err != nil {
			switch {
			case errors.Is(err, whatsapp.ErrSessionNotFound):
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrSessionNotConnected),
				errors.Is(err, whatsapp.ErrChatHistoryCursorNotFound):
				WriteJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			default:
				WriteJSON(w, http.StatusBadGateway, map[string]string{
					"error": fmt.Sprintf("failed to request user chat history: %v", err),
				})
			}
			return
		}

		WriteJSON(w, http.StatusAccepted, map[string]any{
			"id":          strings.ToLower(sessionID),
			"mobileNumber": digits,
			"status":      "history_sync_requested",
			"messageLimit": request.Limit,
			"direction":   "older_than_oldest_stored_message",
			"message":     "WhatsApp delivers the requested messages asynchronously; they are stored as the history-sync event arrives.",
		})
	}
}
