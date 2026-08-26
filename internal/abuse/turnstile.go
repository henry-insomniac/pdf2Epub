package abuse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrChallengeRejected = errors.New("challenge rejected")

type Verifier interface {
	Verify(context.Context, string, string) error
}

type TurnstileConfig struct {
	SecretKey  string
	VerifyURL  string
	HTTPClient *http.Client
}

type Turnstile struct {
	secretKey string
	verifyURL string
	client    *http.Client
}

func NewTurnstile(config TurnstileConfig) (*Turnstile, error) {
	if strings.TrimSpace(config.SecretKey) == "" {
		return nil, errors.New("Turnstile secret key is required")
	}
	if config.VerifyURL == "" {
		config.VerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Turnstile{secretKey: config.SecretKey, verifyURL: config.VerifyURL, client: config.HTTPClient}, nil
}

func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) error {
	if strings.TrimSpace(token) == "" {
		return ErrChallengeRejected
	}
	form := url.Values{"secret": {t.secretKey}, "response": {token}}
	if remoteIP != "" && remoteIP != "unknown" {
		form.Set("remoteip", remoteIP)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := t.client.Do(request)
	if err != nil {
		return fmt.Errorf("verify Turnstile challenge: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Turnstile verification returned status %d", response.StatusCode)
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode Turnstile verification: %w", err)
	}
	if !result.Success {
		return ErrChallengeRejected
	}
	return nil
}
