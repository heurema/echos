package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/heurema/echos/internal/relay"
	"github.com/heurema/echos/internal/relayserver"
)

// run executes the CLI in-process and captures its output.
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = Execute(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

// setupHome points $HOME at a fresh temp directory for the duration of the
// test, isolating ~/.config/echos, ~/.claude, and ~/.codex.
func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// startTestRelay runs a real echos-relay HTTP server in-process and points
// $ECHOS_RELAY at it.
func startTestRelay(t *testing.T) string {
	t.Helper()
	store, err := relayserver.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := relayserver.New(store, relayserver.Config{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Setenv("ECHOS_RELAY", ts.URL)
	return ts.URL
}

// startTestRelayWithStore is like startTestRelay but also returns the
// underlying store, for tests that need to manipulate it directly (e.g.
// registering a key under a mismatched fingerprint).
func startTestRelayWithStore(t *testing.T) (string, *relayserver.Store) {
	t.Helper()
	store, err := relayserver.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := relayserver.New(store, relayserver.Config{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Setenv("ECHOS_RELAY", ts.URL)
	return ts.URL, store
}

func relayClientForTest(baseURL string) *relay.Client {
	return relay.New(baseURL)
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// encodeClaudeProject mirrors internal/session's directory naming.
func encodeClaudeProject(cwd string) string {
	out := make([]byte, len(cwd))
	for i := 0; i < len(cwd); i++ {
		if cwd[i] == '/' {
			out[i] = '-'
		} else {
			out[i] = cwd[i]
		}
	}
	return string(out)
}

// writeClaudeSession creates a fake Claude Code transcript for cwd, so it
// is discoverable and packageable exactly like a real one.
func writeClaudeSession(t *testing.T, home, cwd, id, title string, when time.Time) {
	t.Helper()
	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProject(cwd))
	path := filepath.Join(projDir, id+".jsonl")
	line := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"` + title + `"}]},"cwd":"` + cwd + `","sessionId":"` + id + `"}` + "\n"
	mustWriteFile(t, path, line)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// writeCodexSession creates a fake Codex rollout file + index entry.
func writeCodexSession(t *testing.T, home, cwd, id, title string, when time.Time) {
	t.Helper()
	rolloutRel := filepath.Join("2026", "07", "02", "rollout-2026-07-02T11-53-35-"+id+".jsonl")
	rolloutPath := filepath.Join(home, ".codex", "sessions", rolloutRel)
	mustWriteFile(t, rolloutPath, `{"timestamp":"2026-07-02T09:53:36.435Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"`+cwd+`"}}`+"\n")

	indexPath := filepath.Join(home, ".codex", "session_index.jsonl")
	entry := map[string]string{"id": id, "thread_name": title, "updated_at": when.UTC().Format(time.RFC3339Nano)}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	existing, _ := os.ReadFile(indexPath)
	mustWriteFile(t, indexPath, string(existing)+string(raw)+"\n")
}

func decodeJSON[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode JSON %q: %v", s, err)
	}
	return v
}

func assertJSONFields(t *testing.T, raw string, required ...string) map[string]any {
	t.Helper()
	m := decodeJSON[map[string]any](t, raw)
	for _, k := range required {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing field %q in JSON: %s", k, raw)
		}
	}
	return m
}
