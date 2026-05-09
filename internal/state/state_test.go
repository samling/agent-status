package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordEventSameTurnStopWins(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.RecordEvent(context.Background(), HookEvent{Agent: AgentCodex, SessionID: "session-1", Event: EventUserPromptSubmit, TurnID: "turn-1", ReceivedAt: "2026-05-06T22:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordEvent(context.Background(), HookEvent{Agent: AgentCodex, SessionID: "session-1", Event: EventStop, TurnID: "turn-1", ReceivedAt: "2026-05-06T22:00:01Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordEvent(context.Background(), HookEvent{Agent: AgentCodex, SessionID: "session-1", Event: EventPostToolUse, TurnID: "turn-1", ReceivedAt: "2026-05-06T22:00:02Z"}); err != nil {
		t.Fatal(err)
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].LastEvent != EventStop {
		t.Fatalf("LastEvent = %q, want Stop", sessions[0].LastEvent)
	}
	if got := DeriveStatus(sessions[0]); got != "idle" {
		t.Fatalf("DeriveStatus = %q, want idle", got)
	}

	if _, err := store.RecordEvent(context.Background(), HookEvent{Agent: AgentCodex, SessionID: "session-1", Event: EventUserPromptSubmit, TurnID: "turn-2", ReceivedAt: "2026-05-06T22:00:03Z"}); err != nil {
		t.Fatal(err)
	}
	sessions = store.Sessions()
	if sessions[0].LastEvent != EventUserPromptSubmit {
		t.Fatalf("LastEvent = %q, want UserPromptSubmit", sessions[0].LastEvent)
	}
	if got := DeriveStatus(sessions[0]); got != "active" {
		t.Fatalf("DeriveStatus = %q, want active", got)
	}
}

func TestOpenQuarantinesCorruptState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"session-1":`), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if sessions := store.Sessions(); len(sessions) != 0 {
		t.Fatalf("len(Sessions()) = %d, want 0", len(sessions))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state file exists after quarantine or unexpected stat err: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "state.json.corrupt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("corrupt file matches = %v, want exactly one", matches)
	}
}
