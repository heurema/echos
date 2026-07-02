package main

import "fmt"

type IDCmd struct {
	Key  string `name:"key" type:"existingfile" help:"Reuse an existing unencrypted ed25519 SSH private key instead of generating a fresh one."`
	JSON bool   `name:"json" help:"Emit machine-readable JSON."`
}

func (c *IDCmd) Run(app *App) error {
	id, created, err := app.ensureIdentity(c.Key)
	if err != nil {
		return fail(app, c.JSON, 1, "", "identity error: %v", err)
	}

	if c.JSON {
		return writeJSON(app.Stdout, map[string]any{
			"echo_id":                id.EchoID,
			"public_key_fingerprint": id.Fingerprint,
			"created":                created,
		})
	}
	fmt.Fprintln(app.Stdout, id.EchoID)
	return nil
}
