package handlers

import (
	"fmt"
	"net/http"

	"np-npweb-whatsmeow/internal/whatsapp"
)

func GetSessions(manager *whatsapp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "only GET is allowed",
			})
			return
		}

		sessions, err := manager.ListStatuses()
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to list WhatsApp sessions: %v", err),
			})
			return
		}

		WriteJSON(w, http.StatusOK, map[string]any{
			"sessions": sessions,
		})
	}
}
