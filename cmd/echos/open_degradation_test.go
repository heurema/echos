package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/heurema/echos/internal/envelope"
	"github.com/heurema/echos/internal/identity"
)

// TestOpenDegradationMatrix exercises both branches: a manifest tool that
// matches a registered adapter installs into the right place with a
// tool-specific resume command; a manifest tool with no matching adapter
// saves the files to disk, prints their path in place of a resume command,
// and exits 0.
func TestOpenDegradationMatrix(t *testing.T) {
	aliceHome := setupHome(t)
	relayURL := startTestRelay(t)

	// `echos id` (not the internal package directly) so the key is
	// actually published to the relay, same as any real sender.
	aliceOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("alice id: %s", aliceOut)
	}
	aliceEchoID := assertJSONFields(t, aliceOut, "echo_id")["echo_id"].(string)

	bobHome := t.TempDir()
	os.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id: %s", bobOut)
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	os.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("alice friend add bob: %s", stderr)
	}

	proj := t.TempDir()
	writeClaudeSession(t, aliceHome, proj, "dddddddd-0000-0000-0000-0000000000dd", "same-tool branch", time.Now())
	origWD, _ := os.Getwd()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })
	if _, stderr, code := run(t, "send", "bob"); code != 0 {
		t.Fatalf("send bob (claude session): %s", stderr)
	}

	os.Setenv("HOME", bobHome)
	if _, stderr, code := run(t, "friend", "add", "alice", aliceEchoID); code != 0 {
		t.Fatalf("bob friend add alice: %s", stderr)
	}
	bobProj := t.TempDir()
	if err := os.Chdir(bobProj); err != nil {
		t.Fatal(err)
	}

	// Branch 1: same tool (claude) — full install + resume command.
	openOut, stderr, code := run(t, "open", "--json")
	if code != 0 {
		t.Fatalf("open (same tool): code=%d stderr=%s", code, stderr)
	}
	same := assertJSONFields(t, openOut, "installed_path", "resume_command", "degraded")
	if same["degraded"] != false {
		t.Fatalf("same-tool open should not be degraded: %+v", same)
	}
	if same["resume_command"] == "" {
		t.Fatalf("same-tool open should print a resume command: %+v", same)
	}
	if _, err := os.Stat(same["installed_path"].(string)); err != nil {
		t.Fatalf("same-tool open should have installed the file: %v", err)
	}

	// Branch 2: an unrecognized tool identifier — hand-build and drop an
	// envelope claiming tool="mystery-agent", bypassing `send` (which only
	// ever produces known tools).
	aliceConfigDir := filepath.Join(aliceHome, ".config", "echos")
	aliceID, _, err := identity.Ensure(aliceConfigDir, "") // loads the already-created identity
	if err != nil {
		t.Fatal(err)
	}
	bobConfigDir := filepath.Join(bobHome, ".config", "echos")
	bobIdentity, _, err := identity.Ensure(bobConfigDir, "")
	if err != nil {
		t.Fatal(err)
	}

	blob, err := envelope.Build(envelope.BuildInput{
		Tool:              "mystery-agent",
		SessionID:         "eeeeeeee-0000-0000-0000-0000000000ee",
		Project:           proj,
		Title:             "unknown tool branch",
		CreatedAt:         time.Now(),
		SenderEchoID:      aliceID.EchoID,
		SenderFingerprint: aliceID.Fingerprint,
		Files:             map[string][]byte{"transcript.txt": []byte("hello from mystery-agent")},
	}, aliceID.Signer, bobIdentity.Signer.PublicKey())
	if err != nil {
		t.Fatalf("build mystery envelope: %v", err)
	}
	client := relayClientForTest(relayURL)
	if _, err := client.PostMailbox(context.Background(), bobID, blob); err != nil {
		t.Fatalf("post mystery envelope: %v", err)
	}

	openOut2, stderr2, code2 := run(t, "open", "--json")
	if code2 != 0 {
		t.Fatalf("open (unknown tool) should exit 0: code=%d stderr=%s", code2, stderr2)
	}
	unknown := assertJSONFields(t, openOut2, "installed_path", "resume_command", "degraded")
	if unknown["degraded"] != true {
		t.Fatalf("unknown-tool open should be degraded: %+v", unknown)
	}
	if unknown["resume_command"] != "" {
		t.Fatalf("unknown-tool open should have no resume command: %+v", unknown)
	}
	savedPath := unknown["installed_path"].(string)
	if _, err := os.Stat(filepath.Join(savedPath, "transcript.txt")); err != nil {
		t.Fatalf("unknown-tool open should have saved the files to disk: %v", err)
	}
}
