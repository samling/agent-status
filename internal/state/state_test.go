package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecordEventSameTurnStopWins(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RecordEvent(AgentCodex, "session-1", "UserPromptSubmit", "turn-1", "2026-05-06T22:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordEvent(AgentCodex, "session-1", "Stop", "turn-1", "2026-05-06T22:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordEvent(AgentCodex, "session-1", "PostToolUse", "turn-1", "2026-05-06T22:00:02Z"); err != nil {
		t.Fatal(err)
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].LastEvent != "TurnComplete" {
		t.Fatalf("LastEvent = %q, want TurnComplete", sessions[0].LastEvent)
	}
	if sessions[0].Status != "idle" {
		t.Fatalf("Status = %q, want idle", sessions[0].Status)
	}

	if err := store.RecordEvent(AgentCodex, "session-1", "UserPromptSubmit", "turn-2", "2026-05-06T22:00:03Z"); err != nil {
		t.Fatal(err)
	}
	sessions = store.Sessions()
	if sessions[0].LastEvent != "UserPromptSubmit" {
		t.Fatalf("LastEvent = %q, want UserPromptSubmit", sessions[0].LastEvent)
	}
	if sessions[0].Status != "active" {
		t.Fatalf("Status = %q, want active", sessions[0].Status)
	}
}

func TestReconcileDiscoveredDoesNotClobberHookStatus(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RecordEvent(AgentCodex, "session-1", "Stop", "turn-1", "2026-05-06T22:00:10Z"); err != nil {
		t.Fatal(err)
	}
	changed, err := store.ReconcileDiscovered(AgentCodex, "session-1", mustParseTime(t, "2026-05-06T22:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("ReconcileDiscovered changed = false, want true")
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].LastEvent != "TurnComplete" {
		t.Fatalf("LastEvent = %q, want TurnComplete", sessions[0].LastEvent)
	}
	if sessions[0].Status != "idle" {
		t.Fatalf("Status = %q, want idle", sessions[0].Status)
	}
	if sessions[0].FirstSeenAt != "2026-05-06T22:00:00Z" {
		t.Fatalf("FirstSeenAt = %q, want database timestamp", sessions[0].FirstSeenAt)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
