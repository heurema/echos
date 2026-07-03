package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/heurema/echos/internal/identity"
	"github.com/heurema/echos/internal/relay"
)

// newTabWriter returns the CLI's shared aligned-columns writer, so the
// human-readable tables (friend list, sessions) keep one column config.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// registerHint returns "echos id" as the recovery next-command when err is a
// relay 404 — the challenge/mailbox endpoints 404 when the caller's key is not
// published — so re-registering the identity is one copy-paste away.
func registerHint(err error) string {
	var apiErr *relay.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return "echos id"
	}
	return ""
}

// App is the shared runtime context threaded into every command.
type App struct {
	Stdout    io.Writer
	Stderr    io.Writer
	ConfigDir string
	Now       func() time.Time
}

func newApp(stdout, stderr io.Writer) (*App, error) {
	dir, err := identity.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve config dir: %w", err)
	}
	return &App{Stdout: stdout, Stderr: stderr, ConfigDir: dir, Now: time.Now}, nil
}

func (app *App) relayClient() *relay.Client {
	return relay.New(identity.RelayURL(app.ConfigDir))
}

// ensureIdentity loads (or lazily creates) the local identity. On creation
// it best-effort publishes the public key to the relay; publication
// failures are warned about on stderr but never fail identity creation.
func (app *App) ensureIdentity(keyPath string) (*identity.Identity, bool, error) {
	id, created, err := identity.Ensure(app.ConfigDir, keyPath)
	if err != nil {
		return nil, false, err
	}
	if created {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, pubErr := app.relayClient().PublishKey(ctx, id.EchoID, id.Signer.PublicKey()); pubErr != nil {
			fmt.Fprintf(app.Stderr, "warning: identity created but key publication failed (%v); re-run `echos id` once the relay is reachable\n", pubErr)
		}
	}
	return id, created, nil
}

// cmdError carries the process exit code for a failed command.
type cmdError struct{ code int }

func (e *cmdError) Error() string { return "" }

// fail prints a diagnostic (with a ready-to-run next command, if any) to
// stderr — as JSON when jsonMode is set — and returns an error carrying the
// desired process exit code.
func fail(app *App, jsonMode bool, code int, next string, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if jsonMode {
		payload := map[string]string{"error": msg}
		if next != "" {
			payload["next"] = next
		}
		_ = writeJSON(app.Stderr, payload)
	} else if next != "" {
		fmt.Fprintf(app.Stderr, "%s\n  run: %s\n", msg, next)
	} else {
		fmt.Fprintln(app.Stderr, msg)
	}
	return &cmdError{code: code}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
