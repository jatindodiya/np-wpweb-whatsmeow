package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"np-npweb-whatsmeow/internal/whatsapp"
)

func CloseWpSession(manager *whatsapp.Manager) http.HandlerFunc {
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
		sessionID = strings.ToLower(sessionID)

		status, err := manager.CloseSession(sessionID)
		if err != nil {
			switch {
			case errors.Is(err, whatsapp.ErrSessionNotFound):
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrSessionInitializing):
				WriteJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrManagerClosed):
				WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			default:
				WriteJSON(w, http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("failed to close WhatsApp session: %v", err),
				})
			}
			return
		}

		WriteJSON(w, http.StatusOK, status)
	}
}
