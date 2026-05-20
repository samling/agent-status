package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/sessionview"
	"github.com/samling/agent-status/internal/state"
)

func TestLoadSnapshotSelectsFirstCardWhenSelectionIsStale(t *testing.T) {
	var detailPath string
	var unexpectedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{
				{SessionID: "fresh", Status: "active", FirstSeenAt: "2026-05-14T10:00:00Z"},
			})
		case "/views/sessions/fresh":
			detailPath = r.URL.Path
			writeJSON(w, sessionview.SessionDetail{SessionID: "fresh"})
		default:
			unexpectedPath = r.URL.Path
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := loadSnapshot(serverAddr(server), "stale", sortStatus, nil)().(snapshotMsg)
	if unexpectedPath != "" {
		t.Fatalf("unexpected request path %q", unexpectedPath)
	}
	if msg.detailFor != "fresh" {
		t.Fatalf("detailFor = %q, want fresh", msg.detailFor)
	}
	if msg.detail.SessionID != "fresh" {
		t.Fatalf("detail.SessionID = %q, want fresh", msg.detail.SessionID)
	}
	if detailPath != "/views/sessions/fresh" {
		t.Fatalf("detail path = %q, want /views/sessions/fresh", detailPath)
	}
}

func TestLoadSnapshotDoesNotFetchStaleDetailWhenCardsFail(t *testing.T) {
	var detailPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			detailPath = r.URL.Path
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := loadSnapshot(serverAddr(server), "stale", sortStatus, nil)().(snapshotMsg)
	if msg.serverUp {
		t.Fatal("serverUp = true, want false")
	}
	if msg.detailFor != "" {
		t.Fatalf("detailFor = %q, want empty", msg.detailFor)
	}
	if msg.detail.SessionID != "" {
		t.Fatalf("detail.SessionID = %q, want empty", msg.detail.SessionID)
	}
	if detailPath != "" {
		t.Fatalf("detail endpoint was requested at %q", detailPath)
	}
}

func TestLoadSnapshotCarriesDetailError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{
				{SessionID: "s1", Status: "active", FirstSeenAt: "2026-05-14T10:00:00Z"},
			})
		case "/views/sessions/s1":
			http.Error(w, "detail exploded", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := loadSnapshot(serverAddr(server), "s1", sortStatus, nil)().(snapshotMsg)
	if msg.detailFor != "s1" {
		t.Fatalf("detailFor = %q, want s1", msg.detailFor)
	}
	if msg.detailErr == nil {
		t.Fatal("detailErr = nil, want error")
	}
	if !strings.Contains(msg.detailErr.Error(), "detail exploded") {
		t.Fatalf("detailErr = %v, want detail exploded", msg.detailErr)
	}
}

func TestLoadSnapshotPrefersActiveCardWhenOpening(t *testing.T) {
	var detailPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{
				{SessionID: "waiting", Status: "waiting", FirstSeenAt: "2026-05-14T10:00:00Z"},
				{SessionID: "active", Status: "active", FirstSeenAt: "2026-05-14T10:01:00Z"},
				{SessionID: "idle", Status: "idle", FirstSeenAt: "2026-05-14T10:02:00Z"},
			})
		case "/views/sessions/active":
			detailPath = r.URL.Path
			writeJSON(w, sessionview.SessionDetail{SessionID: "active"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := loadSnapshot(serverAddr(server), "", sortStatus, nil)().(snapshotMsg)
	if msg.detailFor != "active" {
		t.Fatalf("detailFor = %q, want active", msg.detailFor)
	}
	if msg.detail.SessionID != "active" {
		t.Fatalf("detail.SessionID = %q, want active", msg.detail.SessionID)
	}
	if detailPath != "/views/sessions/active" {
		t.Fatalf("detail path = %q, want /views/sessions/active", detailPath)
	}
}

func TestLoadSnapshotPreservesPreviousStatusOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{
				{SessionID: "third", Status: "idle", FirstSeenAt: "2026-05-14T10:00:00Z"},
				{SessionID: "first", Status: "idle", FirstSeenAt: "2026-05-14T10:02:00Z"},
				{SessionID: "second", Status: "idle", FirstSeenAt: "2026-05-14T10:01:00Z"},
			})
		case "/views/sessions/first":
			writeJSON(w, sessionview.SessionDetail{SessionID: "first"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	previousOrder := map[string]int{"first": 0, "second": 1, "third": 2}
	msg := loadSnapshot(serverAddr(server), "first", sortStatus, previousOrder)().(snapshotMsg)

	for i, want := range []string{"first", "second", "third"} {
		if msg.cards[i].SessionID != want {
			t.Fatalf("cards[%d] = %q, want %q; cards=%#v", i, msg.cards[i].SessionID, want, msg.cards)
		}
	}
}

func TestUpdateDownReturnsImmediateDetailLoad(t *testing.T) {
	var detailPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions/s2":
			detailPath = r.URL.Path
			writeJSON(w, sessionview.SessionDetail{SessionID: "s2", Title: "second"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr: serverAddr(server),
		selectedID: "s1",
		cards: []sessionview.SessionCard{
			{SessionID: "s1"},
			{SessionID: "s2"},
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	gotModel := updated.(uiModel)
	if gotModel.selectedID != "s2" {
		t.Fatalf("selectedID = %q, want s2", gotModel.selectedID)
	}
	if cmd == nil {
		t.Fatal("Update returned nil command, want immediate detail load")
	}
	msg := cmd()
	got, ok := msg.(detailMsg)
	if !ok {
		t.Fatalf("command returned %T, want detailMsg", msg)
	}
	if detailPath != "/views/sessions/s2" {
		t.Fatalf("detail path = %q, want /views/sessions/s2", detailPath)
	}
	if got.detailFor != "s2" {
		t.Fatalf("detailFor = %q, want s2", got.detailFor)
	}
	if got.detail.SessionID != "s2" {
		t.Fatalf("detail.SessionID = %q, want s2", got.detail.SessionID)
	}
}

func TestUpdateTabLoadsMessagesForSelectedSession(t *testing.T) {
	var messagesPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions/s1/messages":
			messagesPath = r.URL.Path
			writeJSON(w, sessionview.MessageList{
				SessionID: "s1",
				Messages: []sessionview.MessageSummary{{
					ID:      "7",
					Role:    "user",
					Preview: "newest",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr: serverAddr(server),
		selectedID: "s1",
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	gotModel := updated.(uiModel)
	if gotModel.focusMode != focusMessages {
		t.Fatalf("focusMode = %v, want focusMessages", gotModel.focusMode)
	}
	if cmd == nil {
		t.Fatal("Update returned nil command, want message load")
	}
	msg := cmd()
	got, ok := msg.(messageListMsg)
	if !ok {
		t.Fatalf("command returned %T, want messageListMsg", msg)
	}
	if messagesPath != "/views/sessions/s1/messages" {
		t.Fatalf("messages path = %q, want /views/sessions/s1/messages", messagesPath)
	}
	if got.messages.SessionID != "s1" || len(got.messages.Messages) != 1 {
		t.Fatalf("messages = %#v", got.messages)
	}
}

func TestUpdateEnterFocusesSelectedSession(t *testing.T) {
	var statePath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/state/s1":
			statePath = r.URL.Path
			writeJSON(w, state.Session{SessionID: "s1", PID: 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr: serverAddr(server),
		selectedID: "s1",
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(uiModel)
	if cmd != nil {
		t.Fatal("Update returned a command, want synchronous focus attempt")
	}
	if got.focusMode != focusCards {
		t.Fatalf("focusMode = %v, want focusCards", got.focusMode)
	}
	if statePath != "/state/s1" {
		t.Fatalf("state path = %q, want /state/s1", statePath)
	}
	if !strings.Contains(got.status, "focus error:") {
		t.Fatalf("status = %q, want focus error", got.status)
	}
}

func TestUpdateEnterInMessageListLoadsSelectedMessage(t *testing.T) {
	var detailPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions/s1/messages/new":
			detailPath = r.URL.Path
			writeJSON(w, sessionview.MessageDetail{ID: "new", Role: "assistant", Text: "full message"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr:     serverAddr(server),
		selectedID:     "s1",
		focusMode:      focusMessages,
		messageListFor: "s1",
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages: []sessionview.MessageSummary{
				{ID: "new", Role: "assistant", Preview: "newer"},
				{ID: "old", Role: "user", Preview: "older"},
			},
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	gotModel := updated.(uiModel)
	if gotModel.focusMode != focusMessageBody {
		t.Fatalf("focusMode = %v, want focusMessageBody", gotModel.focusMode)
	}
	if cmd == nil {
		t.Fatal("Update returned nil command, want message detail load")
	}
	msg := cmd()
	got, ok := msg.(messageDetailMsg)
	if !ok {
		t.Fatalf("command returned %T, want messageDetailMsg", msg)
	}
	if detailPath != "/views/sessions/s1/messages/new" {
		t.Fatalf("detail path = %q, want /views/sessions/s1/messages/new", detailPath)
	}
	if got.detail.ID != "new" || got.detail.Text != "full message" {
		t.Fatalf("detail = %#v", got.detail)
	}
}

func TestUpdateEscapeReturnsFromMessageBodyToList(t *testing.T) {
	m := uiModel{
		selectedID: "s1",
		focusMode:  focusMessageBody,
		messageDetail: sessionview.MessageDetail{
			ID:   "7",
			Text: "full message",
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(uiModel)
	if got.focusMode != focusMessages {
		t.Fatalf("focusMode = %v, want focusMessages", got.focusMode)
	}
	if got.messageScroll != 0 {
		t.Fatalf("messageScroll = %d, want 0", got.messageScroll)
	}
}

func TestUpdateShiftTabReturnsFromMessagePaneToSessionList(t *testing.T) {
	for _, mode := range []focusMode{focusMessages, focusMessageBody} {
		m := uiModel{
			selectedID:     "s1",
			focusMode:      mode,
			messageScroll:  3,
			messageRaw:     true,
			messageListFor: "s1",
		}

		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		got := updated.(uiModel)
		if cmd != nil {
			t.Fatalf("mode %v: Update returned command, want nil", mode)
		}
		if got.focusMode != focusCards {
			t.Fatalf("mode %v: focusMode = %v, want focusCards", mode, got.focusMode)
		}
		if got.messageScroll != 0 {
			t.Fatalf("mode %v: messageScroll = %d, want 0", mode, got.messageScroll)
		}
		if got.messageRaw {
			t.Fatalf("mode %v: messageRaw = true, want false", mode)
		}
	}
}

func TestUpdateTabSwapsFromMessagePaneToSessionList(t *testing.T) {
	for _, mode := range []focusMode{focusMessages, focusMessageBody} {
		m := uiModel{
			selectedID:     "s1",
			focusMode:      mode,
			messageScroll:  3,
			messageRaw:     true,
			messageListFor: "s1",
		}

		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		got := updated.(uiModel)
		if cmd != nil {
			t.Fatalf("mode %v: Update returned command, want nil", mode)
		}
		if got.focusMode != focusCards {
			t.Fatalf("mode %v: focusMode = %v, want focusCards", mode, got.focusMode)
		}
		if got.messageScroll != 0 {
			t.Fatalf("mode %v: messageScroll = %d, want 0", mode, got.messageScroll)
		}
		if got.messageRaw {
			t.Fatalf("mode %v: messageRaw = true, want false", mode)
		}
	}
}

func TestUpdateShiftTabSwapsFromSessionListToMessagePane(t *testing.T) {
	var messagesPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions/s1/messages":
			messagesPath = r.URL.Path
			writeJSON(w, sessionview.MessageList{SessionID: "s1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr: serverAddr(server),
		selectedID: "s1",
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	gotModel := updated.(uiModel)
	if gotModel.focusMode != focusMessages {
		t.Fatalf("focusMode = %v, want focusMessages", gotModel.focusMode)
	}
	if cmd == nil {
		t.Fatal("Update returned nil command, want message load")
	}
	if _, ok := cmd().(messageListMsg); !ok {
		t.Fatalf("command returned unexpected message")
	}
	if messagesPath != "/views/sessions/s1/messages" {
		t.Fatalf("messages path = %q, want /views/sessions/s1/messages", messagesPath)
	}
}

func TestMessageSearchFiltersFuzzyMatches(t *testing.T) {
	m := uiModel{
		focusMode: focusMessages,
		messageList: sessionview.MessageList{Messages: []sessionview.MessageSummary{
			{ID: "1", Role: "user", Preview: "build this"},
			{ID: "2", Role: "assistant", Preview: "tests passed"},
		}},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	got := updated.(uiModel)
	if !got.messageSearchMode {
		t.Fatal("messageSearchMode = false, want true")
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t', 'p'}})
	got = updated.(uiModel)
	matches := got.visibleMessages()
	if len(matches) != 1 || matches[0].ID != "2" {
		t.Fatalf("matches = %#v, want only message 2", matches)
	}
}

func TestMessageSearchEscClearsActiveSearch(t *testing.T) {
	m := uiModel{
		focusMode: focusMessages,
		messageList: sessionview.MessageList{Messages: []sessionview.MessageSummary{
			{ID: "1", Role: "user", Preview: "build this"},
			{ID: "2", Role: "assistant", Preview: "tests passed"},
		}},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	got := updated.(uiModel)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t', 'p'}})
	got = updated.(uiModel)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(uiModel)

	if got.messageSearchMode {
		t.Fatal("messageSearchMode = true, want false")
	}
	if got.messageQuery != "" {
		t.Fatalf("messageQuery = %q, want empty", got.messageQuery)
	}
	matches := got.visibleMessages()
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	if got.focusMode != focusMessages {
		t.Fatalf("focusMode = %v, want focusMessages", got.focusMode)
	}
}

func TestMessageSearchEscClearsRetainedQueryBeforeLeavingPane(t *testing.T) {
	m := uiModel{
		focusMode:    focusMessages,
		messageQuery: "tp",
		messageList: sessionview.MessageList{Messages: []sessionview.MessageSummary{
			{ID: "1", Role: "user", Preview: "build this"},
			{ID: "2", Role: "assistant", Preview: "tests passed"},
		}},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(uiModel)
	if got.messageQuery != "" {
		t.Fatalf("messageQuery = %q, want empty", got.messageQuery)
	}
	if got.focusMode != focusMessages {
		t.Fatalf("focusMode = %v, want focusMessages", got.focusMode)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(uiModel)
	if got.focusMode != focusCards {
		t.Fatalf("second esc focusMode = %v, want focusCards", got.focusMode)
	}
}

func TestMessageBodyScrollKeys(t *testing.T) {
	m := uiModel{
		focusMode: focusMessageBody,
		messageDetail: sessionview.MessageDetail{
			ID:   "7",
			Text: strings.Repeat("line\n", 100),
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := updated.(uiModel)
	if got.messageScroll != 1 {
		t.Fatalf("after down messageScroll = %d, want 1", got.messageScroll)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got = updated.(uiModel)
	if got.messageScroll <= 1 {
		t.Fatalf("after ctrl-d messageScroll = %d, want > 1", got.messageScroll)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyUp})
	got = updated.(uiModel)
	if got.messageScroll <= 0 {
		t.Fatalf("after up messageScroll = %d, want positive", got.messageScroll)
	}
}

func TestMessageBodyScrollUsesWrappedLines(t *testing.T) {
	m := uiModel{
		width:     40,
		focusMode: focusMessageBody,
		messageDetail: sessionview.MessageDetail{
			ID:   "7",
			Text: strings.Repeat("x", 50),
		},
	}

	m.scrollMessage(99)
	if m.messageScroll != 2 {
		t.Fatalf("messageScroll = %d, want 2", m.messageScroll)
	}
}

func TestMessageListPageKeysMoveSelection(t *testing.T) {
	messages := make([]sessionview.ConversationMessage, 0, 20)
	for i := 0; i < 20; i++ {
		messages = append(messages, sessionview.ConversationMessage{Role: "user", Text: "message"})
	}
	m := uiModel{
		focusMode: focusMessages,
		detail: sessionview.SessionDetail{
			Conversation: messages,
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := updated.(uiModel)
	if got.messageIndex != halfPage {
		t.Fatalf("after ctrl-d messageIndex = %d, want %d", got.messageIndex, halfPage)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	got = updated.(uiModel)
	if got.messageIndex != 0 {
		t.Fatalf("after ctrl-u messageIndex = %d, want 0", got.messageIndex)
	}
}

func TestTogglesExtraMessagesAndRawMessage(t *testing.T) {
	m := uiModel{focusMode: focusMessages}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	got := updated.(uiModel)
	if !got.showExtraMessages {
		t.Fatal("showExtraMessages = false, want true")
	}

	got.focusMode = focusMessageBody
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	got = updated.(uiModel)
	if !got.messageRaw {
		t.Fatal("messageRaw = false, want true")
	}
}

func TestUpdateAdoptsSnapshotFocusWhenSelectionIsEmpty(t *testing.T) {
	m := uiModel{}

	updated, _ := m.Update(snapshotMsg{
		cards: []sessionview.SessionCard{
			{SessionID: "waiting", Status: "waiting"},
			{SessionID: "active", Status: "active"},
		},
		detailFor: "active",
		detail:    sessionview.SessionDetail{SessionID: "active"},
		serverUp:  true,
	})
	got := updated.(uiModel)

	if got.selectedID != "active" {
		t.Fatalf("selectedID = %q, want active", got.selectedID)
	}
	if got.detailFor != "active" {
		t.Fatalf("detailFor = %q, want active", got.detailFor)
	}
	if got.scrollOffset != 1 {
		t.Fatalf("scrollOffset = %d, want 1", got.scrollOffset)
	}
}

func TestUpdateIgnoresStaleDetailMessage(t *testing.T) {
	m := uiModel{
		selectedID: "s2",
		detailFor:  "s2",
		detail:     sessionview.SessionDetail{SessionID: "s2", Title: "current"},
	}

	updated, _ := m.Update(detailMsg{
		detailFor: "s1",
		detail:    sessionview.SessionDetail{SessionID: "s1", Title: "stale"},
	})
	got := updated.(uiModel)

	if got.detailFor != "s2" {
		t.Fatalf("detailFor = %q, want s2", got.detailFor)
	}
	if got.detail.SessionID != "s2" {
		t.Fatalf("detail.SessionID = %q, want s2", got.detail.SessionID)
	}
	if got.detail.Title != "current" {
		t.Fatalf("detail.Title = %q, want current", got.detail.Title)
	}
}

func TestUpdateTShowsToolsReloadsMessages(t *testing.T) {
	var messagesPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions/s1/messages":
			messagesPath = r.URL.Path
			writeJSON(w, sessionview.MessageList{
				SessionID: "s1",
				Messages: []sessionview.MessageSummary{{
					ID:      "fresh",
					Role:    "tool_result",
					Preview: "fresh tool result",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr:  serverAddr(server),
		selectedID:  "s1",
		focusMode:   focusMessages,
		messageList: sessionview.MessageList{SessionID: "s1"},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	got := updated.(uiModel)
	if !got.showExtraMessages {
		t.Fatal("showExtraMessages = false, want true")
	}
	if cmd == nil {
		t.Fatal("Update returned nil command, want message reload")
	}
	msg := cmd()
	gotMsg, ok := msg.(messageListMsg)
	if !ok {
		t.Fatalf("command returned %T, want messageListMsg", msg)
	}
	if messagesPath != "/views/sessions/s1/messages" {
		t.Fatalf("messages path = %q, want /views/sessions/s1/messages", messagesPath)
	}
	if len(gotMsg.messages.Messages) != 1 || gotMsg.messages.Messages[0].ID != "fresh" {
		t.Fatalf("messages = %#v, want fresh message", gotMsg.messages.Messages)
	}
}

func TestUpdateTShowsToolsFromSessionList(t *testing.T) {
	var messagesPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions/s1/messages":
			messagesPath = r.URL.Path
			writeJSON(w, sessionview.MessageList{
				SessionID: "s1",
				Messages: []sessionview.MessageSummary{{
					ID:      "fresh",
					Role:    "tool_result",
					Preview: "fresh tool result",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr: serverAddr(server),
		selectedID: "s1",
		focusMode:  focusCards,
		cards:      []sessionview.SessionCard{{SessionID: "s1"}},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	got := updated.(uiModel)
	if got.focusMode != focusCards {
		t.Fatalf("focusMode = %v, want focusCards", got.focusMode)
	}
	if !got.showExtraMessages {
		t.Fatal("showExtraMessages = false, want true")
	}
	if cmd == nil {
		t.Fatal("Update returned nil command, want message reload")
	}
	msg := cmd()
	gotMsg, ok := msg.(messageListMsg)
	if !ok {
		t.Fatalf("command returned %T, want messageListMsg", msg)
	}
	if messagesPath != "/views/sessions/s1/messages" {
		t.Fatalf("messages path = %q, want /views/sessions/s1/messages", messagesPath)
	}
	if len(gotMsg.messages.Messages) != 1 || gotMsg.messages.Messages[0].ID != "fresh" {
		t.Fatalf("messages = %#v, want fresh message", gotMsg.messages.Messages)
	}
}

func TestUpdateTickRefreshesToolExpandedMessages(t *testing.T) {
	var messagesPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{{SessionID: "s1", Status: "active"}})
		case "/views/sessions/s1":
			writeJSON(w, sessionview.SessionDetail{SessionID: "s1"})
		case "/views/sessions/s1/messages":
			messagesPath = r.URL.Path
			writeJSON(w, sessionview.MessageList{
				SessionID: "s1",
				Messages: []sessionview.MessageSummary{{
					ID:      "new",
					Role:    "assistant",
					Preview: "newest",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr:        serverAddr(server),
		selectedID:        "s1",
		interval:          time.Nanosecond,
		focusMode:         focusMessages,
		showExtraMessages: true,
		messageListFor:    "s1",
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages: []sessionview.MessageSummary{{
				ID:      "old",
				Role:    "user",
				Preview: "older",
			}},
		},
	}

	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("Update returned nil command, want batch")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("command returned %T, want tea.BatchMsg", cmd())
	}
	var gotMessageList messageListMsg
	for _, subcmd := range batch {
		msg := subcmd()
		if list, ok := msg.(messageListMsg); ok {
			gotMessageList = list
		}
	}
	if messagesPath != "/views/sessions/s1/messages" {
		t.Fatalf("messages path = %q, want /views/sessions/s1/messages", messagesPath)
	}
	if len(gotMessageList.messages.Messages) != 1 || gotMessageList.messages.Messages[0].ID != "new" {
		t.Fatalf("messages = %#v, want refreshed message", gotMessageList.messages.Messages)
	}
}

func TestUpdateTickRefreshesToolExpandedMessagesFromSessionList(t *testing.T) {
	var messagesPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{{SessionID: "s1", Status: "active"}})
		case "/views/sessions/s1":
			writeJSON(w, sessionview.SessionDetail{SessionID: "s1"})
		case "/views/sessions/s1/messages":
			messagesPath = r.URL.Path
			writeJSON(w, sessionview.MessageList{
				SessionID: "s1",
				Messages: []sessionview.MessageSummary{{
					ID:      "new",
					Role:    "assistant",
					Preview: "newest",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr:        serverAddr(server),
		selectedID:        "s1",
		interval:          time.Nanosecond,
		focusMode:         focusCards,
		showExtraMessages: true,
		messageListFor:    "s1",
		messageList: sessionview.MessageList{
			SessionID: "s1",
			Messages: []sessionview.MessageSummary{{
				ID:      "old",
				Role:    "user",
				Preview: "older",
			}},
		},
	}

	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("Update returned nil command, want batch")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("command returned %T, want tea.BatchMsg", cmd())
	}
	var gotMessageList messageListMsg
	for _, subcmd := range batch {
		msg := subcmd()
		if list, ok := msg.(messageListMsg); ok {
			gotMessageList = list
		}
	}
	if messagesPath != "/views/sessions/s1/messages" {
		t.Fatalf("messages path = %q, want /views/sessions/s1/messages", messagesPath)
	}
	if len(gotMessageList.messages.Messages) != 1 || gotMessageList.messages.Messages[0].ID != "new" {
		t.Fatalf("messages = %#v, want refreshed message", gotMessageList.messages.Messages)
	}
}

func TestUpdateIgnoresStaleMessageDetail(t *testing.T) {
	m := uiModel{
		selectedID:       "s1",
		focusMode:        focusMessageBody,
		messageDetailFor: "new",
		messageDetail:    sessionview.MessageDetail{ID: "new", Text: "current"},
	}

	updated, _ := m.Update(messageDetailMsg{
		sessionID: "s1",
		messageID: "old",
		detail:    sessionview.MessageDetail{ID: "old", Text: "stale"},
	})
	got := updated.(uiModel)

	if got.messageDetail.ID != "new" || got.messageDetail.Text != "current" {
		t.Fatalf("messageDetail = %#v, want current new detail", got.messageDetail)
	}
}

func TestUpdateIgnoresStaleSnapshotDetail(t *testing.T) {
	m := uiModel{
		selectedID: "s2",
		detailFor:  "s2",
		detail:     sessionview.SessionDetail{SessionID: "s2", Title: "current"},
	}

	updated, _ := m.Update(snapshotMsg{
		cards: []sessionview.SessionCard{
			{SessionID: "s1"},
			{SessionID: "s2"},
		},
		detailFor: "s1",
		detail:    sessionview.SessionDetail{SessionID: "s1", Title: "stale"},
		serverUp:  true,
	})
	got := updated.(uiModel)

	if got.detailFor != "s2" {
		t.Fatalf("detailFor = %q, want s2", got.detailFor)
	}
	if got.detail.SessionID != "s2" {
		t.Fatalf("detail.SessionID = %q, want s2", got.detail.SessionID)
	}
	if got.detail.Title != "current" {
		t.Fatalf("detail.Title = %q, want current", got.detail.Title)
	}
}

func TestViewShowsEmptySessionListWithoutGenericHeading(t *testing.T) {
	m := uiModel{width: 80, height: 20}

	out := m.View()
	for _, want := range []string{"(no live sessions)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q; output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Sessions") {
		t.Fatalf("View() should not show generic sessions heading; output:\n%s", out)
	}
}

func serverAddr(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
