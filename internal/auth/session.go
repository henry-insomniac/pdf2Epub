package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Session struct {
	Token     string
	Username  string
	CSRFToken string
	ExpiresAt time.Time
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
		CSRFToken: csrfToken,
		ExpiresAt: m.now().Add(m.ttl),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	m.sessions[m.hashToken(token)] = session
	return session, nil
}

func (m *Manager) Validate(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	session, ok := m.sessions[m.hashToken(token)]
	return session, ok
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
