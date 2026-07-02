package main

import (
	"context"
	"fmt"
	"time"
)

type IDCmd struct {
	Key  string `name:"key" type:"existingfile" help:"Reuse an existing unencrypted ed25519 SSH private key instead of generating a fresh one."`
	JSON bool   `name:"json" help:"Emit machine-readable JSON."`
}

func (c *IDCmd) Run(app *App) error {
	id, created, err := app.ensureIdentity(c.Key)
	if err != nil {
		return fail(app, c.JSON, 1, "", "identity error: %v", err)
	}

	// Always (re)publish the key so `echos id` reliably registers the
	// identity on the current relay — POST /keys is idempotent. This is the
	// recovery path when the first publish failed (relay unreachable) or the
	// relay changed since the identity was created.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, pubErr := app.relayClient().PublishKey(ctx, id.EchoID, id.Signer.PublicKey())

	if c.JSON {
		out := map[string]any{
			"echo_id":                id.EchoID,
			"public_key_fingerprint": id.Fingerprint,
			"created":                created,
			"published":              pubErr == nil,
		}
		if pubErr != nil {
			out["publish_error"] = pubErr.Error()
		}
		return writeJSON(app.Stdout, out)
	}
	fmt.Fprintln(app.Stdout, id.EchoID)
	if pubErr != nil {
		fmt.Fprintf(app.Stderr, "warning: key publication to the relay failed (%v)\n", pubErr)
	}
	return nil
}
