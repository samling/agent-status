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
	detailErr error
	sortedBy  sortMode
	serverUp  bool
}
type detailMsg struct {
	detail    sessionview.SessionDetail
	detailFor string
	detailErr error
}
type messageListMsg struct {
	messages  sessionview.MessageList
	sessionID string
	err       error
}
type messageDetailMsg struct {
	detail    sessionview.MessageDetail
	sessionID string
	messageID string
	err       error
}
type errMsg struct{ err error }

func loadSnapshot(serverAddr, selectedID string, mode sortMode, previousOrder map[string]int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c := client.New(serverAddr)
		cards, err := c.SessionCards(ctx)
		serverUp := err == nil
		if err != nil {
			cards = nil
		}
		sortCards(cards, mode, previousOrder)
		focus := initialFocusID(cards, selectedID)
		var detail sessionview.SessionDetail
		var detailErr error
		if focus != "" {
			detail, detailErr = c.SessionDetail(ctx, focus)
		}
		return snapshotMsg{
			cards:     cards,
			detail:    detail,
			detailFor: focus,
			detailErr: detailErr,
			sortedBy:  mode,
			serverUp:  serverUp,
		}
	}
}

func initialFocusID(cards []sessionview.SessionCard, selectedID string) string {
	if selectedID != "" && cardsContain(cards, selectedID) {
		return selectedID
	}
	if selectedID == "" {
		for _, card := range cards {
			if card.ParentSessionID == "" && card.Status == "active" {
				return card.SessionID
			}
		}
	}
	for _, card := range cards {
		if card.ParentSessionID == "" {
			return card.SessionID
		}
	}
	return ""
}

func loadDetail(serverAddr, id string) tea.Cmd {
	return func() tea.Msg {
		if id == "" {
			return detailMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		detail, err := client.New(serverAddr).SessionDetail(ctx, id)
		return detailMsg{
			detail:    detail,
			detailFor: id,
			detailErr: err,
		}
	}
}

func loadMessages(serverAddr, id string) tea.Cmd {
	return func() tea.Msg {
		if id == "" {
			return messageListMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		messages, err := client.New(serverAddr).SessionMessages(ctx, id)
		return messageListMsg{
			messages:  messages,
			sessionID: id,
			err:       err,
		}
	}
}

func loadMessage(serverAddr, sessionID, messageID string) tea.Cmd {
	return func() tea.Msg {
		if sessionID == "" || messageID == "" {
			return messageDetailMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		detail, err := client.New(serverAddr).SessionMessage(ctx, sessionID, messageID)
		return messageDetailMsg{
			detail:    detail,
			sessionID: sessionID,
			messageID: messageID,
			err:       err,
		}
	}
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}
