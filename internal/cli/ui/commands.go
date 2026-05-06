package ui

import (
	"net"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/discovery"
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
	// serverUp is the result of probing the collector's listen socket
	// each tick. False when the dial fails, so the user has a visible
	// signal that updates have stalled.
	serverUp bool
}
type errMsg struct{ err error }

// loadSnapshot is the IO half of the refresh tick: read state from
// disk, enrich with live session metadata, kick off a transcript fetch
// for the focused row, and probe the collector's TCP socket. Sorted
// before returning so the View's implicit first-row default matches
// the focus we picked.
func loadSnapshot(path, serverAddr, selectedID string, mode sortMode) tea.Cmd {
	return func() tea.Msg {
		sessions, err := state.Load(path)
		if err != nil {
			return errMsg{err}
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
			serverUp:  probeServer(serverAddr),
		}
	}
}

// probeServer returns true when a TCP connection to addr succeeds
// within a short timeout. Used as a liveness signal for the indicator
// in the title bar; we don't speak any protocol because the server
// might not have an HTTP path that's free of side effects.
func probeServer(addr string) bool {
	if addr == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}
