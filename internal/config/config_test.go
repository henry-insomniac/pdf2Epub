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
	if got.JobTimeout != 30*time.Minute {
		t.Fatalf("JobTimeout = %s", got.JobTimeout)
	}
	if got.Retention != time.Hour {
		t.Fatalf("Retention = %s", got.Retention)
	}
	if !got.SecureCookie {
		t.Fatal("SecureCookie = false, want true")
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
