package main

import (
	"context"
	"fmt"
	"time"

	"github.com/heurema/echos/internal/envelope"
	"github.com/heurema/echos/internal/identity"
)

type InboxCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable JSON."`
}

type inboxItem struct {
	ID              string `json:"id"`
	FromFingerprint string `json:"from_fingerprint"`
	FromName        string `json:"from_name,omitempty"`
	Tool            string `json:"tool"`
	Received        string `json:"received"`
	TTL             int    `json:"ttl"`
}

func (c *InboxCmd) Run(app *App) error {
	id, _, err := app.ensureIdentity("")
	if err != nil {
		return fail(app, c.JSON, 1, "", "identity error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := app.relayClient()
	pending, err := client.GetMailbox(ctx, id.EchoID, id.Signer)
	if err != nil {
		return fail(app, c.JSON, 1, "", "fetch mailbox: %v", err)
	}

	book, err := identity.LoadFriends(app.ConfigDir)
	if err != nil {
		return fail(app, c.JSON, 1, "", "load friends: %v", err)
	}
	ageID, err := envelope.AgeIdentity(id.PrivateKey)
	if err != nil {
		return fail(app, c.JSON, 1, "", "%v", err)
	}

	items := make([]inboxItem, 0, len(pending))
	for _, p := range pending {
		raw, err := client.GetBlob(ctx, id.EchoID, p.ID, id.Signer)
		if err != nil {
			return fail(app, c.JSON, 1, "", "fetch blob %s: %v", p.ID, err)
		}
		opened, err := envelope.Open(raw, ageID)
		if err != nil {
			return fail(app, c.JSON, 1, "", "decrypt %s: %v", p.ID, err)
		}
		fromName := ""
		if f, ok := book.FindByEchoID(opened.Manifest.SenderEchoID); ok {
			fromName = f.Name
		}
		items = append(items, inboxItem{
			ID:              p.ID,
			FromFingerprint: opened.Manifest.SenderFingerprint,
			FromName:        fromName,
			Tool:            opened.Manifest.Tool,
			Received:        p.ReceivedAt.UTC().Format(time.RFC3339Nano),
			TTL:             int(p.ExpiresAt.Sub(p.ReceivedAt).Seconds()),
		})
	}

	if c.JSON {
		return writeJSON(app.Stdout, items)
	}
	if len(items) == 0 {
		fmt.Fprintln(app.Stdout, "inbox is empty")
		return nil
	}
	for _, it := range items {
		from := it.FromName
		if from == "" {
			from = "unknown (" + it.FromFingerprint + ")"
		}
		fmt.Fprintf(app.Stdout, "%s  from %s  (%s)  %s\n", it.ID, from, it.Tool, it.Received)
	}
	return nil
}
