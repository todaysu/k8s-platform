package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const defaultSessionTTL = 8 * time.Hour

type Config struct {
	ListenAddr              string
	DatabasePath            string
	AdminUsername           string
	AdminPasswordHash       string
	CredentialEncryptionKey string
	SecureCookies           bool
	SessionTTL              time.Duration
}

func Load() (Config, error) {
	secureCookies, err := envBool("SECURE_COOKIES", true)
	if err != nil {
		return Config{}, err
	}

	sessionTTL, err := envDuration("SESSION_TTL", defaultSessionTTL)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:              envOrDefault("LISTEN_ADDR", ":8080"),
		DatabasePath:            envOrDefault("DATABASE_PATH", "./data/platform.db"),
		AdminUsername:           envOrDefault("ADMIN_USERNAME", "admin"),
		AdminPasswordHash:       os.Getenv("ADMIN_PASSWORD_HASH"),
		CredentialEncryptionKey: os.Getenv("CREDENTIAL_ENCRYPTION_KEY"),
		SecureCookies:           secureCookies,
		SessionTTL:              sessionTTL,
	}

	if cfg.AdminPasswordHash == "" {
		return Config{}, errors.New("ADMIN_PASSWORD_HASH is required")
	}
	if cfg.CredentialEncryptionKey == "" {
		return Config{}, errors.New("CREDENTIAL_ENCRYPTION_KEY is required")
	}

	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return parsed, nil
}
