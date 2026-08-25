package auth

import (
	"testing"
	"time"
)

func TestSessionLifecycle(t *testing.T) {
	manager := NewManager("admin", "secret password", 12*time.Hour)

	session, err := manager.Login("admin", "secret password")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}
	if session.Token == "" || session.CSRFToken == "" {
		t.Fatalf("session tokens must not be empty: %#v", session)
	}
	validated, ok := manager.Validate(session.Token)
	if !ok {
		t.Fatal("Validate() = false, want true")
	}
	if validated.CSRFToken != session.CSRFToken {
		t.Fatalf("CSRF token = %q, want %q", validated.CSRFToken, session.CSRFToken)
	}

	manager.Logout(session.Token)
	if _, ok := manager.Validate(session.Token); ok {
		t.Fatal("Validate() after logout = true, want false")
	}
}

func TestLoginUsesGenericInvalidCredentials(t *testing.T) {
	manager := NewManager("admin", "secret password", time.Hour)

	for _, credentials := range [][2]string{
		{"unknown", "secret password"},
		{"admin", "wrong password"},
	} {
		if _, err := manager.Login(credentials[0], credentials[1]); err != ErrInvalidCredentials {
			t.Fatalf("Login(%q, ...) error = %v, want ErrInvalidCredentials", credentials[0], err)
		}
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	manager := NewManager("admin", "secret password", time.Minute)
	manager.now = func() time.Time { return now }

	session, err := manager.Login("admin", "secret password")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}
	manager.now = func() time.Time { return now.Add(2 * time.Minute) }

	if _, ok := manager.Validate(session.Token); ok {
		t.Fatal("Validate() expired session = true, want false")
	}
}
