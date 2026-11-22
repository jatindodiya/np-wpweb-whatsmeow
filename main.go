package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"np-npweb-whatsmeow/internal/config"
	"np-npweb-whatsmeow/internal/routes"
	"np-npweb-whatsmeow/internal/whatsapp"

)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.Load(".env")
	if err != nil {
		fmt.Println("Configuration error:", err)
		return
	}

	waService, err := whatsapp.NewService(ctx, cfg.DBDSN, cfg.MobileNumber)
	if err != nil {
		fmt.Println("WhatsApp startup error:", err)
		return
	}

	srv := &http.Server{
		Addr:    cfg.ServerAddr,
		Handler: routes.NewRouter(waService.Client),
	}

	go func() {
		fmt.Printf("HTTP API listening on http://localhost%s\n", cfg.ServerAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Println("HTTP server error:", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("Shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	waService.Close()
}