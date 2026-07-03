package main

import (
	"os"
	"testing"
	"time"
)

// TestJSONOutputSchemas: each core command's --json output has the
// documented fixed field set.
func TestJSONOutputSchemas(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	idOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("id --json failed")
	}
	assertJSONFields(t, idOut, "echo_id", "public_key_fingerprint", "created")
	aliceID := assertJSONFields(t, idOut, "echo_id")["echo_id"].(string)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id --json failed")
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	friendOut, _, code := run(t, "friend", "add", "bob", bobID, "--json")
	if code != 0 {
		t.Fatalf("friend add --json failed")
	}
	assertJSONFields(t, friendOut, "name", "echo_id", "fingerprint")

	friendListOut, _, code := run(t, "friend", "list", "--json")
	if code != 0 {
		t.Fatalf("friend list --json failed")
	}
	friendListItems := decodeJSON[[]map[string]any](t, friendListOut)
	if len(friendListItems) != 1 {
		t.Fatalf("expected 1 friend, got %d: %s", len(friendListItems), friendListOut)
	}
	for _, k := range []string{"name", "echo_id", "fingerprint", "added_at"} {
		if _, ok := friendListItems[0][k]; !ok {
			t.Fatalf("friend list item missing field %q: %+v", k, friendListItems[0])
		}
	}

	friendRmOut, _, code := run(t, "friend", "rm", "bob", "--json")
	if code != 0 {
		t.Fatalf("friend rm --json failed")
	}
	assertJSONFields(t, friendRmOut, "name", "echo_id", "fingerprint")

	if _, stderr, code := run(t, "friend", "add", "bob", bobID, "--json"); code != 0 {
		t.Fatalf("re-adding bob after friend rm --json failed: %s", stderr)
	}

	proj := t.TempDir()
	writeClaudeSession(t, aliceHome, proj, "33333333-3333-3333-3333-333333333333", "hello", time.Now())
	origWD, _ := os.Getwd()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	sendOut, stderr, code := run(t, "send", "bob", "--json")
	if code != 0 {
		t.Fatalf("send --json failed: %s", stderr)
	}
	assertJSONFields(t, sendOut, "friend", "echo_id", "blob_id", "ttl", "expires_at")

	sessOut, _, code := run(t, "sessions", "--json")
	if code != 0 {
		t.Fatalf("sessions --json failed")
	}
	// sessions returns a JSON array, not an object; just confirm it parses.
	decodeJSON[[]map[string]any](t, sessOut)

	// Now check inbox/open --json from bob's side.
	t.Setenv("HOME", bobHome)
	if _, stderr, code := run(t, "friend", "add", "alice", aliceID); code != 0 {
		t.Fatalf("bob friend add alice: %s", stderr)
	}

	inboxOut, stderr, code := run(t, "inbox", "--json")
	if code != 0 {
		t.Fatalf("inbox --json failed: %s", stderr)
	}
	items := decodeJSON[[]map[string]any](t, inboxOut)
	if len(items) != 1 {
		t.Fatalf("expected 1 inbox item, got %d: %s", len(items), inboxOut)
	}
	for _, k := range []string{"id", "from_fingerprint", "tool", "received", "ttl"} {
		if _, ok := items[0][k]; !ok {
			t.Fatalf("inbox item missing field %q: %+v", k, items[0])
		}
	}

	bobProj := t.TempDir()
	if err := os.Chdir(bobProj); err != nil {
		t.Fatal(err)
	}
	openOut, stderr, code := run(t, "open", "--json")
	if code != 0 {
		t.Fatalf("open --json failed: %s", stderr)
	}
	assertJSONFields(t, openOut, "id", "from", "tool", "installed_path", "resume_command", "degraded")
}
