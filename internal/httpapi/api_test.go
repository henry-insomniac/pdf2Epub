package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"pdf2epub/internal/auth"
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
