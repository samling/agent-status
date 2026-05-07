package ui

import (
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
	// serverUp is the result of a TCP dial against the collector's
	// listen address performed alongside the state read. It powers
	// the connectivity indicator in the header.
	serverUp bool
}
type errMsg struct{ err error }

// loadSnapshot is the IO half of the refresh tick: read state.json
// from disk for the session list, scan agent home directories for
// live metadata (PID, cwd, version, model — written by Claude/codex,
// not by agent-status), and parse the focused row's transcript.
//
// All reads are direct file IO. The collector is the single writer of
// state.json (via tmpfile + atomic rename), so any os.ReadFile here
// returns a consistent snapshot. The connectivity indicator is a
// separate cheap TCP dial against the listen address — see
// server.Reachable — because there's no read-side HTTP surface to
// implicitly probe.
func loadSnapshot(statePath, serverAddr, selectedID string, mode sortMode) tea.Cmd {
	return func() tea.Msg {
		sessions, err := state.Load(statePath)
		if err != nil {
			// Render an empty view instead of erroring out; the
			// disconnected indicator already tells the user the
			// collector is unreachable on transient blips.
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
			if md, ok := meta[focus]; ok {
				detail, _ = discovery.LoadTranscriptForMeta(focus, md)
			}
		}
		return snapshotMsg{
			sessions:  sessions,
			meta:      meta,
			detail:    detail,
			detailFor: focus,
			sortedBy:  mode,
			serverUp:  server.Reachable(serverAddr),
		}
	}
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}
