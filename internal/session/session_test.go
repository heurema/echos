package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func claudeLineJSON(cwd string) string {
	return `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"Fix the acp backend timeout please"}]},"cwd":"` + cwd + `","sessionId":"x"}` + "\n"
}

// TestSessionsDiscovery covers merged, newest-first discovery across Claude
// and Codex, tolerating a missing ~/.claude or ~/.codex entirely.
func TestSessionsDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// No ~/.claude or ~/.codex at all yet.
	sessions, err := DiscoverAll()
	if err != nil {
		t.Fatalf("DiscoverAll on empty home: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions, got %d", len(sessions))
	}

	cwd := "/Users/alice/repos/demo"
	claudeProj := filepath.Join(home, ".claude", "projects", encodeClaudeProject(cwd))
	writeFile(t, filepath.Join(claudeProj, "aaaaaaaa-0000-0000-0000-000000000001.jsonl"), claudeLineJSON(cwd))
	older := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(claudeProj, "aaaaaaaa-0000-0000-0000-000000000001.jsonl"), older, older); err != nil {
		t.Fatal(err)
	}

	newer := time.Now()
	writeFile(t, filepath.Join(claudeProj, "bbbbbbbb-0000-0000-0000-000000000002.jsonl"), claudeLineJSON(cwd))
	if err := os.Chtimes(filepath.Join(claudeProj, "bbbbbbbb-0000-0000-0000-000000000002.jsonl"), newer, newer); err != nil {
		t.Fatal(err)
	}

	codexID := "019f223f-83e1-7a32-9445-eb9070db352d"
	rolloutRel := "2026/07/02/rollout-2026-07-02T11-53-35-" + codexID + ".jsonl"
	rolloutPath := filepath.Join(home, ".codex", "sessions", filepath.FromSlash(rolloutRel))
	writeFile(t, rolloutPath, `{"timestamp":"2026-07-02T09:53:36.435Z","type":"session_meta","payload":{"id":"`+codexID+`","cwd":"`+cwd+`"}}`+"\n")
	indexPath := filepath.Join(home, ".codex", "session_index.jsonl")
	codexUpdated := newer.Add(time.Hour).UTC().Format(time.RFC3339Nano)
	writeFile(t, indexPath, `{"id":"`+codexID+`","thread_name":"Fix acp timeout","updated_at":"`+codexUpdated+`"}`+"\n")

	sessions, err = DiscoverAll()
	if err != nil {
		t.Fatalf("DiscoverAll: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d: %+v", len(sessions), sessions)
	}
	// newest first: codex (2026-07-02T12:00Z) is newest.
	if sessions[0].Tool != ToolCodex || sessions[0].ID != codexID {
		t.Fatalf("sessions[0] = %+v, want the codex session first", sessions[0])
	}
	if sessions[0].Title != "Fix acp timeout" {
		t.Fatalf("codex title = %q", sessions[0].Title)
	}
	if sessions[0].Project != cwd {
		t.Fatalf("codex project = %q, want %q", sessions[0].Project, cwd)
	}

	if sessions[1].ID != "bbbbbbbb-0000-0000-0000-000000000002" {
		t.Fatalf("sessions[1] = %+v, want the newer claude session second", sessions[1])
	}
	if sessions[1].Title == "" || sessions[1].Project != cwd {
		t.Fatalf("claude session metadata not parsed: %+v", sessions[1])
	}
	if sessions[2].ID != "aaaaaaaa-0000-0000-0000-000000000001" {
		t.Fatalf("sessions[2] = %+v, want the older claude session last", sessions[2])
	}

	for i := 0; i+1 < len(sessions); i++ {
		if sessions[i].Updated.Before(sessions[i+1].Updated) {
			t.Fatalf("sessions not newest-first: %v before %v", sessions[i].Updated, sessions[i+1].Updated)
		}
	}
}

func TestClaudePackageInstallRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := filepath.Join(home, "proj")
	id := "cccccccc-0000-0000-0000-000000000003"
	projDir := filepath.Join(home, ".claude", "projects", encodeClaudeProject(cwd))
	writeFile(t, filepath.Join(projDir, id+".jsonl"), claudeLineJSON(cwd))
	writeFile(t, filepath.Join(projDir, id, "subagents", "agent-1.jsonl"), `{"hello":"world"}`+"\n")

	s := Session{Tool: ToolClaude, ID: id, Project: cwd}
	a := ClaudeAdapter{}
	files, err := a.Package(s)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files (transcript+subagent), got %d: %+v", len(files), files)
	}

	pkg := Package{SessionID: id, Files: map[string][]byte{}}
	for _, f := range files {
		data, err := os.ReadFile(f.Abs)
		if err != nil {
			t.Fatal(err)
		}
		pkg.Files[f.Path] = data
	}

	destProj := filepath.Join(home, "otherproj")
	installed, err := a.Install(destProj, pkg)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("installed transcript missing: %v", err)
	}
	subPath := filepath.Join(home, ".claude", "projects", encodeClaudeProject(destProj), id, "subagents", "agent-1.jsonl")
	if _, err := os.Stat(subPath); err != nil {
		t.Fatalf("installed subagent missing: %v", err)
	}
}
