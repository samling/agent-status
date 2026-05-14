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

func TestCardsIncludeAgentStatusTitleAndHint(t *testing.T) {
	store := seedStore(t)
	p := Provider{
		Store: store,
		Meta: fakeMeta{meta: map[string]source.SessionMeta{
			"session-1": {Cwd: "/home/test/github/agent-status", WaitingFor: "approve shell"},
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
	if card.Title != "agent-status" {
		t.Fatalf("Title = %q, want agent-status", card.Title)
	}
	if card.Subtitle != "approve shell" {
		t.Fatalf("Subtitle = %q, want approve shell", card.Subtitle)
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
				"session-1": {Cwd: "/home/test/github/agent-status", Model: "gpt-5.5", Version: "0.128.0", PID: 1234},
			},
			transcript: source.TranscriptInfo{
				GitBranch: "feature/ui",
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
	if got := fieldValue(detail.Metadata, "note"); got != "follow up" {
		t.Fatalf("note field = %q, want follow up", got)
	}
	if got := fieldValue(detail.Metadata, "branch"); got != "feature/ui" {
		t.Fatalf("branch field = %q, want feature/ui", got)
	}
	if len(detail.Conversation) != 2 || detail.Conversation[0].Text != "newer" || detail.Conversation[1].Text != "older" {
		t.Fatalf("conversation order = %#v", detail.Conversation)
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
	if got := fieldValue(detail.Metadata, "cwd"); got != "/tmp/project" {
		t.Fatalf("cwd field = %q, want /tmp/project", got)
	}
}

func fieldValue(fields []Field, label string) string {
	for _, f := range fields {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}
