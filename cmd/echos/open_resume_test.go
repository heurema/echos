package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOpenResumeExec: `echos open --resume` actually executes the printed
// resume command (an explicit opt-in), unlike a plain `echos open` which
// only prints it. The real tool binary is stubbed out so the test proves
// the exec happened without depending on a real claude install.
func TestOpenResumeExec(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	aliceOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("alice id: %s", aliceOut)
	}
	aliceEchoID := assertJSONFields(t, aliceOut, "echo_id")["echo_id"].(string)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id: %s", bobOut)
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("alice friend add bob: %s", stderr)
	}

	proj := t.TempDir()
	writeClaudeSession(t, aliceHome, proj, "ffffffff-0000-0000-0000-0000000000ff", "resume exec branch", time.Now())
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })
	if _, stderr, code := run(t, "send", "bob"); code != 0 {
		t.Fatalf("send bob: %s", stderr)
	}

	t.Setenv("HOME", bobHome)
	if _, stderr, code := run(t, "friend", "add", "alice", aliceEchoID); code != 0 {
		t.Fatalf("bob friend add alice: %s", stderr)
	}
	bobProj := t.TempDir()
	if err := os.Chdir(bobProj); err != nil {
		t.Fatal(err)
	}

	// Stub out the "claude" binary the resume command execs.
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "resumed.marker")
	script := "#!/bin/sh\ntouch \"" + marker + "\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, stderr, code := run(t, "open", "--resume"); code != 0 {
		t.Fatalf("open --resume: code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("open --resume did not exec the stubbed resume command: %v", err)
	}
}
