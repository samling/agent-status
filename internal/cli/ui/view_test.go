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
	if !strings.Contains(out, "a-very-long") || !strings.Contains(out, "...") {
		t.Fatalf("View() missing truncated title; output:\n%s", out)
	}
	if strings.Contains(out, "UserPromptSubmit") {
		t.Fatalf("View() should keep event details out of cards; output:\n%s", out)
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
			Metadata: detailMetadata(
				metadataItem{Label: "model", Value: "gpt-5.5"},
				metadataItem{Label: "branch", Value: "feature/ui"},
			),
			Conversation: []sessionview.ConversationMessage{
				{Role: "user", Text: "newest"},
				{Role: "assistant", Text: "older"},
			},
		},
	}

	out := m.View()
	for _, want := range []string{"codex", "agent-status", "Metadata", "model", "gpt-5.5", "Conversation", "User", "newest"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q; output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CODEX") {
		t.Fatalf("View() should render agent names lowercase; output:\n%s", out)
	}
	if strings.Index(out, "newest") > strings.Index(out, "older") {
		t.Fatalf("conversation not newest first; output:\n%s", out)
	}
}

func TestSelectedCardKeepsFixedHeightAndOmitsPreview(t *testing.T) {
	card := sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "agent-status",
		Subtitle:  "UserPromptSubmit",
	}
	detail := sessionview.SessionDetail{
		SessionID: "s1",
		Conversation: []sessionview.ConversationMessage{
			{Role: "user", Text: "newest"},
		},
	}

	selected := renderCard(card, 36, true, true, detail, false)
	plain := renderCard(card, 36, false, false, sessionview.SessionDetail{}, false)
	if strings.Contains(selected, "user: newest") {
		t.Fatalf("selected card should not include conversation preview; output:\n%s", selected)
	}
	if lipgloss.Height(selected) != lipgloss.Height(plain) {
		t.Fatalf("selected card height = %d, plain height = %d", lipgloss.Height(selected), lipgloss.Height(plain))
	}
	if lipgloss.Height(selected) != 4 {
		t.Fatalf("selected card height = %d, want 4", lipgloss.Height(selected))
	}
}

func TestSelectedCardUsesBorderAccentWithoutInnerBackground(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	card := sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "agent-status",
		Subtitle:  "UserPromptSubmit",
	}

	out := renderCard(card, 36, true, false, sessionview.SessionDetail{}, false)
	if strings.Contains(out, "48;5;237") {
		t.Fatalf("selected card should not render an inner background block; output:\n%q", out)
	}
}

func TestSelectedCardIncludesSelectionMarker(t *testing.T) {
	selected := renderCard(sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "idle",
		Title:     "selected",
	}, 36, true, false, sessionview.SessionDetail{}, false)
	active := renderCard(sessionview.SessionCard{
		SessionID: "s2",
		Agent:     "codex",
		Status:    "active",
		Title:     "active",
	}, 36, false, false, sessionview.SessionDetail{}, false)

	if !strings.Contains(selected, ">") {
		t.Fatalf("selected card should include a selection marker; output:\n%s", selected)
	}
	if strings.Contains(active, ">") {
		t.Fatalf("unselected active card should not include a selection marker; output:\n%s", active)
	}
}

func TestRenderCardStylesSessionName(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	card := sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "agent-status",
	}

	out := renderCard(card, 36, false, false, sessionview.SessionDetail{}, false)
	if !strings.Contains(out, sessionNameStyle.Render("agent-status")) {
		t.Fatalf("renderCard() should style session name; output:\n%q", out)
	}
}

func TestRenderCardsAddsCompactBorders(t *testing.T) {
	m := uiModel{
		width:  90,
		height: 24,
		cards: []sessionview.SessionCard{
			{SessionID: "s1", Agent: "codex", Status: "active", Title: "first", Subtitle: "ready"},
			{SessionID: "s2", Agent: "claude-code", Status: "waiting", Title: "second", Subtitle: "approve Bash"},
		},
		selectedID: "s1",
	}

	out := m.renderCards(36, "s1")
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Fatalf("renderCards() missing card borders; output:\n%s", out)
	}
	if strings.Contains(out, "╯\n\n╭") {
		t.Fatalf("renderCards() has too much space between cards; output:\n%s", out)
	}
	if !strings.Contains(out, "╯\n╭") {
		t.Fatalf("renderCards() should keep cards compactly stacked; output:\n%s", out)
	}
}

func TestRenderCardOmitsMarkerSpaceForLeafSession(t *testing.T) {
	card := sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "leaf",
	}

	out := renderCard(card, 36, false, false, sessionview.SessionDetail{}, false)
	if !strings.Contains(out, "│ codex") {
		t.Fatalf("renderCard() should start leaf agent without marker padding; output:\n%s", out)
	}
	if strings.Contains(out, "│   codex") {
		t.Fatalf("renderCard() should not reserve marker space for leaf sessions; output:\n%s", out)
	}
}

func TestRenderCardShowsParentChildCountInAgentLine(t *testing.T) {
	card := sessionview.SessionCard{
		SessionID:  "parent",
		Agent:      "codex",
		Status:     "idle",
		Title:      "project",
		ChildCount: 16,
	}

	out := renderCard(card, 44, false, false, sessionview.SessionDetail{}, false)
	if !strings.Contains(out, "+ codex (16 children)") {
		t.Fatalf("renderCard() missing parent child count; output:\n%s", out)
	}
}

func TestRenderCardOmitsSubtitleDetails(t *testing.T) {
	card := sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "project",
		Subtitle:  "Stop",
	}

	out := renderCard(card, 36, false, false, sessionview.SessionDetail{}, false)
	if !strings.Contains(out, "project") {
		t.Fatalf("renderCard() missing title line; output:\n%s", out)
	}
	if strings.Contains(out, "Stop") {
		t.Fatalf("renderCard() should omit event details from cards; output:\n%s", out)
	}
}

func TestRenderCardColorsStatusLabels(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	for _, status := range []string{"idle", "waiting", "active"} {
		card := sessionview.SessionCard{
			SessionID: "s1",
			Agent:     "codex",
			Status:    status,
			Title:     "project",
		}
		out := renderCard(card, 36, false, false, sessionview.SessionDetail{}, false)
		if !strings.Contains(out, statusStyle(status).Render(status)) {
			t.Fatalf("renderCard() should color %s status; output:\n%q", status, out)
		}
	}
}

func TestRenderCardsCollapsesChildrenByDefault(t *testing.T) {
	m := uiModel{
		width:  90,
		height: 24,
		cards: []sessionview.SessionCard{
			{SessionID: "parent", Agent: "codex", Status: "active", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "idle", Title: "child"},
		},
		selectedID: "parent",
	}

	out := m.renderCards(36, "parent")
	if strings.Contains(out, "> codex") {
		t.Fatalf("renderCards() showed collapsed child; output:\n%s", out)
	}
	if !strings.Contains(out, "+") {
		t.Fatalf("renderCards() missing collapsed child marker; output:\n%s", out)
	}
}

func TestRenderCardsExpandsChildren(t *testing.T) {
	m := uiModel{
		width:           90,
		height:          24,
		expandedParents: map[string]bool{"parent": true},
		cards: []sessionview.SessionCard{
			{SessionID: "parent", Agent: "codex", Status: "active", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "idle", Title: "child"},
		},
		selectedID: "parent",
	}

	out := m.renderCards(36, "parent")
	if !strings.Contains(out, "child") {
		t.Fatalf("renderCards() missing expanded child; output:\n%s", out)
	}
	if !strings.Contains(out, "-") {
		t.Fatalf("renderCards() missing expanded child marker; output:\n%s", out)
	}
}

func TestRenderCardsIndentsExpandedChildren(t *testing.T) {
	m := uiModel{
		width:           90,
		height:          24,
		expandedParents: map[string]bool{"parent": true},
		cards: []sessionview.SessionCard{
			{SessionID: "parent", Agent: "codex", Status: "active", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "idle", Title: "nested"},
		},
		selectedID: "parent",
	}

	out := m.renderCards(36, "parent")
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "nested") {
			found = true
			if !strings.HasPrefix(line, "  ") {
				t.Fatalf("child card line should be indented; line=%q output:\n%s", line, out)
			}
		}
	}
	if !found {
		t.Fatalf("renderCards() missing child title; output:\n%s", out)
	}
	if strings.Contains(out, "> codex") {
		t.Fatalf("renderCards() should not show a child marker; output:\n%s", out)
	}
}

func TestRenderCardsKeepsOpenChildCountOutOfSubtitle(t *testing.T) {
	m := uiModel{
		width:  90,
		height: 24,
		cards: []sessionview.SessionCard{
			{
				SessionID:      "parent",
				Agent:          "codex",
				Status:         "active",
				Title:          "Compare lazyagent to agent-status",
				Subtitle:       "Stop",
				ChildCount:     17,
				OpenChildCount: 6,
			},
		},
		selectedID: "parent",
	}

	out := m.renderCards(36, "parent")
	if strings.Contains(out, "Stop") {
		t.Fatalf("renderCards() should keep event details out of cards; output:\n%s", out)
	}
	if !strings.Contains(out, "(17 children)") {
		t.Fatalf("renderCards() should show total children in the agent line; output:\n%s", out)
	}
	for _, noisy := range []string{"6 open"} {
		if strings.Contains(out, noisy) {
			t.Fatalf("renderCards() should not append child counts to subtitle; found %q in:\n%s", noisy, out)
		}
	}
}

func TestMoveSelectionUsesVisibleChildren(t *testing.T) {
	m := uiModel{
		width:           90,
		height:          24,
		expandedParents: map[string]bool{"parent": true},
		cards: []sessionview.SessionCard{
			{SessionID: "parent", Agent: "codex", Status: "active", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "idle", Title: "child"},
			{SessionID: "next", Agent: "codex", Status: "idle", Title: "next"},
		},
		selectedID: "parent",
	}

	m.moveSelection(+1)
	if m.selectedID != "child" {
		t.Fatalf("selectedID = %q, want child", m.selectedID)
	}
	m.toggleExpanded("parent")
	if m.selectedID != "parent" {
		t.Fatalf("selectedID after collapse = %q, want parent", m.selectedID)
	}
}

func TestRenderSessionDetailUsesSectionDividerBetweenSections(t *testing.T) {
	out := renderSessionDetail(sessionview.SessionDetail{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "agent-status",
		Metadata: detailMetadata(
			metadataItem{Label: "model", Value: "gpt-5.5"},
		),
		Conversation: []sessionview.ConversationMessage{
			{Role: "user", Text: "hello"},
		},
	}, 40)

	if first := strings.Split(out, "\n")[0]; !strings.Contains(first, "Metadata") {
		t.Fatalf("renderSessionDetail() should start with Metadata; first line %q output:\n%s", first, out)
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "────────") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("renderSessionDetail() divider count = %d, want 1; output:\n%s", count, out)
	}
}

func TestRenderSessionDetailOmitsRepeatedHeader(t *testing.T) {
	out := renderSessionDetail(sessionview.SessionDetail{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "left-pane-session",
		Metadata: detailMetadata(
			metadataItem{Label: "model", Value: "gpt-5.5"},
		),
	}, 80)

	for _, repeated := range []string{"left-pane-session", "active"} {
		if strings.Contains(out, repeated) {
			t.Fatalf("renderSessionDetail() should omit repeated header value %q; output:\n%s", repeated, out)
		}
	}
}

func TestRenderSessionDetailLabelsAssistantAsAgent(t *testing.T) {
	out := renderSessionDetail(sessionview.SessionDetail{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "agent-status",
		Conversation: []sessionview.ConversationMessage{
			{Role: "assistant", Text: "here is the answer"},
		},
	}, 40)

	if !strings.Contains(out, "Agent here is the answer") {
		t.Fatalf("renderSessionDetail() missing Agent label; output:\n%s", out)
	}
	if strings.Contains(out, "AI   here is the answer") {
		t.Fatalf("renderSessionDetail() should not use AI label; output:\n%s", out)
	}
}

func TestRenderSessionDetailDarkensConversationLabels(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	out := renderSessionDetail(sessionview.SessionDetail{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "agent-status",
		Conversation: []sessionview.ConversationMessage{
			{Role: "user", Text: "question"},
			{Role: "assistant", Text: "answer"},
		},
	}, 40)

	for _, want := range []string{"\x1b[38;5;245mUser", "\x1b[38;5;245mAgent"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderSessionDetail() should darken conversation label %q; output:\n%q", want, out)
		}
	}
	for _, want := range []string{"question", "answer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderSessionDetail() missing message %q; output:\n%q", want, out)
		}
	}
	if hasDanglingANSI(out) {
		t.Fatalf("renderSessionDetail() has dangling ANSI sequence; output:\n%q", out)
	}
}

func TestRenderSessionDetailGroupsMetadataRows(t *testing.T) {
	out := renderSessionDetail(sessionview.SessionDetail{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "agent-status",
		Metadata: detailMetadata(
			metadataItem{Label: "agent", Value: "codex"},
			metadataItem{Label: "entrypoint", Value: "vscode"},
			metadataItem{Label: "version", Value: "0.128.0"},
			metadataItem{Label: "model", Value: "gpt-5.5"},
			metadataItem{Label: "session", Value: "agent-status"},
			metadataItem{Label: "cwd", Value: "/tmp/project"},
			metadataItem{Label: "branch", Value: "feature/ui"},
			metadataItem{Label: "pid", Value: "1234"},
			metadataItem{Label: "parent", Value: "root"},
			metadataItem{Label: "children", Value: "2, 1 open"},
		),
	}, 120)

	assertSameRenderedLine(t, out, "agent: codex", "entrypoint: vscode", "version: 0.128.0")
	assertSameRenderedLine(t, out, "model: gpt-5.5")
	assertSameRenderedLine(t, out, "cwd: /tmp/project", "branch: feature/ui")
	assertSameRenderedLine(t, out, "pid: 1234", "parent: root", "children: 2 (1 open)")
}

func assertSameRenderedLine(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		matched := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("rendered output missing same-line parts %#v; output:\n%s", parts, out)
}

func detailMetadata(fields ...metadataItem) sessionview.DetailMetadata {
	var meta sessionview.DetailMetadata
	for _, field := range fields {
		switch field.Label {
		case "agent":
			meta.Agent = field.Value
		case "entrypoint":
			meta.Entrypoint = field.Value
		case "version":
			meta.Version = field.Value
		case "model":
			meta.Model = field.Value
		case "session":
			meta.Session = field.Value
		case "session id":
			meta.SessionID = field.Value
		case "cwd":
			meta.Cwd = field.Value
		case "branch":
			meta.Branch = field.Value
		case "pid":
			if field.Value == "1234" {
				meta.PID = 1234
			}
		case "parent":
			meta.ParentSessionID = field.Value
		case "children":
			meta.ChildCount = 2
			meta.OpenChildCount = 1
		case "last event":
			meta.LastEvent = field.Value
		case "waiting":
			meta.Waiting = field.Value
		case "note":
			meta.Note = field.Value
		}
	}
	return meta
}

func TestViewAddsVerticalPanelDivider(t *testing.T) {
	m := uiModel{
		width:  100,
		height: 24,
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
		},
	}

	out := m.View()
	if !strings.Contains(out, "│") {
		t.Fatalf("View() should include a vertical divider between panes; output:\n%s", out)
	}
}

func TestViewPanelDividerUsesRuleStyleAndFullPaneHeight(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	m := uiModel{
		width:  100,
		height: 24,
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
		},
	}

	out := m.View()
	styledPipe := dimStyle.Render("│")
	if got := strings.Count(out, styledPipe); got != m.cardPaneHeight() {
		t.Fatalf("styled divider height = %d, want %d; output:\n%q", got, m.cardPaneHeight(), out)
	}
	if strings.Contains(out, " │ ") {
		t.Fatalf("divider should be styled, not plain; output:\n%q", out)
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

func TestViewSummarizesParentAndChildSessionCounts(t *testing.T) {
	m := uiModel{
		width:  90,
		height: 20,
		cards: []sessionview.SessionCard{
			{SessionID: "parent-1", Agent: "codex", Status: "idle", Title: "parent 1", ChildCount: 2},
			{SessionID: "child-1", ParentSessionID: "parent-1", Agent: "codex", Status: "idle", Title: "child 1"},
			{SessionID: "child-2", ParentSessionID: "parent-1", Agent: "codex", Status: "idle", Title: "child 2"},
			{SessionID: "parent-2", Agent: "claude-code", Status: "active", Title: "parent 2"},
		},
		selectedID: "parent-1",
	}

	out := m.View()
	if !strings.Contains(out, "2 sessions, 2 children") {
		t.Fatalf("View() missing parent/child count summary; output:\n%s", out)
	}
	if strings.Contains(out, "4 session(s)") {
		t.Fatalf("View() should not count children as top-level sessions; output:\n%s", out)
	}
}

func TestRenderMetadataKeepsNarrowValuesVisible(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	out := strings.Join(renderMetadata([]metadataItem{
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
			Metadata: detailMetadata(
				metadataItem{Label: "note", Value: "old note"},
			),
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
	if !strings.Contains(out, "claude-code") {
		t.Fatalf("View() should keep agent kind visible; output:\n%s", out)
	}
	if strings.Contains(out, "CLAUDE-CODE") {
		t.Fatalf("View() should render agent names lowercase; output:\n%s", out)
	}
	if strings.Contains(out, "approve Bash") {
		t.Fatalf("View() should keep waiting details out of cards; output:\n%s", out)
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
