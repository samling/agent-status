package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/client"
	"github.com/samling/agent-status/internal/sessionview"
	"github.com/samling/agent-status/internal/state"
)

func (m *uiModel) moveSelection(delta int) {
	if len(m.cards) == 0 {
		m.selectedID = ""
		return
	}
	cur := 0
	for i, c := range m.cards {
		if c.SessionID == m.selectedID {
			cur = i
			break
		}
	}
	next := cur + delta
	if next < 0 {
		next = 0
	} else if next >= len(m.cards) {
		next = len(m.cards) - 1
	}
	m.selectedID = m.cards[next].SessionID
}

func (m uiModel) activeSelectionID() string {
	if m.selectedID != "" {
		return m.selectedID
	}
	if len(m.cards) > 0 {
		return m.cards[0].SessionID
	}
	return ""
}

func cardsContain(cards []sessionview.SessionCard, id string) bool {
	for _, c := range cards {
		if c.SessionID == id {
			return true
		}
	}
	return false
}

func (m uiModel) beginNote() uiModel {
	id := m.activeSelectionID()
	if id == "" {
		m.status = "no session to note"
		return m
	}
	m.inputMode = true
	m.inputForID = id
	m.inputBuf = m.notes[id]
	m.status = ""
	return m
}

func (m uiModel) commitNote() uiModel {
	id := m.inputForID
	text := strings.TrimSpace(m.inputBuf)
	m.inputMode = false
	m.inputBuf = ""
	m.inputForID = ""
	if id == "" {
		return m
	}
	if err := state.SaveNote(m.notesPath, id, text); err != nil {
		m.status = "save note error: " + err.Error()
		return m
	}
	if m.notes == nil {
		m.notes = map[string]string{}
	}
	if text == "" {
		delete(m.notes, id)
	} else {
		m.notes[id] = text
	}
	return m
}

func (m uiModel) focusSelected() (uiModel, tea.Cmd) {
	id := m.activeSelectionID()
	if id == "" {
		m.status = "no sessions to focus"
		return m, nil
	}
	m.selectedID = id

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msg, err := client.New(m.serverAddr).Focus(ctx, id)
	if err != nil {
		m.status = "focus error: " + err.Error()
		return m, nil
	}
	m.status = msg
	if m.quitAfterFocus {
		return m, tea.Quit
	}
	return m, nil
}
