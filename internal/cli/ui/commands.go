package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/state"
)

type tickMsg time.Time
type snapshotMsg struct {
	sessions  []state.Session
	meta      map[string]discovery.SessionMeta
	detail    discovery.TranscriptInfo
	detailFor string
	sortedBy  sortMode
	serverUp  bool
}
type errMsg struct{ err error }

func loadSnapshot(statePath, serverAddr, selectedID string, mode sortMode) tea.Cmd {
	return func() tea.Msg {
		sessions, err := state.Load(statePath)
		if err != nil {
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
