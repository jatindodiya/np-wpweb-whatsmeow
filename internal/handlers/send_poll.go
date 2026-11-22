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

type sendPollRequest struct {
	MobileNumber           string   `json:"mobileNumber"`
	GroupJID               string   `json:"groupJid"`
	Question               string   `json:"question"`
	Options                []string `json:"options"`
	SelectableOptionsCount *int     `json:"selectableOptionsCount"`
}

func SendPoll(manager *whatsapp.Manager) http.HandlerFunc {
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
		var request sendPollRequest
		if err := decoder.Decode(&request); err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid request body: %v", err)})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain a single JSON object"})
			return
		}
		selectableOptionsCount := 1
		if request.SelectableOptionsCount != nil {
			selectableOptionsCount = *request.SelectableOptionsCount
		}
		groupJID := strings.TrimSpace(request.GroupJID)
		number := strings.TrimSpace(request.MobileNumber)
		if (groupJID == "") == (number == "") {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "provide exactly one of groupJid or mobileNumber",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var result whatsapp.SendPollMessageResult
		var err error
		if groupJID != "" {
			result, err = manager.SendPollMessage(ctx, strings.ToLower(sessionID), groupJID, request.Question, request.Options, selectableOptionsCount)
		} else {
			number = strings.TrimPrefix(number, "+")
			if len(number) < 10 || len(number) > 15 || !allDigits(number) {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "mobileNumber must contain 10 to 15 digits, optionally prefixed with +"})
				return
			}
			result, err = manager.SendPollToUserMessage(ctx, strings.ToLower(sessionID), number, request.Question, request.Options, selectableOptionsCount)
		}
		if err != nil {
			switch {
			case errors.Is(err, whatsapp.ErrSessionNotFound):
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrSessionNotConnected):
				WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrInvalidPoll):
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrWhatsAppNumberNotRegistered):
				WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			default:
				WriteJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("failed to send WhatsApp poll: %v", err)})
			}
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
