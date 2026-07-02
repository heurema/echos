package envelope

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type testParty struct {
	priv   ed25519.PrivateKey
	signer ssh.Signer
	pub    ssh.PublicKey
}

func newParty(t *testing.T) testParty {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	return testParty{priv: priv, signer: signer, pub: signer.PublicKey()}
}

func buildTestEnvelope(t *testing.T, sender, recipient testParty, files map[string][]byte) []byte {
	t.Helper()
	in := BuildInput{
		Tool:              "claude",
		SessionID:         "a3f1c9",
		Project:           "/Users/alice/repos/demo",
		Title:             "Fix acp backend timeout",
		CreatedAt:         time.Date(2026, 7, 1, 20, 15, 0, 0, time.UTC),
		SenderEchoID:      "abcdef0123456789abcd",
		SenderFingerprint: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
		Files:             files,
	}
	out, err := Build(in, sender.signer, recipient.pub)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return out
}

// TestEnvelopeRoundTrip: pack->sign->encrypt followed by
// decrypt->unpack->verify reproduces the exact native session bytes.
func TestEnvelopeRoundTrip(t *testing.T) {
	sender := newParty(t)
	recipient := newParty(t)
	files := map[string][]byte{
		"a3f1c9.jsonl": []byte(`{"hello":"world"}` + "\n"),
	}
	blob := buildTestEnvelope(t, sender, recipient, files)

	id, err := AgeIdentity(recipient.priv)
	if err != nil {
		t.Fatalf("AgeIdentity: %v", err)
	}
	opened, err := Open(blob, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := opened.VerifyFiles(); err != nil {
		t.Fatalf("VerifyFiles: %v", err)
	}
	if err := opened.VerifySignature(sender.pub); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}

	if !bytes.Equal(opened.Files["a3f1c9.jsonl"], files["a3f1c9.jsonl"]) {
		t.Fatalf("round-tripped bytes do not match original")
	}
	if opened.Manifest.Tool != "claude" || opened.Manifest.SessionID != "a3f1c9" {
		t.Fatalf("manifest fields not preserved: %+v", opened.Manifest)
	}
}

func TestEnvelopeTamperedBlobFailsToOpen(t *testing.T) {
	sender := newParty(t)
	recipient := newParty(t)
	blob := buildTestEnvelope(t, sender, recipient, map[string][]byte{
		"a3f1c9.jsonl": []byte("hello"),
	})

	tampered := append([]byte(nil), blob...)
	// Flip a byte well inside the age ciphertext.
	tampered[len(tampered)-1] ^= 0xFF

	id, err := AgeIdentity(recipient.priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(tampered, id); err == nil {
		t.Fatalf("expected tampered envelope to fail to open")
	}
}

func TestEnvelopeTamperedManifestFailsSignatureVerification(t *testing.T) {
	sender := newParty(t)
	recipient := newParty(t)
	blob := buildTestEnvelope(t, sender, recipient, map[string][]byte{
		"a3f1c9.jsonl": []byte("hello"),
	})

	id, err := AgeIdentity(recipient.priv)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(blob, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Simulate a manifest that was altered after signing.
	opened.Manifest.Title = "something else"
	opened.ManifestBytes = append([]byte(nil), opened.ManifestBytes...)
	opened.ManifestBytes[0] ^= 0xFF

	if err := opened.VerifySignature(sender.pub); err == nil {
		t.Fatalf("expected signature verification to fail on tampered manifest bytes")
	}
}

func TestEnvelopeWrongRecipientKeyFailsToOpen(t *testing.T) {
	sender := newParty(t)
	recipient := newParty(t)
	stranger := newParty(t)
	blob := buildTestEnvelope(t, sender, recipient, map[string][]byte{
		"a3f1c9.jsonl": []byte("hello"),
	})

	id, err := AgeIdentity(stranger.priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(blob, id); err == nil {
		t.Fatalf("expected open with wrong recipient key to fail")
	}
}

// TestSubagentsSubtreeRoundTrip: a Claude session with its optional
// <uuid>/subagents/ subtree round-trips losslessly.
func TestSubagentsSubtreeRoundTrip(t *testing.T) {
	sender := newParty(t)
	recipient := newParty(t)
	files := map[string][]byte{
		"a3f1c9.jsonl":                       []byte(`{"main":"transcript"}` + "\n"),
		"a3f1c9/subagents/agent-1.jsonl":     []byte(`{"sub":"agent-1"}` + "\n"),
		"a3f1c9/subagents/agent-1.meta.json": []byte(`{"meta":true}`),
		"a3f1c9/subagents/agent-2.jsonl":     []byte(`{"sub":"agent-2"}` + "\n"),
	}
	blob := buildTestEnvelope(t, sender, recipient, files)

	id, err := AgeIdentity(recipient.priv)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(blob, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := opened.VerifyFiles(); err != nil {
		t.Fatalf("VerifyFiles: %v", err)
	}
	if err := opened.VerifySignature(sender.pub); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	if len(opened.Files) != len(files) {
		t.Fatalf("got %d files, want %d", len(opened.Files), len(files))
	}
	for path, want := range files {
		got, ok := opened.Files[path]
		if !ok {
			t.Fatalf("missing file %s after round trip", path)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("file %s content mismatch", path)
		}
	}
}

// TestEnvelopeHeaderAndInternalSignature: the header is readable without
// age decryption, and nothing sender-identifying is recoverable without it.
func TestEnvelopeHeaderAndInternalSignature(t *testing.T) {
	sender := newParty(t)
	recipient := newParty(t)
	blob := buildTestEnvelope(t, sender, recipient, map[string][]byte{
		"a3f1c9.jsonl": []byte("hello"),
	})

	if len(blob) < HeaderLen {
		t.Fatalf("blob too short")
	}
	if string(blob[:4]) != Magic {
		t.Fatalf("magic bytes = %q, want %q", blob[:4], Magic)
	}
	if blob[4] != Version {
		t.Fatalf("version byte = %d, want %d", blob[4], Version)
	}

	v, ciphertext, err := ParseHeader(blob)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if v != Version {
		t.Fatalf("ParseHeader version = %d", v)
	}
	// The remaining bytes are the age ciphertext; nothing sender-related
	// should be visible in it as plaintext.
	if bytes.Contains(ciphertext, []byte(sender.pub.Marshal())) {
		t.Fatalf("sender public key leaked into ciphertext")
	}
	if bytes.Contains(blob, []byte("Fix acp backend timeout")) {
		t.Fatalf("title leaked outside the ciphertext")
	}

	// Without decrypting, the manifest/signature must not be recoverable.
	if bytes.Contains(ciphertext, []byte("manifest.json")) {
		t.Fatalf("tar entry names leaked in cleartext")
	}

	// Wrong-length or bad-magic headers are rejected.
	if _, _, err := ParseHeader([]byte("ECH")); err == nil {
		t.Fatalf("expected error for too-short header")
	}
	bad := append([]byte("XCHO"), byte(Version))
	if _, _, err := ParseHeader(bad); err == nil {
		t.Fatalf("expected error for bad magic")
	}
}
