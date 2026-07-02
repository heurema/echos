package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIdentityPublishFailureDegrades: when key publication to the relay
// fails during identity creation, the identity is still created, a warning
// is printed to stderr, and the command still exits 0 — publication is
// best-effort and must never block identity creation.
func TestIdentityPublishFailureDegrades(t *testing.T) {
	home := setupHome(t)
	identityPath := filepath.Join(home, ".config", "echos", "identity")

	// A relay URL with nothing listening: POST /keys fails fast.
	ts := httptest.NewServer(nil)
	ts.Close()
	t.Setenv("ECHOS_RELAY", ts.URL)

	out, stderr, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("id --json should exit 0 even when key publication fails: code=%d stderr=%s", code, stderr)
	}
	fields := assertJSONFields(t, out, "echo_id", "public_key_fingerprint", "created")
	if fields["created"] != true {
		t.Fatalf("expected created=true despite publish failure: %+v", fields)
	}
	if _, err := os.Stat(identityPath); err != nil {
		t.Fatalf("identity file missing despite publish failure: %v", err)
	}
	if !strings.Contains(stderr, "warning: identity created but key publication failed") {
		t.Fatalf("stderr should warn about the publication failure: %s", stderr)
	}
}
