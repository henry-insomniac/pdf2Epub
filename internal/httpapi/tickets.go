package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

type uploadTicket struct {
	ownerID   string
	expiresAt time.Time
}

type uploadTickets struct {
	mu      sync.Mutex
	tickets map[[32]byte]uploadTicket
	ttl     time.Duration
	now     func() time.Time
}

func newUploadTickets(ttl time.Duration) *uploadTickets {
	return &uploadTickets{
		tickets: make(map[[32]byte]uploadTicket),
		ttl:     ttl,
		now:     time.Now,
	}
}

func (t *uploadTickets) Issue(ownerID string) (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanupLocked()
	t.tickets[sha256.Sum256([]byte(token))] = uploadTicket{ownerID: ownerID, expiresAt: t.now().Add(t.ttl)}
	return token, nil
}

func (t *uploadTickets) Consume(ownerID, token string) bool {
	if token == "" {
		return false
	}
	key := sha256.Sum256([]byte(token))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanupLocked()
	ticket, ok := t.tickets[key]
	delete(t.tickets, key)
	return ok && ticket.ownerID == ownerID && t.now().Before(ticket.expiresAt)
}

func (t *uploadTickets) cleanupLocked() {
	now := t.now()
	for key, ticket := range t.tickets {
		if !now.Before(ticket.expiresAt) {
			delete(t.tickets, key)
		}
	}
}
