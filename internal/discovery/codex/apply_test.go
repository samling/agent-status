package codex

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

// Apply on first sight inserts a row stamped with the requested event.
func TestApplyInsertSessionStart(t *testing.T) {
	store := mustOpenStore(t)
	created := mustParseTime(t, "2026-05-06T22:00:00Z")

	if !Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-fresh",
		StartedAt: created,
		Event:     state.EventSessionStart,
	}) {
		t.Fatal("Apply returned false on insert")
	}
	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].LastEvent != state.EventSessionStart {
		t.Fatalf("LastEvent = %q, want SessionStart", sessions[0].LastEvent)
	}
	if got := state.DeriveStatus(sessions[0]); got != "idle" {
		t.Fatalf("DeriveStatus = %q, want idle", got)
	}
}

// Apply defaults the inserted event to "Discovered" when sess.Event is empty.
func TestApplyInsertEventDefault(t *testing.T) {
	store := mustOpenStore(t)

	if !Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-old",
		StartedAt: mustParseTime(t, "2026-05-06T20:00:00Z"),
	}) {
		t.Fatal("Apply returned false on insert")
	}
	sessions := store.Sessions()
	if sessions[0].LastEvent != state.EventDiscovered {
		t.Fatalf("LastEvent = %q, want Discovered (default)", sessions[0].LastEvent)
	}
}

// Apply on an already-present session refines FirstSeenAt to the earlier
// discovered timestamp without clobbering hook-driven LastEvent / status.
func TestApplyRefineDoesNotClobberHookStatus(t *testing.T) {
	store := mustOpenStore(t)
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentCodex,
		SessionID:  "session-1",
		Event:      state.EventStop,
		TurnID:     "turn-1",
		ReceivedAt: "2026-05-06T22:00:10Z",
	}); err != nil {
		t.Fatal(err)
	}

	if !Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-1",
		StartedAt: mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:     state.EventDiscovered,
	}) {
		t.Fatal("Apply returned false on refine")
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].LastEvent != state.EventStop {
		t.Fatalf("LastEvent = %q, want Stop (not clobbered)", sessions[0].LastEvent)
	}
	if got := state.DeriveStatus(sessions[0]); got != "idle" {
		t.Fatalf("DeriveStatus = %q, want idle", got)
	}
	if sessions[0].FirstSeenAt != "2026-05-06T22:00:00Z" {
		t.Fatalf("FirstSeenAt = %q, want backdated discovery timestamp", sessions[0].FirstSeenAt)
	}
}

// Apply must not overwrite an in-flight hook event when re-polled with the
// same SessionStart.
func TestApplyRefineNoClobberAfterHookProgress(t *testing.T) {
	store := mustOpenStore(t)
	created := mustParseTime(t, "2026-05-06T22:00:00Z")

	Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-fresh",
		StartedAt: created,
		Event:     state.EventSessionStart,
	})
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentCodex,
		SessionID:  "session-fresh",
		Event:      state.EventUserPromptSubmit,
		TurnID:     "turn-1",
		ReceivedAt: "2026-05-06T22:00:05Z",
	}); err != nil {
		t.Fatal(err)
	}

	Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-fresh",
		StartedAt: created,
		Event:     state.EventSessionStart,
	})

	sessions := store.Sessions()
	if sessions[0].LastEvent != state.EventUserPromptSubmit {
		t.Fatalf("LastEvent = %q after re-poll, want UserPromptSubmit (no clobber)", sessions[0].LastEvent)
	}
}

func TestApplyTurnAbortTransitionsActiveSessionIdle(t *testing.T) {
	store := mustOpenStore(t)
	created := mustParseTime(t, "2026-05-06T22:00:00Z")
	promptAt := "2026-05-06T22:00:05Z"
	abortAt := mustParseTime(t, "2026-05-06T22:00:10Z")

	Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-1",
		StartedAt: created,
		Event:     state.EventSessionStart,
	})
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentCodex,
		SessionID:  "session-1",
		Event:      state.EventUserPromptSubmit,
		TurnID:     "turn-1",
		ReceivedAt: promptAt,
	}); err != nil {
		t.Fatal(err)
	}

	if !Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-1",
		StartedAt: created,
		Event:     state.EventStop,
		EventAt:   abortAt,
		TurnID:    "turn-1",
	}) {
		t.Fatal("Apply returned false for turn abort")
	}

	sessions := store.Sessions()
	if sessions[0].LastEvent != state.EventStop {
		t.Fatalf("LastEvent = %q, want Stop", sessions[0].LastEvent)
	}
	if sessions[0].TurnID != "turn-1" {
		t.Fatalf("TurnID = %q, want turn-1", sessions[0].TurnID)
	}
	if sessions[0].StatusAt != abortAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("StatusAt = %q, want abort timestamp", sessions[0].StatusAt)
	}
	if got := state.DeriveStatus(sessions[0]); got != "idle" {
		t.Fatalf("DeriveStatus = %q, want idle", got)
	}
}

func TestApplyIgnoresOlderTurnAbortAfterNewPrompt(t *testing.T) {
	store := mustOpenStore(t)
	created := mustParseTime(t, "2026-05-06T22:00:00Z")

	Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-1",
		StartedAt: created,
		Event:     state.EventSessionStart,
	})
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentCodex,
		SessionID:  "session-1",
		Event:      state.EventUserPromptSubmit,
		TurnID:     "turn-2",
		ReceivedAt: "2026-05-06T22:00:20Z",
	}); err != nil {
		t.Fatal(err)
	}

	Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-1",
		StartedAt: created,
		Event:     state.EventStop,
		EventAt:   mustParseTime(t, "2026-05-06T22:00:10Z"),
		TurnID:    "turn-1",
	})

	sessions := store.Sessions()
	if sessions[0].LastEvent != state.EventUserPromptSubmit {
		t.Fatalf("LastEvent = %q, want UserPromptSubmit", sessions[0].LastEvent)
	}
	if got := state.DeriveStatus(sessions[0]); got != "active" {
		t.Fatalf("DeriveStatus = %q, want active", got)
	}
}

// Hook with AgentUnidentified must not downgrade a concrete agent label that
// was already stamped (whether by an earlier hook with a real header, or by
// discovery winning the first-sight race).
func TestHookUnidentifiedDoesNotDowngrade(t *testing.T) {
	store := mustOpenStore(t)
	// Discovery inserts first with a concrete label.
	Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-1",
		StartedAt: mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:     state.EventSessionStart,
	})

	// Hook arrives later with no X-Agent header (server fills in unidentified).
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentUnidentified,
		SessionID:  "session-1",
		Event:      state.EventUserPromptSubmit,
		TurnID:     "turn-1",
		ReceivedAt: "2026-05-06T22:00:05Z",
	}); err != nil {
		t.Fatal(err)
	}

	sessions := store.Sessions()
	if got := sessions[0].Agent; got != state.AgentCodex {
		t.Fatalf("Agent = %q, want %q (specific label must not be downgraded)", got, state.AgentCodex)
	}
}

// Apply on an existing row must promote a placeholder Agent stamped by a
// header-less hook to the concrete label discovery knows from the filesystem.
func TestApplyPromotesAgentFromUnidentified(t *testing.T) {
	store := mustOpenStore(t)
	// Hook fired first with no X-Agent header.
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentUnidentified,
		SessionID:  "session-1",
		Event:      state.EventUserPromptSubmit,
		TurnID:     "turn-1",
		ReceivedAt: "2026-05-06T22:00:05Z",
	}); err != nil {
		t.Fatal(err)
	}

	Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: "session-1",
		StartedAt: mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:     state.EventDiscovered,
	})

	sessions := store.Sessions()
	if got := sessions[0].Agent; got != state.AgentCodex {
		t.Fatalf("Agent = %q, want %q (discovery should promote unidentified)", got, state.AgentCodex)
	}
}

func mustOpenStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
