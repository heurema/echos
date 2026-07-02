package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ClaudeAdapter discovers and packages Claude Code sessions stored at
// ~/.claude/projects/<enc-cwd>/<uuid>.jsonl (+ optional <uuid>/subagents/).
type ClaudeAdapter struct{}

func (ClaudeAdapter) Tool() Tool { return ToolClaude }

func claudeProjectsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// encodeClaudeProject mirrors Claude Code's directory naming: the cwd with
// every slash replaced by a dash.
func encodeClaudeProject(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

func (ClaudeAdapter) Discover() ([]Session, error) {
	root, err := claudeProjectsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sessions []Session
	for _, projEntry := range entries {
		if !projEntry.IsDir() {
			continue
		}
		projDir := filepath.Join(root, projEntry.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".jsonl")
			path := filepath.Join(projDir, f.Name())
			info, err := f.Info()
			if err != nil {
				continue
			}
			cwd, title := parseClaudeHead(path)
			sessions = append(sessions, Session{
				Tool:    ToolClaude,
				ID:      id,
				Project: cwd,
				Title:   title,
				Updated: info.ModTime(),
			})
		}
	}
	SortNewestFirst(sessions)
	return sessions, nil
}

type claudeLine struct {
	Type    string         `json:"type"`
	CWD     string         `json:"cwd"`
	Message *claudeMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// parseClaudeHead scans the head of a transcript for the session's cwd and
// a title derived from the first user message.
func parseClaudeHead(path string) (cwd, title string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	const maxLines = 200
	for i := 0; sc.Scan() && i < maxLines; i++ {
		var line claudeLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if cwd == "" && line.CWD != "" {
			cwd = line.CWD
		}
		if title == "" && line.Type == "user" && line.Message != nil && line.Message.Role == "user" {
			if t := firstText(line.Message.Content); t != "" {
				title = t
			}
		}
		if cwd != "" && title != "" {
			break
		}
	}
	return cwd, title
}

func firstText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return summarize(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return summarize(b.Text)
			}
		}
	}
	return ""
}

var whitespaceRun = regexp.MustCompile(`\s+`)

func summarize(s string) string {
	s = whitespaceRun.ReplaceAllString(strings.TrimSpace(s), " ")
	const maxLen = 80
	r := []rune(s)
	if len(r) > maxLen {
		s = string(r[:maxLen])
	}
	return s
}

func (ClaudeAdapter) Package(s Session) ([]File, error) {
	root, err := claudeProjectsRoot()
	if err != nil {
		return nil, err
	}
	projDir := filepath.Join(root, encodeClaudeProject(s.Project))
	transcript := filepath.Join(projDir, s.ID+".jsonl")
	if _, err := os.Stat(transcript); err != nil {
		return nil, fmt.Errorf("claude session %s: %w", s.ID, err)
	}
	files := []File{{Path: s.ID + ".jsonl", Abs: transcript}}

	subagentsDir := filepath.Join(projDir, s.ID, "subagents")
	entries, err := os.ReadDir(subagentsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			files = append(files, File{
				Path: filepath.Join(s.ID, "subagents", e.Name()),
				Abs:  filepath.Join(subagentsDir, e.Name()),
			})
		}
	}
	return files, nil
}

// claudeSafePath reports whether a packaged relative path is one this
// adapter would legitimately produce for the given session id: the
// transcript itself, or a file under its subagents/ subtree.
func claudeSafePath(sessionID, relPath string) bool {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	if clean != relPath || strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(clean) {
		return false
	}
	if clean == sessionID+".jsonl" {
		return true
	}
	prefix := sessionID + "/subagents/"
	return strings.HasPrefix(clean, prefix) && len(clean) > len(prefix)
}

func (ClaudeAdapter) Install(dir string, pkg Package) (string, error) {
	root, err := claudeProjectsRoot()
	if err != nil {
		return "", err
	}
	projDir := filepath.Join(root, encodeClaudeProject(dir))

	for relPath := range pkg.Files {
		if !claudeSafePath(pkg.SessionID, relPath) {
			return "", fmt.Errorf("refusing unsafe claude session path %q", relPath)
		}
	}
	for relPath, data := range pkg.Files {
		dest := filepath.Join(projDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", dest, err)
		}
	}
	return filepath.Join(projDir, pkg.SessionID+".jsonl"), nil
}

func (ClaudeAdapter) ResumeCommand(id string) string {
	return "claude --resume " + id
}
