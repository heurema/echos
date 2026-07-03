// Package session discovers and packages coding-agent sessions (Claude
// Code, Codex) behind a common model, so the envelope and relay layers
// never need to know tool-specific layouts.
package session

import (
	"fmt"
	"sort"
	"time"
)

// Tool identifies a supported coding agent.
type Tool string

const (
	ToolClaude Tool = "claude"
	ToolCodex  Tool = "codex"
)

// Session is the common model surfaced by `echos sessions`.
type Session struct {
	Tool    Tool      `json:"tool"`
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`

	// SourceDir is the absolute path of the on-disk project directory the
	// session was actually found under during Discover, as opposed to
	// Project (the transcript-embedded cwd, which may not match the real
	// directory name for symlinked/sandboxed/renamed homes). Adapters
	// should package from SourceDir when set. Not part of the JSON schema.
	SourceDir string `json:"-"`
}

// File is a native session file: a relative path (adapter-owned, used both
// on disk and inside the envelope) paired with its absolute source location
// on the sender's machine.
type File struct {
	Path string
	Abs  string
}

// Package is a set of native files for one session, ready to install.
type Package struct {
	SessionID string
	Title     string
	Project   string
	Updated   time.Time
	Files     map[string][]byte // relative path -> content
}

// Adapter isolates all tool-specific knowledge behind one seam.
type Adapter interface {
	Tool() Tool
	// Discover finds local sessions for this tool, newest first.
	Discover() ([]Session, error)
	// Package lists the native files that make up a session.
	Package(s Session) ([]File, error)
	// Install writes a received package into dir (the recipient's project
	// checkout, or the tool's fixed location), validating that every path
	// is safe and adapter-owned. Returns the primary installed path.
	Install(dir string, pkg Package) (string, error)
	// ResumeCommand returns the tool's resume command for a session id.
	ResumeCommand(id string) string
}

// Adapters returns the built-in tool adapters.
func Adapters() []Adapter {
	return []Adapter{&ClaudeAdapter{}, &CodexAdapter{}}
}

// AdapterFor looks up the adapter for a tool identifier, as found in a
// manifest. ok is false for an unknown/unrecognized tool.
func AdapterFor(tool Tool) (Adapter, bool) {
	for _, a := range Adapters() {
		if a.Tool() == tool {
			return a, true
		}
	}
	return nil, false
}

// DiscoverAll merges sessions from every adapter, newest first. It tolerates
// individual adapters reporting no local state (e.g. tool never installed).
func DiscoverAll() ([]Session, error) {
	var all []Session
	for _, a := range Adapters() {
		ss, err := a.Discover()
		if err != nil {
			return nil, fmt.Errorf("discover %s sessions: %w", a.Tool(), err)
		}
		all = append(all, ss...)
	}
	SortNewestFirst(all)
	return all, nil
}

// SortNewestFirst sorts sessions by Updated, most recent first.
func SortNewestFirst(sessions []Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].Updated.After(sessions[j].Updated)
	})
}
