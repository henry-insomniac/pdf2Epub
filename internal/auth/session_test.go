package auth

import (
	"testing"
	"time"
)

func TestGuestSessionIsSignedAndSurvivesManagerRestart(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	first := NewManager("admin", "password", time.Hour, secret)
	session, err := first.CreateGuest()
	if err != nil {
		t.Fatalf("CreateGuest(): %v", err)
	}
	if session.Role != RoleGuest || session.SubjectID == "" || session.CSRFToken == "" {
		t.Fatalf("guest session = %#v", session)
	}

	restarted := NewManager("admin", "password", time.Hour, secret)
	restored, ok := restarted.Validate(session.Token)
	if !ok {
		t.Fatal("signed guest session was not valid after manager restart")
	}
	if restored.SubjectID != session.SubjectID || restored.CSRFToken != session.CSRFToken {
		t.Fatalf("restored session = %#v, want subject/csrf from %#v", restored, session)
	}

	tampered := session.Token[:len(session.Token)-1] + "x"
	if _, ok := restarted.Validate(tampered); ok {
		t.Fatal("tampered guest session was accepted")
	}
}

func TestGuestSessionCanBeReissuedForRecoveredWallet(t *testing.T) {
	manager := NewManager("", "", time.Hour, []byte("01234567890123456789012345678901"))
	session, err := manager.CreateGuestFor("guest:0123456789abcdef")
	if err != nil {
		t.Fatalf("CreateGuestFor(): %v", err)
	}
	if session.SubjectID != "guest:0123456789abcdef" {
		t.Fatalf("SubjectID = %q", session.SubjectID)
	}
	if _, err := manager.CreateGuestFor("admin:wrong-boundary"); err == nil {
		t.Fatal("CreateGuestFor() accepted a non-guest subject")
	}
}

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
