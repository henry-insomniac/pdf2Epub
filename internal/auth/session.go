package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Session struct {
	Token     string
	Username  string
	SubjectID string
	Role      string
	CSRFToken string
	ExpiresAt time.Time
}

const (
	RoleAdmin = "admin"
	RoleGuest = "guest"
)

type guestClaims struct {
	SubjectID string `json:"sub"`
	CSRFToken string `json:"csrf"`
	ExpiresAt int64  `json:"exp"`
}

type Manager struct {
	mu       sync.Mutex
	username string
	password string
	ttl      time.Duration
	sessions map[[32]byte]Session
	now      func() time.Time
	secret   []byte
}

func NewManager(username, password string, ttl time.Duration, secret ...[]byte) *Manager {
	key := make([]byte, 32)
	if len(secret) > 0 && len(secret[0]) > 0 {
		key = append(key[:0], secret[0]...)
	} else {
		_, _ = rand.Read(key)
	}
	return &Manager{
		username: username,
		password: password,
		ttl:      ttl,
		sessions: make(map[[32]byte]Session),
		now:      func() time.Time { return time.Now().UTC() },
		secret:   key,
	}
}

func (m *Manager) Login(username, password string) (Session, error) {
	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(m.username))
	passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(m.password))
	if usernameOK&passwordOK != 1 {
		return Session{}, ErrInvalidCredentials
	}
	token, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	session := Session{
		Token:     token,
		Username:  m.username,
		SubjectID: "admin:" + m.username,
		Role:      RoleAdmin,
		CSRFToken: csrfToken,
		ExpiresAt: m.now().Add(m.ttl),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	m.sessions[m.hashToken(token)] = session
	return session, nil
}

// CreateGuest creates a signed, stateless guest session. The stable subject ID
// lets a browser recover its credit balance after a service restart without
// exposing a reusable password or storing the raw session token server-side.
func (m *Manager) CreateGuest() (Session, error) {
	subjectToken, err := randomToken(18)
	if err != nil {
		return Session{}, err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	claims := guestClaims{
		SubjectID: "guest:" + subjectToken,
		CSRFToken: csrfToken,
		ExpiresAt: m.now().Add(m.ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return Session{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := m.signGuestPayload(encoded)
	return Session{
		Token:     "g." + encoded + "." + signature,
		Username:  "访客",
		SubjectID: claims.SubjectID,
		Role:      RoleGuest,
		CSRFToken: claims.CSRFToken,
		ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
	}, nil
}

func (m *Manager) Validate(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	if strings.HasPrefix(token, "g.") {
		return m.validateGuest(token)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	session, ok := m.sessions[m.hashToken(token)]
	return session, ok
}

func (m *Manager) validateGuest(token string) (Session, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "g" {
		return Session{}, false
	}
	expected := m.signGuestPayload(parts[1])
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expected)) != 1 {
		return Session{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Session{}, false
	}
	var claims guestClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.SubjectID == "" || claims.CSRFToken == "" {
		return Session{}, false
	}
	expiresAt := time.Unix(claims.ExpiresAt, 0).UTC()
	if !m.now().Before(expiresAt) {
		return Session{}, false
	}
	return Session{
		Token:     token,
		Username:  "访客",
		SubjectID: claims.SubjectID,
		Role:      RoleGuest,
		CSRFToken: claims.CSRFToken,
		ExpiresAt: expiresAt,
	}, true
}

func (m *Manager) Logout(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, m.hashToken(token))
}

func (m *Manager) deleteExpiredLocked() {
	now := m.now()
	for key, session := range m.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(m.sessions, key)
		}
	}
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (m *Manager) hashToken(token string) [32]byte {
	hash := hmac.New(sha256.New, m.secret)
	_, _ = hash.Write([]byte(token))
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func (m *Manager) signGuestPayload(payload string) string {
	hash := hmac.New(sha256.New, m.secret)
	_, _ = hash.Write([]byte("guest-session:" + payload))
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}
