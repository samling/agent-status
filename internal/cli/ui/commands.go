package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/client"
	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type tickMsg time.Time
type snapshotMsg struct {
	sessions  []state.Session
	meta      map[string]source.SessionMeta
	detail    source.TranscriptInfo
	detailFor string
	sortedBy  sortMode
	serverUp  bool
}
type errMsg struct{ err error }

func loadSnapshot(serverAddr, selectedID string, mode sortMode) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c := client.New(serverAddr)
		sessions, err := c.Sessions(ctx)
		serverUp := err == nil
		if err != nil {
			sessions = nil
		}
		meta, _ := c.Meta(ctx)
		sortSessions(sessions, mode)
		focus := selectedID
		if focus == "" && len(sessions) > 0 {
			focus = sessions[0].SessionID
		}
		var detail source.TranscriptInfo
		if focus != "" {
			if _, ok := meta[focus]; ok {
				detail, _ = c.Transcript(ctx, focus)
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
