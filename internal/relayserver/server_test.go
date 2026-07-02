package relayserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testClock is a manually-advanceable clock so TTL/expiry tests don't sleep.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock(t time.Time) *testClock { return &testClock{t: t} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type testParty struct {
	priv   ed25519.PrivateKey
	signer ssh.Signer
	pub    ssh.PublicKey
	fpr    string
}

func newTestParty(t *testing.T) testParty {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pub := signer.PublicKey()
	return testParty{priv: priv, signer: signer, pub: pub, fpr: fingerprintForTest(pub)}
}

func fingerprintForTest(pub ssh.PublicKey) string {
	sum := sha256.Sum256(pub.Marshal())
	return hex.EncodeToString(sum[:])[:20]
}

func newTestServer(t *testing.T, cfg Config) (*Server, *httptest.Server, *testClock) {
	t.Helper()
	clock := newTestClock(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC))
	cfg.Now = clock.Now
	store, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, clock
}

func registerKey(t *testing.T, base string, p testParty) {
	t.Helper()
	body, _ := json.Marshal(postKeysRequest{
		Fingerprint: p.fpr,
		PublicKey:   base64.StdEncoding.EncodeToString(p.pub.Marshal()),
	})
	resp, err := http.Post(base+"/keys", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /keys status = %d", resp.StatusCode)
	}
}

// authHeaders fetches a challenge for p and signs it, returning ready-to-use
// request headers for GET /mailbox or GET /blob.
func authHeaders(t *testing.T, base string, p testParty) http.Header {
	t.Helper()
	resp, err := http.Get(base + "/challenge?fpr=" + url.QueryEscape(p.fpr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /challenge status = %d", resp.StatusCode)
	}
	var out struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	nonce, err := base64.StdEncoding.DecodeString(out.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := p.signer.Sign(rand.Reader, nonce)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Echos-Fingerprint", p.fpr)
	h.Set("X-Echos-Nonce", out.Nonce)
	h.Set("X-Echos-Signature", base64.StdEncoding.EncodeToString(ssh.Marshal(sig)))
	return h
}

func doGet(t *testing.T, urlStr string, headers http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestRelayAuthRejectsUnsignedOrInvalidReads: GET /mailbox and GET /blob
// reject requests lacking a valid challenge-signature.
func TestRelayAuthRejectsUnsignedOrInvalidReads(t *testing.T) {
	_, ts, _ := newTestServer(t, Config{})
	alice := newTestParty(t)
	bob := newTestParty(t)
	registerKey(t, ts.URL, alice)
	registerKey(t, ts.URL, bob)

	// Drop a blob into alice's mailbox.
	postResp, err := http.Post(ts.URL+"/mailbox/"+alice.fpr, "application/octet-stream", bytes.NewReader([]byte("ciphertext")))
	if err != nil {
		t.Fatal(err)
	}
	var putOut struct {
		ID string `json:"id"`
	}
	json.NewDecoder(postResp.Body).Decode(&putOut)
	postResp.Body.Close()

	// No auth headers at all.
	resp := doGet(t, ts.URL+"/mailbox/"+alice.fpr, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /mailbox status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doGet(t, ts.URL+"/blob/"+putOut.ID, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /blob status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid signature, but for the wrong mailbox (bob authenticating for
	// alice's mailbox/blob).
	h := authHeaders(t, ts.URL, bob)
	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bob reading alice's mailbox status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	h = authHeaders(t, ts.URL, bob)
	resp = doGet(t, ts.URL+"/blob/"+putOut.ID, h)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bob reading alice's blob status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Tampered signature.
	h = authHeaders(t, ts.URL, alice)
	sigBytes, _ := base64.StdEncoding.DecodeString(h.Get("X-Echos-Signature"))
	sigBytes[len(sigBytes)-1] ^= 0xFF
	h.Set("X-Echos-Signature", base64.StdEncoding.EncodeToString(sigBytes))
	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered signature status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Now prove the happy path actually works with a fresh challenge.
	h = authHeaders(t, ts.URL, alice)
	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET /mailbox status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestRelayZeroKnowledgeStorageAndExpiry: blobs are ciphertext only, keyed
// by recipient fingerprint, and expire per TTL with 410 on read.
func TestRelayZeroKnowledgeStorageAndExpiry(t *testing.T) {
	_, ts, clock := newTestServer(t, Config{TTL: time.Hour})
	alice := newTestParty(t)
	registerKey(t, ts.URL, alice)

	payload := []byte("opaque-envelope-bytes-no-plaintext-project-or-sender")
	resp, err := http.Post(ts.URL+"/mailbox/"+alice.fpr, "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /mailbox status = %d", resp.StatusCode)
	}
	var out struct {
		ID        string `json:"id"`
		TTL       int    `json:"ttl"`
		ExpiresAt string `json:"expires_at"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.TTL != 3600 {
		t.Fatalf("ttl = %d, want 3600", out.TTL)
	}

	h := authHeaders(t, ts.URL, alice)
	resp = doGet(t, ts.URL+"/blob/"+out.ID, h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /blob status = %d, want 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("blob bytes altered by the relay")
	}

	// Mailbox listing exposes only metadata, never content.
	h = authHeaders(t, ts.URL, alice)
	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(body, payload) {
		t.Fatalf("mailbox listing leaked blob content")
	}

	// Advance past the TTL: the blob should now be gone (410).
	clock.Advance(2 * time.Hour)
	h = authHeaders(t, ts.URL, alice)
	resp = doGet(t, ts.URL+"/blob/"+out.ID, h)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expired blob status = %d, want 410", resp.StatusCode)
	}
	resp.Body.Close()

	h = authHeaders(t, ts.URL, alice)
	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	var items []map[string]any
	json.NewDecoder(resp.Body).Decode(&items)
	resp.Body.Close()
	if len(items) != 0 {
		t.Fatalf("expired blob still listed in mailbox: %+v", items)
	}
}

// TestChallengeNonceExpiryAndReplay: a nonce older than its TTL, or reused
// after a successful request, is rejected.
func TestChallengeNonceExpiryAndReplay(t *testing.T) {
	_, ts, clock := newTestServer(t, Config{ChallengeTTL: 60 * time.Second})
	alice := newTestParty(t)
	registerKey(t, ts.URL, alice)

	// Expiry.
	h := authHeaders(t, ts.URL, alice)
	clock.Advance(61 * time.Second)
	resp := doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired-nonce status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Replay: a fresh nonce, used successfully once, must fail on reuse.
	h = authHeaders(t, ts.URL, alice)
	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first use status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed-nonce status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestRelayLimits: oversized blobs are rejected with 413 and persist
// nothing, and per-IP rate limiting returns 429.
func TestRelayLimits(t *testing.T) {
	_, ts, _ := newTestServer(t, Config{MaxBlobSize: 16, RateLimit: 3})
	alice := newTestParty(t)
	registerKey(t, ts.URL, alice)

	oversized := bytes.Repeat([]byte("x"), 17)
	resp, err := http.Post(ts.URL+"/mailbox/"+alice.fpr, "application/octet-stream", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized blob status = %d, want 413", resp.StatusCode)
	}
	resp.Body.Close()

	h := authHeaders(t, ts.URL, alice)
	resp = doGet(t, ts.URL+"/mailbox/"+alice.fpr, h)
	var items []map[string]any
	json.NewDecoder(resp.Body).Decode(&items)
	resp.Body.Close()
	if len(items) != 0 {
		t.Fatalf("oversized blob was persisted: %+v", items)
	}

	// registerKey already spent 1 of the 3 POST /keys+/mailbox budget for
	// this IP; the oversized POST /mailbox above spent another. One POST
	// /mailbox remains before the limiter should trip.
	small := []byte("ok")
	resp, err = http.Post(ts.URL+"/mailbox/"+alice.fpr, "application/octet-stream", bytes.NewReader(small))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("third POST status = %d, want 201", resp.StatusCode)
	}

	resp, err = http.Post(ts.URL+"/mailbox/"+alice.fpr, "application/octet-stream", bytes.NewReader(small))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("fourth POST status = %d, want 429", resp.StatusCode)
	}
}
