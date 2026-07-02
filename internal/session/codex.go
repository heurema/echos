package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CodexAdapter discovers and packages Codex sessions indexed by
// ~/.codex/session_index.jsonl, backed by rollout files under
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl.
type CodexAdapter struct{}

func (CodexAdapter) Tool() Tool { return ToolCodex }

func codexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

type codexIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

var uuidRE = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// rolloutsByID walks the sessions root once and maps session id -> path
// relative to sessionsRoot, keyed by the uuid embedded in each filename.
func rolloutsByID(sessionsRoot string) map[string]string {
	found := map[string]string{}
	filepath.WalkDir(sessionsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate unreadable entries
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		id := uuidRE.FindString(d.Name())
		if id == "" {
			return nil
		}
		rel, err := filepath.Rel(sessionsRoot, path)
		if err != nil {
			return nil
		}
		found[id] = filepath.ToSlash(rel)
		return nil
	})
	return found
}

func (CodexAdapter) Discover() ([]Session, error) {
	home, err := codexHome()
	if err != nil {
		return nil, err
	}
	idxPath := filepath.Join(home, "session_index.jsonl")
	f, err := os.Open(idxPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sessionsRoot := filepath.Join(home, "sessions")
	rollouts := rolloutsByID(sessionsRoot)

	var sessions []Session
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var entry codexIndexEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		rel, ok := rollouts[entry.ID]
		if !ok {
			continue
		}
		updated, err := time.Parse(time.RFC3339Nano, entry.UpdatedAt)
		if err != nil {
			continue
		}
		cwd := parseCodexCWD(filepath.Join(sessionsRoot, filepath.FromSlash(rel)))
		sessions = append(sessions, Session{
			Tool:    ToolCodex,
			ID:      entry.ID,
			Project: cwd,
			Title:   entry.ThreadName,
			Updated: updated,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	SortNewestFirst(sessions)
	return sessions, nil
}

type codexSessionMeta struct {
	Type    string `json:"type"`
	Payload struct {
		CWD string `json:"cwd"`
	} `json:"payload"`
}

func parseCodexCWD(rolloutPath string) string {
	f, err := os.Open(rolloutPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if !sc.Scan() {
		return ""
	}
	var meta codexSessionMeta
	if json.Unmarshal(sc.Bytes(), &meta) != nil {
		return ""
	}
	return meta.Payload.CWD
}

func (CodexAdapter) Package(s Session) ([]File, error) {
	home, err := codexHome()
	if err != nil {
		return nil, err
	}
	sessionsRoot := filepath.Join(home, "sessions")
	rollouts := rolloutsByID(sessionsRoot)
	rel, ok := rollouts[s.ID]
	if !ok {
		return nil, fmt.Errorf("codex session %s: rollout file not found", s.ID)
	}
	return []File{{Path: rel, Abs: filepath.Join(sessionsRoot, filepath.FromSlash(rel))}}, nil
}

// codexSafePath reports whether a packaged relative path is a plausible
// rollout location: YYYY/MM/DD/rollout-*-<id>.jsonl, no traversal.
func codexSafePath(sessionID, relPath string) bool {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	if clean != relPath || filepath.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." {
		return false
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 4 {
		return false
	}
	base := parts[3]
	return strings.HasPrefix(base, "rollout-") && strings.HasSuffix(base, sessionID+".jsonl")
}

func (CodexAdapter) Install(dir string, pkg Package) (string, error) {
	home, err := codexHome()
	if err != nil {
		return "", err
	}
	sessionsRoot := filepath.Join(home, "sessions")

	for relPath := range pkg.Files {
		if !codexSafePath(pkg.SessionID, relPath) {
			return "", fmt.Errorf("refusing unsafe codex session path %q", relPath)
		}
	}

	var installedPath string
	for relPath, data := range pkg.Files {
		dest := filepath.Join(sessionsRoot, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", dest, err)
		}
		installedPath = dest
	}

	if err := upsertCodexIndex(home, pkg); err != nil {
		return "", err
	}
	return installedPath, nil
}

func upsertCodexIndex(home string, pkg Package) error {
	idxPath := filepath.Join(home, "session_index.jsonl")
	var entries []codexIndexEntry
	if raw, err := os.ReadFile(idxPath); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(raw)))
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var e codexIndexEntry
			if json.Unmarshal([]byte(line), &e) == nil {
				entries = append(entries, e)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read session_index.jsonl: %w", err)
	}

	updatedAt := pkg.Updated.UTC().Format(time.RFC3339Nano)
	found := false
	for i, e := range entries {
		if e.ID == pkg.SessionID {
			entries[i] = codexIndexEntry{ID: pkg.SessionID, ThreadName: pkg.Title, UpdatedAt: updatedAt}
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, codexIndexEntry{ID: pkg.SessionID, ThreadName: pkg.Title, UpdatedAt: updatedAt})
	}

	var b strings.Builder
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(idxPath, []byte(b.String()), 0o644)
}

func (CodexAdapter) ResumeCommand(id string) string {
	return "codex resume " + id
}
