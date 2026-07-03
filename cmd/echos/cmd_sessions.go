package main

import (
	"fmt"
	"time"

	"github.com/heurema/echos/internal/session"
)

type SessionsCmd struct {
	JSON bool   `name:"json" help:"Emit machine-readable JSON."`
	Tool string `name:"tool" help:"Filter by tool: claude or codex."`
	N    int    `name:"n" default:"20" help:"Max sessions to show."`
}

func (c *SessionsCmd) Run(app *App) error {
	sessions, err := session.DiscoverAll()
	if err != nil {
		return fail(app, c.JSON, 1, "", "discover sessions: %v", err)
	}

	if c.Tool != "" {
		filtered := sessions[:0]
		for _, s := range sessions {
			if string(s.Tool) == c.Tool {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}
	if c.N > 0 && len(sessions) > c.N {
		sessions = sessions[:c.N]
	}

	if c.JSON {
		if sessions == nil {
			sessions = []session.Session{}
		}
		return writeJSON(app.Stdout, sessions)
	}

	if len(sessions) == 0 {
		fmt.Fprintln(app.Stdout, "no sessions found")
		return nil
	}
	tw := newTabWriter(app.Stdout)
	fmt.Fprintln(tw, "TOOL\tID\tPROJECT\tTITLE\tUPDATED")
	for _, s := range sessions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Tool, s.ID, s.Project, s.Title, s.Updated.Format(time.RFC3339))
	}
	return tw.Flush()
}
