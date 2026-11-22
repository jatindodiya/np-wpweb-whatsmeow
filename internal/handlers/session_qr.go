package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"np-npweb-whatsmeow/internal/whatsapp"
)

func GetSessionQR(manager *whatsapp.Manager) http.HandlerFunc {
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

		qrData, err := manager.GetQRCode(strings.ToLower(sessionID))
		if err != nil {
			switch {
			case errors.Is(err, whatsapp.ErrSessionNotFound):
				WriteJSON(w, http.StatusNotFound, map[string]string{
					"error": err.Error(),
				})
			case errors.Is(err, whatsapp.ErrQRCodeUnavailable):
				WriteJSON(w, http.StatusConflict, map[string]string{
					"error": err.Error(),
				})
			default:
				WriteJSON(w, http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("failed to get QR code: %v", err),
				})
			}
			return
		}

		image, err := qrcode.Encode(qrData, qrcode.Medium, 512)
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to generate QR image: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(image); err != nil {
			fmt.Println("Failed to write QR image response:", err)
		}
	}
}
