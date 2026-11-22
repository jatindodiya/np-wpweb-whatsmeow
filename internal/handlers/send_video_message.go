package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"np-npweb-whatsmeow/internal/whatsapp"
)

const maxVideoRequestSize = (64 << 20) + (1 << 20)

func SendVideoMessage(manager *whatsapp.Manager) http.HandlerFunc {
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
		r.Body = http.MaxBytesReader(w, r.Body, maxVideoRequestSize)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "video request exceeds the 64 MiB upload limit"})
			} else {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid multipart request: %v", err)})
			}
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
			if err := validateMediaFormKeys(r.MultipartForm, "video", "mobileNumber", "groupJid", "caption", "viewOnce"); err != nil {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		number, err := singleFormValue(r, "mobileNumber", false)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		groupJID, err := singleFormValue(r, "groupJid", false)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		digits, groupJID, err := whatsapp.ValidateRecipient(number, groupJID)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		caption, err := singleFormValue(r, "caption", false)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if len([]rune(caption)) > 1024 {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "caption must be 1024 characters or fewer"})
			return
		}
		viewOnceText, err := singleFormValue(r, "viewOnce", false)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		viewOnce := false
		switch strings.ToLower(strings.TrimSpace(viewOnceText)) {
		case "", "false":
		case "true":
			viewOnce = true
		default:
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "viewOnce must be true or false"})
			return
		}
		file, _, err := r.FormFile("video")
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "video file is required"})
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("read video upload: %v", err)})
			return
		}
		if len(data) > 64<<20 {
			WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "video must be 64 MiB or smaller"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()
		result, err := manager.SendVideoMessage(ctx, strings.ToLower(sessionID), digits, groupJID, caption, viewOnce, data)
		if err != nil {
			writeMediaError(w, "video", err)
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
