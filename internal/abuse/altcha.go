package abuse

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

type Issuer interface {
	Issue(context.Context) (any, error)
}

type ALTCHA struct {
	signatureSecret string
	keySecret       string
	cost            int
	counterMin      int
	counterMax      int
	ttl             time.Duration
	now             func() time.Time
	mu              sync.Mutex
	used            map[[32]byte]time.Time
}

func NewALTCHA(rootSecret []byte) (*ALTCHA, error) {
	if len(rootSecret) < 32 {
		return nil, errors.New("ALTCHA root secret must contain at least 32 bytes")
	}
	return &ALTCHA{
		signatureSecret: deriveALTCHASecret(rootSecret, "challenge-signature"),
		keySecret:       deriveALTCHASecret(rootSecret, "derived-key-signature"),
		cost:            5000,
		counterMin:      5000,
		counterMax:      10000,
		ttl:             2 * time.Minute,
		now:             func() time.Time { return time.Now().UTC() },
		used:            make(map[[32]byte]time.Time),
	}, nil
}

func (a *ALTCHA) Issue(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	counter, err := randomCounter(a.counterMin, a.counterMax)
	if err != nil {
		return nil, err
	}
	expiresAt := a.now().Add(a.ttl)
	challenge, err := altcha.CreateChallenge(altcha.CreateChallengeOptions{
		Algorithm:              "PBKDF2/SHA-256",
		Cost:                   a.cost,
		Counter:                &counter,
		DeriveKey:              altcha.DeriveKeyPBKDF2(),
		ExpiresAt:              &expiresAt,
		HMACSignatureSecret:    a.signatureSecret,
		HMACKeySignatureSecret: a.keySecret,
		KeyLength:              32,
	})
	if err != nil {
		return nil, err
	}
	return challenge, nil
}

func (a *ALTCHA) Verify(ctx context.Context, token, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := decodeALTCHAPayload(token)
	if err != nil {
		return ErrChallengeRejected
	}
	var payload altcha.Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ErrChallengeRejected
	}
	result, err := altcha.VerifySolution(altcha.VerifySolutionOptions{
		Challenge:              payload.Challenge,
		Solution:               payload.Solution,
		DeriveKey:              altcha.DeriveKeyPBKDF2(),
		HMACSignatureSecret:    a.signatureSecret,
		HMACKeySignatureSecret: a.keySecret,
	})
	if err != nil || !result.Verified {
		return ErrChallengeRejected
	}
	if payload.Challenge.Signature == "" {
		return ErrChallengeRejected
	}
	// A challenge is single-use regardless of whether an attacker changes the
	// JSON whitespace, property order or Base64 alphabet around the same proof.
	key := sha256.Sum256([]byte(payload.Challenge.Signature))
	now := a.now()
	expiresAt := time.Unix(payload.Challenge.Parameters.ExpiresAt, 0)
	a.mu.Lock()
	defer a.mu.Unlock()
	for candidate, expiry := range a.used {
		if !now.Before(expiry) {
			delete(a.used, candidate)
		}
	}
	if _, exists := a.used[key]; exists {
		return ErrChallengeRejected
	}
	a.used[key] = expiresAt
	return nil
}

func decodeALTCHAPayload(token string) ([]byte, error) {
	if token == "" {
		return nil, errors.New("ALTCHA payload is empty")
	}
	if token[0] == '{' {
		return []byte(token), nil
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(token); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("ALTCHA payload is not valid base64")
}

func deriveALTCHASecret(root []byte, purpose string) string {
	mac := hmac.New(sha256.New, root)
	_, _ = mac.Write([]byte("pdf2epub-altcha:" + purpose))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randomCounter(minimum, maximum int) (int, error) {
	if minimum <= 0 || maximum < minimum {
		return 0, errors.New("invalid ALTCHA counter range")
	}
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return 0, err
	}
	return minimum + int(binary.BigEndian.Uint64(buffer[:])%uint64(maximum-minimum+1)), nil
}
