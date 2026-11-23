package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	DBDSN      string
	ServerAddr string
	WebhookURL string
}

func LoadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"'")
		if key != "" {
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, value)
			}
		}
	}
	return scanner.Err()
}

func Load(envPath string) (Config, error) {
	if err := LoadEnvFile(envPath); err != nil {
		return Config{}, fmt.Errorf("failed to load %s: %w", envPath, err)
	}

	addr := strings.TrimSpace(os.Getenv("SERVER_ADDR"))
	if addr == "" {
		addr = ":4010"
	}

	dbDSN := strings.TrimSpace(os.Getenv("DB_DSN"))
	if dbDSN == "" {
		dbDSN = "file:whatsmeow.db?_foreign_keys=on"
	}

	webhookURL := strings.TrimSpace(os.Getenv("WHATSAPP_WEBHOOK_URL"))
	if webhookURL != "" {
		parsed, err := url.ParseRequestURI(webhookURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return Config{}, fmt.Errorf("WHATSAPP_WEBHOOK_URL must be an absolute HTTP(S) URL")
		}
	}

	return Config{
		DBDSN:      dbDSN,
		ServerAddr: addr,
		WebhookURL: webhookURL,
	}, nil
}