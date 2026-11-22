package routes

import (
	"fmt"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow"
	"np-npweb-whatsmeow/internal/handlers"
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

func NewRouter(waClient *whatsmeow.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/contacts", handlers.GetContacts(waClient))
	mux.HandleFunc("/groups", handlers.GetGroups(waClient))
	return RequestLoggingMiddleware(mux)
}