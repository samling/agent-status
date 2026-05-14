package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/client"
	"github.com/samling/agent-status/internal/sessionview"
)

type tickMsg time.Time
type snapshotMsg struct {
	cards     []sessionview.SessionCard
	detail    sessionview.SessionDetail
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
		cards, err := c.SessionCards(ctx)
		serverUp := err == nil
		if err != nil {
			cards = nil
		}
		sortCards(cards, mode)
		focus := selectedID
		if focus == "" && len(cards) > 0 {
			focus = cards[0].SessionID
		}
		var detail sessionview.SessionDetail
		if focus != "" {
			detail, _ = c.SessionDetail(ctx, focus)
		}
		return snapshotMsg{
			cards:     cards,
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
