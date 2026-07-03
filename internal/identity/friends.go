package identity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode"

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

// NamesForEchoID returns the local aliases saved for echoID, in stored order.
func (b *FriendBook) NamesForEchoID(echoID string) []string {
	var names []string
	for _, f := range b.Friends {
		if f.EchoID == echoID {
			names = append(names, f.Name)
		}
	}
	return names
}

// ValidFriendName rejects names that would corrupt `friend list`'s tabwriter
// output (tabs/newlines shift or fabricate columns/rows) or any other control
// character. Enforced on write (Upsert) and re-checked at the CLI boundary.
func ValidFriendName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Upsert adds or replaces a friend by name. It rejects an empty name or one
// containing a control character.
func (b *FriendBook) Upsert(f Friend) error {
	if !ValidFriendName(f.Name) {
		return fmt.Errorf("invalid friend name %q: must be non-empty with no control characters", f.Name)
	}
	for i, existing := range b.Friends {
		if existing.Name == f.Name {
			b.Friends[i] = f
			return nil
		}
	}
	b.Friends = append(b.Friends, f)
	return nil
}

// Remove deletes every friend with the given name, returning the last
// removed Friend and true, or (Friend{}, false) if no friend has that name.
// Normal use never stores two friends under the same name (Upsert replaces
// in place), but a hand-edited or externally-written friends.json might;
// removing all of them keeps "removed" meaning gone, not "one instance
// gone".
func (b *FriendBook) Remove(name string) (Friend, bool) {
	kept := b.Friends[:0]
	removed, found := Friend{}, false
	for _, f := range b.Friends {
		if f.Name == name {
			removed, found = f, true
			continue
		}
		kept = append(kept, f)
	}
	b.Friends = kept
	return removed, found
}
