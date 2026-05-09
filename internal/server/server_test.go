package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type fakeMeta struct {
	meta       map[string]source.SessionMeta
	transcript source.TranscriptInfo
}

func (f fakeMeta) LatestMeta() map[string]source.SessionMeta { return f.meta }
func (f fakeMeta) Transcript(string, string, source.SessionMeta) (source.TranscriptInfo, error) {
	return f.transcript, nil
}

func TestHookUsesAgentHeader(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	body := `{"session_id":"session-1","hook_event_name":"SessionStart"}`
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	req.Header.Set("X-Agent", "codex")
	rr := httptest.NewRecorder()

	Handler(store, nil).ServeHTTP(rr, req)
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

	Handler(store, nil).ServeHTTP(rr, req)
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

	Handler(store, nil).ServeHTTP(rr, req)
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
	if sessions[0].LastEvent != state.EventUserPromptSubmit {
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

	Handler(store, nil).ServeHTTP(rr, req)
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
	if sessions[0].LastEvent != state.EventPreToolUse {
		t.Fatalf("LastEvent = %q, want PreToolUse", sessions[0].LastEvent)
	}
}

func TestHookRejectsInvalidJSON(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"session_id":`))
	req.Header.Set("X-Agent", "codex")
	rr := httptest.NewRecorder()

	Handler(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if sessions := store.Sessions(); len(sessions) != 0 {
		t.Fatalf("len(Sessions()) = %d, want 0", len(sessions))
	}
}

func TestHookRejectsOversizedBody(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(strings.Repeat("x", maxHookBodyBytes+1)))
	req.Header.Set("X-Agent", "codex")
	rr := httptest.NewRecorder()

	Handler(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
	if sessions := store.Sessions(); len(sessions) != 0 {
		t.Fatalf("len(Sessions()) = %d, want 0", len(sessions))
	}
}

func TestStateListReturnsAllSessions(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i, id := range []string{"session-a", "session-b"} {
		if _, err := store.InsertSession(ctx, state.Session{
			SessionID:   id,
			Agent:       state.AgentClaudeCode,
			PID:         1000 + i,
			FirstSeenAt: "2026-05-08T00:00:00Z",
			LastEvent:   state.EventDiscovered,
			LastEventAt: "2026-05-08T00:00:00Z",
			StatusAt:    "2026-05-08T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	rr := httptest.NewRecorder()
	Handler(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got []state.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(got))
	}
}

func TestStateOneReturnsSessionWithPID(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertSession(context.Background(), state.Session{
		SessionID:   "session-1",
		Agent:       state.AgentClaudeCode,
		PID:         4242,
		FirstSeenAt: "2026-05-08T00:00:00Z",
		LastEvent:   state.EventDiscovered,
		LastEventAt: "2026-05-08T00:00:00Z",
		StatusAt:    "2026-05-08T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/state/session-1", nil)
	rr := httptest.NewRecorder()
	Handler(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var got state.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want session-1", got.SessionID)
	}
	if got.PID != 4242 {
		t.Fatalf("PID = %d, want 4242", got.PID)
	}
}

func TestHealthzReturnsOK(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	Handler(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got["status"] != "ok" {
		t.Fatalf("status field = %q, want ok", got["status"])
	}
}

func TestVersionReturnsBuildVersion(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rr := httptest.NewRecorder()
	Handler(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got["version"] == "" {
		t.Fatalf("version field empty; body=%s", rr.Body.String())
	}
}

func TestMetaReturnsCachedSnapshot(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	mp := fakeMeta{
		meta: map[string]source.SessionMeta{
			"session-1": {PID: 1234, Cwd: "/tmp/proj", Version: "2.1.0"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/meta", nil)
	rr := httptest.NewRecorder()
	Handler(store, mp).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var got map[string]source.SessionMeta
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got["session-1"].PID != 1234 || got["session-1"].Cwd != "/tmp/proj" {
		t.Fatalf("meta = %+v, want session-1 with PID 1234 and cwd /tmp/proj", got)
	}
}

func TestStateTranscriptReturnsTranscript(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertSession(context.Background(), state.Session{
		SessionID:   "session-1",
		Agent:       state.AgentClaudeCode,
		FirstSeenAt: "2026-05-08T00:00:00Z",
		LastEvent:   state.EventDiscovered,
		LastEventAt: "2026-05-08T00:00:00Z",
		StatusAt:    "2026-05-08T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	mp := fakeMeta{transcript: source.TranscriptInfo{Model: "test-model", TurnCount: 7}}
	req := httptest.NewRequest(http.MethodGet, "/state/session-1/transcript", nil)
	rr := httptest.NewRecorder()
	Handler(store, mp).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var got source.TranscriptInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got.Model != "test-model" || got.TurnCount != 7 {
		t.Fatalf("transcript = %+v, want model=test-model turns=7", got)
	}
}

func TestStateTranscriptReturns404ForUnknownSession(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/state/missing/transcript", nil)
	rr := httptest.NewRecorder()
	Handler(store, fakeMeta{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestStateOneReturns404ForUnknownSession(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/state/does-not-exist", nil)
	rr := httptest.NewRecorder()
	Handler(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}
