package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEndToEndSendInboxOpen_Claude: alice sends a Claude session to bob; bob
// lists it in inbox, opens it, sees it verified from alice, installs the
// transcript so `claude --resume <id>` would work, and the resume command
// is printed but not executed.
func TestEndToEndSendInboxOpen_Claude(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	aliceOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("alice id: %s", aliceOut)
	}
	aliceID := assertJSONFields(t, aliceOut, "echo_id")["echo_id"].(string)

	bobHome := t.TempDir()
	os.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id: %s", bobOut)
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	if _, stderr, code := run(t, "friend", "add", "alice", aliceID); code != 0 {
		t.Fatalf("bob friend add alice: %s", stderr)
	}
	os.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("alice friend add bob: %s", stderr)
	}

	proj := t.TempDir()
	const sessID = "f1f1f1f1-0000-0000-0000-0000000000f1"
	writeClaudeSession(t, aliceHome, proj, sessID, "Fix acp backend timeout", time.Now())
	origWD, _ := os.Getwd()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	sendOut, stderr, code := run(t, "send", "bob", "--json")
	if code != 0 {
		t.Fatalf("send: %s", stderr)
	}
	sendFields := assertJSONFields(t, sendOut, "ttl")
	if sendFields["ttl"].(float64) <= 0 {
		t.Fatalf("send did not report a positive ttl: %+v", sendFields)
	}

	os.Setenv("HOME", bobHome)
	inboxOut, stderr, code := run(t, "inbox", "--json")
	if code != 0 {
		t.Fatalf("inbox: %s", stderr)
	}
	items := decodeJSON[[]map[string]any](t, inboxOut)
	if len(items) != 1 || items[0]["from_name"] != "alice" {
		t.Fatalf("inbox should list one item from alice: %s", inboxOut)
	}

	bobProj := t.TempDir()
	if err := os.Chdir(bobProj); err != nil {
		t.Fatal(err)
	}
	openOut, stderr, code := run(t, "open", "--json")
	if code != 0 {
		t.Fatalf("open: %s", stderr)
	}
	openFields := assertJSONFields(t, openOut, "from", "installed_path", "resume_command", "degraded")
	if openFields["from"] != "alice" {
		t.Fatalf("open did not verify sender as alice: %+v", openFields)
	}
	if openFields["degraded"] != false {
		t.Fatalf("open should not be degraded: %+v", openFields)
	}
	wantResume := "claude --resume " + sessID
	if openFields["resume_command"] != wantResume {
		t.Fatalf("resume_command = %v, want %q", openFields["resume_command"], wantResume)
	}

	installed := openFields["installed_path"].(string)
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("installed transcript not found: %v", err)
	}
	if !strings.Contains(string(data), sessID) {
		t.Fatalf("installed transcript missing session id")
	}
}

// TestEndToEndSendInboxOpen_Codex: sending a Codex session end-to-end
// results in `echos open` installing the native rollout file under
// ~/.codex/sessions/ and updating ~/.codex/session_index.jsonl on the
// recipient's machine, then printing `codex resume <id>` without running it.
func TestEndToEndSendInboxOpen_Codex(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	aliceOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("alice id: %s", aliceOut)
	}
	aliceID := assertJSONFields(t, aliceOut, "echo_id")["echo_id"].(string)

	bobHome := t.TempDir()
	os.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id: %s", bobOut)
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	if _, stderr, code := run(t, "friend", "add", "alice", aliceID); code != 0 {
		t.Fatalf("bob friend add alice: %s", stderr)
	}
	os.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("alice friend add bob: %s", stderr)
	}

	proj := t.TempDir()
	const sessID = "019f223f-83e1-7a32-9445-eb9070db352d"
	writeCodexSession(t, aliceHome, proj, sessID, "Fix acp timeout", time.Now())
	origWD, _ := os.Getwd()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	if _, stderr, code := run(t, "send", "bob"); code != 0 {
		t.Fatalf("send: %s", stderr)
	}

	os.Setenv("HOME", bobHome)
	bobProj := t.TempDir()
	if err := os.Chdir(bobProj); err != nil {
		t.Fatal(err)
	}
	openOut, stderr, code := run(t, "open", "--json")
	if code != 0 {
		t.Fatalf("open: %s", stderr)
	}
	openFields := assertJSONFields(t, openOut, "from", "tool", "installed_path", "resume_command", "degraded")
	if openFields["tool"] != "codex" {
		t.Fatalf("tool = %v, want codex", openFields["tool"])
	}
	if openFields["degraded"] != false {
		t.Fatalf("open should not be degraded: %+v", openFields)
	}
	wantResume := "codex resume " + sessID
	if openFields["resume_command"] != wantResume {
		t.Fatalf("resume_command = %v, want %q", openFields["resume_command"], wantResume)
	}

	installed := openFields["installed_path"].(string)
	if !strings.HasPrefix(installed, filepath.Join(bobHome, ".codex", "sessions")) {
		t.Fatalf("installed_path %q not under ~/.codex/sessions", installed)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("installed rollout file missing: %v", err)
	}

	indexPath := filepath.Join(bobHome, ".codex", "session_index.jsonl")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("session_index.jsonl not written: %v", err)
	}
	if !strings.Contains(string(indexData), sessID) {
		t.Fatalf("session_index.jsonl missing the received session id: %s", indexData)
	}
}
