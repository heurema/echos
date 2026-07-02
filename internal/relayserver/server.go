package relayserver

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/ssh"
)

// Config tunes the relay's limits and defaults. Zero values fall back to
// the documented defaults.
type Config struct {
	TTL          time.Duration // default mailbox TTL for new blobs
	ChallengeTTL time.Duration // default 60s
	MaxBlobSize  int64         // default 25MiB
	RateLimit    int           // requests/minute per IP, POST /keys and POST /mailbox/{fpr}
	Now          func() time.Time
}

const (
	DefaultTTL          = 24 * time.Hour
	DefaultChallengeTTL = 60 * time.Second
	DefaultMaxBlobSize  = 25 * 1024 * 1024
	DefaultRateLimit    = 10
)

func (c *Config) setDefaults() {
	if c.TTL == 0 {
		c.TTL = DefaultTTL
	}
	if c.ChallengeTTL == 0 {
		c.ChallengeTTL = DefaultChallengeTTL
	}
	if c.MaxBlobSize == 0 {
		c.MaxBlobSize = DefaultMaxBlobSize
	}
	if c.RateLimit == 0 {
		c.RateLimit = DefaultRateLimit
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Server implements Relay API v1 over the Store.
type Server struct {
	store      *Store
	challenges *ChallengeStore
	limiter    *IPRateLimiter
	cfg        Config
}

func New(store *Store, cfg Config) *Server {
	cfg.setDefaults()
	limiter := NewIPRateLimiter(cfg.RateLimit)
	limiter.now = cfg.Now
	return &Server{
		store:      store,
		challenges: NewChallengeStore(cfg.ChallengeTTL),
		limiter:    limiter,
		cfg:        cfg,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /keys", s.handlePostKeys)
	mux.HandleFunc("GET /keys/{fpr}", s.handleGetKey)
	mux.HandleFunc("GET /challenge", s.handleChallenge)
	mux.HandleFunc("POST /mailbox/{fpr}", s.handlePostMailbox)
	mux.HandleFunc("GET /mailbox/{fpr}", s.handleGetMailbox)
	mux.HandleFunc("GET /blob/{id}", s.handleGetBlob)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type postKeysRequest struct {
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
}

func (s *Server) handlePostKeys(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	var req postKeysRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Fingerprint == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pubRaw, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid public_key encoding")
		return
	}
	if _, err := ssh.ParsePublicKey(pubRaw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid public key")
		return
	}

	created, err := s.store.PutKey(req.Fingerprint, pubRaw)
	if err == ErrConflict {
		writeError(w, http.StatusConflict, "fingerprint already registered with a different key")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"fingerprint": req.Fingerprint, "created": created})
}

func (s *Server) handleGetKey(w http.ResponseWriter, r *http.Request) {
	fpr := r.PathValue("fpr")
	pub, err := s.store.GetKey(fpr)
	if err == ErrNotFound {
		writeError(w, http.StatusNotFound, "unknown fingerprint")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"fingerprint": fpr,
		"public_key":  base64.StdEncoding.EncodeToString(pub),
	})
}

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	fpr := r.URL.Query().Get("fpr")
	if fpr == "" {
		writeError(w, http.StatusBadRequest, "missing fpr query parameter")
		return
	}
	if _, err := s.store.GetKey(fpr); err != nil {
		writeError(w, http.StatusNotFound, "unknown fingerprint")
		return
	}
	nonce, expiresAt, err := s.challenges.Issue(fpr, s.cfg.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"nonce":      nonce,
		"expires_at": expiresAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) handlePostMailbox(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	fpr := r.PathValue("fpr")

	limited := io.LimitReader(r.Body, s.cfg.MaxBlobSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if int64(len(data)) > s.cfg.MaxBlobSize {
		writeError(w, http.StatusRequestEntityTooLarge, "envelope exceeds max blob size")
		return
	}

	meta, err := s.store.PutBlob(fpr, data, s.cfg.TTL, s.cfg.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         meta.ID,
		"ttl":        int(s.cfg.TTL.Seconds()),
		"expires_at": meta.ExpiresAt.UTC().Format(time.RFC3339Nano),
	})
}

// authenticate validates the challenge-signature headers and returns the
// fingerprint they authenticate as. It does not check resource ownership.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (fpr string, ok bool) {
	fpr = r.Header.Get("X-Echos-Fingerprint")
	nonceB64 := r.Header.Get("X-Echos-Nonce")
	sigB64 := r.Header.Get("X-Echos-Signature")
	if fpr == "" || nonceB64 == "" || sigB64 == "" {
		writeError(w, http.StatusUnauthorized, "missing authentication headers")
		return "", false
	}

	if err := s.challenges.Consume(nonceB64, fpr, s.cfg.Now()); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired challenge")
		return "", false
	}

	pubRaw, err := s.store.GetKey(fpr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unknown fingerprint")
		return "", false
	}
	pub, err := ssh.ParsePublicKey(pubRaw)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid stored key")
		return "", false
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid nonce encoding")
		return "", false
	}
	sigRaw, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid signature encoding")
		return "", false
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(sigRaw, &sig); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid signature format")
		return "", false
	}
	if err := pub.Verify(nonce, &sig); err != nil {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return "", false
	}
	return fpr, true
}

func (s *Server) handleGetMailbox(w http.ResponseWriter, r *http.Request) {
	pathFPR := r.PathValue("fpr")
	authFPR, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if authFPR != pathFPR {
		writeError(w, http.StatusUnauthorized, "not authorized for this mailbox")
		return
	}

	metas, err := s.store.ListMailbox(pathFPR, s.cfg.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	type item struct {
		ID         string `json:"id"`
		Size       int    `json:"size"`
		ReceivedAt string `json:"received_at"`
		ExpiresAt  string `json:"expires_at"`
	}
	items := make([]item, 0, len(metas))
	for _, m := range metas {
		items = append(items, item{
			ID:         m.ID,
			Size:       m.Size,
			ReceivedAt: m.ReceivedAt.UTC().Format(time.RFC3339Nano),
			ExpiresAt:  m.ExpiresAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetBlob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	authFPR, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	meta, data, err := s.store.GetBlob(id, s.cfg.Now())
	if err == ErrNotFound {
		writeError(w, http.StatusNotFound, "unknown blob")
		return
	}
	if err == ErrExpired {
		writeError(w, http.StatusGone, "blob expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if meta.RecipientFPR != authFPR {
		writeError(w, http.StatusUnauthorized, "not authorized for this blob")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
