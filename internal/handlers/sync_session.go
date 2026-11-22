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

func SyncWpSession(manager *whatsapp.Manager) http.HandlerFunc {
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

		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		result, err := manager.Sync(ctx, strings.ToLower(sessionID))
		if err != nil {
			if errors.Is(err, whatsapp.ErrSessionNotFound) {
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error":  fmt.Sprintf("WhatsApp sync failed: %v", err),
				"result": result,
			})
			return
		}
		if result.Status == "partial" {
			WriteJSON(w, http.StatusMultiStatus, result)
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
