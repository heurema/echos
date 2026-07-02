package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/heurema/echos/internal/relayserver"
)

func newParty(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(signer.PublicKey().Marshal())
	return signer, hex.EncodeToString(sum[:])[:20]
}

func newTestRelay(t *testing.T) *Client {
	t.Helper()
	store, err := relayserver.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := relayserver.New(store, relayserver.Config{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL)
}

func TestClientPublishFetchChallengeMailboxBlob(t *testing.T) {
	ctx := context.Background()
	c := newTestRelay(t)

	aliceSigner, aliceFPR := newParty(t)
	bobSigner, bobFPR := newParty(t)

	created, err := c.PublishKey(ctx, aliceFPR, aliceSigner.PublicKey())
	if err != nil || !created {
		t.Fatalf("PublishKey alice: created=%v err=%v", created, err)
	}
	created, err = c.PublishKey(ctx, aliceFPR, aliceSigner.PublicKey())
	if err != nil || created {
		t.Fatalf("re-publish should be idempotent: created=%v err=%v", created, err)
	}
	if _, err := c.PublishKey(ctx, bobFPR, bobSigner.PublicKey()); err != nil {
		t.Fatalf("PublishKey bob: %v", err)
	}

	fetched, err := c.GetKey(ctx, aliceFPR)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if !bytes.Equal(fetched.Marshal(), aliceSigner.PublicKey().Marshal()) {
		t.Fatalf("fetched key does not match published key")
	}

	if _, err := c.GetKey(ctx, "0000000000000000dead"); err == nil {
		t.Fatalf("expected error for unknown fingerprint")
	}

	// Alice sends bob an envelope.
	envelope := []byte("pretend-ciphertext")
	res, err := c.PostMailbox(ctx, bobFPR, envelope)
	if err != nil {
		t.Fatalf("PostMailbox: %v", err)
	}
	if res.TTL != 24*time.Hour {
		t.Fatalf("ttl = %v, want 24h default", res.TTL)
	}

	items, err := c.GetMailbox(ctx, bobFPR, bobSigner)
	if err != nil {
		t.Fatalf("GetMailbox: %v", err)
	}
	if len(items) != 1 || items[0].ID != res.ID {
		t.Fatalf("GetMailbox = %+v, want one item with id %s", items, res.ID)
	}

	blob, err := c.GetBlob(ctx, bobFPR, res.ID, bobSigner)
	if err != nil {
		t.Fatalf("GetBlob: %v", err)
	}
	if !bytes.Equal(blob, envelope) {
		t.Fatalf("GetBlob content mismatch")
	}

	// Alice cannot read bob's mailbox or blob.
	if _, err := c.GetMailbox(ctx, bobFPR, aliceSigner); err == nil {
		t.Fatalf("expected alice to be rejected from bob's mailbox")
	}
	if _, err := c.GetBlob(ctx, bobFPR, res.ID, aliceSigner); err == nil {
		t.Fatalf("expected alice to be rejected from bob's blob")
	}
}
