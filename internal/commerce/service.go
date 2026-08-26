package commerce

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"pdf2epub/internal/app"
	"pdf2epub/internal/domain"
)

var (
	ErrInsufficientCredits = errors.New("insufficient credits")
	ErrOrderNotFound       = errors.New("order not found")
	ErrInvalidPayment      = errors.New("invalid payment event")
	ErrBillingUnavailable  = errors.New("billing is unavailable")
)

var (
	accountsBucket  = []byte("accounts")
	balancesBucket  = []byte("balances")
	ledgerBucket    = []byte("credit_ledger")
	referenceBucket = []byte("credit_references")
	chargesBucket   = []byte("job_charges")
	ordersBucket    = []byte("orders")
	eventsBucket    = []byte("payment_events")
)

type Pack struct {
	Credits    int64  `json:"credits"`
	PriceLabel string `json:"price_label"`
	PriceID    string `json:"-"`
}

type CheckoutRequest struct {
	OrderID    string
	AccountID  string
	PriceID    string
	SuccessURL string
	CancelURL  string
}

type CheckoutResult struct {
	SessionID string
	URL       string
}

type PaymentEvent struct {
	ID        string
	Type      string
	SessionID string
	OrderID   string
	AccountID string
	Paid      bool
	Final     bool
}

type Gateway interface {
	CreateCheckout(context.Context, CheckoutRequest) (CheckoutResult, error)
	VerifyWebhook([]byte, string) (PaymentEvent, error)
}

type Config struct {
	DatabasePath string
	PublicURL    string
	Pack         Pack
	Gateway      Gateway
}

type Service struct {
	db        *bolt.DB
	publicURL string
	pack      Pack
	gateway   Gateway
	now       func() time.Time
}

type accountRecord struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type ledgerEntry struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Delta     int64     `json:"delta"`
	Reason    string    `json:"reason"`
	Reference string    `json:"reference"`
	CreatedAt time.Time `json:"created_at"`
}

type chargeRecord struct {
	JobID     string    `json:"job_id"`
	AccountID string    `json:"account_id"`
	Credits   int64     `json:"credits"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type orderRecord struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"account_id"`
	Credits           int64     `json:"credits"`
	PriceID           string    `json:"price_id"`
	ProviderSessionID string    `json:"provider_session_id,omitempty"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func Open(config Config) (*Service, error) {
	if strings.TrimSpace(config.DatabasePath) == "" {
		return nil, errors.New("commerce database path is required")
	}
	if config.Pack.Credits <= 0 || config.Pack.PriceID == "" || config.Pack.PriceLabel == "" {
		return nil, errors.New("a positive credit pack with a provider price is required")
	}
	if config.Gateway == nil {
		return nil, errors.New("payment gateway is required")
	}
	directory := filepath.Dir(config.DatabasePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create commerce data directory: %w", err)
	}
	db, err := bolt.Open(config.DatabasePath, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open commerce database: %w", err)
	}
	service := &Service{
		db:        db,
		publicURL: strings.TrimRight(config.PublicURL, "/"),
		pack:      config.Pack,
		gateway:   config.Gateway,
		now:       func() time.Time { return time.Now().UTC() },
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{accountsBucket, balancesBucket, ledgerBucket, referenceBucket, chargesBucket, ordersBucket, eventsBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %q: %w", name, err)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := service.reconcileOpenCharges(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("reconcile interrupted job charges: %w", err)
	}
	return service, nil
}

func (s *Service) Close() error {
	return s.db.Close()
}

func (s *Service) Pack() Pack {
	return Pack{Credits: s.pack.Credits, PriceLabel: s.pack.PriceLabel}
}

func (s *Service) EnsureAccount(ctx context.Context, accountID string) error {
	if err := validateAccount(ctx, accountID); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return s.ensureAccountTx(tx, accountID)
	})
}

func (s *Service) Balance(ctx context.Context, accountID string) (int64, error) {
	if err := validateAccount(ctx, accountID); err != nil {
		return 0, err
	}
	var balance int64
	err := s.db.View(func(tx *bolt.Tx) error {
		balance = decodeInt64(tx.Bucket(balancesBucket).Get([]byte(accountID)))
		return nil
	})
	return balance, err
}

func (s *Service) GrantCredits(ctx context.Context, accountID string, credits int64, reason, reference string) error {
	if err := validateAccount(ctx, accountID); err != nil {
		return err
	}
	if credits <= 0 || strings.TrimSpace(reference) == "" {
		return errors.New("positive credits and reference are required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := s.ensureAccountTx(tx, accountID); err != nil {
			return err
		}
		return s.addLedgerEntryTx(tx, accountID, credits, reason, reference)
	})
}

func (s *Service) AuthorizeJob(ctx context.Context, accountID, jobID string) error {
	if err := validateAccount(ctx, accountID); err != nil {
		return err
	}
	if jobID == "" {
		return errors.New("job id is required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := s.ensureAccountTx(tx, accountID); err != nil {
			return err
		}
		charges := tx.Bucket(chargesBucket)
		if value := charges.Get([]byte(jobID)); value != nil {
			var existing chargeRecord
			if err := json.Unmarshal(value, &existing); err != nil {
				return err
			}
			if existing.AccountID == accountID && (existing.Status == "authorized" || existing.Status == "completed") {
				return nil
			}
			return errors.New("job charge reference is already used")
		}
		balance := decodeInt64(tx.Bucket(balancesBucket).Get([]byte(accountID)))
		if balance < 1 {
			return ErrInsufficientCredits
		}
		if err := s.addLedgerEntryTx(tx, accountID, -1, "job_authorized", "job:"+jobID); err != nil {
			return err
		}
		now := s.now()
		return putJSON(charges, []byte(jobID), chargeRecord{
			JobID: jobID, AccountID: accountID, Credits: 1, Status: "authorized", CreatedAt: now, UpdatedAt: now,
		})
	})
}

func (s *Service) RecordJobOutcome(ctx context.Context, outcome app.JobOutcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		charges := tx.Bucket(chargesBucket)
		value := charges.Get([]byte(outcome.JobID))
		if value == nil {
			return nil
		}
		var charge chargeRecord
		if err := json.Unmarshal(value, &charge); err != nil {
			return err
		}
		if charge.AccountID != outcome.OwnerID {
			return errors.New("job outcome owner does not match charge")
		}
		if charge.Status == "completed" || charge.Status == "refunded" {
			return nil
		}
		if outcome.Status == domain.JobSucceeded {
			charge.Status = "completed"
		} else {
			if err := s.addLedgerEntryTx(tx, charge.AccountID, charge.Credits, "job_refunded", "refund:"+outcome.JobID); err != nil {
				return err
			}
			charge.Status = "refunded"
		}
		charge.UpdatedAt = s.now()
		return putJSON(charges, []byte(outcome.JobID), charge)
	})
}

func (s *Service) reconcileOpenCharges(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		charges := tx.Bucket(chargesBucket)
		pending := make([]chargeRecord, 0)
		if err := charges.ForEach(func(_, value []byte) error {
			var charge chargeRecord
			if err := json.Unmarshal(value, &charge); err != nil {
				return err
			}
			if charge.Status == "authorized" {
				pending = append(pending, charge)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, charge := range pending {
			if err := s.addLedgerEntryTx(tx, charge.AccountID, charge.Credits, "job_restart_refunded", "refund:"+charge.JobID); err != nil {
				return err
			}
			charge.Status = "refunded"
			charge.UpdatedAt = s.now()
			if err := putJSON(charges, []byte(charge.JobID), charge); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) CreateCheckout(ctx context.Context, accountID string) (CheckoutResult, error) {
	if err := validateAccount(ctx, accountID); err != nil {
		return CheckoutResult{}, err
	}
	if s.publicURL == "" {
		return CheckoutResult{}, ErrBillingUnavailable
	}
	orderID, err := randomID("ord_")
	if err != nil {
		return CheckoutResult{}, err
	}
	now := s.now()
	order := orderRecord{
		ID: orderID, AccountID: accountID, Credits: s.pack.Credits, PriceID: s.pack.PriceID,
		Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		if err := s.ensureAccountTx(tx, accountID); err != nil {
			return err
		}
		return putJSON(tx.Bucket(ordersBucket), []byte(orderID), order)
	}); err != nil {
		return CheckoutResult{}, err
	}
	result, err := s.gateway.CreateCheckout(ctx, CheckoutRequest{
		OrderID: orderID, AccountID: accountID, PriceID: s.pack.PriceID,
		SuccessURL: s.publicURL + "/?checkout=success", CancelURL: s.publicURL + "/?checkout=canceled",
	})
	if err != nil {
		return CheckoutResult{}, err
	}
	if result.SessionID == "" || result.URL == "" {
		return CheckoutResult{}, errors.New("payment gateway returned an incomplete checkout")
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ordersBucket)
		value := bucket.Get([]byte(orderID))
		if value == nil {
			return ErrOrderNotFound
		}
		if err := json.Unmarshal(value, &order); err != nil {
			return err
		}
		order.ProviderSessionID = result.SessionID
		order.UpdatedAt = s.now()
		return putJSON(bucket, []byte(orderID), order)
	}); err != nil {
		return CheckoutResult{}, err
	}
	return result, nil
}

func (s *Service) HandleWebhook(ctx context.Context, payload []byte, signature string) (bool, error) {
	event, err := s.gateway.VerifyWebhook(payload, signature)
	if err != nil {
		return false, err
	}
	if event.ID == "" {
		return false, ErrInvalidPayment
	}
	credited := false
	err = s.db.Update(func(tx *bolt.Tx) error {
		events := tx.Bucket(eventsBucket)
		if events.Get([]byte(event.ID)) != nil {
			return nil
		}
		if err := events.Put([]byte(event.ID), []byte(s.now().Format(time.RFC3339Nano))); err != nil {
			return err
		}
		if !event.Final {
			return nil
		}
		orders := tx.Bucket(ordersBucket)
		value := orders.Get([]byte(event.OrderID))
		if value == nil {
			return ErrOrderNotFound
		}
		var order orderRecord
		if err := json.Unmarshal(value, &order); err != nil {
			return err
		}
		if order.AccountID != event.AccountID || event.SessionID == "" {
			return ErrInvalidPayment
		}
		if order.ProviderSessionID == "" {
			// A signed callback may win the race with persisting the Checkout
			// response. Binding the provider session here keeps that crash window
			// recoverable without trusting browser state.
			order.ProviderSessionID = event.SessionID
		} else if order.ProviderSessionID != event.SessionID {
			return ErrInvalidPayment
		}
		if order.Status == "paid" {
			return nil
		}
		if !event.Paid {
			order.Status = "failed"
			order.UpdatedAt = s.now()
			return putJSON(orders, []byte(order.ID), order)
		}
		if err := s.addLedgerEntryTx(tx, order.AccountID, order.Credits, "order_paid", "order:"+order.ID); err != nil {
			return err
		}
		order.Status = "paid"
		order.UpdatedAt = s.now()
		credited = true
		return putJSON(orders, []byte(order.ID), order)
	})
	return credited, err
}

func (s *Service) ensureAccountTx(tx *bolt.Tx, accountID string) error {
	bucket := tx.Bucket(accountsBucket)
	if bucket.Get([]byte(accountID)) != nil {
		return nil
	}
	return putJSON(bucket, []byte(accountID), accountRecord{ID: accountID, CreatedAt: s.now()})
}

func (s *Service) addLedgerEntryTx(tx *bolt.Tx, accountID string, delta int64, reason, reference string) error {
	references := tx.Bucket(referenceBucket)
	if references.Get([]byte(reference)) != nil {
		return nil
	}
	entryID, err := randomID("led_")
	if err != nil {
		return err
	}
	entry := ledgerEntry{
		ID: entryID, AccountID: accountID, Delta: delta, Reason: reason,
		Reference: reference, CreatedAt: s.now(),
	}
	if err := putJSON(tx.Bucket(ledgerBucket), []byte(entryID), entry); err != nil {
		return err
	}
	if err := references.Put([]byte(reference), []byte(entryID)); err != nil {
		return err
	}
	balances := tx.Bucket(balancesBucket)
	balance := decodeInt64(balances.Get([]byte(accountID))) + delta
	if balance < 0 {
		return ErrInsufficientCredits
	}
	return balances.Put([]byte(accountID), encodeInt64(balance))
}

func validateAccount(ctx context.Context, accountID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(accountID) == "" {
		return errors.New("account id is required")
	}
	return nil
}

func putJSON(bucket *bolt.Bucket, key []byte, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket.Put(key, encoded)
}

func encodeInt64(value int64) []byte {
	buffer := make([]byte, 8)
	binary.BigEndian.PutUint64(buffer, uint64(value))
	return buffer
}

func decodeInt64(value []byte) int64 {
	if len(value) != 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(value))
}

func randomID(prefix string) (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buffer), nil
}
