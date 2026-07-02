package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestCLIBehaviorContract: exit codes (0 ok / 1 error / 2 needs
// clarification), every error prints a ready-to-run next command (both text
// and --json), --json works across commands, and stdin is never read.
func TestCLIBehaviorContract(t *testing.T) {
	home := setupHome(t)
	startTestRelay(t)

	// 0: ok.
	if _, stderr, code := run(t, "sessions", "--json"); code != 0 {
		t.Fatalf("sessions --json: code=%d stderr=%s", code, stderr)
	}

	// 1: error, with a ready-to-run next command (text mode).
	stdout, stderr, code := run(t, "send", "bob")
	if code != 1 {
		t.Fatalf("send with unknown friend: code=%d, want 1 (stdout=%s stderr=%s)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "run: echos friend add bob") {
		t.Fatalf("stderr missing ready-to-run next command: %s", stderr)
	}

	// 1: error, with a ready-to-run next command (--json mode).
	_, stderrJSON, codeJSON := run(t, "send", "bob", "--json")
	if codeJSON != 1 {
		t.Fatalf("send --json with unknown friend: code=%d, want 1", codeJSON)
	}
	errPayload := assertJSONFields(t, stderrJSON, "error", "next")
	if !strings.Contains(errPayload["next"].(string), "echos friend add bob") {
		t.Fatalf("json error missing next command: %+v", errPayload)
	}

	// 2: needs clarification — a friend exists, sessions exist, but none in
	// the current directory's project.
	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, bobCode := run(t, "id", "--json")
	if bobCode != 0 {
		t.Fatalf("bob id: %s", bobOut)
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", home)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("friend add bob: code=%d stderr=%s", code, stderr)
	}

	elsewhere := t.TempDir()
	writeClaudeSession(t, home, elsewhere, "22222222-2222-2222-2222-222222222222", "hello", time.Now())

	emptyDir := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(emptyDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	stdout2, stderr2, code2 := run(t, "send", "bob")
	if code2 != 2 {
		t.Fatalf("send with no session in cwd but sessions elsewhere: code=%d, want 2 (stdout=%s stderr=%s)", code2, stdout2, stderr2)
	}
	if !strings.Contains(stderr2, "run: echos send bob") {
		t.Fatalf("needs-clarification stderr missing ready-to-run next command: %s", stderr2)
	}
}

// TestCLIBehaviorContractNeverReadsStdin: no command reads from stdin.
func TestCLIBehaviorContractNeverReadsStdin(t *testing.T) {
	setupHome(t)
	startTestRelay(t)

	r, w, err := os.Pipe() // never written to; a Read would block forever
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		run(t, "id", "--json")
		run(t, "sessions", "--json")
		run(t, "inbox", "--json")
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a command blocked, suggesting it tried to read stdin")
	}
}
