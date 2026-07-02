package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heurema/echos/internal/envelope"
	"github.com/heurema/echos/internal/identity"
	"github.com/heurema/echos/internal/session"
)

type SendCmd struct {
	Friend    string `arg:"" help:"Local alias of the friend to send to."`
	SessionID string `arg:"" optional:"" name:"session-id" help:"Session id to send; default: latest session in the current directory's project."`
	JSON      bool   `name:"json" help:"Emit machine-readable JSON."`
}

func (c *SendCmd) Run(app *App) error {
	id, _, err := app.ensureIdentity("")
	if err != nil {
		return fail(app, c.JSON, 1, "", "identity error: %v", err)
	}

	book, err := identity.LoadFriends(app.ConfigDir)
	if err != nil {
		return fail(app, c.JSON, 1, "", "load friends: %v", err)
	}
	friend, ok := book.Find(c.Friend)
	if !ok {
		return fail(app, c.JSON, 1, fmt.Sprintf("echos friend add %s <their-echo-id>", c.Friend), "no friend %q", c.Friend)
	}
	recipientPub, err := friend.SSHPublicKey()
	if err != nil {
		return fail(app, c.JSON, 1, "", "friend %s has an invalid stored key: %v", c.Friend, err)
	}

	sessions, err := session.DiscoverAll()
	if err != nil {
		return fail(app, c.JSON, 1, "", "discover sessions: %v", err)
	}

	sess, exitCode, hint, msg := selectSendSession(sessions, c.SessionID, c.Friend)
	if msg != "" {
		return fail(app, c.JSON, exitCode, hint, "%s", msg)
	}

	adapter, ok := session.AdapterFor(sess.Tool)
	if !ok {
		return fail(app, c.JSON, 1, "", "no adapter for tool %q", sess.Tool)
	}
	files, err := adapter.Package(sess)
	if err != nil {
		return fail(app, c.JSON, 1, "", "package session %s: %v", sess.ID, err)
	}
	fileMap := make(map[string][]byte, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f.Abs)
		if err != nil {
			return fail(app, c.JSON, 1, "", "read %s: %v", f.Abs, err)
		}
		fileMap[f.Path] = data
	}

	blob, err := envelope.Build(envelope.BuildInput{
		Tool:              string(sess.Tool),
		SessionID:         sess.ID,
		Project:           sess.Project,
		Title:             sess.Title,
		CreatedAt:         app.Now(),
		SenderEchoID:      id.EchoID,
		SenderFingerprint: id.Fingerprint,
		Files:             fileMap,
	}, id.Signer, recipientPub)
	if err != nil {
		return fail(app, c.JSON, 1, "", "build envelope: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := app.relayClient().PostMailbox(ctx, friend.EchoID, blob)
	if err != nil {
		return fail(app, c.JSON, 1, "", "send: %v", err)
	}

	if c.JSON {
		return writeJSON(app.Stdout, map[string]any{
			"friend":     c.Friend,
			"echo_id":    friend.EchoID,
			"blob_id":    res.ID,
			"ttl":        int(res.TTL.Seconds()),
			"expires_at": res.ExpiresAt.UTC().Format(time.RFC3339Nano),
		})
	}
	fmt.Fprintf(app.Stdout, "✓ %s: %q → %s (%s)\n", sess.ID, sess.Title, c.Friend, res.TTL)
	return nil
}

// selectSendSession resolves which session to send. When explicit is empty,
// it defaults to the newest session belonging to the current directory's
// project; if none exists there but sessions exist elsewhere, it returns a
// needs-clarification (exit 2) result listing recent sessions.
func selectSendSession(sessions []session.Session, explicit, friend string) (sess session.Session, exitCode int, hint, msg string) {
	if explicit != "" {
		for _, s := range sessions {
			if s.ID == explicit {
				return s, 0, "", ""
			}
		}
		return session.Session{}, 1, "echos sessions", fmt.Sprintf("no session %q", explicit)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return session.Session{}, 1, "", fmt.Sprintf("resolve cwd: %v", err)
	}
	cwd = resolvePath(cwd)

	var inProject []session.Session
	for _, s := range sessions {
		if resolvePath(s.Project) == cwd {
			inProject = append(inProject, s)
		}
	}
	if len(inProject) > 0 {
		session.SortNewestFirst(inProject)
		return inProject[0], 0, "", ""
	}

	if len(sessions) == 0 {
		return session.Session{}, 1, "", "no sessions found on this machine"
	}

	recent := sessions
	if len(recent) > 5 {
		recent = recent[:5]
	}
	ids := make([]string, 0, len(recent))
	for _, s := range recent {
		ids = append(ids, fmt.Sprintf("%s (%s)", s.ID, s.Project))
	}
	hint = fmt.Sprintf("echos send %s <session-id>", friend)
	msg = fmt.Sprintf("no sessions in this dir; recent: %s", strings.Join(ids, ", "))
	return session.Session{}, 2, hint, msg
}

// resolvePath normalizes a path for comparison, resolving symlinks (e.g.
// macOS's /var -> /private/var) so a session's recorded project cwd
// compares equal to the process's current directory even when one side
// went through symlink resolution and the other didn't.
func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
