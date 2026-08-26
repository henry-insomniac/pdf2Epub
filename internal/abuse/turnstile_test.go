package abuse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTurnstileVerifiesTokenWithRemoteIP(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm(): %v", err)
		}
		received = r.Form
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	verifier, err := NewTurnstile(TurnstileConfig{SecretKey: "secret", VerifyURL: server.URL})
	if err != nil {
		t.Fatalf("NewTurnstile(): %v", err)
	}
	if err := verifier.Verify(context.Background(), "token", "203.0.113.8"); err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	if received.Get("secret") != "secret" || received.Get("response") != "token" || received.Get("remoteip") != "203.0.113.8" {
		t.Fatalf("verification form = %#v", received)
	}
}

func TestTurnstileRejectsFailedChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	t.Cleanup(server.Close)
	verifier, _ := NewTurnstile(TurnstileConfig{SecretKey: "secret", VerifyURL: server.URL})
	if err := verifier.Verify(context.Background(), "bad", ""); !errors.Is(err, ErrChallengeRejected) {
		t.Fatalf("Verify() error = %v, want ErrChallengeRejected", err)
	}
}
