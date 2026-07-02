package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestSendDefaultsToCwdProjectLatestSession: with two+ project directories
// each holding a session, `echos send <friend>` with no session-id picks
// only the newest session belonging to the *current directory's* project,
// never a newer session belonging to a different project.
func TestSendDefaultsToCwdProjectLatestSession(t *testing.T) {
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

	projA := t.TempDir()
	projB := t.TempDir()

	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	writeClaudeSession(t, aliceHome, projA, "aaaaaaaa-0000-0000-0000-00000000000a", "session in project A", older)
	// A strictly newer session, but it belongs to a *different* project.
	writeClaudeSession(t, aliceHome, projB, "bbbbbbbb-0000-0000-0000-00000000000b", "session in project B", newer)

	origWD, _ := os.Getwd()
	if err := os.Chdir(projA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	if _, stderr, code := run(t, "send", "bob"); code != 0 {
		t.Fatalf("send bob from projA: code=%d stderr=%s", code, stderr)
	}

	// Verify which session actually got sent by having bob open it and
	// inspecting the resume command (which embeds the session id).
	t.Setenv("HOME", bobHome)
	if _, stderr, code := run(t, "friend", "add", "alice", mustEchoID(t, aliceHome)); code != 0 {
		t.Fatalf("bob friend add alice: %s", stderr)
	}
	bobProj := t.TempDir()
	if err := os.Chdir(bobProj); err != nil {
		t.Fatal(err)
	}
	openOut, stderr, code := run(t, "open", "--json")
	if code != 0 {
		t.Fatalf("bob open: code=%d stderr=%s", code, stderr)
	}
	fields := assertJSONFields(t, openOut, "resume_command")
	resumeCmd := fields["resume_command"].(string)
	if !strings.Contains(resumeCmd, "aaaaaaaa-0000-0000-0000-00000000000a") {
		t.Fatalf("expected the session from projA to be sent, got resume command %q", resumeCmd)
	}
	if strings.Contains(resumeCmd, "bbbbbbbb-0000-0000-0000-00000000000b") {
		t.Fatalf("send picked the newer session from a different project: %q", resumeCmd)
	}
}

// mustEchoID reads the echo-id for the identity stored under home, by
// temporarily switching $HOME.
func mustEchoID(t *testing.T, home string) string {
	t.Helper()
	cur := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", cur)
	out, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("id --json for %s failed", home)
	}
	return assertJSONFields(t, out, "echo_id")["echo_id"].(string)
}
