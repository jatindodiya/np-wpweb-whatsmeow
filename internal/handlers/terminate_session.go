package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"np-npweb-whatsmeow/internal/whatsapp"
)

type terminateSessionRequest struct {
	ID string `json:"id"`
}

func TerminateWpSession(manager *whatsapp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "only POST is allowed",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()

		var request terminateSessionRequest
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
		if !whatsapp.ValidSessionID(request.ID) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "id must be a UUID",
			})
			return
		}
		request.ID = strings.ToLower(request.ID)

		if err := manager.Terminate(r.Context(), request.ID); err != nil {
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
			"id":     request.ID,
			"status": "terminated",
		})
	}
}
