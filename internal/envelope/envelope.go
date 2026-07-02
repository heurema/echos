// Package envelope implements the Envelope v1 wire format: a magic+version
// header followed by an age ciphertext of a gzip'd tar containing
// manifest.json, signature.sig, and the native session files.
package envelope

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

const (
	// Magic is the literal ASCII header prefix of every Envelope v1 blob.
	Magic = "ECHO"
	// Version is the current envelope format version.
	Version   byte = 1
	HeaderLen      = len(Magic) + 1
)

// BuildInput is everything needed to pack, sign, and encrypt one session.
type BuildInput struct {
	Tool              string
	SessionID         string
	Project           string
	Title             string
	CreatedAt         time.Time
	SenderEchoID      string
	SenderFingerprint string
	Files             map[string][]byte // relative path -> content
}

// Build packs the session into manifest.json + signature.sig + native
// files, tars and gzips them, encrypts to the recipient's key, and prepends
// the Envelope v1 header. The manifest is signed by signer over its own
// serialized bytes.
func Build(in BuildInput, signer ssh.Signer, recipient ssh.PublicKey) ([]byte, error) {
	manifest := Manifest{
		Version:           1,
		Tool:              in.Tool,
		SessionID:         in.SessionID,
		Project:           in.Project,
		Title:             in.Title,
		SenderEchoID:      in.SenderEchoID,
		SenderFingerprint: in.SenderFingerprint,
		CreatedAt:         in.CreatedAt.UTC().Format(time.RFC3339),
	}

	paths := make([]string, 0, len(in.Files))
	for p := range in.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		data := in.Files[p]
		sum := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, ManifestFile{
			Path:   p,
			Size:   int64(len(data)),
			SHA256: hex.EncodeToString(sum[:]),
		})
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("envelope: marshal manifest: %w", err)
	}

	sig, err := signer.Sign(rand.Reader, manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("envelope: sign manifest: %w", err)
	}
	sigBytes := ssh.Marshal(sig)

	tarball, err := buildTarGz(manifestBytes, sigBytes, paths, in.Files)
	if err != nil {
		return nil, err
	}

	ageRecipient, err := agessh.NewEd25519Recipient(recipient)
	if err != nil {
		return nil, fmt.Errorf("envelope: recipient key: %w", err)
	}

	var ciphertext bytes.Buffer
	w, err := age.Encrypt(&ciphertext, ageRecipient)
	if err != nil {
		return nil, fmt.Errorf("envelope: age encrypt: %w", err)
	}
	if _, err := w.Write(tarball); err != nil {
		return nil, fmt.Errorf("envelope: write plaintext: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("envelope: finalize ciphertext: %w", err)
	}

	out := make([]byte, 0, HeaderLen+ciphertext.Len())
	out = append(out, Magic...)
	out = append(out, Version)
	out = append(out, ciphertext.Bytes()...)
	return out, nil
}

func buildTarGz(manifestBytes, sigBytes []byte, order []string, files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	write := func(name string, data []byte) error {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("envelope: tar header %s: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("envelope: tar write %s: %w", name, err)
		}
		return nil
	}

	if err := write("manifest.json", manifestBytes); err != nil {
		return nil, err
	}
	if err := write("signature.sig", sigBytes); err != nil {
		return nil, err
	}
	for _, p := range order {
		if err := write(p, files[p]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("envelope: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("envelope: close gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// ParseHeader validates the 5-byte magic+version header and returns the
// version byte and the remaining age ciphertext, without performing any
// decryption.
func ParseHeader(data []byte) (version byte, ciphertext []byte, err error) {
	if len(data) < HeaderLen {
		return 0, nil, errors.New("envelope: too short to contain a header")
	}
	if string(data[:len(Magic)]) != Magic {
		return 0, nil, errors.New("envelope: bad magic bytes")
	}
	return data[len(Magic)], data[HeaderLen:], nil
}

// AgeIdentity wraps a raw ed25519 private key as an age.Identity capable of
// decrypting Envelope v1 blobs addressed to the matching public key.
func AgeIdentity(priv ed25519.PrivateKey) (age.Identity, error) {
	if priv == nil {
		return nil, errors.New("envelope: no local private key available to decrypt (ssh-agent-backed identity)")
	}
	return agessh.NewEd25519Identity(priv)
}

// Opened is the result of decrypting and unpacking an envelope. File
// integrity (VerifyFiles) and sender authenticity (VerifySignature) are
// separate steps left to the caller, since not every use (e.g. inbox
// listing) needs both.
type Opened struct {
	Manifest      *Manifest
	ManifestBytes []byte
	Signature     []byte
	Files         map[string][]byte
}

// Open validates the header, age-decrypts, and safely unpacks the tar.gz
// payload. Any archive entry with an absolute path or a ".." traversal
// component aborts the whole operation before anything is returned.
func Open(data []byte, identity age.Identity) (*Opened, error) {
	v, ciphertext, err := ParseHeader(data)
	if err != nil {
		return nil, err
	}
	if v != Version {
		return nil, fmt.Errorf("envelope: unsupported version %d", v)
	}

	plain, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return nil, fmt.Errorf("envelope: age decrypt: %w", err)
	}
	plainBytes, err := io.ReadAll(plain)
	if err != nil {
		return nil, fmt.Errorf("envelope: read plaintext: %w", err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(plainBytes))
	if err != nil {
		return nil, fmt.Errorf("envelope: gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("envelope: read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !safeTarPath(hdr.Name) {
			return nil, fmt.Errorf("envelope: unsafe archive path %q", hdr.Name)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("envelope: read tar entry %s: %w", hdr.Name, err)
		}
		files[hdr.Name] = content
	}

	manifestBytes, ok := files["manifest.json"]
	if !ok {
		return nil, errors.New("envelope: missing manifest.json")
	}
	sigBytes, ok := files["signature.sig"]
	if !ok {
		return nil, errors.New("envelope: missing signature.sig")
	}
	delete(files, "manifest.json")
	delete(files, "signature.sig")

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("envelope: parse manifest.json: %w", err)
	}

	return &Opened{
		Manifest:      &manifest,
		ManifestBytes: manifestBytes,
		Signature:     sigBytes,
		Files:         files,
	}, nil
}

// safeTarPath rejects absolute paths and any ".." traversal component.
func safeTarPath(name string) bool {
	if name == "" || path.IsAbs(name) {
		return false
	}
	clean := path.Clean(name)
	if clean != name {
		return false
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	return true
}

// VerifyFiles recomputes SHA-256 over every unpacked native file and
// confirms it matches the corresponding manifest entry.
func (o *Opened) VerifyFiles() error {
	manifestPaths := make(map[string]bool, len(o.Manifest.Files))
	for _, mf := range o.Manifest.Files {
		manifestPaths[mf.Path] = true
		data, ok := o.Files[mf.Path]
		if !ok {
			return fmt.Errorf("envelope: missing packaged file %s", mf.Path)
		}
		if int64(len(data)) != mf.Size {
			return fmt.Errorf("envelope: size mismatch for %s", mf.Path)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != mf.SHA256 {
			return fmt.Errorf("envelope: checksum mismatch for %s", mf.Path)
		}
	}
	// Fail closed on any unpacked file the signed manifest does not list, so an
	// unmanifested entry can never be installed unverified (the signature only
	// binds manifest.json, so the full file set must reconcile against it).
	for name := range o.Files {
		if !manifestPaths[name] {
			return fmt.Errorf("envelope: unmanifested file %s", name)
		}
	}
	return nil
}

// VerifySignature verifies signature.sig against manifest.json's raw bytes
// using the sender's public key.
func (o *Opened) VerifySignature(pub ssh.PublicKey) error {
	var sig ssh.Signature
	if err := ssh.Unmarshal(o.Signature, &sig); err != nil {
		return fmt.Errorf("envelope: parse signature: %w", err)
	}
	if err := pub.Verify(o.ManifestBytes, &sig); err != nil {
		return fmt.Errorf("envelope: signature verification failed: %w", err)
	}
	return nil
}
