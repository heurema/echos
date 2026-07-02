package identity

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
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
