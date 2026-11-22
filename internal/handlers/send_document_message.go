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

const maxDocumentRequestSize = (64 << 20) + (1 << 20)

func SendDocumentMessage(manager *whatsapp.Manager) http.HandlerFunc {
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
		r.Body = http.MaxBytesReader(w, r.Body, maxDocumentRequestSize)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "document request exceeds the 64 MiB upload limit"})
			} else {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid multipart request: %v", err)})
			}
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
			if err := validateMediaFormKeys(r.MultipartForm, "document", "mobileNumber", "groupJid", "caption"); err != nil {
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
		file, header, err := r.FormFile("document")
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "document file is required"})
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("read document upload: %v", err)})
			return
		}
		if len(data) > 64<<20 {
			WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "document must be 64 MiB or smaller"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		result, err := manager.SendDocumentMessage(ctx, strings.ToLower(sessionID), digits, groupJID, caption, header.Filename, data)
		if err != nil {
			writeMediaError(w, "document", err)
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
