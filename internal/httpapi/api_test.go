package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"pdf2epub/internal/auth"
	"pdf2epub/internal/commerce"
)

func TestAuthenticationFlow(t *testing.T) {
	sessions := auth.NewManager("admin", "secret password", 12*time.Hour)
	server := httptest.NewServer(New(sessions, nil, 100<<20, false).Handler())
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New(): %v", err)
	}
	client := &http.Client{Jar: jar}

	response := doJSON(t, client, http.MethodGet, server.URL+"/api/v1/session", nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous session status = %d, want 401", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "wrong password",
	}, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d, want 401", response.StatusCode)
	}
	var invalidBody errorEnvelope
	decodeJSON(t, response, &invalidBody)
	if invalidBody.Error.Code != "identity.invalid_credentials" {
		t.Fatalf("error code = %q", invalidBody.Error.Code)
	}

	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "secret password",
	}, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", response.StatusCode)
	}
	var loginBody sessionResponse
	decodeJSON(t, response, &loginBody)
	if loginBody.CSRFToken == "" {
		t.Fatal("login CSRF token is empty")
	}

	response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/session", nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated session status = %d, want 200", response.StatusCode)
	}
	var sessionBody sessionResponse
	decodeJSON(t, response, &sessionBody)
	if sessionBody.Username != "admin" || sessionBody.CSRFToken != loginBody.CSRFToken {
		t.Fatalf("session body = %#v", sessionBody)
	}

	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/logout", nil, "wrong")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d, want 403", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/logout", nil, loginBody.CSRFToken)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/session", nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after logout status = %d, want 401", response.StatusCode)
	}
	response.Body.Close()
}

func TestPublicAccessCreatesPasswordlessGuestSession(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	sessions := auth.NewManager("admin", "secret password", 12*time.Hour, secret)
	billing, err := commerce.Open(commerce.Config{
		DatabasePath: filepath.Join(t.TempDir(), "commerce.db"),
		PublicURL:    "https://example.test",
		Pack:         commerce.Pack{Credits: 5, PriceLabel: "US$1.99", PriceID: "price_test"},
		Gateway:      apiGatewayStub{},
	})
	if err != nil {
		t.Fatalf("commerce.Open(): %v", err)
	}
	t.Cleanup(func() { _ = billing.Close() })
	server := httptest.NewServer(NewWithOptions(sessions, nil, 100<<20, false, Options{
		PublicAccess: true, Commerce: billing, Challenge: allowChallenge{}, ChallengeSiteKey: "site_test",
	}).Handler())
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New(): %v", err)
	}
	client := &http.Client{Jar: jar}

	response := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/guest", map[string]string{}, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("guest session status = %d, want 201", response.StatusCode)
	}
	var created sessionResponse
	decodeJSON(t, response, &created)
	if created.Role != auth.RoleGuest || created.Access != "public" || created.CSRFToken == "" {
		t.Fatalf("guest session = %#v", created)
	}

	response = doJSON(t, client, http.MethodGet, server.URL+"/api/v1/session", nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restored guest session status = %d, want 200", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "secret password",
	}, "")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("public password login status = %d, want 404", response.StatusCode)
	}
	response.Body.Close()
}

type allowChallenge struct{}

func (allowChallenge) Verify(context.Context, string, string) error { return nil }

type apiGatewayStub struct{}

func (apiGatewayStub) CreateCheckout(context.Context, commerce.CheckoutRequest) (commerce.CheckoutResult, error) {
	return commerce.CheckoutResult{SessionID: "cs_test", URL: "https://checkout.example/session"}, nil
}

func (apiGatewayStub) VerifyWebhook([]byte, string) (commerce.PaymentEvent, error) {
	return commerce.PaymentEvent{}, nil
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any, csrf string) *http.Response {
	t.Helper()
	var requestBody bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&requestBody).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	request, err := http.NewRequest(method, url, &requestBody)
	if err != nil {
		t.Fatalf("http.NewRequest(): %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("client.Do(): %v", err)
	}
	return response
}

func decodeJSON(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
