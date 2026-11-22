package handlers

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"

	"np-npweb-whatsmeow/internal/whatsapp"
)

func validateMediaFormKeys(form *multipart.Form, fileField string, valueFields ...string) error {
	allowedValues := make(map[string]struct{}, len(valueFields))
	for _, field := range valueFields {
		allowedValues[field] = struct{}{}
	}
	for key := range form.Value {
		if _, ok := allowedValues[key]; !ok {
			return fmt.Errorf("unknown form field %q", key)
		}
	}
	for key := range form.File {
		if key != fileField {
			return fmt.Errorf("unknown file field %q", key)
		}
	}
	if len(form.File[fileField]) != 1 {
		return fmt.Errorf("exactly one %s file is required", fileField)
	}
	return nil
}

func writeMediaError(w http.ResponseWriter, mediaType string, err error) {
	switch {
	case errors.Is(err, whatsapp.ErrSessionNotFound):
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, whatsapp.ErrSessionNotConnected):
		WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, whatsapp.ErrWhatsAppNumberNotRegistered):
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	case errors.Is(err, whatsapp.ErrInvalidVideo), errors.Is(err, whatsapp.ErrInvalidDocument):
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, whatsapp.ErrMediaTranscoderUnavailable):
		WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	default:
		WriteJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("failed to send WhatsApp %s: %v", mediaType, err),
		})
	}
}
