package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOpenRejectsUnknownSender: `echos open` from a sender absent from
// friends exits 1 with the sender fingerprint and a hint, and installs
// nothing, unless --allow-unknown is passed.
func TestOpenRejectsUnknownSender(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)
	aliceID := mustEchoID(t, aliceHome)

	bobHome := t.TempDir()
	os.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id: %s", bobOut)
	}
	os.Setenv("HOME", aliceHome)
	// Deliberately do NOT add bob as alice's friend for this send — that's
	// irrelevant; what matters is bob not having alice as a friend.

	proj := t.TempDir()
	writeClaudeSession(t, aliceHome, proj, "cccccccc-0000-0000-0000-0000000000cc", "secret work", time.Now())
	origWD, _ := os.Getwd()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	// alice needs bob as a friend to send to him.
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("alice friend add bob: %s", stderr)
	}
	if _, stderr, code := run(t, "send", "bob"); code != 0 {
		t.Fatalf("send bob: %s", stderr)
	}

	os.Setenv("HOME", bobHome)
	bobProj := t.TempDir()
	if err := os.Chdir(bobProj); err != nil {
		t.Fatal(err)
	}

	// Bob has NOT added alice as a friend.
	stdout, stderr, code := run(t, "open", "--json")
	if code != 1 {
		t.Fatalf("open from unknown sender: code=%d, want 1 (stdout=%s stderr=%s)", code, stdout, stderr)
	}
	errPayload := assertJSONFields(t, stderr, "error")
	if !strings.Contains(errPayload["error"].(string), aliceID) {
		t.Fatalf("error should include the sender's fingerprint: %s", stderr)
	}

	resolvedBobProj, err := filepath.EvalSymlinks(bobProj)
	if err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(bobHome, ".claude", "projects", encodeClaudeProject(resolvedBobProj), "cccccccc-0000-0000-0000-0000000000cc.jsonl")
	if _, err := os.Stat(installedPath); !os.IsNotExist(err) {
		t.Fatalf("open must not install anything when the sender is unknown")
	}

	// --allow-unknown opts in.
	stdout, stderr, code = run(t, "open", "--allow-unknown", "--json")
	if code != 0 {
		t.Fatalf("open --allow-unknown: code=%d stderr=%s", code, stderr)
	}
	payload := assertJSONFields(t, stdout, "installed_path", "degraded")
	if payload["degraded"] != false {
		t.Fatalf("expected a normal (non-degraded) install: %+v", payload)
	}
	if _, err := os.Stat(installedPath); err != nil {
		t.Fatalf("open --allow-unknown should have installed the transcript: %v", err)
	}
}
