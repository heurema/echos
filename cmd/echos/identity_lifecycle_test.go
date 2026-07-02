package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIdentityLifecycle: `echos id` is idempotent and prints a stable
// echo-id; any command needing an identity lazily creates it with 0600
// permissions; pure-local discovery never creates one as a side effect.
func TestIdentityLifecycle(t *testing.T) {
	home := setupHome(t)
	identityPath := filepath.Join(home, ".config", "echos", "identity")

	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("identity should not exist before any command runs")
	}

	out, stderr, code := run(t, "sessions", "--json")
	if code != 0 {
		t.Fatalf("sessions --json failed: code=%d stderr=%s", code, stderr)
	}
	if out != "[]\n" {
		t.Fatalf("sessions --json = %q, want empty array", out)
	}
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("`echos sessions` must not create an identity as a side effect")
	}

	out1, stderr1, code1 := run(t, "id", "--json")
	if code1 != 0 {
		t.Fatalf("id --json (first) failed: code=%d stderr=%s", code1, stderr1)
	}
	first := assertJSONFields(t, out1, "echo_id", "public_key_fingerprint", "created")
	if first["created"] != true {
		t.Fatalf("first `echos id` should report created=true: %+v", first)
	}

	info, err := os.Stat(identityPath)
	if err != nil {
		t.Fatalf("identity file missing after `echos id`: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity perms = %v, want 0600", info.Mode().Perm())
	}

	out2, stderr2, code2 := run(t, "id", "--json")
	if code2 != 0 {
		t.Fatalf("id --json (second) failed: code=%d stderr=%s", code2, stderr2)
	}
	second := assertJSONFields(t, out2, "echo_id", "public_key_fingerprint", "created")
	if second["created"] != false {
		t.Fatalf("second `echos id` should report created=false: %+v", second)
	}
	if second["echo_id"] != first["echo_id"] {
		t.Fatalf("echo-id changed across invocations: %v vs %v", first["echo_id"], second["echo_id"])
	}
}
