package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"np-npweb-whatsmeow/internal/whatsapp"
)

func GetUserChatHistory(manager *whatsapp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "only GET is allowed",
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

		mobileNumber := strings.TrimSpace(r.URL.Query().Get("mobileNumber"))
		digits := strings.TrimPrefix(mobileNumber, "+")
		if len(digits) < 10 || len(digits) > 15 || !allDigits(digits) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "mobileNumber must contain 10 to 15 digits, optionally prefixed with +",
			})
			return
		}

		limit := 50
		if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
			parsedLimit, err := strconv.Atoi(rawLimit)
			if err != nil || parsedLimit < 1 || parsedLimit > 100 {
				WriteJSON(w, http.StatusBadRequest, map[string]string{
					"error": "limit must be between 1 and 100",
				})
				return
			}
			limit = parsedLimit
		}

		var before int64
		if rawBefore := r.URL.Query().Get("before"); rawBefore != "" {
			parsedBefore, err := strconv.ParseInt(rawBefore, 10, 64)
			if err != nil || parsedBefore <= 0 {
				WriteJSON(w, http.StatusBadRequest, map[string]string{
					"error": "before must be a positive Unix timestamp in milliseconds",
				})
				return
			}
			before = parsedBefore
		}
		beforeID := r.URL.Query().Get("beforeId")
		if beforeID != "" && before == 0 {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "beforeId requires before",
			})
			return
		}

		messages, hasMore, err := manager.GetChatHistory(r.Context(), strings.ToLower(sessionID), digits, limit, before, beforeID)
		if err != nil {
			if errors.Is(err, whatsapp.ErrSessionNotFound) {
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to get user chat history: %v", err),
			})
			return
		}

		response := map[string]any{
			"mobileNumber": digits,
			"count":        len(messages),
			"hasMore":      hasMore,
			"messages":     messages,
		}
		if hasMore && len(messages) > 0 {
			last := messages[len(messages)-1]
			response["nextBefore"] = last.TimestampMS
			response["nextBeforeId"] = last.ID
		}
		WriteJSON(w, http.StatusOK, response)
	}
}
