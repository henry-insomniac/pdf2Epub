package commerce

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type StripeConfig struct {
	SecretKey     string
	WebhookSecret string
	APIBaseURL    string
	HTTPClient    *http.Client
	Tolerance     time.Duration
}

type StripeGateway struct {
	secretKey     string
	webhookSecret string
	apiBaseURL    string
	client        *http.Client
	tolerance     time.Duration
	now           func() time.Time
}

func NewStripeGateway(config StripeConfig) (*StripeGateway, error) {
	if strings.TrimSpace(config.SecretKey) == "" || strings.TrimSpace(config.WebhookSecret) == "" {
		return nil, errors.New("Stripe secret key and webhook secret are required")
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = "https://api.stripe.com"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.Tolerance <= 0 {
		config.Tolerance = 5 * time.Minute
	}
	return &StripeGateway{
		secretKey: config.SecretKey, webhookSecret: config.WebhookSecret,
		apiBaseURL: strings.TrimRight(config.APIBaseURL, "/"), client: config.HTTPClient,
		tolerance: config.Tolerance, now: time.Now,
	}, nil
}

func (g *StripeGateway) CreateCheckout(ctx context.Context, request CheckoutRequest) (CheckoutResult, error) {
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", request.SuccessURL)
	form.Set("cancel_url", request.CancelURL)
	form.Set("client_reference_id", request.AccountID)
	form.Set("metadata[order_id]", request.OrderID)
	form.Set("line_items[0][price]", request.PriceID)
	form.Set("line_items[0][quantity]", "1")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiBaseURL+"/v1/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return CheckoutResult{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+g.secretKey)
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpRequest.Header.Set("Idempotency-Key", request.OrderID)
	response, err := g.client.Do(httpRequest)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("create Stripe Checkout session: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("read Stripe response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CheckoutResult{}, fmt.Errorf("Stripe Checkout returned status %d", response.StatusCode)
	}
	var result struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return CheckoutResult{}, fmt.Errorf("decode Stripe response: %w", err)
	}
	return CheckoutResult{SessionID: result.ID, URL: result.URL}, nil
}

func (g *StripeGateway) VerifyWebhook(payload []byte, signatureHeader string) (PaymentEvent, error) {
	timestamp, signatures, err := parseStripeSignature(signatureHeader)
	if err != nil {
		return PaymentEvent{}, err
	}
	eventTime := time.Unix(timestamp, 0)
	delta := g.now().Sub(eventTime)
	if delta < 0 {
		delta = -delta
	}
	if delta > g.tolerance {
		return PaymentEvent{}, errors.New("Stripe webhook timestamp is outside the allowed tolerance")
	}
	signedPayload := strconv.FormatInt(timestamp, 10) + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(g.webhookSecret))
	_, _ = mac.Write([]byte(signedPayload))
	expected := mac.Sum(nil)
	verified := false
	for _, signature := range signatures {
		decoded, err := hex.DecodeString(signature)
		if err == nil && subtle.ConstantTimeCompare(decoded, expected) == 1 {
			verified = true
		}
	}
	if !verified {
		return PaymentEvent{}, errors.New("Stripe webhook signature is invalid")
	}

	var envelope struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID                string            `json:"id"`
				PaymentStatus     string            `json:"payment_status"`
				ClientReferenceID string            `json:"client_reference_id"`
				Metadata          map[string]string `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return PaymentEvent{}, errors.New("Stripe webhook payload is invalid")
	}
	event := PaymentEvent{
		ID: envelope.ID, Type: envelope.Type, SessionID: envelope.Data.Object.ID,
		OrderID: envelope.Data.Object.Metadata["order_id"], AccountID: envelope.Data.Object.ClientReferenceID,
	}
	switch envelope.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		event.Paid = envelope.Data.Object.PaymentStatus == "paid" || envelope.Data.Object.PaymentStatus == "no_payment_required"
		event.Final = event.Paid
	case "checkout.session.async_payment_failed", "checkout.session.expired":
		event.Final = true
	default:
		event.Final = false
	}
	return event, nil
}

func parseStripeSignature(header string) (int64, []string, error) {
	var timestamp int64
	var signatures []string
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return 0, nil, errors.New("Stripe webhook timestamp is invalid")
			}
			timestamp = parsed
		case "v1":
			signatures = append(signatures, value)
		}
	}
	if timestamp == 0 || len(signatures) == 0 {
		return 0, nil, errors.New("Stripe webhook signature header is incomplete")
	}
	return timestamp, signatures, nil
}
