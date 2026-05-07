package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samling/agent-status/internal/state"
)

func TestHookUsesAgentHeader(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{"session_id":"session-1","hook_event_name":"SessionStart"}`
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	req.Header.Set("X-Agent", "codex")
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

func TestHookMissingHeaderIsUnidentified(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{"session_id":"session-1","hook_event_name":"SessionStart"}`
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
	if sessions[0].Agent != state.AgentUnidentified {
		t.Fatalf("Agent = %q, want %q", sessions[0].Agent, state.AgentUnidentified)
	}
}

func TestHookAcceptsCodexDocumentedFields(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{
		"session_id": "session-1",
		"transcript_path": null,
		"cwd": "/tmp/project",
		"hook_event_name": "UserPromptSubmit",
		"model": "gpt-5.5",
		"permission_mode": "default",
		"turn_id": "turn-1",
		"prompt": "hello"
	}`
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	req.Header.Set("X-Agent", "codex")
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

func TestHookAcceptsClaudeAgentFields(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{
		"session_id": "session-1",
		"hook_event_name": "PreToolUse",
		"transcript_path": "/home/test/.claude/projects/project/session-1.jsonl",
		"cwd": "/tmp/project",
		"permission_mode": "default",
		"agent_id": "agent-1",
		"agent_type": "Explore",
		"tool_name": "Bash",
		"tool_input": {"command":"npm test"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	req.Header.Set("X-Agent", "claude-code")
	rr := httptest.NewRecorder()

	Handler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if sessions[0].Agent != state.AgentClaudeCode {
		t.Fatalf("Agent = %q, want %q", sessions[0].Agent, state.AgentClaudeCode)
	}
	if sessions[0].LastEvent != "PreToolUse" {
		t.Fatalf("LastEvent = %q, want PreToolUse", sessions[0].LastEvent)
	}
}
