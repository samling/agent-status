package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samling/agent-status/internal/state"
)

func TestHookInfersCodexFromTranscriptPath(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{
		"session_id": "session-1",
		"turn_id": "turn-1",
		"hook_event_name": "Stop",
		"transcript_path": "/home/test/.codex/sessions/2026/05/06/rollout.jsonl"
	}`
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	rr := httptest.NewRecorder()

	Handler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].Agent != state.AgentCodex {
		t.Fatalf("Agent = %q, want %q", sessions[0].Agent, state.AgentCodex)
	}
	if sessions[0].Status != "idle" {
		t.Fatalf("Status = %q, want idle", sessions[0].Status)
	}
}

func TestHookUsesAgentQueryHint(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{"session_id":"session-1","hook_event_name":"SessionStart"}`
	req := httptest.NewRequest(http.MethodPost, "/hook?agent=codex", strings.NewReader(body))
	rr := httptest.NewRecorder()

	Handler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].Agent != state.AgentCodex {
		t.Fatalf("Agent = %q, want %q", sessions[0].Agent, state.AgentCodex)
	}
}

func TestHookAcceptsCamelCaseEnvelope(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{"sessionId":"session-1","hookEventName":"UserPromptSubmit","turnId":"turn-1","transcriptPath":"/home/test/.codex/sessions/rollout.jsonl"}`
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	rr := httptest.NewRecorder()

	Handler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].Agent != state.AgentCodex {
		t.Fatalf("Agent = %q, want %q", sessions[0].Agent, state.AgentCodex)
	}
	if sessions[0].LastEvent != "UserPromptSubmit" {
		t.Fatalf("LastEvent = %q, want UserPromptSubmit", sessions[0].LastEvent)
	}
	if sessions[0].TurnID != "turn-1" {
		t.Fatalf("TurnID = %q, want turn-1", sessions[0].TurnID)
	}
}
