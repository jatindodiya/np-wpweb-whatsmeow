package routes

import (
	"fmt"
	"net/http"
	"time"

	"np-npweb-whatsmeow/internal/handlers"
	"np-npweb-whatsmeow/internal/whatsapp"
)

func RequestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			fmt.Printf("%s %s completed in %.2fms\n", r.Method, r.URL.Path, time.Since(start).Seconds()*1000)
		}()
		next.ServeHTTP(w, r)
	})
}

func NewRouter(waManager *whatsapp.Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handlers.GetHealth)
	mux.HandleFunc("/initWpSession", handlers.InitWpSession(waManager))
	mux.HandleFunc("/startWpSession", handlers.StartWpSession(waManager))
	mux.HandleFunc("/closeWpSession", handlers.CloseWpSession(waManager))
	mux.HandleFunc("/terminateWpSession", handlers.TerminateWpSession(waManager))
	mux.HandleFunc("/sessions", handlers.GetSessions(waManager))
	mux.HandleFunc("/session/qr", handlers.GetSessionQR(waManager))
	mux.HandleFunc("/session/status", handlers.GetSessionStatus(waManager))
	mux.HandleFunc("/contacts", handlers.GetContacts(waManager.GetClient))
	mux.HandleFunc("/groups", handlers.GetGroups(waManager.GetClient))
	mux.HandleFunc("/checkRegisteredUsers", handlers.CheckWhatsAppNumber(waManager.GetClient))
	mux.HandleFunc("/userChatHistory", handlers.GetUserChatHistory(waManager))
	mux.HandleFunc("/syncWpSession", handlers.SyncWpSession(waManager))
	mux.HandleFunc("/syncChatHistory", handlers.SyncChatHistory(waManager))
	mux.HandleFunc("/syncUserChatHistory", handlers.SyncUserChatHistory(waManager))
	return RequestLoggingMiddleware(mux)
}