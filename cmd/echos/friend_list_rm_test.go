package main

import (
	"strings"
	"testing"
)

// TestFriendListEmpty: `friend list` on an empty book prints "no friends" in
// text mode and an empty JSON array (not null) in --json mode.
func TestFriendListEmpty(t *testing.T) {
	setupHome(t)
	startTestRelay(t)

	stdout, stderr, code := run(t, "friend", "list")
	if code != 0 {
		t.Fatalf("friend list: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "no friends") {
		t.Fatalf("friend list stdout = %q, want it to contain %q", stdout, "no friends")
	}

	jsonOut, stderr, code := run(t, "friend", "list", "--json")
	if code != 0 {
		t.Fatalf("friend list --json: code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(jsonOut) != "[]" {
		t.Fatalf("friend list --json on empty book = %q, want []", jsonOut)
	}
}

// TestFriendListShowsAddedFriends: after adding two friends with real
// echo-ids, `friend list` shows both sorted by name, and `friend list
// --json` includes name/echo_id/fingerprint/added_at but not pubkey.
func TestFriendListShowsAddedFriends(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id --json failed")
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "zed", bobID); code != 0 {
		t.Fatalf("friend add zed: code=%d stderr=%s", code, stderr)
	}

	// A second, genuinely distinct identity so we have two real echo-ids.
	carolHome := t.TempDir()
	t.Setenv("HOME", carolHome)
	carolOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("carol id --json failed")
	}
	carolID := assertJSONFields(t, carolOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "amy", carolID); code != 0 {
		t.Fatalf("friend add amy: code=%d stderr=%s", code, stderr)
	}

	stdout, stderr, code := run(t, "friend", "list")
	if code != 0 {
		t.Fatalf("friend list: code=%d stderr=%s", code, stderr)
	}
	amyIdx := strings.Index(stdout, "amy")
	zedIdx := strings.Index(stdout, "zed")
	if amyIdx == -1 || zedIdx == -1 {
		t.Fatalf("friend list missing an entry: %s", stdout)
	}
	if amyIdx > zedIdx {
		t.Fatalf("friend list not sorted by name: %s", stdout)
	}

	jsonOut, stderr, code := run(t, "friend", "list", "--json")
	if code != 0 {
		t.Fatalf("friend list --json: code=%d stderr=%s", code, stderr)
	}
	items := decodeJSON[[]map[string]any](t, jsonOut)
	if len(items) != 2 {
		t.Fatalf("expected 2 friends, got %d: %s", len(items), jsonOut)
	}
	for _, item := range items {
		for _, k := range []string{"name", "echo_id", "fingerprint", "added_at"} {
			if _, ok := item[k]; !ok {
				t.Fatalf("friend list item missing field %q: %+v", k, item)
			}
		}
		if _, ok := item["pubkey"]; ok {
			t.Fatalf("friend list item must not include pubkey: %+v", item)
		}
	}
}

// TestFriendRmRemovesFriend: `friend rm <name>` removes a saved friend, and
// a subsequent `echos send <name>` fails with the standard "no friend"
// error.
func TestFriendRmRemovesFriend(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id --json failed")
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("friend add bob: code=%d stderr=%s", code, stderr)
	}

	stdout, stderr, code := run(t, "friend", "rm", "bob")
	if code != 0 {
		t.Fatalf("friend rm bob: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "bob") {
		t.Fatalf("friend rm stdout = %q, want it to mention bob", stdout)
	}

	listOut, _, code := run(t, "friend", "list")
	if code != 0 {
		t.Fatalf("friend list after rm failed")
	}
	if strings.Contains(listOut, "bob") {
		t.Fatalf("bob still present after friend rm: %s", listOut)
	}

	_, sendStderr, sendCode := run(t, "send", "bob")
	if sendCode != 1 {
		t.Fatalf("send bob after rm: code=%d, want 1 (stderr=%s)", sendCode, sendStderr)
	}
	if !strings.Contains(sendStderr, "run: echos friend add bob") {
		t.Fatalf("send bob after rm missing ready-to-run next command: %s", sendStderr)
	}
}

// TestFriendRmUnknownName: `friend rm <name>` on a nonexistent name exits 1,
// and in --json mode the stderr payload has "error" and "next" fields.
func TestFriendRmUnknownName(t *testing.T) {
	setupHome(t)
	startTestRelay(t)

	_, stderr, code := run(t, "friend", "rm", "ghost")
	if code != 1 {
		t.Fatalf("friend rm ghost: code=%d, want 1 (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, `"ghost"`) {
		t.Fatalf("friend rm ghost stderr = %q, want it to mention ghost", stderr)
	}
	if !strings.Contains(stderr, "run: echos friend list") {
		t.Fatalf("friend rm ghost stderr missing next-command hint: %s", stderr)
	}

	_, stderrJSON, codeJSON := run(t, "friend", "rm", "ghost", "--json")
	if codeJSON != 1 {
		t.Fatalf("friend rm ghost --json: code=%d, want 1", codeJSON)
	}
	fields := assertJSONFields(t, stderrJSON, "error", "next")
	if !strings.Contains(fields["error"].(string), "ghost") {
		t.Fatalf("friend rm ghost --json error = %q, want it to mention ghost", fields["error"])
	}
	if fields["next"] != "echos friend list" {
		t.Fatalf("friend rm ghost --json next = %q, want %q", fields["next"], "echos friend list")
	}
}

// TestFriendAddSameEchoIDSecondAlias: adding the same echo-id under a
// second local alias succeeds and warns on stderr, mentioning the original
// alias.
func TestFriendAddSameEchoIDSecondAlias(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id --json failed")
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("friend add bob: code=%d stderr=%s", code, stderr)
	}

	_, stderr, code := run(t, "friend", "add", "bobby", bobID)
	if code != 0 {
		t.Fatalf("friend add bobby (same echo-id, second alias): code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "warning") {
		t.Fatalf("friend add bobby stderr missing warning: %s", stderr)
	}
	if !strings.Contains(stderr, `"bob"`) {
		t.Fatalf("friend add bobby stderr missing original alias name: %s", stderr)
	}
}

// TestFriendAddIdempotentNoWarning: re-adding an alias that already points
// at the given echo-id is a no-op refresh, not "another alias" — it must
// not warn.
func TestFriendAddIdempotentNoWarning(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id --json failed")
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID); code != 0 {
		t.Fatalf("friend add bob: code=%d stderr=%s", code, stderr)
	}

	_, stderr, code := run(t, "friend", "add", "bob", bobID)
	if code != 0 {
		t.Fatalf("re-adding bob with the same echo-id: code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "warning") {
		t.Fatalf("re-adding an existing alias for its own echo-id must not warn: stderr=%s", stderr)
	}
}

// TestFriendAddSameEchoIDSecondAliasJSON: in --json mode, the duplicate-alias
// warning is folded into the JSON payload, not printed as plain text to
// stderr (which would otherwise break stderr's machine-readable contract).
func TestFriendAddSameEchoIDSecondAliasJSON(t *testing.T) {
	aliceHome := setupHome(t)
	startTestRelay(t)

	bobHome := t.TempDir()
	t.Setenv("HOME", bobHome)
	bobOut, _, code := run(t, "id", "--json")
	if code != 0 {
		t.Fatalf("bob id --json failed")
	}
	bobID := assertJSONFields(t, bobOut, "echo_id")["echo_id"].(string)
	t.Setenv("HOME", aliceHome)

	if _, stderr, code := run(t, "friend", "add", "bob", bobID, "--json"); code != 0 {
		t.Fatalf("friend add bob --json: code=%d stderr=%s", code, stderr)
	}

	stdout, stderr, code := run(t, "friend", "add", "bobby", bobID, "--json")
	if code != 0 {
		t.Fatalf("friend add bobby --json (same echo-id, second alias): code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("friend add bobby --json must not write plain text to stderr, got: %s", stderr)
	}
	fields := assertJSONFields(t, stdout, "name", "echo_id", "fingerprint", "warning")
	if !strings.Contains(fields["warning"].(string), `"bob"`) {
		t.Fatalf("friend add bobby --json warning field missing original alias name: %+v", fields)
	}
}
