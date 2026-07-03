package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/heurema/echos/internal/identity"
)

type FriendCmd struct {
	Add  FriendAddCmd  `cmd:"" help:"Add a friend by echo-id, fetching and verifying their key from the relay."`
	List FriendListCmd `cmd:"" help:"List saved friends."`
	Rm   FriendRmCmd   `cmd:"" aliases:"remove" help:"Remove a friend by name."`
}

type FriendAddCmd struct {
	Name   string `arg:"" help:"Local alias for this friend."`
	EchoID string `arg:"" name:"echo-id" help:"Their echo-id, as printed by their 'echos id'."`
	JSON   bool   `name:"json" help:"Emit machine-readable JSON."`
}

func (c *FriendAddCmd) Run(app *App) error {
	// Reject a malformed name before the relay round-trip — it's knowable from
	// the args alone. Upsert re-checks as defense-in-depth.
	if !identity.ValidFriendName(c.Name) {
		return fail(app, c.JSON, 1, "", "invalid friend name %q: must be non-empty with no control characters", c.Name)
	}
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
	var warning string
	if existing, ok := book.FindByEchoID(gotEchoID); ok && existing.Name != c.Name {
		if current, ok := book.Find(c.Name); !ok || current.EchoID != gotEchoID {
			warning = fmt.Sprintf("echo-id %s is already saved as %q; adding another alias %q for the same friend", gotEchoID, existing.Name, c.Name)
		}
	}
	if err := book.Upsert(identity.Friend{
		Name:        c.Name,
		EchoID:      gotEchoID,
		Fingerprint: fingerprint,
		PublicKey:   base64.StdEncoding.EncodeToString(pub.Marshal()),
		AddedAt:     app.Now(),
	}); err != nil {
		return fail(app, c.JSON, 1, "", "%v", err)
	}
	if err := book.Save(); err != nil {
		return fail(app, c.JSON, 1, "", "save friends: %v", err)
	}

	if c.JSON {
		out := map[string]any{
			"name":        c.Name,
			"echo_id":     gotEchoID,
			"fingerprint": fingerprint,
		}
		if warning != "" {
			out["warning"] = warning
		}
		return writeJSON(app.Stdout, out)
	}
	fmt.Fprintf(app.Stdout, "added %s (%s)\n", c.Name, gotEchoID)
	if warning != "" {
		fmt.Fprintf(app.Stderr, "warning: %s\n", warning)
	}
	return nil
}

type FriendListCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable JSON."`
}

func (c *FriendListCmd) Run(app *App) error {
	book, err := identity.LoadFriends(app.ConfigDir)
	if err != nil {
		return fail(app, c.JSON, 1, "", "load friends: %v", err)
	}
	friends := append([]identity.Friend(nil), book.Friends...)
	sort.Slice(friends, func(i, j int) bool { return friends[i].Name < friends[j].Name })

	if c.JSON {
		type friendView struct {
			Name        string    `json:"name"`
			EchoID      string    `json:"echo_id"`
			Fingerprint string    `json:"fingerprint"`
			AddedAt     time.Time `json:"added_at"`
		}
		views := make([]friendView, len(friends))
		for i, f := range friends {
			views[i] = friendView{Name: f.Name, EchoID: f.EchoID, Fingerprint: f.Fingerprint, AddedAt: f.AddedAt}
		}
		return writeJSON(app.Stdout, views)
	}

	if len(friends) == 0 {
		fmt.Fprintln(app.Stdout, "no friends")
		return nil
	}
	tw := tabwriter.NewWriter(app.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tECHO-ID\tFINGERPRINT\tADDED")
	for _, f := range friends {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", displayName(f.Name), f.EchoID, f.Fingerprint, f.AddedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}

// displayName strips control characters so a hand-edited friends.json can't
// corrupt the tabwriter table (Upsert prevents storing such names, but a
// tampered file bypasses that guard).
func displayName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
}

type FriendRmCmd struct {
	Name string `arg:"" help:"Local alias of the friend to remove."`
	JSON bool   `name:"json" help:"Emit machine-readable JSON."`
}

func (c *FriendRmCmd) Run(app *App) error {
	book, err := identity.LoadFriends(app.ConfigDir)
	if err != nil {
		return fail(app, c.JSON, 1, "", "load friends: %v", err)
	}
	f, ok := book.Remove(c.Name)
	if !ok {
		return fail(app, c.JSON, 1, "echos friend list", "no friend %q", c.Name)
	}
	if err := book.Save(); err != nil {
		return fail(app, c.JSON, 1, "", "save friends: %v", err)
	}

	var stillKnownAs []string
	for _, other := range book.Friends {
		if other.EchoID == f.EchoID {
			stillKnownAs = append(stillKnownAs, other.Name)
		}
	}

	if c.JSON {
		out := map[string]any{
			"name":        f.Name,
			"echo_id":     f.EchoID,
			"fingerprint": f.Fingerprint,
		}
		if len(stillKnownAs) > 0 {
			out["still_known_as"] = stillKnownAs
		}
		return writeJSON(app.Stdout, out)
	}
	fmt.Fprintf(app.Stdout, "removed %s (%s)\n", f.Name, f.EchoID)
	if len(stillKnownAs) > 0 {
		// Advisory → stderr, matching `friend add`'s duplicate-alias warning,
		// so text-mode stdout carries only the primary result.
		fmt.Fprintf(app.Stderr, "note: %s is still known as %s\n", f.EchoID, strings.Join(stillKnownAs, ", "))
	}
	return nil
}
