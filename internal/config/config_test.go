package config

import (
	"testing"
	"time"
)

func TestLoadRequiresCredentials(t *testing.T) {
	t.Setenv("BTC_USERNAME", "")
	t.Setenv("BTC_PASSWORD", "")
	t.Setenv("BTC_SESSION_SECRET", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want missing credentials error")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("BTC_USERNAME", "admin")
	t.Setenv("BTC_PASSWORD", "correct horse battery staple")
	t.Setenv("BTC_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("BTC_ADDR", "")
	t.Setenv("BTC_SECURE_COOKIE", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got.Address != "0.0.0.0:8080" {
		t.Fatalf("Address = %q", got.Address)
	}
	if got.MaxUploadBytes != 100<<20 {
		t.Fatalf("MaxUploadBytes = %d", got.MaxUploadBytes)
	}
	if got.MaxPages != 1000 {
		t.Fatalf("MaxPages = %d", got.MaxPages)
	}
	if got.FixedLayoutDPI != 144 {
		t.Fatalf("FixedLayoutDPI = %d", got.FixedLayoutDPI)
	}
	if got.JobTimeout != 30*time.Minute {
		t.Fatalf("JobTimeout = %s", got.JobTimeout)
	}
	if got.Retention != time.Hour {
		t.Fatalf("Retention = %s", got.Retention)
	}
	if !got.SecureCookie {
		t.Fatal("SecureCookie = false, want true")
	}
	if got.R2Enabled {
		t.Fatal("R2Enabled = true, want false")
	}
	if got.DownloadURLTTL != 15*time.Minute {
		t.Fatalf("DownloadURLTTL = %s", got.DownloadURLTTL)
	}
}

func TestLoadValidatesFixedLayoutDPI(t *testing.T) {
	t.Setenv("BTC_USERNAME", "admin")
	t.Setenv("BTC_PASSWORD", "correct horse battery staple")
	t.Setenv("BTC_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("BTC_FIXED_LAYOUT_DPI", "240")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid fixed layout DPI error")
	}
}

func TestLoadRejectsShortSessionSecret(t *testing.T) {
	t.Setenv("BTC_USERNAME", "admin")
	t.Setenv("BTC_PASSWORD", "correct horse battery staple")
	t.Setenv("BTC_SESSION_SECRET", "short")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want short secret error")
	}
}

func TestLoadEnablesCompleteR2Configuration(t *testing.T) {
	t.Setenv("BTC_USERNAME", "admin")
	t.Setenv("BTC_PASSWORD", "correct horse battery staple")
	t.Setenv("BTC_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("BTC_R2_ACCOUNT_ID", "account-id")
	t.Setenv("BTC_R2_ACCESS_KEY_ID", "access-key")
	t.Setenv("BTC_R2_SECRET_ACCESS_KEY", "secret-key")
	t.Setenv("BTC_R2_BUCKET", "pdf2epub")
	t.Setenv("BTC_DOWNLOAD_URL_TTL", "10m")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !got.R2Enabled || got.R2Bucket != "pdf2epub" {
		t.Fatalf("R2 config = %#v", got)
	}
	if got.DownloadURLTTL != 10*time.Minute {
		t.Fatalf("DownloadURLTTL = %s", got.DownloadURLTTL)
	}
}

func TestLoadRejectsPartialR2Configuration(t *testing.T) {
	t.Setenv("BTC_USERNAME", "admin")
	t.Setenv("BTC_PASSWORD", "correct horse battery staple")
	t.Setenv("BTC_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("BTC_R2_BUCKET", "pdf2epub")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want partial R2 configuration error")
	}
}

func TestLoadEnablesPublicPaidAccessOnlyWithCompleteSecurityConfiguration(t *testing.T) {
	t.Setenv("BTC_PUBLIC_ACCESS", "true")
	t.Setenv("BTC_PUBLIC_URL", "https://epub.yi-flow.com")
	t.Setenv("BTC_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("BTC_SECURE_COOKIE", "true")
	t.Setenv("BTC_COMMERCE_DB_PATH", "/var/lib/pdf2epub-data/commerce.db")
	t.Setenv("BTC_STRIPE_SECRET_KEY", "sk_test")
	t.Setenv("BTC_STRIPE_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("BTC_STRIPE_PRICE_ID", "price_test")
	t.Setenv("BTC_TURNSTILE_SITE_KEY", "site_test")
	t.Setenv("BTC_TURNSTILE_SECRET_KEY", "turnstile_secret")
	t.Setenv("BTC_R2_ACCOUNT_ID", "account-id")
	t.Setenv("BTC_R2_ACCESS_KEY_ID", "access-key")
	t.Setenv("BTC_R2_SECRET_ACCESS_KEY", "secret-key")
	t.Setenv("BTC_R2_BUCKET", "pdf2epub")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !got.PublicAccess || got.QueueCapacity != 3 || got.SessionTTL != 365*24*time.Hour || got.MaxUploadBytes != 90<<20 {
		t.Fatalf("public config = %#v", got)
	}
}

func TestLoadRejectsPublicAccessWithoutHTTPSAndPaymentControls(t *testing.T) {
	t.Setenv("BTC_PUBLIC_ACCESS", "true")
	t.Setenv("BTC_PUBLIC_URL", "http://epub.yi-flow.com")
	t.Setenv("BTC_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("BTC_SECURE_COOKIE", "true")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want unsafe public access configuration error")
	}
}
