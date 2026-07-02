package envelope

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

// maliciousEnvelope hand-builds a tar.gz with an unsafe entry path,
// bypassing Build's normal (safe) path handling, to exercise Open's own
// defenses against a hostile sender.
func maliciousEnvelope(t *testing.T, sender testParty, recipient testParty, badPath string) []byte {
	t.Helper()

	fileContent := []byte("payload")
	sum := sha256.Sum256(fileContent)
	manifest := Manifest{
		Version:      1,
		Tool:         "claude",
		SessionID:    "a3f1c9",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		SenderEchoID: echoIDForTest(sender.pub),
		Files: []ManifestFile{
			{Path: badPath, Size: int64(len(fileContent)), SHA256: hex.EncodeToString(sum[:])},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := sender.signer.Sign(rand.Reader, manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	sigBytes := ssh.Marshal(sig)

	var tarBuf bytes.Buffer
	gz := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gz)
	write := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", manifestBytes)
	write("signature.sig", sigBytes)
	write(badPath, fileContent)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	ageRecipient, err := agessh.NewEd25519Recipient(recipient.pub)
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext bytes.Buffer
	w, err := age.Encrypt(&ciphertext, ageRecipient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	out := make([]byte, 0, HeaderLen+ciphertext.Len())
	out = append(out, Magic...)
	out = append(out, Version)
	out = append(out, ciphertext.Bytes()...)
	return out
}

func echoIDForTest(pub ssh.PublicKey) string {
	sum := sha256.Sum256(pub.Marshal())
	return hex.EncodeToString(sum[:])[:20]
}

// TestOpenRejectsUnsafeArchivePaths: Open refuses to unpack (and returns no
// partial result) when a packaged entry has an absolute path or a ".."
// traversal component.
func TestOpenRejectsUnsafeArchivePaths(t *testing.T) {
	sender := newParty(t)
	recipient := newParty(t)

	cases := []string{
		"../../../etc/passwd",
		"/etc/passwd",
		"a3f1c9/../../evil",
		"..",
	}

	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			blob := maliciousEnvelope(t, sender, recipient, bad)
			id, err := AgeIdentity(recipient.priv)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := Open(blob, id)
			if err == nil {
				t.Fatalf("expected Open to reject unsafe path %q, got %+v", bad, opened)
			}
			if opened != nil {
				t.Fatalf("expected nil Opened on rejection, got %+v", opened)
			}
		})
	}
}
