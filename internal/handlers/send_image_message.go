package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"np-npweb-whatsmeow/internal/whatsapp"
)

const (
	maxImageUploadSize = 16 << 20
	maxImageRequestSize = maxImageUploadSize + (1 << 20)
)

func SendImageMessage(manager *whatsapp.Manager) http.HandlerFunc {
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

		r.Body = http.MaxBytesReader(w, r.Body, maxImageRequestSize)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "image request exceeds the maximum size"})
			} else {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid multipart request: %v", err)})
			}
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
			if err := validateImageFormKeys(r.MultipartForm); err != nil {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}

		number, err := singleFormValue(r, "mobileNumber", true)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		number = strings.TrimSpace(number)
		digits := strings.TrimPrefix(number, "+")
		if len(digits) < 10 || len(digits) > 15 || !allDigits(digits) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "mobileNumber must contain 10 to 15 digits, optionally prefixed with +",
			})
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

		file, _, err := r.FormFile("image")
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "image file is required"})
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxImageUploadSize+1))
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("read image upload: %v", err)})
			return
		}
		if len(data) > maxImageUploadSize {
			WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "image must be 16 MiB or smaller"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		result, err := manager.SendImageMessage(ctx, strings.ToLower(sessionID), digits, caption, data)
		if err != nil {
			switch {
			case errors.Is(err, whatsapp.ErrSessionNotFound):
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrSessionNotConnected):
				WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrWhatsAppNumberNotRegistered):
				WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			case errors.Is(err, whatsapp.ErrInvalidImage):
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			default:
				WriteJSON(w, http.StatusBadGateway, map[string]string{
					"error": fmt.Sprintf("failed to send WhatsApp image: %v", err),
				})
			}
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func validateImageFormKeys(form *multipart.Form) error {
	for key := range form.Value {
		if key != "mobileNumber" && key != "caption" {
			return fmt.Errorf("unknown form field %q", key)
		}
	}
	for key := range form.File {
		if key != "image" {
			return fmt.Errorf("unknown file field %q", key)
		}
	}
	if len(form.File["image"]) != 1 {
		return errors.New("exactly one image file is required")
	}
	return nil
}

func singleFormValue(r *http.Request, key string, required bool) (string, error) {
	values := r.MultipartForm.Value[key]
	if len(values) > 1 {
		return "", fmt.Errorf("form field %q must be provided at most once", key)
	}
	if len(values) == 0 {
		if required {
			return "", fmt.Errorf("form field %q is required", key)
		}
		return "", nil
	}
	return values[0], nil
}
