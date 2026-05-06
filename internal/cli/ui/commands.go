package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/state"
)

// tea.Msg types produced by the commands below. tickMsg fires on each
// refresh interval; snapshotMsg carries a fresh state read; errMsg
// surfaces a load failure to the View.
type tickMsg time.Time
type snapshotMsg struct {
	sessions  []state.Session
	meta      map[string]discovery.SessionMeta
	detail    discovery.TranscriptInfo
	detailFor string
	// sortedBy records the sort mode used to produce sessions, so
	// Update can skip re-sorting in the common case where the user
	// hasn't changed the mode mid-load.
	sortedBy sortMode
	// serverUp is true when the /state call succeeded. Doubling as
	// the connectivity indicator avoids a second TCP-level probe; if
	// the load failed we already know the server is unreachable or
	// broken, and an extra dial wouldn't add information.
	serverUp bool
}
type errMsg struct{ err error }

// loadSnapshot is the IO half of the refresh tick: hit /state for the
// session list, enrich with live session metadata from disk (PID, cwd,
// etc. — Claude's session files, not agent-status state), and kick off
// a transcript fetch for the focused row.
//
// Sessions come from the collector's /state endpoint rather than the
// disk file. Going through the API keeps the TUI honest about being a
// REST client of the server: same wire shape every other consumer
// uses, no second source of truth to drift from.
func loadSnapshot(serverAddr, selectedID string, mode sortMode) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sessions, err := server.LoadState(ctx, serverAddr)
		serverUp := err == nil
		if err != nil {
			// Treat unreachable collector as "no sessions" so the UI
			// keeps drawing instead of erroring out; the disconnected
			// indicator already tells the user what's going on.
			sessions = nil
		}
		meta, _ := discovery.LiveSessionMeta()
		sortSessions(sessions, mode)
		focus := selectedID
		if focus == "" && len(sessions) > 0 {
			focus = sessions[0].SessionID
		}
		var detail discovery.TranscriptInfo
		if focus != "" {
			if md, ok := meta[focus]; ok && md.Cwd != "" {
				detail, _ = discovery.LoadTranscript(focus, md.Cwd)
			}
		}
		return snapshotMsg{
			sessions:  sessions,
			meta:      meta,
			detail:    detail,
			detailFor: focus,
			sortedBy:  mode,
			serverUp:  serverUp,
		}
	}
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}
