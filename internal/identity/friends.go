package identity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

// Friend is a locally-stored alias for a person's echos identity.
type Friend struct {
	Name        string    `json:"name"`
	EchoID      string    `json:"echo_id"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"pubkey"` // base64 SSH wire-format bytes
	AddedAt     time.Time `json:"added_at"`
}

// PublicKey parses the friend's stored public key.
func (f Friend) SSHPublicKey() (ssh.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(f.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode pubkey for %s: %w", f.Name, err)
	}
	return ssh.ParsePublicKey(raw)
}

// FriendBook is the local address book, ~/.config/echos/friends.json.
type FriendBook struct {
	path    string
	Friends []Friend `json:"friends"`
}

func friendsPath(dir string) string { return filepath.Join(dir, "friends.json") }

// LoadFriends reads the address book, tolerating a missing file.
func LoadFriends(dir string) (*FriendBook, error) {
	b := &FriendBook{path: friendsPath(dir)}
	raw, err := os.ReadFile(b.path)
	if os.IsNotExist(err) {
		return b, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read friends.json: %w", err)
	}
	if err := json.Unmarshal(raw, b); err != nil {
		return nil, fmt.Errorf("parse friends.json: %w", err)
	}
	return b, nil
}

// Save writes the address book back to disk.
func (b *FriendBook) Save() error {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal friends.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(b.path), err)
	}
	if err := os.WriteFile(b.path, raw, 0o644); err != nil {
		return fmt.Errorf("write friends.json: %w", err)
	}
	return nil
}

// Find looks up a friend by local alias.
func (b *FriendBook) Find(name string) (Friend, bool) {
	for _, f := range b.Friends {
		if f.Name == name {
			return f, true
		}
	}
	return Friend{}, false
}

// FindByEchoID looks up a friend by echo-id (used to resolve senders).
func (b *FriendBook) FindByEchoID(echoID string) (Friend, bool) {
	for _, f := range b.Friends {
		if f.EchoID == echoID {
			return f, true
		}
	}
	return Friend{}, false
}

// Upsert adds or replaces a friend by name.
func (b *FriendBook) Upsert(f Friend) {
	for i, existing := range b.Friends {
		if existing.Name == f.Name {
			b.Friends[i] = f
			return
		}
	}
	b.Friends = append(b.Friends, f)
}

// Remove deletes the friend with the given name, returning the removed
// Friend and true, or (Friend{}, false) if no friend has that name.
func (b *FriendBook) Remove(name string) (Friend, bool) {
	for i, f := range b.Friends {
		if f.Name == name {
			b.Friends = append(b.Friends[:i], b.Friends[i+1:]...)
			return f, true
		}
	}
	return Friend{}, false
}
