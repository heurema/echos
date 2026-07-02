// Package relayserver implements the zero-knowledge relay: a public-key
// directory plus a TTL-bound mailbox/blob store. It never sees envelope
// plaintext, sender identity, or project information.
package relayserver

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"time"

	"go.etcd.io/bbolt"
)

var (
	ErrNotFound = errors.New("relayserver: not found")
	ErrConflict = errors.New("relayserver: fingerprint registered with a different key")
	ErrExpired  = errors.New("relayserver: expired")
)

var (
	bucketKeys         = []byte("keys")
	bucketBlobMeta     = []byte("blob_meta")
	bucketBlobData     = []byte("blob_data")
	bucketMailboxIndex = []byte("mailbox_index")
)

// BlobMeta is everything the relay knows about a stored envelope: never its
// content, sender, or project.
type BlobMeta struct {
	ID           string    `json:"id"`
	RecipientFPR string    `json:"recipient_fpr"`
	Size         int       `json:"size"`
	ReceivedAt   time.Time `json:"received_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Store is the bbolt-backed persistence layer: a key directory and a
// TTL-bound mailbox of ciphertext blobs.
type Store struct {
	db *bbolt.DB
}

// OpenStore opens (creating if needed) a bbolt database at path.
func OpenStore(path string) (*Store, error) {
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, b := range [][]byte{bucketKeys, bucketBlobMeta, bucketBlobData, bucketMailboxIndex} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// PutKey stores fpr -> pubKey. created is false when the fingerprint was
// already registered with the identical key (idempotent); ErrConflict is
// returned when it was registered with a different key.
func (s *Store) PutKey(fpr string, pubKey []byte) (created bool, err error) {
	err = s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketKeys)
		existing := b.Get([]byte(fpr))
		if existing != nil {
			if bytes.Equal(existing, pubKey) {
				created = false
				return nil
			}
			return ErrConflict
		}
		created = true
		return b.Put([]byte(fpr), pubKey)
	})
	return created, err
}

// GetKey returns the raw SSH wire-format public key for fpr.
func (s *Store) GetKey(fpr string) ([]byte, error) {
	var pub []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucketKeys).Get([]byte(fpr))
		if v == nil {
			return ErrNotFound
		}
		pub = append([]byte(nil), v...)
		return nil
	})
	return pub, err
}

func randomBlobID() (string, error) {
	raw := make([]byte, 15)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// PutBlob stores a ciphertext envelope in recipientFPR's mailbox.
func (s *Store) PutBlob(recipientFPR string, data []byte, ttl time.Duration, now time.Time) (BlobMeta, error) {
	id, err := randomBlobID()
	if err != nil {
		return BlobMeta{}, err
	}
	meta := BlobMeta{
		ID:           id,
		RecipientFPR: recipientFPR,
		Size:         len(data),
		ReceivedAt:   now,
		ExpiresAt:    now.Add(ttl),
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return BlobMeta{}, err
	}
	err = s.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket(bucketBlobMeta).Put([]byte(id), metaBytes); err != nil {
			return err
		}
		if err := tx.Bucket(bucketBlobData).Put([]byte(id), data); err != nil {
			return err
		}
		return tx.Bucket(bucketMailboxIndex).Put(mailboxIndexKey(recipientFPR, id), nil)
	})
	if err != nil {
		return BlobMeta{}, err
	}
	return meta, nil
}

func mailboxIndexKey(fpr, id string) []byte {
	return []byte(fpr + "\x00" + id)
}

// ListMailbox returns metadata for every non-expired blob addressed to fpr.
func (s *Store) ListMailbox(fpr string, now time.Time) ([]BlobMeta, error) {
	var metas []BlobMeta
	prefix := mailboxIndexKey(fpr, "")
	err := s.db.View(func(tx *bbolt.Tx) error {
		c := tx.Bucket(bucketMailboxIndex).Cursor()
		metaBucket := tx.Bucket(bucketBlobMeta)
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			id := string(k[len(prefix):])
			mv := metaBucket.Get([]byte(id))
			if mv == nil {
				continue
			}
			var meta BlobMeta
			if json.Unmarshal(mv, &meta) != nil {
				continue
			}
			if now.After(meta.ExpiresAt) {
				continue
			}
			metas = append(metas, meta)
		}
		return nil
	})
	return metas, err
}

// GetBlob returns the metadata and raw bytes for id, ErrExpired if its TTL
// has passed, or ErrNotFound if unknown.
func (s *Store) GetBlob(id string, now time.Time) (BlobMeta, []byte, error) {
	var meta BlobMeta
	var data []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		mv := tx.Bucket(bucketBlobMeta).Get([]byte(id))
		if mv == nil {
			return ErrNotFound
		}
		if err := json.Unmarshal(mv, &meta); err != nil {
			return err
		}
		if now.After(meta.ExpiresAt) {
			return ErrExpired
		}
		dv := tx.Bucket(bucketBlobData).Get([]byte(id))
		if dv == nil {
			return ErrNotFound
		}
		data = append([]byte(nil), dv...)
		return nil
	})
	return meta, data, err
}

// Sweep deletes every blob whose TTL has passed as of now.
func (s *Store) Sweep(now time.Time) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		metaBucket := tx.Bucket(bucketBlobMeta)
		dataBucket := tx.Bucket(bucketBlobData)
		idxBucket := tx.Bucket(bucketMailboxIndex)

		var expired []BlobMeta
		c := metaBucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var meta BlobMeta
			if json.Unmarshal(v, &meta) != nil {
				continue
			}
			if now.After(meta.ExpiresAt) {
				expired = append(expired, meta)
			}
		}
		for _, meta := range expired {
			if err := metaBucket.Delete([]byte(meta.ID)); err != nil {
				return err
			}
			if err := dataBucket.Delete([]byte(meta.ID)); err != nil {
				return err
			}
			if err := idxBucket.Delete(mailboxIndexKey(meta.RecipientFPR, meta.ID)); err != nil {
				return err
			}
		}
		return nil
	})
}

// StartSweeper runs Sweep on interval until stop is closed.
func (s *Store) StartSweeper(interval time.Duration, now func() time.Time, stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = s.Sweep(now())
			}
		}
	}()
}
