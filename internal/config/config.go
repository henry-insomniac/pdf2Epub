package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address             string
	Username            string
	Password            string
	SessionSecret       []byte
	SecureCookie        bool
	WorkDir             string
	MaxUploadBytes      int64
	MaxPages            int
	FixedLayoutDPI      int
	JobTimeout          time.Duration
	Retention           time.Duration
	ShutdownTimeout     time.Duration
	SessionTTL          time.Duration
	EPUBCheckCommand    string
	RequireEPUBCheck    bool
	R2Enabled           bool
	R2AccountID         string
	R2AccessKeyID       string
	R2SecretKey         string
	R2Bucket            string
	R2Prefix            string
	DownloadURLTTL      time.Duration
	PublicAccess        bool
	PublicURL           string
	CommerceDBPath      string
	QueueCapacity       int
	CreditPackCredits   int64
	CreditPackLabel     string
	PaymentProvider     string
	StripeSecretKey     string
	StripeWebhookSecret string
	StripePriceID       string
	VoucherSecret       []byte
	ChallengeProvider   string
	TurnstileSiteKey    string
	TurnstileSecretKey  string
}

func Load() (Config, error) {
	cfg := Config{
		Address:             envOrDefault("BTC_ADDR", "0.0.0.0:8080"),
		Username:            secretValue("BTC_USERNAME"),
		Password:            secretValue("BTC_PASSWORD"),
		SessionSecret:       []byte(secretValue("BTC_SESSION_SECRET")),
		SecureCookie:        envBool("BTC_SECURE_COOKIE", true),
		WorkDir:             envOrDefault("BTC_WORK_DIR", "/tmp/pdf2epub"),
		MaxUploadBytes:      100 << 20,
		MaxPages:            1000,
		FixedLayoutDPI:      144,
		JobTimeout:          30 * time.Minute,
		Retention:           time.Hour,
		ShutdownTimeout:     15 * time.Second,
		SessionTTL:          12 * time.Hour,
		EPUBCheckCommand:    envOrDefault("BTC_EPUBCHECK_COMMAND", "epubcheck"),
		RequireEPUBCheck:    envBool("BTC_REQUIRE_EPUBCHECK", true),
		R2AccountID:         strings.TrimSpace(secretValue("BTC_R2_ACCOUNT_ID")),
		R2AccessKeyID:       strings.TrimSpace(secretValue("BTC_R2_ACCESS_KEY_ID")),
		R2SecretKey:         secretValue("BTC_R2_SECRET_ACCESS_KEY"),
		R2Bucket:            strings.TrimSpace(envOrDefault("BTC_R2_BUCKET", "")),
		R2Prefix:            strings.Trim(strings.TrimSpace(envOrDefault("BTC_R2_PREFIX", "epub")), "/"),
		DownloadURLTTL:      15 * time.Minute,
		PublicAccess:        envBool("BTC_PUBLIC_ACCESS", false),
		PublicURL:           strings.TrimRight(strings.TrimSpace(envOrDefault("BTC_PUBLIC_URL", "")), "/"),
		CommerceDBPath:      envOrDefault("BTC_COMMERCE_DB_PATH", "/tmp/pdf2epub-commerce/commerce.db"),
		CreditPackCredits:   5,
		CreditPackLabel:     strings.TrimSpace(envOrDefault("BTC_CREDIT_PACK_LABEL", "5 次额度")),
		PaymentProvider:     strings.ToLower(strings.TrimSpace(envOrDefault("BTC_PAYMENT_PROVIDER", ""))),
		StripeSecretKey:     secretValue("BTC_STRIPE_SECRET_KEY"),
		StripeWebhookSecret: secretValue("BTC_STRIPE_WEBHOOK_SECRET"),
		StripePriceID:       strings.TrimSpace(secretValue("BTC_STRIPE_PRICE_ID")),
		VoucherSecret:       []byte(secretValue("BTC_VOUCHER_SECRET")),
		ChallengeProvider:   strings.ToLower(strings.TrimSpace(envOrDefault("BTC_CHALLENGE_PROVIDER", ""))),
		TurnstileSiteKey:    strings.TrimSpace(secretValue("BTC_TURNSTILE_SITE_KEY")),
		TurnstileSecretKey:  secretValue("BTC_TURNSTILE_SECRET_KEY"),
	}
	if !cfg.PublicAccess && (cfg.Username == "" || cfg.Password == "") {
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
	if value := strings.TrimSpace(os.Getenv("BTC_QUEUE_CAPACITY")); value != "" {
		capacity, err := strconv.Atoi(value)
		if err != nil || capacity < 0 || capacity > 20 {
			return Config{}, fmt.Errorf("BTC_QUEUE_CAPACITY must be between 0 and 20: %q", value)
		}
		cfg.QueueCapacity = capacity
	} else if cfg.PublicAccess {
		cfg.QueueCapacity = 3
	}
	if value := strings.TrimSpace(os.Getenv("BTC_MAX_UPLOAD_MIB")); value != "" {
		maxMiB, err := strconv.ParseInt(value, 10, 64)
		if err != nil || maxMiB <= 0 || maxMiB > 500 {
			return Config{}, fmt.Errorf("BTC_MAX_UPLOAD_MIB must be between 1 and 500: %q", value)
		}
		cfg.MaxUploadBytes = maxMiB << 20
	} else if cfg.PublicAccess {
		// Keeps the complete multipart request below Cloudflare's 100 MB
		// request limit on entry plans.
		cfg.MaxUploadBytes = 90 << 20
	}
	if value := strings.TrimSpace(os.Getenv("BTC_CREDIT_PACK_CREDITS")); value != "" {
		credits, err := strconv.ParseInt(value, 10, 64)
		if err != nil || credits <= 0 || credits > 1000 {
			return Config{}, fmt.Errorf("BTC_CREDIT_PACK_CREDITS must be between 1 and 1000: %q", value)
		}
		cfg.CreditPackCredits = credits
	}
	if cfg.PublicAccess {
		if cfg.PaymentProvider == "" {
			if cfg.StripeSecretKey != "" || cfg.StripeWebhookSecret != "" || cfg.StripePriceID != "" {
				cfg.PaymentProvider = "stripe"
			} else {
				cfg.PaymentProvider = "voucher"
			}
		}
		if cfg.ChallengeProvider == "" {
			if cfg.TurnstileSiteKey != "" || cfg.TurnstileSecretKey != "" {
				cfg.ChallengeProvider = "turnstile"
			} else {
				cfg.ChallengeProvider = "altcha"
			}
		}
		if !cfg.SecureCookie {
			return Config{}, errors.New("BTC_SECURE_COOKIE must be true when BTC_PUBLIC_ACCESS is enabled")
		}
		parsedPublicURL, err := url.Parse(cfg.PublicURL)
		if err != nil || parsedPublicURL.Scheme != "https" || parsedPublicURL.Host == "" || parsedPublicURL.User != nil || parsedPublicURL.RawQuery != "" || parsedPublicURL.Fragment != "" {
			return Config{}, errors.New("BTC_PUBLIC_URL must be an HTTPS origin without credentials, query or fragment")
		}
		if !filepath.IsAbs(cfg.CommerceDBPath) {
			return Config{}, errors.New("BTC_COMMERCE_DB_PATH must be an absolute path")
		}
		switch cfg.PaymentProvider {
		case "stripe":
			if cfg.StripeSecretKey == "" || cfg.StripeWebhookSecret == "" || cfg.StripePriceID == "" {
				return Config{}, errors.New("Stripe secret key, webhook secret and price ID are required when BTC_PAYMENT_PROVIDER=stripe")
			}
		case "voucher":
			if len(cfg.VoucherSecret) < 32 {
				return Config{}, errors.New("BTC_VOUCHER_SECRET must contain at least 32 bytes when BTC_PAYMENT_PROVIDER=voucher")
			}
		default:
			return Config{}, errors.New("BTC_PAYMENT_PROVIDER must be stripe or voucher")
		}
		switch cfg.ChallengeProvider {
		case "turnstile":
			if cfg.TurnstileSiteKey == "" || cfg.TurnstileSecretKey == "" {
				return Config{}, errors.New("Turnstile site key and secret key are required when BTC_CHALLENGE_PROVIDER=turnstile")
			}
		case "altcha":
		default:
			return Config{}, errors.New("BTC_CHALLENGE_PROVIDER must be altcha or turnstile")
		}
		if cfg.CreditPackLabel == "" {
			return Config{}, errors.New("BTC_CREDIT_PACK_LABEL is required for public access")
		}
		if !cfg.R2Enabled {
			return Config{}, errors.New("private R2 artifact delivery is required for public access")
		}
		cfg.SessionTTL = 365 * 24 * time.Hour
	}
	if value := strings.TrimSpace(os.Getenv("BTC_DOWNLOAD_URL_TTL")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("BTC_DOWNLOAD_URL_TTL must be a positive duration: %q", value)
		}
		cfg.DownloadURLTTL = duration
	}
	if value := strings.TrimSpace(os.Getenv("BTC_FIXED_LAYOUT_DPI")); value != "" {
		dpi, err := strconv.Atoi(value)
		if err != nil || dpi < 72 || dpi > 200 {
			return Config{}, fmt.Errorf("BTC_FIXED_LAYOUT_DPI must be between 72 and 200: %q", value)
		}
		cfg.FixedLayoutDPI = dpi
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

func ReadSecret(name string) string {
	return secretValue(name)
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
