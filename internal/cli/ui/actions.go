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
	visible := m.visibleCards()
	if len(visible) == 0 {
		m.selectedID = ""
		m.scrollOffset = 0
		return
	}
	cur := 0
	for i, c := range visible {
		if c.SessionID == m.selectedID {
			cur = i
			break
		}
	}
	next := cur + delta
	if next < 0 {
		next = 0
	} else if next >= len(visible) {
		next = len(visible) - 1
	}
	m.selectedID = visible[next].SessionID
	m.keepSelectionVisible()
}

func (m uiModel) activeSelectionID() string {
	if m.selectedID != "" {
		return m.selectedID
	}
	visible := m.visibleCards()
	if len(visible) > 0 {
		return visible[0].SessionID
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

func cardIndex(cards []sessionview.SessionCard, id string) int {
	for i, c := range cards {
		if c.SessionID == id {
			return i
		}
	}
	return -1
}

func parentIDFor(cards []sessionview.SessionCard, id string) string {
	for _, c := range cards {
		if c.SessionID == id {
			return c.ParentSessionID
		}
	}
	return ""
}

func (m *uiModel) clampCardScroll() {
	visible := m.visibleCards()
	if len(visible) == 0 {
		m.scrollOffset = 0
		return
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if m.scrollOffset >= len(visible) {
		m.scrollOffset = len(visible) - 1
	}
}

func (m *uiModel) keepSelectionVisible() {
	m.clampCardScroll()
	visible := m.visibleCards()
	if len(visible) == 0 {
		return
	}
	id := m.selectedID
	if id == "" {
		id = visible[0].SessionID
	}
	idx := cardIndex(visible, id)
	if idx < 0 {
		return
	}
	if idx < m.scrollOffset {
		m.scrollOffset = idx
		return
	}
	for m.scrollOffset < idx {
		_, end := m.visibleCardRangeFrom(m.scrollOffset, id)
		if idx < end {
			break
		}
		m.scrollOffset++
	}
	m.clampCardScroll()
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
	m = m.updateNoteDisplay(id, text)
	return m
}

func (m uiModel) updateNoteDisplay(id, text string) uiModel {
	for i := range m.cards {
		if m.cards[i].SessionID == id {
			m.cards[i].Note = text
			break
		}
	}
	if m.detailFor != id || m.detail.SessionID != id {
		return m
	}
	m.detail.Metadata.Note = text
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

func (m uiModel) visibleCards() []sessionview.SessionCard {
	if len(m.cards) == 0 {
		return nil
	}
	top := make([]sessionview.SessionCard, 0, len(m.cards))
	children := map[string][]sessionview.SessionCard{}
	parents := map[string]struct{}{}
	for _, card := range m.cards {
		if card.ParentSessionID == "" {
			top = append(top, card)
			parents[card.SessionID] = struct{}{}
			continue
		}
		children[card.ParentSessionID] = append(children[card.ParentSessionID], card)
	}
	out := make([]sessionview.SessionCard, 0, len(m.cards))
	for _, card := range top {
		out = append(out, card)
		if !m.expandedParents[card.SessionID] {
			continue
		}
		for _, child := range children[card.SessionID] {
			if _, ok := parents[child.ParentSessionID]; ok {
				out = append(out, child)
			}
		}
	}
	return out
}

func (m *uiModel) toggleExpanded(id string) bool {
	if id == "" {
		return false
	}
	parentID := parentIDFor(m.cards, id)
	if parentID != "" {
		id = parentID
	}
	card, ok := cardByID(m.cards, id)
	if !ok || card.ChildCount == 0 {
		return false
	}
	if m.expandedParents == nil {
		m.expandedParents = map[string]bool{}
	}
	if m.expandedParents[id] {
		delete(m.expandedParents, id)
		if parentIDFor(m.cards, m.selectedID) == id {
			m.selectedID = id
		}
	} else {
		m.expandedParents[id] = true
	}
	m.keepSelectionVisible()
	return true
}

func (m *uiModel) expandSelected() bool {
	card, ok := cardByID(m.cards, m.selectedID)
	if !ok || card.ChildCount == 0 {
		return false
	}
	if m.expandedParents == nil {
		m.expandedParents = map[string]bool{}
	}
	if m.expandedParents[card.SessionID] {
		return false
	}
	m.expandedParents[card.SessionID] = true
	m.keepSelectionVisible()
	return true
}

func (m *uiModel) collapseSelected() bool {
	if parentID := parentIDFor(m.cards, m.selectedID); parentID != "" {
		m.selectedID = parentID
		if m.expandedParents != nil {
			delete(m.expandedParents, parentID)
		}
		m.keepSelectionVisible()
		return true
	}
	card, ok := cardByID(m.cards, m.selectedID)
	if !ok || card.ChildCount == 0 || !m.expandedParents[card.SessionID] {
		return false
	}
	delete(m.expandedParents, card.SessionID)
	m.keepSelectionVisible()
	return true
}

func (m *uiModel) pruneExpandedParents() {
	if len(m.expandedParents) == 0 {
		return
	}
	for id := range m.expandedParents {
		card, ok := cardByID(m.cards, id)
		if !ok || card.ChildCount == 0 {
			delete(m.expandedParents, id)
		}
	}
}

func cardByID(cards []sessionview.SessionCard, id string) (sessionview.SessionCard, bool) {
	for _, card := range cards {
		if card.SessionID == id {
			return card, true
		}
	}
	return sessionview.SessionCard{}, false
}
