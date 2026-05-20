package ui

import (
	"errors"
	"fmt"
	"regexp"
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

func TestViewShowsFocusedMessageList(t *testing.T) {
	m := uiModel{
		width:      100,
		height:     28,
		selectedID: "s1",
		focusMode:  focusMessages,
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
		}},
		detailFor: "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Metadata:  detailMetadata(metadataItem{Label: "model", Value: "gpt-5.5"}),
			Conversation: []sessionview.ConversationMessage{
				{Role: "assistant", Text: "newer message"},
				{Role: "user", Text: "older message"},
			},
		},
		messageListFor: "s1",
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages: []sessionview.MessageSummary{
				{ID: "new", Role: "assistant", Preview: "newer message"},
				{ID: "tool", Role: "tool_call", Preview: "Bash"},
				{ID: "result", Role: "tool_result", Preview: "tests passed"},
				{ID: "old", Role: "user", Preview: "older message"},
			},
		},
	}

	out := m.View()
	if strings.Contains(out, "›") {
		t.Fatalf("View() should highlight selection without a cursor marker; output:\n%s", out)
	}
	for _, want := range []string{"Conversation", "Agent", "newer message"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q; output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Messages") {
		t.Fatalf("View() should keep the conversation section label; output:\n%s", out)
	}
	for _, hidden := range []string{"Tool", "Result", "Bash", "tests passed"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("View() should hide %q from the preview; output:\n%s", hidden, out)
		}
	}
	if strings.Contains(out, "(no conversation preview)") {
		t.Fatalf("View() should render focused messages, output:\n%s", out)
	}
}

func TestMessageListToggleShowsToolAndResultRows(t *testing.T) {
	m := uiModel{
		width:             100,
		height:            28,
		selectedID:        "s1",
		focusMode:         focusMessages,
		showExtraMessages: true,
		cards:             []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:         "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Conversation: []sessionview.ConversationMessage{
				{Role: "assistant", Text: "newer message"},
				{Role: "user", Text: "older message"},
			},
		},
		messageListFor: "s1",
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages: []sessionview.MessageSummary{
				{ID: "new", Role: "assistant", Preview: "newer message"},
				{ID: "tool", Role: "tool_call", Preview: "Bash"},
				{ID: "result", Role: "tool_result", Preview: "tests passed"},
				{ID: "old", Role: "user", Preview: "older message"},
			},
		},
	}

	out := m.View()
	for _, want := range []string{"Tool", "Bash", "Result", "tests passed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing toggled extra row %q; output:\n%s", want, out)
		}
	}
}

func TestSessionListToggleShowsToolAndResultRows(t *testing.T) {
	m := uiModel{
		width:             100,
		height:            28,
		selectedID:        "s1",
		focusMode:         focusCards,
		showExtraMessages: true,
		cards:             []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:         "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Conversation: []sessionview.ConversationMessage{
				{Role: "assistant", Text: "newer message"},
				{Role: "user", Text: "older message"},
			},
		},
		messageListFor: "s1",
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages: []sessionview.MessageSummary{
				{ID: "new", Role: "assistant", Preview: "newer message"},
				{ID: "tool", Role: "tool_call", Preview: "Bash"},
				{ID: "result", Role: "tool_result", Preview: "tests passed"},
				{ID: "old", Role: "user", Preview: "older message"},
			},
		},
	}

	out := m.View()
	for _, want := range []string{"Tool", "Bash", "Result", "tests passed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing toggled extra row %q; output:\n%s", want, out)
		}
	}
}

func TestExpandedMessageToggleShowsRawPayload(t *testing.T) {
	m := uiModel{
		width:      100,
		height:     28,
		selectedID: "s1",
		focusMode:  focusMessageBody,
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:  "s1",
		detail:     sessionview.SessionDetail{SessionID: "s1"},
		messageDetail: sessionview.MessageDetail{
			ID:      "7",
			Text:    "filtered body",
			RawText: "{\n  \"content\": \"raw body\"\n}",
		},
	}

	filtered := m.View()
	if !strings.Contains(filtered, "filtered body") || strings.Contains(filtered, "raw body") {
		t.Fatalf("filtered view mismatch; output:\n%s", filtered)
	}
	m.messageRaw = true
	raw := m.View()
	if !strings.Contains(raw, "Message (raw)") || !strings.Contains(raw, "\"content\": \"raw body\"") || strings.Contains(raw, "filtered body") {
		t.Fatalf("raw view mismatch; output:\n%s", raw)
	}
}

func TestRenderMessageBodyWrapsLongLines(t *testing.T) {
	m := uiModel{
		messageDetail: sessionview.MessageDetail{
			ID:   "7",
			Text: "abcdefghijklmnopqrstuvwxyz",
		},
	}

	got := m.renderMessageBody(10, 4)
	want := []string{"abcdefghij", "klmnopqrst", "uvwxyz"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("renderMessageBody() = %#v, want %#v", got, want)
	}
}

func TestRenderSelectedMessageSummaryHighlightsWholeLineGray(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	out := renderMessageSummaryLine(sessionview.MessageSummary{
		Role:    "assistant",
		Preview: "newer message",
	}, 32, true)

	if !regexp.MustCompile(`\x1b\[[0-9;]*48;5;240[0-9;]*mnewer message`).MatchString(out) {
		t.Fatalf("selected message text should use gray background; output:\n%q", out)
	}
	if strings.Contains(out, "\x1b[48;5;11m") || strings.Contains(out, "\x1b[43m") {
		t.Fatalf("selected message should not use yellow background; output:\n%q", out)
	}
}

func TestViewShowsSelectedEntryRailWhenDetailPaneIsActive(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	m := uiModel{
		width:      100,
		height:     18,
		selectedID: "s1",
		focusMode:  focusMessages,
		cards:      []sessionview.SessionCard{{SessionID: "s1", Agent: "codex", Status: "active", Title: "agent-status"}},
		detailFor:  "s1",
		detail:     sessionview.SessionDetail{SessionID: "s1"},
	}

	out := m.View()
	selectedRail := statusRail("active")
	if got, want := strings.Count(out, selectedRail), 2; got != want {
		t.Fatalf("selected entry rail height = %d lines, want %d; output:\n%q", got, want, out)
	}
	if !strings.Contains(out, selectedRail+" codex") ||
		!strings.Contains(out, selectedRail+" "+sessionNameStyle.Render("agent-status")) {
		t.Fatalf("selected entry rail should attach to the selected card; output:\n%q", out)
	}
	if strings.Contains(out, selectedRail+" "+headerStyle.Render("Metadata")) {
		t.Fatalf("selected entry rail should not attach to the detail pane; output:\n%q", out)
	}
	if got, want := strings.Count(out, accentStyle.Render("│")), m.cardPaneHeight()-2; got != want {
		t.Fatalf("detail pane divider rail height = %d lines, want %d; output:\n%q", got, want, out)
	}
	for _, want := range []string{accentStyle.Render("╭"), accentStyle.Render("╰")} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail pane divider rail missing %q; output:\n%q", want, out)
		}
	}
}

func TestViewShowsLeftPaneActiveRail(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	m := uiModel{
		width:      100,
		height:     18,
		selectedID: "s1",
		focusMode:  focusCards,
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:  "s1",
		detail:     sessionview.SessionDetail{SessionID: "s1"},
	}

	out := m.View()
	if strings.Contains(out, accentStyle.Render("╭")) || strings.Contains(out, accentStyle.Render("╰")) {
		t.Fatalf("left pane active rail should not render left-edge corners; output:\n%q", out)
	}
	for _, want := range []string{accentStyle.Render("╮"), accentStyle.Render("╯")} {
		if !strings.Contains(out, want) {
			t.Fatalf("left pane active rail missing %q; output:\n%q", want, out)
		}
	}
	if got, want := strings.Count(out, accentStyle.Render("│")), m.cardPaneHeight()-2; got != want {
		t.Fatalf("left pane active rail height = %d lines, want %d; output:\n%q", got, want, out)
	}
	if !strings.Contains(out, accentStyle.Render("╮")+"  "+headerStyle.Render("Metadata")) {
		t.Fatalf("metadata pane should have padding after the active rail; output:\n%q", out)
	}
	if strings.Contains(out, dividerStyle.Render("│")) {
		t.Fatalf("left pane active state should not render a separate dim divider; output:\n%q", out)
	}
}

func TestRenderUnselectedSessionCardsAlwaysShowStatusRail(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	for _, tc := range []struct {
		status string
		color  string
	}{
		{status: "idle", color: "8"},
		{status: "active", color: "10"},
		{status: "waiting", color: "11"},
	} {
		out := renderCard(sessionview.SessionCard{Agent: "opencode", Status: tc.status, Title: tc.status}, 36, false, false, sessionview.SessionDetail{}, false)
		wantRail := statusRail(tc.status)
		if got, want := strings.Count(out, wantRail), 2; got != want {
			t.Fatalf("%s rail count = %d, want %d; output:\n%q", tc.status, got, want, out)
		}
		if regexp.MustCompile(`\x1b\[[0-9;]*48;5;240[0-9;]*m`).MatchString(out) {
			t.Fatalf("unselected %s card should not have gray background; output:\n%q", tc.status, out)
		}
	}
}

func TestRenderSelectedSessionCardUsesGrayHighlightAndStatusRail(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	out := renderCard(sessionview.SessionCard{Agent: "opencode", Status: "waiting", Title: "needs approval"}, 42, true, true, sessionview.SessionDetail{}, false)
	wantRail := statusRail("waiting")
	if got, want := strings.Count(out, wantRail), 2; got != want {
		t.Fatalf("selected status rail count = %d, want %d; output:\n%q", got, want, out)
	}
	if !regexp.MustCompile(`\x1b\[[0-9;]*48;5;238[0-9;]*m`).MatchString(out) {
		t.Fatalf("selected card should use darker gray background; output:\n%q", out)
	}
	bodyPrefix := ansiPrefix(selectedCardTextStyle(selectedSessionFillColor(), false))
	if !strings.Contains(out, bodyPrefix+" opencode") {
		t.Fatalf("selected card body text should use light foreground on dark gray; output:\n%q", out)
	}
	if !strings.Contains(out, selectedStatusTextStyle("waiting").Render("waiting")) {
		t.Fatalf("selected card status text should keep status color on dark gray; output:\n%q", out)
	}
	if !strings.Contains(out, selectedSessionNameStyle().Render("needs approval")) {
		t.Fatalf("selected card title should keep session name color on dark gray; output:\n%q", out)
	}
	if strings.Contains(out, "\x1b[48;5;11m") || strings.Contains(out, "\x1b[43m") {
		t.Fatalf("selected card should not use status color as fill; output:\n%q", out)
	}
}

func TestDefaultAndFocusedConversationPreviewMatch(t *testing.T) {
	base := uiModel{
		width:      100,
		height:     28,
		selectedID: "s1",
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Conversation: []sessionview.ConversationMessage{
				{Role: "assistant", Text: "newer message"},
				{Role: "user", Text: "older message"},
			},
		},
		messageListFor: "s1",
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages: []sessionview.MessageSummary{
				{ID: "new", Role: "assistant", Preview: "newer message"},
				{ID: "tool", Role: "tool_call", Preview: "Bash"},
				{ID: "result", Role: "tool_result", Preview: "tests passed"},
				{ID: "old", Role: "user", Preview: "older message"},
			},
		},
	}
	focused := base
	focused.focusMode = focusMessages

	defaultOut := base.View()
	focusedOut := focused.View()
	for _, want := range []string{"Conversation", "newer message", "older message"} {
		if !strings.Contains(defaultOut, want) || !strings.Contains(focusedOut, want) {
			t.Fatalf("default/focused outputs should both contain %q\ndefault:\n%s\nfocused:\n%s", want, defaultOut, focusedOut)
		}
	}
	for _, hidden := range []string{"Bash", "tests passed"} {
		if strings.Contains(defaultOut, hidden) || strings.Contains(focusedOut, hidden) {
			t.Fatalf("preview should hide %q in both states\ndefault:\n%s\nfocused:\n%s", hidden, defaultOut, focusedOut)
		}
	}
}

func TestViewFocusedMessageListKeepsPreviewWhileLoading(t *testing.T) {
	m := uiModel{
		width:      100,
		height:     28,
		selectedID: "s1",
		focusMode:  focusMessages,
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Conversation: []sessionview.ConversationMessage{
				{Role: "user", Text: "newest preview"},
				{Role: "assistant", Text: "older preview"},
			},
		},
	}

	out := m.View()
	if !strings.Contains(out, "Conversation") || !strings.Contains(out, "newest preview") {
		t.Fatalf("View() should keep the existing preview while loading; output:\n%s", out)
	}
	if strings.Contains(out, "(loading messages)") || strings.Contains(out, "Messages") {
		t.Fatalf("View() should not replace the pane during focus entry; output:\n%s", out)
	}
}

func TestViewFocusedMessageListIsHeightBounded(t *testing.T) {
	conversation := make([]sessionview.ConversationMessage, 0, 20)
	messages := make([]sessionview.MessageSummary, 0, 20)
	for i := 0; i < 20; i++ {
		conversation = append(conversation, sessionview.ConversationMessage{Role: "user", Text: fmt.Sprintf("message %02d", i)})
		messages = append(messages, sessionview.MessageSummary{ID: fmt.Sprintf("%d", i), Role: "user", Preview: fmt.Sprintf("message %02d", i)})
	}
	m := uiModel{
		width:          100,
		height:         18,
		selectedID:     "s1",
		focusMode:      focusMessages,
		messageIndex:   12,
		messageListFor: "s1",
		cards:          []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:      "s1",
		detail: sessionview.SessionDetail{
			SessionID:    "s1",
			Conversation: conversation,
		},
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages:  messages,
		},
	}

	out := m.View()
	if !strings.Contains(out, "message 12") {
		t.Fatalf("View() missing selected message; output:\n%s", out)
	}
	if strings.Contains(out, "message 19") {
		t.Fatalf("View() should clip the message list to pane height; output:\n%s", out)
	}
}

func TestDefaultConversationPreviewIsHeightBounded(t *testing.T) {
	conversation := make([]sessionview.ConversationMessage, 0, 20)
	for i := 0; i < 20; i++ {
		conversation = append(conversation, sessionview.ConversationMessage{Role: "user", Text: fmt.Sprintf("message %02d", i)})
	}
	m := uiModel{
		width:      100,
		height:     18,
		selectedID: "s1",
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID:    "s1",
			Conversation: conversation,
		},
	}

	out := m.View()
	if !strings.Contains(out, "message 00") {
		t.Fatalf("View() missing first preview message; output:\n%s", out)
	}
	if strings.Contains(out, "message 19") {
		t.Fatalf("View() should clip default conversation preview to pane height; output:\n%s", out)
	}
}

func TestViewShowsFocusedMessageBody(t *testing.T) {
	m := uiModel{
		width:      100,
		height:     28,
		selectedID: "s1",
		focusMode:  focusMessageBody,
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Metadata:  detailMetadata(metadataItem{Label: "model", Value: "gpt-5.5"}),
		},
		messageDetailFor: "7",
		messageDetail: sessionview.MessageDetail{
			ID:   "7",
			Role: "tool_result",
			Text: "full message body",
		},
	}

	out := m.View()
	if !strings.Contains(out, "Message") || !strings.Contains(out, "full message body") {
		t.Fatalf("View() missing message body; output:\n%s", out)
	}
	if strings.Contains(out, "(no conversation preview)") {
		t.Fatalf("View() should replace conversation preview; output:\n%s", out)
	}
}

func TestViewKeymapUsesFForFocus(t *testing.T) {
	m := uiModel{width: 80, height: 20}

	out := m.View()
	if !strings.Contains(out, "enter") || !strings.Contains(out, "focus") {
		t.Fatalf("View() missing enter focus key; output:\n%s", out)
	}
	if !strings.Contains(out, "tab") || !strings.Contains(out, "detail") {
		t.Fatalf("View() missing tab detail key; output:\n%s", out)
	}
	if strings.Contains(out, "f focus") {
		t.Fatalf("View() should not show f focus; output:\n%s", out)
	}
}

func TestViewKeymapNamesToolToggleState(t *testing.T) {
	m := uiModel{width: 100, height: 24, focusMode: focusMessages}

	out := m.View()
	if !strings.Contains(out, "show tools") {
		t.Fatalf("View() missing show tools hint; output:\n%s", out)
	}

	m.showExtraMessages = true
	out = m.View()
	if !strings.Contains(out, "hide tools") {
		t.Fatalf("View() missing hide tools hint; output:\n%s", out)
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
	if lipgloss.Height(selected) != 2 {
		t.Fatalf("selected card height = %d, want 2", lipgloss.Height(selected))
	}
}

func TestSelectedCardOutsideSessionListDoesNotUseBackground(t *testing.T) {
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
		t.Fatalf("selected card should not render a background outside the session list; output:\n%q", out)
	}
}

func TestRenderCardsHighlightsSelectedCardGrayWhenSessionListIsActive(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	m := uiModel{
		width:      90,
		height:     24,
		focusMode:  focusCards,
		selectedID: "selected",
		cards: []sessionview.SessionCard{
			{SessionID: "selected", Agent: "codex", Status: "idle", Title: "selected", Age: "5h"},
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
		},
	}

	out := m.renderCards(36, "selected")
	fill := "238"
	normalPrefix := ansiPrefix(selectedCardTextStyle(lipgloss.Color(fill), false))
	titlePrefix := ansiPrefix(selectedSessionNameStyle())
	for _, text := range []string{"codex", "idle", "5h"} {
		if !regexp.MustCompile(regexp.QuoteMeta(normalPrefix) + `[^\n]*` + regexp.QuoteMeta(text)).MatchString(out) {
			t.Fatalf("selected card text %q should keep selected background; output:\n%q", text, out)
		}
	}
	if !strings.Contains(out, statusRail("idle")+normalPrefix+" codex") {
		t.Fatalf("selected card should include left padding inside the fill; output:\n%q", out)
	}
	if !regexp.MustCompile(regexp.QuoteMeta(normalPrefix) + ` codex[^\n]*idle\x1b\[0m`).MatchString(out) {
		t.Fatalf("selected card should include right padding inside the fill; output:\n%q", out)
	}
	if !regexp.MustCompile(regexp.QuoteMeta(titlePrefix) + regexp.QuoteMeta("selected")).MatchString(out) {
		t.Fatalf("selected card title should remain bold on selected background; output:\n%q", out)
	}
	if strings.Contains(out, titlePrefix+"codex") || strings.Contains(out, titlePrefix+"idle") || strings.Contains(out, titlePrefix+"5h") {
		t.Fatalf("selected card should not bold all text; output:\n%q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "codex") || strings.Contains(line, "selected") {
			if strings.ContainsAny(line, "╭╮╰╯│") {
				t.Fatalf("selected card should not render card borders; output:\n%q", out)
			}
		}
	}
	if strings.Contains(out, statusStyle("idle").Render("idle")) ||
		strings.Contains(out, sessionNameStyle.Render("selected")) {
		t.Fatalf("selected card should not use status or session-name foreground colors; output:\n%q", out)
	}
	activePrefix := ansiPrefix(lipgloss.NewStyle().Background(cardAccentColor("active", false)))
	if strings.Contains(out, activePrefix) {
		t.Fatalf("unselected active card should not use an activity background; output:\n%q", out)
	}
}

func TestRenderCardsKeepsActiveRailWithGraySelectedFill(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	m := uiModel{
		width:      90,
		height:     24,
		focusMode:  focusCards,
		selectedID: "active",
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active", Age: "5h"},
		},
	}

	out := m.renderCards(36, "active")
	fill := lipgloss.Color("238")
	selectedPrefix := ansiPrefix(lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Background(fill))
	if !strings.Contains(out, selectedPrefix) {
		t.Fatalf("selected active card should use gray fill; output:\n%q", out)
	}
	if got, want := strings.Count(out, statusRail("active")), 2; got != want {
		t.Fatalf("selected active card rail count = %d, want %d; output:\n%q", got, want, out)
	}
	if strings.ContainsAny(out, "╭╮╰╯│") {
		t.Fatalf("selected active card should not render card borders; output:\n%q", out)
	}
}

func TestRenderCardsLeavesCardsUnfilledWhenSessionPaneIsActive(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	m := uiModel{
		width:      90,
		height:     24,
		focusMode:  focusMessages,
		selectedID: "selected",
		cards: []sessionview.SessionCard{
			{SessionID: "selected", Agent: "codex", Status: "idle", Title: "selected"},
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
		},
	}

	out := m.renderCards(36, "selected")
	selectedPrefix := ansiPrefix(lipgloss.NewStyle().Background(cardAccentColor("idle", true)))
	activePrefix := ansiPrefix(lipgloss.NewStyle().Background(cardAccentColor("active", false)))
	if strings.Contains(out, selectedPrefix) || strings.Contains(out, activePrefix) {
		t.Fatalf("cards should be unfilled outside the session list; output:\n%q", out)
	}
}

func ansiPrefix(style lipgloss.Style) string {
	rendered := style.Render("x")
	parts := strings.SplitN(rendered, "x", 2)
	return parts[0]
}

func TestSelectedCardDoesNotUseSelectionMarker(t *testing.T) {
	selected := renderCard(sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "idle",
		Title:     "selected",
	}, 36, true, true, sessionview.SessionDetail{}, false)
	active := renderCard(sessionview.SessionCard{
		SessionID: "s2",
		Agent:     "codex",
		Status:    "active",
		Title:     "active",
	}, 36, false, false, sessionview.SessionDetail{}, false)

	if strings.Contains(selected, ">") {
		t.Fatalf("selected card should use gray fill and rail instead of a marker; output:\n%s", selected)
	}
	if strings.Contains(active, ">") {
		t.Fatalf("unselected active card should not include a selection marker; output:\n%s", active)
	}
}

func TestSelectedCardUsesRailOutsideSessionListFocus(t *testing.T) {
	selected := renderCard(sessionview.SessionCard{
		SessionID: "s1",
		Agent:     "codex",
		Status:    "active",
		Title:     "selected",
	}, 36, true, false, sessionview.SessionDetail{}, false)

	if got, want := strings.Count(selected, statusRail("active")), 2; got != want {
		t.Fatalf("selected card rail height = %d lines, want %d; output:\n%s", got, want, selected)
	}
	if strings.Contains(selected, ">") {
		t.Fatalf("selected card should use a rail instead of a marker outside list focus; output:\n%s", selected)
	}
}

func TestSelectedCardRailMatchesEffectiveStatus(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	for _, tc := range []struct {
		name        string
		status      string
		childStatus string
		wantStatus  string
	}{
		{name: "active", status: "active", wantStatus: "active"},
		{name: "waiting", status: "waiting", wantStatus: "waiting"},
		{name: "idle", status: "idle", wantStatus: "idle"},
		{name: "child waiting", status: "idle", childStatus: "waiting", wantStatus: "waiting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := renderCard(sessionview.SessionCard{
				SessionID:   "s1",
				Agent:       "codex",
				Status:      tc.status,
				ChildStatus: tc.childStatus,
				Title:       "selected",
			}, 36, true, false, sessionview.SessionDetail{}, false)

			if got, want := strings.Count(out, statusRail(tc.wantStatus)), 2; got != want {
				t.Fatalf("selected card rail count = %d, want %d; output:\n%q", got, want, out)
			}
		})
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

func TestRenderCardsUsesCompactRows(t *testing.T) {
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
	if strings.ContainsAny(out, "╭╮╰╯│") {
		t.Fatalf("renderCards() should not render card borders; output:\n%s", out)
	}
	if !strings.Contains(out, "\n\n") {
		t.Fatalf("renderCards() should add a blank line between cards; output:\n%s", out)
	}
	if lipgloss.Height(out) != 7 {
		t.Fatalf("renderCards() height = %d, want 7; output:\n%s", lipgloss.Height(out), out)
	}
}

func TestRenderCardsShowsActiveAndIdleSections(t *testing.T) {
	m := uiModel{
		width:      90,
		height:     24,
		selectedID: "active",
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active", Age: "1h"},
			{SessionID: "waiting", Agent: "codex", Status: "waiting", Title: "waiting"},
			{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
		},
	}

	out := m.renderCards(36, "active")
	if !strings.Contains(out, "Active Sessions") {
		t.Fatalf("renderCards() missing active section heading; output:\n%s", out)
	}
	if !strings.Contains(out, "Idle Sessions") {
		t.Fatalf("renderCards() missing idle section heading; output:\n%s", out)
	}
	if strings.Index(out, "Active Sessions") > strings.Index(out, "active") {
		t.Fatalf("active heading should appear before active cards; output:\n%s", out)
	}
	if strings.Index(out, "Idle Sessions") > strings.Index(out, "idle") {
		t.Fatalf("idle heading should appear before idle cards; output:\n%s", out)
	}
	if strings.Index(out, "idle") < strings.Index(out, "Idle Sessions") {
		t.Fatalf("idle card should appear under idle section; output:\n%s", out)
	}
}

func TestRenderCardsOmitsEmptySectionHeadings(t *testing.T) {
	m := uiModel{
		width:      90,
		height:     24,
		selectedID: "active",
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
		},
	}

	out := m.renderCards(36, "active")
	if !strings.Contains(out, "Active Sessions") {
		t.Fatalf("renderCards() missing active section heading; output:\n%s", out)
	}
	if strings.Contains(out, "Idle Sessions") {
		t.Fatalf("renderCards() should omit empty idle section; output:\n%s", out)
	}

	m.cards = []sessionview.SessionCard{
		{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
	}
	m.selectedID = "idle"
	out = m.renderCards(36, "idle")
	if strings.Contains(out, "Active Sessions") {
		t.Fatalf("renderCards() should omit empty active section; output:\n%s", out)
	}
	if !strings.Contains(out, "Idle Sessions") {
		t.Fatalf("renderCards() missing idle section heading; output:\n%s", out)
	}
}

func TestRenderCardsScrollBudgetIncludesSectionHeadings(t *testing.T) {
	m := uiModel{
		width:      90,
		height:     12,
		selectedID: "active",
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
			{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
		},
	}

	out := m.renderCards(36, "active")
	if lipgloss.Height(out) > m.cardPaneHeight() {
		t.Fatalf("renderCards() height = %d, want <= %d; output:\n%s", lipgloss.Height(out), m.cardPaneHeight(), out)
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
	if !strings.HasPrefix(out, statusRail("active")+" codex") {
		t.Fatalf("renderCard() should start leaf agent after status rail and left padding; output:\n%s", out)
	}
	if strings.Contains(out, "   codex") {
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

func TestRenderCardShowsSessionAgeWithoutTimestamps(t *testing.T) {
	card := sessionview.SessionCard{
		SessionID:    "s1",
		Agent:        "codex",
		Status:       "active",
		Title:        "project",
		Age:          "2h",
		FirstSeenAt:  "2026-05-15T11:21:19Z",
		StatusAt:     "2026-05-15T11:23:00Z",
		ActivityTime: "2m ago",
	}

	out := renderCard(card, 42, false, false, sessionview.SessionDetail{}, false)
	for _, want := range []string{"active", "2h"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderCard() missing %q; output:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"created", "updated", "2026-05-15"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("renderCard() should keep full timestamps out of cards; output:\n%s", out)
		}
	}
	if strings.Index(out, "active") > strings.Index(out, "2h") {
		t.Fatalf("renderCard() should place age under the status; output:\n%s", out)
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

func TestRenderCardsKeepsExpandedChildrenWithParentSection(t *testing.T) {
	m := uiModel{
		width:           90,
		height:          24,
		expandedParents: map[string]bool{"parent": true},
		cards: []sessionview.SessionCard{
			{SessionID: "parent", Agent: "codex", Status: "active", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "idle", Title: "child"},
			{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
		},
		selectedID: "parent",
	}

	out := m.renderCards(36, "parent")
	parentIndex := strings.Index(out, "parent")
	childIndex := strings.LastIndex(out, "child")
	idleHeadingIndex := strings.Index(out, "Idle Sessions")
	if parentIndex < 0 || childIndex < 0 || idleHeadingIndex < 0 {
		t.Fatalf("renderCards() missing expected content; output:\n%s", out)
	}
	if !(parentIndex < childIndex && childIndex < idleHeadingIndex) {
		t.Fatalf("expanded child should stay with active parent before idle section; output:\n%s", out)
	}
}

func TestRenderCardsKeepsExpandedChildrenWithIdleParentSection(t *testing.T) {
	m := uiModel{
		width:           90,
		height:          24,
		expandedParents: map[string]bool{"parent": true},
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
			{SessionID: "parent", Agent: "codex", Status: "idle", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "active", Title: "child"},
		},
		selectedID: "active",
	}

	out := m.renderCards(36, "active")
	idleHeadingIndex := strings.Index(out, "Idle Sessions")
	parentIndex := strings.Index(out, "parent")
	childIndex := strings.LastIndex(out, "child")
	if idleHeadingIndex < 0 || parentIndex < 0 || childIndex < 0 {
		t.Fatalf("renderCards() missing expected content; output:\n%s", out)
	}
	if !(idleHeadingIndex < parentIndex && parentIndex < childIndex) {
		t.Fatalf("expanded child should stay with idle parent under idle section; output:\n%s", out)
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

func TestSectionDividerUsesDividerStyle(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(oldProfile)

	out := sectionDivider(12)
	want := dividerStyle.Render(strings.Repeat("─", 12))
	if out != want {
		t.Fatalf("sectionDivider() = %q, want %q", out, want)
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

func TestViewPanelRailUsesAccentStyleAndFullPaneHeight(t *testing.T) {
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
	activePipe := accentStyle.Render("│")
	if got, want := strings.Count(out, activePipe), m.cardPaneHeight()-2; got != want {
		t.Fatalf("active rail height = %d, want %d; output:\n%q", got, want, out)
	}
	if got := strings.Count(out, accentStyle.Render("╮")); got != 1 {
		t.Fatalf("active rail top corner count = %d, want 1; output:\n%q", got, out)
	}
	if got := strings.Count(out, accentStyle.Render("╯")); got != 1 {
		t.Fatalf("active rail bottom corner count = %d, want 1; output:\n%q", got, out)
	}
	if strings.Contains(out, dividerStyle.Render("│")) {
		t.Fatalf("active rail should replace the dim divider; output:\n%q", out)
	}
	if strings.Contains(out, " │ ") {
		t.Fatalf("active rail should be styled, not plain; output:\n%q", out)
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
