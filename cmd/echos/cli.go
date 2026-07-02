package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/alecthomas/kong"
)

// CLI is the root command grammar. Every command reads nothing from stdin
// and accepts --json for machine-readable output.
type CLI struct {
	ID       IDCmd       `cmd:"" help:"Print my echo-id (identity created lazily, idempotent)."`
	Friend   FriendCmd   `cmd:"" help:"Manage the local address book."`
	Sessions SessionsCmd `cmd:"" help:"List local coding-agent sessions, newest first."`
	Send     SendCmd     `cmd:"" help:"Send a session to a friend through the relay."`
	Inbox    InboxCmd    `cmd:"" help:"List pending items sent to me."`
	Open     OpenCmd     `cmd:"" help:"Decrypt, verify, and install a received session."`
}

// Execute parses args and runs the selected command, returning the process
// exit code. It never reads stdin.
func Execute(args []string, stdout, stderr io.Writer) int {
	app, err := newApp(stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	exited := false
	exitCode := 0

	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("echos"),
		kong.Description("Share a running coding-agent session with a friend, end-to-end encrypted through an ephemeral zero-knowledge relay."),
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) { exited = true; exitCode = code }),
	)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	kctx, err := parser.Parse(args)
	if exited {
		return exitCode
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	if err := kctx.Run(app); err != nil {
		var ce *cmdError
		if errors.As(err, &ce) {
			return ce.code
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
