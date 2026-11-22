package handlers

import (
	"errors"
	"net/http"

	"np-npweb-whatsmeow/internal/whatsapp"
)

func GetSessionStatus(manager *whatsapp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "only GET is allowed",
			})
			return
		}

		status, err := manager.GetStatus(r.URL.Query().Get("id"))
		if err != nil {
			if errors.Is(err, whatsapp.ErrSessionNotFound) {
				WriteJSON(w, http.StatusNotFound, map[string]string{
					"error": err.Error(),
				})
				return
			}
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		WriteJSON(w, http.StatusOK, status)
	}
}
