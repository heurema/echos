// Package identity manages the local echos identity: an ed25519 key pair
// used to sign envelopes and to authenticate to the relay, plus the derived
// fingerprint and echo-id used to address a person.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// EchoIDLen is the number of hex characters (80 bits) of the fingerprint
// used as the echo-id, and as the relay's {fpr} path parameter.
const EchoIDLen = 20

// Identity is a local ed25519 signing identity. PrivateKey is nil when the
// identity is backed by ssh-agent (no raw key material available locally);
// such an identity can sign but cannot decrypt age ciphertexts.
type Identity struct {
	Signer      ssh.Signer
	PrivateKey  ed25519.PrivateKey
	Fingerprint string
	EchoID      string
}

// ConfigDir returns ~/.config/echos, honoring $HOME so tests can redirect it.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "echos"), nil
}

func keyPath(dir string) string    { return filepath.Join(dir, "identity") }
func pubKeyPath(dir string) string { return filepath.Join(dir, "identity.pub") }

// Fingerprint returns the lowercase hex-encoded SHA-256 digest of the
// public key's SSH wire-format bytes.
func Fingerprint(pub ssh.PublicKey) string {
	sum := sha256.Sum256(pub.Marshal())
	return hex.EncodeToString(sum[:])
}

// EchoID returns the first EchoIDLen hex characters of Fingerprint(pub).
func EchoID(pub ssh.PublicKey) string {
	return Fingerprint(pub)[:EchoIDLen]
}

func fromSigner(s ssh.Signer, priv ed25519.PrivateKey) *Identity {
	pub := s.PublicKey()
	return &Identity{Signer: s, PrivateKey: priv, Fingerprint: Fingerprint(pub), EchoID: EchoID(pub)}
}

// Exists reports whether an identity has already been created at dir.
func Exists(dir string) bool {
	_, err := os.Stat(pubKeyPath(dir))
	return err == nil
}

// Ensure loads the identity at dir, lazily creating one if none exists.
// If keyPath is non-empty and no identity exists yet, the given SSH private
// key is reused instead of generating a fresh one. created reports whether
// a new identity was created by this call.
func Ensure(dir, externalKeyPath string) (id *Identity, created bool, err error) {
	if Exists(dir) {
		id, err = load(dir)
		return id, false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, fmt.Errorf("create %s: %w", dir, err)
	}
	if externalKeyPath != "" {
		id, err = createFromExternalKey(dir, externalKeyPath)
	} else {
		id, err = createFresh(dir)
	}
	if err != nil {
		return nil, false, err
	}
	return id, true, nil
}

func createFresh(dir string) (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("wrap ed25519 key: %w", err)
	}
	if err := persistPrivateKey(dir, priv); err != nil {
		return nil, err
	}
	if err := persistPublicKey(dir, signer.PublicKey()); err != nil {
		return nil, err
	}
	_ = pub
	return fromSigner(signer, priv), nil
}

func createFromExternalKey(dir, externalKeyPath string) (*Identity, error) {
	raw, err := os.ReadFile(externalKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", externalKeyPath, err)
	}

	parsed, parseErr := ssh.ParseRawPrivateKey(raw)
	if parseErr == nil {
		edKey, ok := parsed.(*ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("unsupported key type at %s: only ed25519 keys are supported", externalKeyPath)
		}
		signer, err := ssh.NewSignerFromKey(*edKey)
		if err != nil {
			return nil, fmt.Errorf("wrap key %s: %w", externalKeyPath, err)
		}
		if err := persistPrivateKey(dir, *edKey); err != nil {
			return nil, err
		}
		if err := persistPublicKey(dir, signer.PublicKey()); err != nil {
			return nil, err
		}
		return fromSigner(signer, *edKey), nil
	}

	if _, missingPass := parseErr.(*ssh.PassphraseMissingError); !missingPass {
		return nil, fmt.Errorf("parse key %s: %w", externalKeyPath, parseErr)
	}

	// Passphrase-protected: never prompt (no interactivity). Fall back to
	// ssh-agent, matched by the key's public counterpart.
	signer, err := agentSignerFor(externalKeyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("key %s is passphrase-protected: %w; supply an unencrypted ed25519 key or load it into ssh-agent", externalKeyPath, err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, fmt.Errorf("unsupported key type at %s: only ed25519 keys are supported", externalKeyPath)
	}
	// The raw private key is not available (it lives in the agent); persist
	// only the public half. A future load() re-attaches via ssh-agent.
	if err := persistPublicKey(dir, signer.PublicKey()); err != nil {
		return nil, err
	}
	return fromSigner(signer, nil), nil
}

func agentSignerFor(pubPath string) (ssh.Signer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK is not set")
	}
	wantRaw, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pubPath, err)
	}
	want, _, _, _, err := ssh.ParseAuthorizedKey(wantRaw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", pubPath, err)
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("dial ssh-agent: %w", err)
	}
	client := agent.NewClient(conn)
	signers, err := client.Signers()
	if err != nil {
		return nil, fmt.Errorf("list ssh-agent signers: %w", err)
	}
	for _, s := range signers {
		if string(s.PublicKey().Marshal()) == string(want.Marshal()) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no matching key loaded in ssh-agent")
}

func load(dir string) (*Identity, error) {
	if _, err := os.Stat(keyPath(dir)); err == nil {
		raw, err := os.ReadFile(keyPath(dir))
		if err != nil {
			return nil, fmt.Errorf("read identity: %w", err)
		}
		parsed, err := ssh.ParseRawPrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("parse identity: %w", err)
		}
		edKey, ok := parsed.(*ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("identity at %s is not an ed25519 key", keyPath(dir))
		}
		signer, err := ssh.NewSignerFromKey(*edKey)
		if err != nil {
			return nil, fmt.Errorf("wrap identity: %w", err)
		}
		return fromSigner(signer, *edKey), nil
	}
	// Agent-backed identity: only the public key was persisted.
	signer, err := agentSignerFor(pubKeyPath(dir))
	if err != nil {
		return nil, fmt.Errorf("load agent-backed identity: %w", err)
	}
	return fromSigner(signer, nil), nil
}

func persistPrivateKey(dir string, priv ed25519.PrivateKey) error {
	block, err := ssh.MarshalPrivateKey(priv, "echos")
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	if err := os.WriteFile(keyPath(dir), pem.EncodeToMemory(block), 0o600); err != nil {
		return fmt.Errorf("write identity: %w", err)
	}
	return nil
}

func persistPublicKey(dir string, pub ssh.PublicKey) error {
	line := ssh.MarshalAuthorizedKey(pub)
	if err := os.WriteFile(pubKeyPath(dir), line, 0o644); err != nil {
		return fmt.Errorf("write identity.pub: %w", err)
	}
	return nil
}
