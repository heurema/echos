package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/heurema/echos/internal/identity"
)

type FriendCmd struct {
	Add FriendAddCmd `cmd:"" help:"Add a friend by echo-id, fetching and verifying their key from the relay."`
}

type FriendAddCmd struct {
	Name   string `arg:"" help:"Local alias for this friend."`
	EchoID string `arg:"" name:"echo-id" help:"Their echo-id, as printed by their 'echos id'."`
	JSON   bool   `name:"json" help:"Emit machine-readable JSON."`
}

func (c *FriendAddCmd) Run(app *App) error {
	next := fmt.Sprintf("echos friend add %s %s", c.Name, c.EchoID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pub, err := app.relayClient().GetKey(ctx, c.EchoID)
	if err != nil {
		return fail(app, c.JSON, 1, next, "fetch key for %s: %v", c.EchoID, err)
	}

	gotEchoID := identity.EchoID(pub)
	if gotEchoID != c.EchoID {
		return fail(app, c.JSON, 1, "", "key fetched for %s does not hash to that echo-id (got %s) — refusing to add", c.EchoID, gotEchoID)
	}

	book, err := identity.LoadFriends(app.ConfigDir)
	if err != nil {
		return fail(app, c.JSON, 1, "", "load friends: %v", err)
	}
	fingerprint := identity.Fingerprint(pub)
	book.Upsert(identity.Friend{
		Name:        c.Name,
		EchoID:      gotEchoID,
		Fingerprint: fingerprint,
		PublicKey:   base64.StdEncoding.EncodeToString(pub.Marshal()),
		AddedAt:     app.Now(),
	})
	if err := book.Save(); err != nil {
		return fail(app, c.JSON, 1, "", "save friends: %v", err)
	}

	if c.JSON {
		return writeJSON(app.Stdout, map[string]any{
			"name":        c.Name,
			"echo_id":     gotEchoID,
			"fingerprint": fingerprint,
		})
	}
	fmt.Fprintf(app.Stdout, "added %s (%s)\n", c.Name, gotEchoID)
	return nil
}
