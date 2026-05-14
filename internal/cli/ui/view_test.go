package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/samling/agent-status/internal/sessionview"
)

func TestViewTruncatesLongCardTitle(t *testing.T) {
	longTitle := "a-very-long-project-name-that-will-truncate"
	m := uiModel{
		width:  70,
		height: 20,
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     longTitle,
			Subtitle:  "UserPromptSubmit",
		}},
		selectedID: "s1",
	}

	out := m.View()
	if strings.Contains(out, longTitle) {
		t.Fatalf("View() contains untruncated title %q", longTitle)
	}
	if !strings.Contains(out, "a-very-long-project-...") {
		t.Fatalf("View() missing truncated title; output:\n%s", out)
	}
	if !strings.Contains(out, "UserPromptSubmit") {
		t.Fatalf("View() should keep subtitle visible; output:\n%s", out)
	}
}

func TestViewShowsSessionCardsAndRightPaneDetail(t *testing.T) {
	m := uiModel{
		width:  100,
		height: 28,
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Subtitle:  "UserPromptSubmit",
			StatusAt:  "2026-05-14T10:00:00Z",
		}},
		selectedID: "s1",
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Metadata: []sessionview.Field{
				{Label: "model", Value: "gpt-5.5"},
				{Label: "branch", Value: "feature/ui"},
			},
			Conversation: []sessionview.ConversationMessage{
				{Role: "user", Text: "newest"},
				{Role: "assistant", Text: "older"},
			},
		},
	}

	out := m.View()
	for _, want := range []string{"CODEX", "agent-status", "Metadata", "model", "gpt-5.5", "Conversation", "User", "newest"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q; output:\n%s", want, out)
		}
	}
	if strings.Index(out, "newest") > strings.Index(out, "older") {
		t.Fatalf("conversation not newest first; output:\n%s", out)
	}
}

func TestMoveSelectionScrollsSelectedCardIntoView(t *testing.T) {
	cards := make([]sessionview.SessionCard, 12)
	for i := range cards {
		cards[i] = sessionview.SessionCard{
			SessionID: fmt.Sprintf("s%02d", i),
			Agent:     "codex",
			Status:    "active",
			Title:     fmt.Sprintf("session-%02d", i),
			Subtitle:  "UserPromptSubmit",
		}
	}
	m := uiModel{
		width:      80,
		height:     12,
		cards:      cards,
		selectedID: "s00",
	}

	for i := 0; i < 9; i++ {
		m.moveSelection(+1)
	}

	if m.scrollOffset == 0 {
		t.Fatal("scrollOffset stayed at 0, want selected card scrolled into view")
	}
	out := m.View()
	if !strings.Contains(out, "session-09") {
		t.Fatalf("View() missing selected card; output:\n%s", out)
	}
	if strings.Contains(out, "session-00") {
		t.Fatalf("View() still shows first card after scrolling; output:\n%s", out)
	}
	if !strings.Contains(out, "10/12") {
		t.Fatalf("View() missing position hint; output:\n%s", out)
	}
}

func TestRenderMetadataKeepsNarrowValuesVisible(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	out := strings.Join(renderMetadata([]sessionview.Field{
		{Label: "model", Value: "gpt-5.5"},
		{Label: "branch", Value: "feature/ui"},
	}, 20), "\n")
	for _, want := range []string{"gpt-5.5", "feature/ui"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderMetadata() missing %q; output:\n%q", want, out)
		}
	}
	if hasDanglingANSI(out) {
		t.Fatalf("renderMetadata() has dangling ANSI sequence; output:\n%q", out)
	}
}

func TestViewConfigReplacesDetailPaneInSmallHeight(t *testing.T) {
	m := uiModel{
		width:      90,
		height:     10,
		showConfig: true,
		configPath: "/tmp/config.toml",
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Subtitle:  "UserPromptSubmit",
		}},
		selectedID: "s1",
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Conversation: []sessionview.ConversationMessage{
				{Role: "user", Text: "conversation should be hidden"},
			},
		},
	}

	out := m.View()
	if !strings.Contains(out, "Config") {
		t.Fatalf("View() missing Config; output:\n%s", out)
	}
	if strings.Contains(out, "conversation should be hidden") {
		t.Fatalf("View() should replace detail pane with config; output:\n%s", out)
	}
}

func TestViewShowsDetailUnavailable(t *testing.T) {
	m := uiModel{
		width:  90,
		height: 20,
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Subtitle:  "UserPromptSubmit",
		}},
		selectedID: "s1",
		detailFor:  "s1",
		detailErr:  errors.New("GET /views/sessions/s1: 500"),
	}

	out := m.View()
	if !strings.Contains(out, "detail unavailable") {
		t.Fatalf("View() missing detail unavailable state; output:\n%s", out)
	}
	if strings.Contains(out, "select a session") {
		t.Fatalf("View() should not ask to select a session; output:\n%s", out)
	}
}

func TestCommitNoteUpdatesVisibleDetailMetadata(t *testing.T) {
	m := uiModel{
		width:      100,
		height:     24,
		notesPath:  t.TempDir() + "/notes.json",
		notes:      map[string]string{"s1": "old note"},
		inputMode:  true,
		inputForID: "s1",
		inputBuf:   "new note",
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
		}},
		selectedID: "s1",
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Metadata: []sessionview.Field{
				{Label: "note", Value: "old note"},
			},
		},
	}

	m = m.commitNote()
	out := m.View()
	if !strings.Contains(out, "new note") {
		t.Fatalf("View() missing saved note; output:\n%s", out)
	}
	if strings.Contains(out, "old note") {
		t.Fatalf("View() still shows stale note; output:\n%s", out)
	}
}

func TestViewKeepsAgentVisibleInNarrowPane(t *testing.T) {
	m := uiModel{
		width:  70,
		height: 20,
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "claude-code",
			Status:    "waiting",
			Title:     "a-very-long-project-name-that-will-truncate",
			Subtitle:  "approve Bash",
		}},
		selectedID: "s1",
	}

	out := m.View()
	if !strings.Contains(out, "CLAUDE-CODE") {
		t.Fatalf("View() should keep agent kind visible; output:\n%s", out)
	}
	if !strings.Contains(out, "approve Bash") {
		t.Fatalf("View() should keep waiting hint visible; output:\n%s", out)
	}
}

func hasDanglingANSI(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			continue
		}
		if i+1 >= len(s) || s[i+1] != '[' {
			return true
		}
		i += 2
		for ; i < len(s); i++ {
			if s[i] >= '@' && s[i] <= '~' {
				break
			}
		}
		if i == len(s) {
			return true
		}
	}
	return false
}
