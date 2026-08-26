package commerce

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStripeGatewayCreatesCheckoutAndVerifiesWebhook(t *testing.T) {
	var received url.Values
	stripeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test" || r.Header.Get("Idempotency-Key") != "ord_test" {
			t.Errorf("Stripe request headers = %#v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		received, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cs_test","url":"https://checkout.stripe.test/session"}`))
	}))
	t.Cleanup(stripeServer.Close)
	gateway, err := NewStripeGateway(StripeConfig{
		SecretKey: "sk_test", WebhookSecret: "whsec_test", APIBaseURL: stripeServer.URL,
	})
	if err != nil {
		t.Fatalf("NewStripeGateway(): %v", err)
	}
	result, err := gateway.CreateCheckout(context.Background(), CheckoutRequest{
		OrderID: "ord_test", AccountID: "guest:buyer", PriceID: "price_test",
		SuccessURL: "https://example/success", CancelURL: "https://example/cancel",
	})
	if err != nil || result.SessionID != "cs_test" {
		t.Fatalf("CreateCheckout() = %#v, %v", result, err)
	}
	if received.Get("line_items[0][price]") != "price_test" || received.Get("metadata[order_id]") != "ord_test" {
		t.Fatalf("Stripe form = %#v", received)
	}

	now := time.Unix(1_800_000_000, 0)
	gateway.now = func() time.Time { return now }
	payload := []byte(`{"id":"evt_test","type":"checkout.session.completed","data":{"object":{"id":"cs_test","payment_status":"paid","client_reference_id":"guest:buyer","metadata":{"order_id":"ord_test"}}}}`)
	signed := strconv.FormatInt(now.Unix(), 10) + "." + string(payload)
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	_, _ = mac.Write([]byte(signed))
	header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))
	event, err := gateway.VerifyWebhook(payload, header)
	if err != nil {
		t.Fatalf("VerifyWebhook(): %v", err)
	}
	if !event.Paid || !event.Final || event.OrderID != "ord_test" || event.AccountID != "guest:buyer" {
		t.Fatalf("payment event = %#v", event)
	}
	if _, err := gateway.VerifyWebhook(payload, strings.Replace(header, "v1=", "v1=00", 1)); err == nil {
		t.Fatal("invalid webhook signature was accepted")
	}
}
