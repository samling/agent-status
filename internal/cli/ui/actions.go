package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/focus"
	"github.com/samling/agent-status/internal/state"
)

func (m *uiModel) moveSelection(delta int) {
	if len(m.sessions) == 0 {
		m.selectedID = ""
		return
	}
	cur := 0
	for i, s := range m.sessions {
		if s.SessionID == m.selectedID {
			cur = i
			break
		}
	}
	next := cur + delta
	if next < 0 {
		next = 0
	} else if next >= len(m.sessions) {
		next = len(m.sessions) - 1
	}
	m.selectedID = m.sessions[next].SessionID
}

func (m uiModel) activeSelectionID() string {
	if m.selectedID != "" {
		return m.selectedID
	}
	if len(m.sessions) > 0 {
		return m.sessions[0].SessionID
	}
	return ""
}

func sessionsContain(ss []state.Session, id string) bool {
	for _, s := range ss {
		if s.SessionID == id {
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

	sm, ok := m.meta[id]
	if !ok {
		fresh, err := discovery.LiveSessionMeta()
		if err != nil {
			m.status = "focus error: " + err.Error()
			return m, nil
		}
		sm, ok = fresh[id]
	}
	if !ok {
		m.status = "session not found in live meta"
		return m, nil
	}
	if sm.PID <= 0 {
		m.status = "session has no live PID"
		return m, nil
	}
	msg, err := focus.PID(sm.PID)
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
