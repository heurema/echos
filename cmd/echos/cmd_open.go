package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/heurema/echos/internal/envelope"
	"github.com/heurema/echos/internal/identity"
	"github.com/heurema/echos/internal/session"
)

type OpenCmd struct {
	ID           string `arg:"" optional:"" help:"Blob id to open; default: newest inbox item."`
	Dir          string `name:"dir" help:"Target project directory (default: current directory)."`
	JSON         bool   `name:"json" help:"Emit machine-readable JSON."`
	AllowUnknown bool   `name:"allow-unknown" help:"Install even if the sender is not in friends."`
	Resume       bool   `name:"resume" help:"Execute the printed resume command (opt-in; default is to only print it)."`
}

func (c *OpenCmd) Run(app *App) error {
	id, _, err := app.ensureIdentity("")
	if err != nil {
		return fail(app, c.JSON, 1, "", "identity error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := app.relayClient()

	blobID := c.ID
	if blobID == "" {
		pending, err := client.GetMailbox(ctx, id.EchoID, id.Signer)
		if err != nil {
			return fail(app, c.JSON, 1, "", "fetch mailbox: %v", err)
		}
		if len(pending) == 0 {
			return fail(app, c.JSON, 1, "", "inbox is empty")
		}
		newest := pending[0]
		for _, p := range pending[1:] {
			if p.ReceivedAt.After(newest.ReceivedAt) {
				newest = p
			}
		}
		blobID = newest.ID
	}

	raw, err := client.GetBlob(ctx, id.EchoID, blobID, id.Signer)
	if err != nil {
		return fail(app, c.JSON, 1, "", "fetch blob %s: %v", blobID, err)
	}

	ageID, err := envelope.AgeIdentity(id.PrivateKey)
	if err != nil {
		return fail(app, c.JSON, 1, "", "%v", err)
	}
	opened, err := envelope.Open(raw, ageID)
	if err != nil {
		return fail(app, c.JSON, 1, "", "open envelope: %v", err)
	}
	if err := opened.VerifyFiles(); err != nil {
		return fail(app, c.JSON, 1, "", "integrity check failed: %v", err)
	}

	book, err := identity.LoadFriends(app.ConfigDir)
	if err != nil {
		return fail(app, c.JSON, 1, "", "load friends: %v", err)
	}
	friend, known := book.FindByEchoID(opened.Manifest.SenderEchoID)
	if !known && !c.AllowUnknown {
		return fail(app, c.JSON, 1, "echos open --allow-unknown",
			"sender unknown (%s) — add friend or pass --allow-unknown", opened.Manifest.SenderEchoID)
	}
	if known {
		friendPub, err := friend.SSHPublicKey()
		if err != nil {
			return fail(app, c.JSON, 1, "", "friend %s has an invalid stored key: %v", friend.Name, err)
		}
		if err := opened.VerifySignature(friendPub); err != nil {
			return fail(app, c.JSON, 1, "", "sender signature verification failed: %v", err)
		}
	}

	dir := c.Dir
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return fail(app, c.JSON, 1, "", "resolve cwd: %v", err)
		}
	}

	adapter, adapterOK := session.AdapterFor(session.Tool(opened.Manifest.Tool))
	degraded := !adapterOK
	var installedPath, resumeCmd string
	if adapterOK {
		pkg := session.Package{
			SessionID: opened.Manifest.SessionID,
			Title:     opened.Manifest.Title,
			Project:   opened.Manifest.Project,
			Updated:   app.Now(),
			Files:     opened.Files,
		}
		installedPath, err = adapter.Install(dir, pkg)
		if err != nil {
			return fail(app, c.JSON, 1, "", "install: %v", err)
		}
		resumeCmd = adapter.ResumeCommand(opened.Manifest.SessionID)
	} else {
		installedPath, err = saveDegraded(dir, opened.Manifest.Tool, opened.Manifest.SessionID, opened.Files)
		if err != nil {
			return fail(app, c.JSON, 1, "", "save received files: %v", err)
		}
	}

	from := friend.Name
	if !known {
		from = "unknown (" + opened.Manifest.SenderEchoID + ")"
	}

	if c.JSON {
		if err := writeJSON(app.Stdout, map[string]any{
			"id":             blobID,
			"from":           from,
			"tool":           opened.Manifest.Tool,
			"installed_path": installedPath,
			"resume_command": resumeCmd,
			"degraded":       degraded,
		}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(app.Stdout, "from %s ✓ · installed at %s\n", from, installedPath)
		if resumeCmd != "" {
			fmt.Fprintf(app.Stdout, "resume: %s\n", resumeCmd)
		} else {
			fmt.Fprintf(app.Stdout, "unrecognized tool %q — files saved to %s\n", opened.Manifest.Tool, installedPath)
		}
	}

	if c.Resume && resumeCmd != "" {
		return runResume(resumeCmd)
	}
	return nil
}

// saveDegraded writes packaged files to disk verbatim for a tool this echos
// build has no adapter for, so an agent can still read them as context.
func saveDegraded(dir, tool, sessionID string, files map[string][]byte) (string, error) {
	savedDir := filepath.Join(dir, "echos-received", tool+"-"+sessionID)
	for relPath, data := range files {
		clean := filepath.Clean(filepath.FromSlash(relPath))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing unsafe path %q", relPath)
		}
		dest := filepath.Join(savedDir, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return "", err
		}
	}
	return savedDir, nil
}

func runResume(resumeCmd string) error {
	parts := strings.Fields(resumeCmd)
	if len(parts) == 0 {
		return nil
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return &cmdError{code: 1}
	}
	return nil
}
