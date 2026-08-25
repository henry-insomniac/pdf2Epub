package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	R2Enabled        bool
	R2AccountID      string
	R2AccessKeyID    string
	R2SecretKey      string
	R2Bucket         string
	R2Prefix         string
	DownloadURLTTL   time.Duration
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
		R2AccountID:      strings.TrimSpace(secretValue("BTC_R2_ACCOUNT_ID")),
		R2AccessKeyID:    strings.TrimSpace(secretValue("BTC_R2_ACCESS_KEY_ID")),
		R2SecretKey:      secretValue("BTC_R2_SECRET_ACCESS_KEY"),
		R2Bucket:         strings.TrimSpace(envOrDefault("BTC_R2_BUCKET", "")),
		R2Prefix:         strings.Trim(strings.TrimSpace(envOrDefault("BTC_R2_PREFIX", "epub")), "/"),
		DownloadURLTTL:   15 * time.Minute,
	}
	if cfg.Username == "" || cfg.Password == "" {
		return Config{}, errors.New("BTC_USERNAME and BTC_PASSWORD are required")
	}
	if len(cfg.SessionSecret) < 32 {
		return Config{}, errors.New("BTC_SESSION_SECRET must contain at least 32 bytes")
	}
	configuredR2Values := 0
	for _, value := range []string{cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretKey, cfg.R2Bucket} {
		if value != "" {
			configuredR2Values++
		}
	}
	if configuredR2Values != 0 && configuredR2Values != 4 {
		return Config{}, errors.New("BTC_R2_ACCOUNT_ID, BTC_R2_ACCESS_KEY_ID, BTC_R2_SECRET_ACCESS_KEY and BTC_R2_BUCKET must be configured together")
	}
	cfg.R2Enabled = configuredR2Values == 4
	if value := strings.TrimSpace(os.Getenv("BTC_DOWNLOAD_URL_TTL")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("BTC_DOWNLOAD_URL_TTL must be a positive duration: %q", value)
		}
		cfg.DownloadURLTTL = duration
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
