// Package relay is the HTTP client for Relay API v1: the zero-knowledge
// key directory and TTL-bound mailbox.
package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/ssh"
)

// APIError is returned for any non-2xx relay response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("relay: %d %s", e.Status, e.Message)
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relay: %s %s: %w", req.Method, req.URL.Path, err)
	}
	return resp, nil
}

func apiErrorFrom(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)
	msg := body.Error
	if msg == "" {
		msg = string(data)
	}
	return &APIError{Status: resp.StatusCode, Message: msg}
}

// PublishKey registers fpr -> pub with the relay. created is false if the
// fingerprint was already registered with the identical key.
func (c *Client) PublishKey(ctx context.Context, fpr string, pub ssh.PublicKey) (created bool, err error) {
	body, err := json.Marshal(map[string]string{
		"fingerprint": fpr,
		"public_key":  base64.StdEncoding.EncodeToString(pub.Marshal()),
	})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/keys", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return false, apiErrorFrom(resp)
	}
	var out struct {
		Created bool `json:"created"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Created, nil
}

// GetKey fetches and parses the public key registered for fpr.
func (c *Client) GetKey(ctx context.Context, fpr string) (ssh.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/keys/"+url.PathEscape(fpr), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiErrorFrom(resp)
	}
	var out struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(out.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("relay: decode public_key: %w", err)
	}
	return ssh.ParsePublicKey(raw)
}

// Challenge fetches a short-lived, single-use nonce for fpr.
func (c *Client) Challenge(ctx context.Context, fpr string) (nonce []byte, expiresAt time.Time, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/challenge?fpr="+url.QueryEscape(fpr), nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, apiErrorFrom(resp)
	}
	var out struct {
		Nonce     string `json:"nonce"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, time.Time{}, err
	}
	nonce, err = base64.StdEncoding.DecodeString(out.Nonce)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("relay: decode nonce: %w", err)
	}
	expiresAt, err = time.Parse(time.RFC3339Nano, out.ExpiresAt)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("relay: parse expires_at: %w", err)
	}
	return nonce, expiresAt, nil
}

// MailboxResult is the outcome of dropping an envelope in a friend's
// mailbox.
type MailboxResult struct {
	ID        string
	TTL       time.Duration
	ExpiresAt time.Time
}

// PostMailbox drops envelope into fpr's mailbox.
func (c *Client) PostMailbox(ctx context.Context, fpr string, envelope []byte) (MailboxResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/mailbox/"+url.PathEscape(fpr), bytes.NewReader(envelope))
	if err != nil {
		return MailboxResult{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.do(req)
	if err != nil {
		return MailboxResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return MailboxResult{}, apiErrorFrom(resp)
	}
	var out struct {
		ID        string `json:"id"`
		TTL       int    `json:"ttl"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return MailboxResult{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, out.ExpiresAt)
	if err != nil {
		return MailboxResult{}, fmt.Errorf("relay: parse expires_at: %w", err)
	}
	return MailboxResult{ID: out.ID, TTL: time.Duration(out.TTL) * time.Second, ExpiresAt: expiresAt}, nil
}

// MailboxItem is one pending blob's relay-visible metadata.
type MailboxItem struct {
	ID         string
	Size       int
	ReceivedAt time.Time
	ExpiresAt  time.Time
}

func (c *Client) authenticatedGet(ctx context.Context, path, fpr string, signer ssh.Signer) (*http.Response, error) {
	nonce, _, err := c.Challenge(ctx, fpr)
	if err != nil {
		return nil, err
	}
	sig, err := signer.Sign(rand.Reader, nonce)
	if err != nil {
		return nil, fmt.Errorf("relay: sign challenge: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Echos-Fingerprint", fpr)
	req.Header.Set("X-Echos-Nonce", base64.StdEncoding.EncodeToString(nonce))
	req.Header.Set("X-Echos-Signature", base64.StdEncoding.EncodeToString(ssh.Marshal(sig)))
	return c.do(req)
}

// GetMailbox lists pending items addressed to fpr, authenticating with a
// freshly-issued challenge signed by signer (which must own fpr).
func (c *Client) GetMailbox(ctx context.Context, fpr string, signer ssh.Signer) ([]MailboxItem, error) {
	resp, err := c.authenticatedGet(ctx, "/mailbox/"+url.PathEscape(fpr), fpr, signer)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiErrorFrom(resp)
	}
	var raw []struct {
		ID         string `json:"id"`
		Size       int    `json:"size"`
		ReceivedAt string `json:"received_at"`
		ExpiresAt  string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	items := make([]MailboxItem, 0, len(raw))
	for _, r := range raw {
		received, err := time.Parse(time.RFC3339Nano, r.ReceivedAt)
		if err != nil {
			return nil, fmt.Errorf("relay: parse received_at: %w", err)
		}
		expires, err := time.Parse(time.RFC3339Nano, r.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("relay: parse expires_at: %w", err)
		}
		items = append(items, MailboxItem{ID: r.ID, Size: r.Size, ReceivedAt: received, ExpiresAt: expires})
	}
	return items, nil
}

// GetBlob fetches the raw envelope bytes for id, authenticating as fpr
// (the blob's recipient).
func (c *Client) GetBlob(ctx context.Context, fpr, id string, signer ssh.Signer) ([]byte, error) {
	resp, err := c.authenticatedGet(ctx, "/blob/"+url.PathEscape(id), fpr, signer)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiErrorFrom(resp)
	}
	return io.ReadAll(resp.Body)
}
