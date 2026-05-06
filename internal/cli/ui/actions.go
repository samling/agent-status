package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/state"
)

// moveSelection shifts the highlighted row by delta, clamped to the
// visible session range. If selectedID is unset or stale we treat
// row 0 (the View's default) as the starting point so the delta
// applies on the first keystroke.
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

// activeSelectionID returns the currently focused session id, falling
// back to the visible first row when the user hasn't picked anything.
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

// focusSelected POSTs to /focus/{id} on the collector. The TUI used
// to call focus.PID directly, but routing through the same REST
// endpoint as notification activations means there's exactly one
// place that knows how to focus a session — the server. The PID
// lookup, ancestry walk, and compositor IPC all happen server-side.
func (m uiModel) focusSelected() (uiModel, tea.Cmd) {
	id := m.activeSelectionID()
	if id == "" {
		m.status = "no sessions to focus"
		return m, nil
	}
	m.selectedID = id
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := server.Focus(ctx, m.serverAddr, id)
	if err != nil {
		m.status = "focus error: " + err.Error()
		return m, nil
	}
	m.status = resp.Message
	if m.quitAfterFocus {
		return m, tea.Quit
	}
	return m, nil
}
