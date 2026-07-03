package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestEnsureCreatesFreshIdentity(t *testing.T) {
	dir := t.TempDir()

	if Exists(dir) {
		t.Fatalf("identity should not exist yet")
	}

	id, created, err := Ensure(dir, "")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true on first Ensure")
	}
	if len(id.EchoID) != EchoIDLen {
		t.Fatalf("echo-id length = %d, want %d", len(id.EchoID), EchoIDLen)
	}
	if id.Fingerprint[:EchoIDLen] != id.EchoID {
		t.Fatalf("echo-id must be the first %d chars of the fingerprint", EchoIDLen)
	}

	info, err := os.Stat(keyPath(dir))
	if err != nil {
		t.Fatalf("stat identity: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity perms = %v, want 0600", info.Mode().Perm())
	}

	id2, created2, err := Ensure(dir, "")
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if created2 {
		t.Fatalf("second Ensure should not create a new identity")
	}
	if id2.EchoID != id.EchoID {
		t.Fatalf("echo-id changed across Ensure calls: %s vs %s", id.EchoID, id2.EchoID)
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	dir := t.TempDir()
	id, _, err := Ensure(dir, "")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	got := Fingerprint(id.Signer.PublicKey())
	if got != id.Fingerprint {
		t.Fatalf("Fingerprint(pub) = %s, want %s", got, id.Fingerprint)
	}
}

func TestFriendBookRoundTrip(t *testing.T) {
	dir := t.TempDir()
	book, err := LoadFriends(dir)
	if err != nil {
		t.Fatalf("LoadFriends on missing file: %v", err)
	}
	if len(book.Friends) != 0 {
		t.Fatalf("expected empty book, got %d friends", len(book.Friends))
	}

	id, _, err := Ensure(t.TempDir(), "")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	pub := id.Signer.PublicKey()

	book.Upsert(Friend{
		Name:        "bob",
		EchoID:      id.EchoID,
		Fingerprint: id.Fingerprint,
		PublicKey:   b64(pub.Marshal()),
	})
	if err := book.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadFriends(dir)
	if err != nil {
		t.Fatalf("LoadFriends: %v", err)
	}
	f, ok := reloaded.Find("bob")
	if !ok {
		t.Fatalf("bob not found after reload")
	}
	if f.EchoID != id.EchoID {
		t.Fatalf("echo id mismatch after reload")
	}
	got, err := f.SSHPublicKey()
	if err != nil {
		t.Fatalf("SSHPublicKey: %v", err)
	}
	if string(got.Marshal()) != string(pub.Marshal()) {
		t.Fatalf("public key mismatch after reload")
	}

	_, ok = reloaded.FindByEchoID(id.EchoID)
	if !ok {
		t.Fatalf("FindByEchoID failed")
	}
}

func TestFriendBookRemove(t *testing.T) {
	dir := t.TempDir()
	book, err := LoadFriends(dir)
	if err != nil {
		t.Fatalf("LoadFriends on missing file: %v", err)
	}

	book.Upsert(Friend{Name: "alice", EchoID: "alice-echo-id"})
	book.Upsert(Friend{Name: "bob", EchoID: "bob-echo-id"})
	book.Upsert(Friend{Name: "carol", EchoID: "carol-echo-id"})

	removed, ok := book.Remove("bob")
	if !ok {
		t.Fatalf("Remove(bob) = false, want true")
	}
	if removed.EchoID != "bob-echo-id" {
		t.Fatalf("Remove(bob) returned wrong friend: %+v", removed)
	}
	if len(book.Friends) != 2 {
		t.Fatalf("expected 2 friends after remove, got %d", len(book.Friends))
	}
	if book.Friends[0].Name != "alice" || book.Friends[1].Name != "carol" {
		t.Fatalf("Remove did not preserve order of remaining friends: %+v", book.Friends)
	}

	if err := book.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := LoadFriends(dir)
	if err != nil {
		t.Fatalf("LoadFriends: %v", err)
	}
	if _, ok := reloaded.Find("bob"); ok {
		t.Fatalf("bob still present after reload")
	}

	_, ok = book.Remove("nobody")
	if ok {
		t.Fatalf("Remove(nobody) = true, want false")
	}
	if len(book.Friends) != 2 {
		t.Fatalf("Remove of a missing name changed the book: %+v", book.Friends)
	}
}

func TestRelayURLResolution(t *testing.T) {
	dir := t.TempDir()
	os.Unsetenv("ECHOS_RELAY")
	if got := RelayURL(dir); got != defaultRelayURL {
		t.Fatalf("RelayURL = %s, want default %s", got, defaultRelayURL)
	}

	cfg := `{"relay_url":"https://relay.example"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RelayURL(dir); got != "https://relay.example" {
		t.Fatalf("RelayURL = %s, want config value", got)
	}

	t.Setenv("ECHOS_RELAY", "https://env.example")
	if got := RelayURL(dir); got != "https://env.example" {
		t.Fatalf("RelayURL = %s, want env override", got)
	}
}

func b64(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

// TestIdentityExternalKeyUnencryptedReuse: Ensure with --key pointing at an
// unencrypted ed25519 SSH private key reuses that exact key (not a freshly
// generated one), and persists it so a later Ensure with no --key loads the
// same identity.
func TestIdentityExternalKeyUnencryptedReuse(t *testing.T) {
	dir := t.TempDir()
	keyDir := t.TempDir()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test-key")
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	id, created, err := Ensure(dir, keyPath)
	if err != nil {
		t.Fatalf("Ensure with external unencrypted key: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true on first Ensure")
	}
	wantSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if id.Fingerprint != Fingerprint(wantSigner.PublicKey()) {
		t.Fatalf("identity does not reuse the supplied external key")
	}
	if id.PrivateKey == nil || !id.PrivateKey.Equal(priv) {
		t.Fatalf("identity private key does not match the supplied external key")
	}

	id2, created2, err := Ensure(dir, "")
	if err != nil {
		t.Fatalf("second Ensure (no --key): %v", err)
	}
	if created2 {
		t.Fatalf("second Ensure should load the persisted identity, not create a new one")
	}
	if id2.Fingerprint != id.Fingerprint {
		t.Fatalf("fingerprint changed across Ensure calls: %s vs %s", id.Fingerprint, id2.Fingerprint)
	}
}

// TestIdentityExternalKeyRejectsNonEd25519: Ensure with --key pointing at a
// non-ed25519 key (RSA) is rejected, and no identity is persisted.
func TestIdentityExternalKeyRejectsNonEd25519(t *testing.T) {
	dir := t.TempDir()
	keyDir := t.TempDir()

	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(rsaPriv, "test-rsa-key")
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	keyPath := filepath.Join(keyDir, "id_rsa")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Ensure(dir, keyPath); err == nil {
		t.Fatalf("expected Ensure to reject a non-ed25519 external key")
	}
	if Exists(dir) {
		t.Fatalf("no identity should be persisted when the external key is rejected")
	}
}

// TestIdentityExternalKeySSHAgentFallback: Ensure with --key pointing at a
// passphrase-protected ed25519 key (never prompted for) falls back to
// ssh-agent, matched by the key's public counterpart, and produces an
// identity that can sign but holds no raw private key material locally.
func TestIdentityExternalKeySSHAgentFallback(t *testing.T) {
	dir := t.TempDir()
	keyDir := t.TempDir()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test-key", []byte("s3cret"))
	if err != nil {
		t.Fatalf("MarshalPrivateKeyWithPassphrase: %v", err)
	}
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath+".pub", ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644); err != nil {
		t.Fatal(err)
	}

	// A short-lived dir outside t.TempDir(): unix socket paths are capped
	// well below what a nested per-subtest temp dir can produce.
	sockDir, err := os.MkdirTemp("", "echos-agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "a.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv, Comment: "test-key"}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(keyring, conn)
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sockPath)

	id, created, err := Ensure(dir, keyPath)
	if err != nil {
		t.Fatalf("Ensure with passphrase-protected key + ssh-agent: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true on first Ensure")
	}
	if id.Fingerprint != Fingerprint(signer.PublicKey()) {
		t.Fatalf("identity fingerprint does not match the agent-backed key")
	}
	if id.PrivateKey != nil {
		t.Fatalf("agent-backed identity should hold no raw private key material")
	}

	msg := []byte("hello")
	sig, err := id.Signer.Sign(rand.Reader, msg)
	if err != nil {
		t.Fatalf("sign via agent-backed signer: %v", err)
	}
	if err := signer.PublicKey().Verify(msg, sig); err != nil {
		t.Fatalf("agent-produced signature does not verify: %v", err)
	}
}
