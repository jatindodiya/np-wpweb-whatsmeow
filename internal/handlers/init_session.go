package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"np-npweb-whatsmeow/internal/whatsapp"
)

var phoneNumberPattern = regexp.MustCompile(`^[0-9]{10,15}$`)

type initWpSessionRequest struct {
	ID          string          `json:"id"`
	PhoneNumber string          `json:"phoneNumber"`
	Mode        string          `json:"mode"`
	OtherParams json.RawMessage `json:"otherParams"`
}

type initWpSessionResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Mode        string `json:"mode"`
	PairingCode string `json:"pairingCode,omitempty"`
	QRCodeURL   string `json:"qrCodeUrl,omitempty"`
}

func InitWpSession(manager *whatsapp.Manager) http.HandlerFunc {
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

		var request initWpSessionRequest
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
		if !phoneNumberPattern.MatchString(request.PhoneNumber) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "phoneNumber must contain 10 to 15 digits",
			})
			return
		}
		if request.Mode != "code" && request.Mode != "qr" {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "mode must be code or qr",
			})
			return
		}
		if len(request.OtherParams) > 0 && !bytes.HasPrefix(bytes.TrimSpace(request.OtherParams), []byte("{")) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "otherParams must be an object",
			})
			return
		}

		result, err := manager.Initialize(r.Context(), request.ID, request.PhoneNumber, request.Mode)
		if err != nil {
			if errors.Is(err, whatsapp.ErrSessionAlreadyExists) {
				WriteJSON(w, http.StatusConflict, map[string]string{
					"error": err.Error(),
				})
				return
			}
			if errors.Is(err, whatsapp.ErrSessionNumberMismatch) {
				WriteJSON(w, http.StatusConflict, map[string]string{
					"error": err.Error(),
				})
				return
			}
			if errors.Is(err, whatsapp.ErrManagerClosed) {
				WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
					"error": err.Error(),
				})
				return
			}
			WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to initialize WhatsApp session: %v", err),
			})
			return
		}

		status := http.StatusCreated
		if result.Existing {
			status = http.StatusOK
		}
		WriteJSON(w, status, initWpSessionResponse{
			ID:          request.ID,
			Status:      result.Status,
			Mode:        request.Mode,
			PairingCode: result.PairingCode,
			QRCodeURL:   qrCodeURL(request.ID, result),
		})
	}
}

func qrCodeURL(id string, result whatsapp.SessionInitResult) string {
	if result.QRCode == "" {
		return ""
	}
	return "/session/qr?id=" + id
}
