package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSendExplicitIDMismatchedProjectDir reproduces issue #3: the on-disk
// Claude project directory name can differ from
// encodeClaudeProject(embedded cwd) (symlinked/sandboxed/renamed homes).
// `echos send <friend> <session-id>` must still succeed, since Discover()
// records the real directory in Session.SourceDir and Package() now uses
// that instead of re-deriving the path from the embedded cwd.
func TestSendExplicitIDMismatchedProjectDir(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id: %s", bobOut)
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("friend add bob: %s", stderr)
	}

	const sessionID = "ffffffff-0000-0000-0000-00000000000f"
	embeddedCWD := "/Users/alice/repos/wherever-this-was-recorded"
	// The on-disk directory name is deliberately unrelated to
	// encodeClaudeProject(embeddedCWD), simulating a symlinked/sandboxed/
	// renamed home where the transcript's recorded cwd doesn't match the
	// directory it physically lives under.
	projDir := filepath.Join(aliceHome, ".claude", "projects", "unrelated-on-disk-name")
	transcript := filepath.Join(projDir, sessionID+".jsonl")
	line := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"mismatched dir session"}]},"cwd":"` + embeddedCWD + `","sessionId":"` + sessionID + `"}` + "\n"
	mustWriteFile(t, transcript, line)
	now := time.Now()
	if err := os.Chtimes(transcript, now, now); err != nil {
		t.Fatal(err)
	}

	sendOut, stderr, code := run(t, "send", "bob", sessionID, "--json")
	if code != 0 {
		t.Fatalf("send bob %s failed: code=%d stderr=%s", sessionID, code, stderr)
	}
	assertJSONFields(t, sendOut, "friend", "echo_id", "blob_id", "ttl", "expires_at")

	t.Setenv("HOME", bobHome)
	if _, stderr, code := run(t, "friend", "add", "alice", mustEchoID(t, aliceHome)); code != 0 {
		t.Fatalf("bob friend add alice: %s", stderr)
	}
	inboxOut, stderr, code := run(t, "inbox", "--json")
	if code != 0 {
		t.Fatalf("inbox --json failed: %s", stderr)
	}
	items := decodeJSON[[]map[string]any](t, inboxOut)
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 uploaded envelope in bob's inbox, got %d: %s", len(items), inboxOut)
	}
}
