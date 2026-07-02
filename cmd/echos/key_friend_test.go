package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/heurema/echos/internal/identity"
)

// TestKeyPublicationAndFriendResolution: identity creation publishes the
// key to the relay so a subsequent GET /keys/{fpr} returns it; friend add
// verifies the fetched key hashes to the given echo-id and refuses to store
// a friend when it does not.
func TestKeyPublicationAndFriendResolution(t *testing.T) {
	setupHome(t)
	relayURL, store := startTestRelayWithStore(t)

	idOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("id --json failed")
	}
	aliceID := assertJSONFields(t, idOut, "echo_id")["echo_id"].(string)

	client := relayClientForTest(relayURL)
	pub, err := client.GetKey(context.Background(), aliceID)
	if err != nil {
		t.Fatalf("GET /keys/{fpr} after identity creation: %v", err)
	}
	if identity.EchoID(pub) != aliceID {
		t.Fatalf("fetched key does not hash to the published echo-id")
	}

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)

	friendsPath := filepath.Join(bobHome, ".config", "echos", "friends.json")

	if _, stderr, code := run(t, "friend", "add", "alice", aliceID); code != 0 {
		t.Fatalf("friend add with correct echo-id should succeed: code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(friendsPath); err != nil {
		t.Fatalf("friends.json not written after successful friend add: %v", err)
	}
	os.Remove(friendsPath)

	// Register a real key under a fingerprint that does not actually hash
	// to it (a misbehaving/compromised relay), and confirm the client
	// catches the mismatch and refuses to store a friend.
	_, mismatchedPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mismatchedSigner, err := ssh.NewSignerFromKey(mismatchedPriv)
	if err != nil {
		t.Fatal(err)
	}
	const wrongFPR = "0000000000000000dead"
	if _, err := store.PutKey(wrongFPR, mismatchedSigner.PublicKey().Marshal()); err != nil {
		t.Fatal(err)
	}

	if _, stderr, code := run(t, "friend", "add", "mallory", wrongFPR); code == 0 {
		t.Fatalf("friend add with mismatched key/echo-id should fail, stderr=%s", stderr)
	}
	if _, err := os.Stat(friendsPath); !os.IsNotExist(err) {
		t.Fatalf("friend add with mismatched key must not store a friend")
	}
}
