package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Address          string
	Username         string
	Password         string
	SessionSecret    []byte
	SecureCookie     bool
	WorkDir          string
	MaxUploadBytes   int64
	MaxPages         int
	JobTimeout       time.Duration
	Retention        time.Duration
	ShutdownTimeout  time.Duration
	SessionTTL       time.Duration
	EPUBCheckCommand string
	RequireEPUBCheck bool
}

func Load() (Config, error) {
	cfg := Config{
		Address:          envOrDefault("BTC_ADDR", "0.0.0.0:8080"),
		Username:         secretValue("BTC_USERNAME"),
		Password:         secretValue("BTC_PASSWORD"),
		SessionSecret:    []byte(secretValue("BTC_SESSION_SECRET")),
		SecureCookie:     envBool("BTC_SECURE_COOKIE", true),
		WorkDir:          envOrDefault("BTC_WORK_DIR", "/tmp/pdf2epub"),
		MaxUploadBytes:   100 << 20,
		MaxPages:         1000,
		JobTimeout:       30 * time.Minute,
		Retention:        time.Hour,
		ShutdownTimeout:  15 * time.Second,
		SessionTTL:       12 * time.Hour,
		EPUBCheckCommand: envOrDefault("BTC_EPUBCHECK_COMMAND", "epubcheck"),
		RequireEPUBCheck: envBool("BTC_REQUIRE_EPUBCHECK", true),
	}
	if cfg.Username == "" || cfg.Password == "" {
		return Config{}, errors.New("BTC_USERNAME and BTC_PASSWORD are required")
	}
	if len(cfg.SessionSecret) < 32 {
		return Config{}, errors.New("BTC_SESSION_SECRET must contain at least 32 bytes")
	}
	return cfg, nil
}

func secretValue(name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	path := os.Getenv(name + "_FILE")
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data[:len(data)-trailingNewlines(data)])
}

func trailingNewlines(data []byte) int {
	count := 0
	for i := len(data) - 1; i >= 0 && (data[i] == '\n' || data[i] == '\r'); i-- {
		count++
	}
	return count
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
