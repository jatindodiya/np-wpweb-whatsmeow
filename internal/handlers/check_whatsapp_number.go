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

type checkWhatsAppNumberRequest struct {
	MobileNumber string `json:"mobileNumber"`
}

type checkWhatsAppNumberResponse struct {
	MobileNumber   string `json:"mobileNumber"`
	IsWhatsAppUser bool   `json:"isWhatsAppUser"`
	JID            string `json:"jid,omitempty"`
}

func CheckWhatsAppNumber(getClient clientLookup) http.HandlerFunc {
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
		var request checkWhatsAppNumberRequest
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
		normalizedNumber := "+" + digits

		client, exists := getClient(strings.ToLower(sessionID))
		if !exists || client == nil {
			WriteJSON(w, http.StatusNotFound, map[string]string{
				"error": "WhatsApp session not found",
			})
			return
		}
		if !client.IsConnected() || !client.IsLoggedIn() {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "WhatsApp session is not connected",
			})
			return
		}

		result, err := client.IsOnWhatsApp(r.Context(), []string{normalizedNumber})
		if err != nil {
			WriteJSON(w, http.StatusBadGateway, map[string]string{
				"error": fmt.Sprintf("failed to check WhatsApp number: %v", err),
			})
			return
		}

		response := checkWhatsAppNumberResponse{MobileNumber: normalizedNumber}
		for _, match := range result {
			if match.IsIn {
				response.IsWhatsAppUser = true
				response.JID = match.JID.String()
				break
			}
		}
		WriteJSON(w, http.StatusOK, response)
	}
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
