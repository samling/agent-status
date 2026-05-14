package ui

import (
	"strings"
	"testing"

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
