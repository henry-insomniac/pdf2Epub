package abuse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

func TestALTCHAIssuesVerifiesAndRejectsReplay(t *testing.T) {
	provider, err := NewALTCHA([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewALTCHA(): %v", err)
	}
	provider.cost = 1
	provider.counterMin = 2
	provider.counterMax = 2
	challengeValue, err := provider.Issue(context.Background())
	if err != nil {
		t.Fatalf("Issue(): %v", err)
	}
	challenge := challengeValue.(altcha.Challenge)
	solution, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{
		Challenge: challenge,
		DeriveKey: altcha.DeriveKeyPBKDF2(),
	})
	if err != nil || solution == nil {
		t.Fatalf("SolveChallenge() = %#v, %v", solution, err)
	}
	payload, err := json.Marshal(altcha.Payload{Challenge: challenge, Solution: *solution})
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	token := base64.StdEncoding.EncodeToString(payload)
	if err := provider.Verify(context.Background(), token, "127.0.0.1"); err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	reformatted, err := json.MarshalIndent(altcha.Payload{Challenge: challenge, Solution: *solution}, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(): %v", err)
	}
	if err := provider.Verify(context.Background(), string(reformatted), "127.0.0.1"); !errors.Is(err, ErrChallengeRejected) {
		t.Fatalf("replayed Verify() error = %v, want ErrChallengeRejected", err)
	}
}

func TestALTCHARejectsExpiredChallenge(t *testing.T) {
	provider, err := NewALTCHA([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewALTCHA(): %v", err)
	}
	provider.cost = 1
	provider.counterMin = 1
	provider.counterMax = 1
	provider.ttl = time.Millisecond
	challengeValue, err := provider.Issue(context.Background())
	if err != nil {
		t.Fatalf("Issue(): %v", err)
	}
	challenge := challengeValue.(altcha.Challenge)
	solution, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: challenge, DeriveKey: altcha.DeriveKeyPBKDF2()})
	if err != nil {
		t.Fatalf("SolveChallenge(): %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	payload, _ := json.Marshal(altcha.Payload{Challenge: challenge, Solution: *solution})
	if err := provider.Verify(context.Background(), string(payload), ""); !errors.Is(err, ErrChallengeRejected) {
		t.Fatalf("expired Verify() error = %v, want ErrChallengeRejected", err)
	}
}
