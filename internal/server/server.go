package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/focus"
	"github.com/samling/agent-status/internal/state"
)

func Handler(s *state.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", makeHookHandler(s))
	mux.HandleFunc("/state", makeStateHandler(s))
	// /focus       -> focus the first waiting session (404 if none)
	// /focus/{id}  -> focus the named session (404 if missing/dead)
	// Registered with and without trailing slash so both match.
	focusH := makeFocusHandler(s)
	mux.HandleFunc("/focus", focusH)
	mux.HandleFunc("/focus/", focusH)
	return mux
}

type envelope struct {
	Agent          string `json:"agent"`
	SessionID      string `json:"session_id"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	TurnID         string `json:"turn_id"`
}

func makeHookHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			log.Printf("hook: %s %s rejected (method not allowed)", r.Method, r.URL.Path)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("hook: read body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			log.Printf("hook: unmarshal: %v (%d bytes)", err, len(body))
		}

		agent := inferAgent(env, r.URL.Query().Get("agent"))
		receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.RecordEvent(agent, env.SessionID, env.HookEventName, env.TurnID, receivedAt); err != nil {
			log.Printf("hook: record error session=%s event=%s: %v", state.ShortID(env.SessionID), env.HookEventName, err)
			http.Error(w, "record failed", http.StatusInternalServerError)
			return
		}
		log.Printf("hook: agent=%s event=%s session=%s", agent, env.HookEventName, state.ShortID(env.SessionID))

		w.WriteHeader(http.StatusNoContent)
	}
}

func inferAgent(env envelope, agentHint string) string {
	if agent := strings.TrimSpace(env.Agent); agent != "" {
		return state.NormalizeAgent(agent)
	}
	if agent := strings.TrimSpace(agentHint); agent != "" {
		return state.NormalizeAgent(agent)
	}
	if isCodexTranscriptPath(env.TranscriptPath) {
		return state.AgentCodex
	}
	return state.AgentClaudeCode
}

func isCodexTranscriptPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	path = filepath.ToSlash(path)
	return strings.Contains(path, "/.codex/") || strings.HasPrefix(path, ".codex/")
}

func makeStateHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// No log line: /state is polled every refresh tick by the
		// TUI and statusline, so anything written here drowns the
		// genuinely interesting events (hooks, focus calls).
		sessions := s.Sessions()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	}
}

// FocusResponse is the JSON body returned by /focus on success. Kept
// here so CLI and other clients can decode without redefining the shape.
type FocusResponse struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// makeFocusHandler resolves a session id (explicit or "first waiting")
// to a live PID and invokes focus.PID. The endpoint is the single
// source of truth for "focus a session" so notification activations,
// CLI invocations, and any future clients all behave identically.
func makeFocusHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/focus")
		id = strings.TrimPrefix(id, "/")
		if id == "" {
			for _, sess := range s.Sessions() {
				if sess.Status == "waiting" {
					id = sess.SessionID
					break
				}
			}
			if id == "" {
				http.Error(w, "no waiting session", http.StatusNotFound)
				return
			}
		}
		meta, err := discovery.LiveSessionMeta()
		if err != nil {
			http.Error(w, "lookup session meta: "+err.Error(), http.StatusInternalServerError)
			return
		}
		sm, ok := meta[id]
		if !ok {
			http.Error(w, "session not found: "+id, http.StatusNotFound)
			return
		}
		if sm.PID <= 0 {
			http.Error(w, "session has no live PID", http.StatusUnprocessableEntity)
			return
		}
		msg, err := focus.PID(sm.PID)
		if err != nil {
			log.Printf("focus: session=%s pid=%d error: %v", state.ShortID(id), sm.PID, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("focus: session=%s pid=%d %s", state.ShortID(id), sm.PID, msg)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FocusResponse{SessionID: id, Message: msg})
	}
}
