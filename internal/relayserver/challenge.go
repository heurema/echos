package relayserver

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var (
	ErrChallengeUnknown  = errors.New("relayserver: unknown or already-used nonce")
	ErrChallengeExpired  = errors.New("relayserver: nonce expired")
	ErrChallengeWrongFPR = errors.New("relayserver: nonce issued for a different fingerprint")
)

type challengeRecord struct {
	fpr       string
	expiresAt time.Time
}

// ChallengeStore issues short-lived, single-use nonces used to authenticate
// reads (GET /mailbox, GET /blob) via a signature challenge.
type ChallengeStore struct {
	mu      sync.Mutex
	byNonce map[string]challengeRecord
	ttl     time.Duration
}

func NewChallengeStore(ttl time.Duration) *ChallengeStore {
	return &ChallengeStore{byNonce: map[string]challengeRecord{}, ttl: ttl}
}

// Issue creates a new nonce bound to fpr, valid until now+ttl.
func (c *ChallengeStore) Issue(fpr string, now time.Time) (nonce string, expiresAt time.Time, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	nonce = base64.StdEncoding.EncodeToString(raw)
	expiresAt = now.Add(c.ttl)

	c.mu.Lock()
	c.sweepLocked(now)
	c.byNonce[nonce] = challengeRecord{fpr: fpr, expiresAt: expiresAt}
	c.mu.Unlock()
	return nonce, expiresAt, nil
}

// sweepLocked drops expired records so nonces that are issued but never
// consumed cannot grow the store without bound. Callers must hold c.mu.
func (c *ChallengeStore) sweepLocked(now time.Time) {
	for nonce, rec := range c.byNonce {
		if now.After(rec.expiresAt) {
			delete(c.byNonce, nonce)
		}
	}
}

// Consume validates and single-use-consumes nonce for fpr. The nonce is
// removed regardless of outcome, so a nonce authenticates at most once.
func (c *ChallengeStore) Consume(nonce, fpr string, now time.Time) error {
	c.mu.Lock()
	rec, ok := c.byNonce[nonce]
	if ok {
		delete(c.byNonce, nonce)
	}
	c.mu.Unlock()

	if !ok {
		return ErrChallengeUnknown
	}
	if now.After(rec.expiresAt) {
		return ErrChallengeExpired
	}
	if rec.fpr != fpr {
		return ErrChallengeWrongFPR
	}
	return nil
}
