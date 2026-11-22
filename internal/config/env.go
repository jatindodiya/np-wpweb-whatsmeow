package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	MobileNumber string
	DBDSN        string
	ServerAddr   string
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

	rawPhone := os.Getenv("MOBILE_NUMBER")
	phoneNumber := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(rawPhone), "+", ""), " ", "")
	if phoneNumber == "" {
		return Config{}, fmt.Errorf("MOBILE_NUMBER is not set in %s", envPath)
	}

	addr := strings.TrimSpace(os.Getenv("SERVER_ADDR"))
	if addr == "" {
		addr = ":4010"
	}

	dbDSN := strings.TrimSpace(os.Getenv("DB_DSN"))
	if dbDSN == "" {
		dbDSN = "file:whatsmeow.db?_foreign_keys=on"
	}

	return Config{
		MobileNumber: phoneNumber,
		DBDSN:        dbDSN,
		ServerAddr:   addr,
	}, nil
}