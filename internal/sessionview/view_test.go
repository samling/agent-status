package sessionview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type fakeMeta struct {
	meta       map[string]source.SessionMeta
	transcript source.TranscriptInfo
	err        error
}

func (f fakeMeta) LatestMeta() map[string]source.SessionMeta { return f.meta }
func (f fakeMeta) Transcript(string, string, source.SessionMeta) (source.TranscriptInfo, error) {
	return f.transcript, f.err
}

func seedStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.InsertSession(context.Background(), state.Session{
		SessionID:   "session-1",
		Agent:       state.AgentCodex,
		PID:         1234,
		FirstSeenAt: "2026-05-14T10:00:00Z",
		LastEvent:   state.EventUserPromptSubmit,
		LastEventAt: "2026-05-14T10:01:00Z",
		StatusAt:    "2026-05-14T10:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func insertTestSession(t *testing.T, store *state.Store, id string) {
	t.Helper()
	_, err := store.InsertSession(context.Background(), state.Session{
		SessionID:   id,
		Agent:       state.AgentCodex,
		PID:         1234,
		FirstSeenAt: "2026-05-14T10:00:00Z",
		LastEvent:   state.EventDiscovered,
		LastEventAt: "2026-05-14T10:01:00Z",
		StatusAt:    "2026-05-14T10:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCardsIncludeAgentStatusTitleAndHint(t *testing.T) {
	store := seedStore(t)
	p := Provider{
		Store: store,
		Meta: fakeMeta{meta: map[string]source.SessionMeta{
			"session-1": {Name: "Compare lazyagent to agent-status", Cwd: "/home/test/github/agent-status", WaitingFor: "approve shell"},
		}},
	}

	cards, err := p.Cards(context.Background())
	if err != nil {
		t.Fatalf("Cards() error = %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("len(Cards()) = %d, want 1", len(cards))
	}
	card := cards[0]
	if card.Agent != state.AgentCodex || card.Status != "active" {
		t.Fatalf("card identity = %#v", card)
	}
	if card.Title != "Compare lazyagent to agent-status" {
		t.Fatalf("Title = %q, want Compare lazyagent to agent-status", card.Title)
	}
	if card.Subtitle != "" {
		t.Fatalf("Subtitle = %q, want empty", card.Subtitle)
	}
}

func TestDetailReturnsMetadataNotesAndNewestFirstConversation(t *testing.T) {
	store := seedStore(t)
	notesPath := filepath.Join(t.TempDir(), "notes.json")
	if err := os.WriteFile(notesPath, []byte(`{"session-1":"follow up"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Provider{
		Store:     store,
		NotesPath: notesPath,
		Meta: fakeMeta{
			meta: map[string]source.SessionMeta{
				"session-1": {Name: "Useful session name", Entrypoint: "vscode", Cwd: "/home/test/github/agent-status", Model: "gpt-5.5", Version: "0.128.0", PID: 1234},
			},
			transcript: source.TranscriptInfo{
				GitBranch:           "feature/ui",
				UserMessages:        17,
				AgentMessages:       5,
				InputTokens:         100000,
				OutputTokens:        250,
				CacheCreationTokens: 50,
				CacheReadTokens:     400,
				RecentMessages: []source.ConversationMessage{
					{Role: "user", Text: "older", Timestamp: "2026-05-14T10:00:00Z"},
					{Role: "assistant", Text: "newer", Timestamp: "2026-05-14T10:01:00Z"},
				},
			},
		},
	}

	detail, err := p.Detail(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if got := detail.Metadata.Note; got != "follow up" {
		t.Fatalf("note field = %q, want follow up", got)
	}
	if detail.Title != "Useful session name" {
		t.Fatalf("Title = %q, want Useful session name", detail.Title)
	}
	if got := detail.Metadata.Session; got != "Useful session name" {
		t.Fatalf("session field = %q, want Useful session name", got)
	}
	if got := detail.Metadata.SessionID; got != "session-1" {
		t.Fatalf("session id field = %q, want session-1", got)
	}
	if got := detail.Metadata.Entrypoint; got != "vscode" {
		t.Fatalf("entrypoint field = %q, want vscode", got)
	}
	if got := detail.Metadata.LastEvent; got != state.EventUserPromptSubmit {
		t.Fatalf("last event field = %q, want %s", got, state.EventUserPromptSubmit)
	}
	if got := detail.Metadata.Branch; got != "feature/ui" {
		t.Fatalf("branch field = %q, want feature/ui", got)
	}
	if got := detail.Metadata.UserMessages; got != 17 {
		t.Fatalf("user msgs field = %d, want 17", got)
	}
	if got := detail.Metadata.AgentMessages; got != 5 {
		t.Fatalf("agent msgs field = %d, want 5", got)
	}
	if got := detail.Metadata.InputTokens; got != 100000 {
		t.Fatalf("input tokens field = %d, want 100000", got)
	}
	if got := detail.Metadata.OutputTokens; got != 250 {
		t.Fatalf("output tokens field = %d, want 250", got)
	}
	if got := detail.Metadata.CacheCreationTokens; got != 50 {
		t.Fatalf("cache create field = %d, want 50", got)
	}
	if got := detail.Metadata.CacheReadTokens; got != 400 {
		t.Fatalf("cache read field = %d, want 400", got)
	}
	if len(detail.Conversation) != 2 || detail.Conversation[0].Text != "newer" || detail.Conversation[1].Text != "older" {
		t.Fatalf("conversation order = %#v", detail.Conversation)
	}
}

func TestCardsExposeChildrenAndDropOrphans(t *testing.T) {
	store := seedStore(t)
	insertTestSession(t, store, "child-1")
	insertTestSession(t, store, "orphan-1")
	p := Provider{
		Store: store,
		Meta: fakeMeta{meta: map[string]source.SessionMeta{
			"session-1": {Name: "parent", ChildCount: 2, OpenChildCount: 1},
			"child-1":   {Name: "child", ParentSessionID: "session-1", ChildStatus: "open"},
			"orphan-1":  {Name: "orphan", ParentSessionID: "missing"},
		}},
	}

	cards, err := p.Cards(context.Background())
	if err != nil {
		t.Fatalf("Cards() error = %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("len(Cards()) = %d, want 2: %#v", len(cards), cards)
	}
	parent := cardByID(cards, "session-1")
	if parent.ChildCount != 2 || parent.OpenChildCount != 1 {
		t.Fatalf("parent child counts = %d/%d, want 2/1", parent.ChildCount, parent.OpenChildCount)
	}
	child := cardByID(cards, "child-1")
	if child.ParentSessionID != "session-1" || child.ChildStatus != "open" {
		t.Fatalf("child grouping = %#v", child)
	}
	if got := cardByID(cards, "orphan-1"); got.SessionID != "" {
		t.Fatalf("orphan card should be filtered, got %#v", got)
	}
}

func TestMetadataShowsZeroMessageSide(t *testing.T) {
	meta := detailMetadata(state.Session{
		SessionID: "session-1",
		Agent:     state.AgentCodex,
		LastEvent: state.EventDiscovered,
	}, source.SessionMeta{}, source.TranscriptInfo{
		UserMessages:  3,
		AgentMessages: 0,
	}, "")

	if got := meta.UserMessages; got != 3 {
		t.Fatalf("user msgs field = %d, want 3", got)
	}
	if got := meta.AgentMessages; got != 0 {
		t.Fatalf("agent msgs field = %d, want 0", got)
	}
}

func TestDetailKeepsMetadataWhenTranscriptFails(t *testing.T) {
	store := seedStore(t)
	p := Provider{
		Store: store,
		Meta: fakeMeta{
			meta: map[string]source.SessionMeta{"session-1": {Cwd: "/tmp/project"}},
			err:  errors.New("parse failed"),
		},
	}

	detail, err := p.Detail(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if detail.TranscriptError == "" {
		t.Fatalf("TranscriptError was empty")
	}
	if got := detail.Metadata.Cwd; got != "/tmp/project" {
		t.Fatalf("cwd field = %q, want /tmp/project", got)
	}
}

func TestDetailReturnsErrSessionNotFound(t *testing.T) {
	store := seedStore(t)
	p := Provider{Store: store}

	_, err := p.Detail(context.Background(), "missing")
	if !errors.Is(err, state.ErrSessionNotFound) {
		t.Fatalf("Detail() error = %v, want ErrSessionNotFound", err)
	}
}

func TestCardsDefaultWithoutMetaOrNotes(t *testing.T) {
	store := seedStore(t)
	p := Provider{Store: store}

	cards, err := p.Cards(context.Background())
	if err != nil {
		t.Fatalf("Cards() error = %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("len(Cards()) = %d, want 1", len(cards))
	}
	card := cards[0]
	if card.Title != "-" {
		t.Fatalf("Title = %q, want -", card.Title)
	}
	if card.Subtitle != "" {
		t.Fatalf("Subtitle = %q, want empty", card.Subtitle)
	}
	if card.Note != "" {
		t.Fatalf("Note = %q, want empty", card.Note)
	}
}

func TestDetailDefaultsWhenMetaProviderReturnsNilAndNotesMissing(t *testing.T) {
	store := seedStore(t)
	p := Provider{
		Store:     store,
		NotesPath: filepath.Join(t.TempDir(), "missing-notes.json"),
		Meta:      fakeMeta{},
	}

	detail, err := p.Detail(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if detail.Title != "-" {
		t.Fatalf("Title = %q, want -", detail.Title)
	}
	for label, got := range map[string]string{
		"model":   detail.Metadata.Model,
		"branch":  detail.Metadata.Branch,
		"version": detail.Metadata.Version,
		"cwd":     detail.Metadata.Cwd,
		"waiting": detail.Metadata.Waiting,
		"note":    detail.Metadata.Note,
	} {
		if got != "" {
			t.Fatalf("%s field = %q, want empty", label, got)
		}
	}
	if got := detail.Metadata.PID; got != 1234 {
		t.Fatalf("pid field = %d, want 1234", got)
	}
	if detail.TranscriptError != "" {
		t.Fatalf("TranscriptError = %q, want empty", detail.TranscriptError)
	}
}

func cardByID(cards []SessionCard, id string) SessionCard {
	for _, card := range cards {
		if card.SessionID == id {
			return card
		}
	}
	return SessionCard{}
}
