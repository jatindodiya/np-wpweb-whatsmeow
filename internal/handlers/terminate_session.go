package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"np-npweb-whatsmeow/internal/whatsapp"
)

func TerminateWpSession(manager *whatsapp.Manager) http.HandlerFunc {
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

		if err := manager.Terminate(r.Context(), sessionID); err != nil {
			if errors.Is(err, whatsapp.ErrSessionNotFound) {
				WriteJSON(w, http.StatusNotFound, map[string]string{
					"error": err.Error(),
				})
				return
			}
			if errors.Is(err, whatsapp.ErrSessionInitializing) {
				WriteJSON(w, http.StatusConflict, map[string]string{
					"error": err.Error(),
				})
				return
			}
			WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error":             fmt.Sprintf("session was removed locally, but cleanup was incomplete: %v", err),
				"terminatedLocally": true,
			})
			return
		}

		WriteJSON(w, http.StatusOK, map[string]string{
			"id":     sessionID,
			"status": "terminated",
		})
	}
}
