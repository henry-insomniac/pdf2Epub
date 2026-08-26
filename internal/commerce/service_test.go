package commerce

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"pdf2epub/internal/app"
	"pdf2epub/internal/domain"
)

type gatewayStub struct {
	checkout CheckoutResult
	event    PaymentEvent
	request  CheckoutRequest
}

func (g *gatewayStub) CreateCheckout(_ context.Context, request CheckoutRequest) (CheckoutResult, error) {
	g.request = request
	return g.checkout, nil
}

func (g *gatewayStub) VerifyWebhook([]byte, string) (PaymentEvent, error) {
	return g.event, nil
}

func TestCreditLedgerDebitsAndRefundsIdempotently(t *testing.T) {
	gateway := &gatewayStub{checkout: CheckoutResult{SessionID: "cs_test", URL: "https://checkout.example/session"}}
	service := openTestService(t, gateway)
	ctx := context.Background()

	if err := service.AuthorizeJob(ctx, "guest:one", "job-empty"); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("AuthorizeJob() without credits error = %v, want ErrInsufficientCredits", err)
	}
	if err := service.GrantCredits(ctx, "guest:one", 2, "test", "grant:test"); err != nil {
		t.Fatalf("GrantCredits(): %v", err)
	}
	if err := service.GrantCredits(ctx, "guest:one", 2, "test", "grant:test"); err != nil {
		t.Fatalf("idempotent GrantCredits(): %v", err)
	}
	if balance, _ := service.Balance(ctx, "guest:one"); balance != 2 {
		t.Fatalf("balance after grant = %d, want 2", balance)
	}
	if err := service.AuthorizeJob(ctx, "guest:one", "job-refund"); err != nil {
		t.Fatalf("AuthorizeJob(): %v", err)
	}
	if balance, _ := service.Balance(ctx, "guest:one"); balance != 1 {
		t.Fatalf("balance after debit = %d, want 1", balance)
	}
	outcome := app.JobOutcome{JobID: "job-refund", OwnerID: "guest:one", Status: domain.JobFailed}
	if err := service.RecordJobOutcome(ctx, outcome); err != nil {
		t.Fatalf("RecordJobOutcome(): %v", err)
	}
	if err := service.RecordJobOutcome(ctx, outcome); err != nil {
		t.Fatalf("idempotent RecordJobOutcome(): %v", err)
	}
	if balance, _ := service.Balance(ctx, "guest:one"); balance != 2 {
		t.Fatalf("balance after refund = %d, want 2", balance)
	}

	if err := service.AuthorizeJob(ctx, "guest:one", "job-success"); err != nil {
		t.Fatalf("AuthorizeJob(success): %v", err)
	}
	if err := service.RecordJobOutcome(ctx, app.JobOutcome{JobID: "job-success", OwnerID: "guest:one", Status: domain.JobSucceeded}); err != nil {
		t.Fatalf("RecordJobOutcome(success): %v", err)
	}
	if balance, _ := service.Balance(ctx, "guest:one"); balance != 1 {
		t.Fatalf("balance after successful job = %d, want 1", balance)
	}
}

func TestPaidWebhookCreditsOnlyOnceAndValidatesOrderBinding(t *testing.T) {
	gateway := &gatewayStub{checkout: CheckoutResult{SessionID: "cs_paid", URL: "https://checkout.example/session"}}
	service := openTestService(t, gateway)
	ctx := context.Background()
	result, err := service.CreateCheckout(ctx, "guest:buyer")
	if err != nil {
		t.Fatalf("CreateCheckout(): %v", err)
	}
	if result.URL == "" || gateway.request.OrderID == "" {
		t.Fatalf("checkout result/request = %#v / %#v", result, gateway.request)
	}
	gateway.event = PaymentEvent{
		ID: "evt_paid", Type: "checkout.session.completed", SessionID: "cs_paid",
		OrderID: gateway.request.OrderID, AccountID: "guest:buyer", Paid: true, Final: true,
	}
	credited, err := service.HandleWebhook(ctx, []byte("fixture"), "signature")
	if err != nil || !credited {
		t.Fatalf("HandleWebhook() = %v, %v", credited, err)
	}
	credited, err = service.HandleWebhook(ctx, []byte("fixture"), "signature")
	if err != nil || credited {
		t.Fatalf("duplicate HandleWebhook() = %v, %v", credited, err)
	}
	if balance, _ := service.Balance(ctx, "guest:buyer"); balance != 5 {
		t.Fatalf("paid balance = %d, want 5", balance)
	}
}

func TestOpenRefundsInterruptedAuthorizedJob(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "commerce.db")
	gateway := &gatewayStub{checkout: CheckoutResult{SessionID: "cs_test", URL: "https://checkout.example/session"}}
	config := Config{
		DatabasePath: databasePath, PublicURL: "https://epub.example",
		Pack: Pack{Credits: 5, PriceLabel: "USD 1.99", PriceID: "price_test"}, Gateway: gateway,
	}
	service, err := Open(config)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	ctx := context.Background()
	if err := service.GrantCredits(ctx, "guest:restart", 1, "test", "grant:restart"); err != nil {
		t.Fatalf("GrantCredits(): %v", err)
	}
	if err := service.AuthorizeJob(ctx, "guest:restart", "job-interrupted"); err != nil {
		t.Fatalf("AuthorizeJob(): %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	restarted, err := Open(config)
	if err != nil {
		t.Fatalf("Open(restart): %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if balance, err := restarted.Balance(ctx, "guest:restart"); err != nil || balance != 1 {
		t.Fatalf("balance after restart = %d, %v; want 1", balance, err)
	}
}

func openTestService(t *testing.T, gateway Gateway) *Service {
	t.Helper()
	service, err := Open(Config{
		DatabasePath: filepath.Join(t.TempDir(), "commerce.db"),
		PublicURL:    "https://epub.example",
		Pack:         Pack{Credits: 5, PriceLabel: "US$1.99", PriceID: "price_test"},
		Gateway:      gateway,
	})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}
