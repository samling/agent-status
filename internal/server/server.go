// Package server hosts the HTTP collector and state read API.
package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
	"github.com/samling/agent-status/internal/version"
)

// MetaProvider is the daemon-side hook for surfacing discovery-owned data
// over HTTP. The daemon wires in an implementation backed by the live
// discovery cache; tests can pass a stub or a nil-returning default.
type MetaProvider interface {
	// LatestMeta returns a snapshot of the most recent SessionMeta map.
	LatestMeta() map[string]source.SessionMeta
	// Transcript loads the transcript for sessionID belonging to agent,
	// using meta to resolve any agent-specific paths.
	Transcript(sessionID, agent string, meta source.SessionMeta) (source.TranscriptInfo, error)
}

// nopMeta is the zero-value MetaProvider used when callers (notably tests)
// don't supply one. Endpoints that depend on meta still respond, just with
// empty payloads.
type nopMeta struct{}

func (nopMeta) LatestMeta() map[string]source.SessionMeta { return nil }
func (nopMeta) Transcript(string, string, source.SessionMeta) (source.TranscriptInfo, error) {
	return source.TranscriptInfo{}, nil
}

// Handler builds the HTTP mux. mp may be nil; when nil, the meta-backed
// endpoints respond with empty payloads.
func Handler(s *state.Store, mp MetaProvider) http.Handler {
	if mp == nil {
		mp = nopMeta{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", makeHookHandler(s))
	mux.HandleFunc("GET /state", makeStateListHandler(s))
	mux.HandleFunc("GET /state/{session_id}", makeStateOneHandler(s))
	mux.HandleFunc("GET /state/{session_id}/transcript", makeTranscriptHandler(s, mp))
	mux.HandleFunc("GET /meta", makeMetaHandler(mp))
	mux.HandleFunc("GET /healthz", makeHealthHandler())
	mux.HandleFunc("GET /version", makeVersionHandler())
	return logMiddleware(mux)
}

func makeMetaHandler(mp MetaProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meta := mp.LatestMeta()
		if meta == nil {
			meta = map[string]source.SessionMeta{}
		}
		writeJSON(r.Context(), w, http.StatusOK, meta)
	}
}

func makeTranscriptHandler(s *state.Store, mp MetaProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("session_id")
		if id == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		sess, ok := s.GetSession(id)
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		meta := mp.LatestMeta()[id]
		info, err := mp.Transcript(id, sess.Agent, meta)
		if err != nil {
			slog.WarnContext(r.Context(), "transcript: load failed",
				"session", state.ShortID(id), "agent", sess.Agent, "err", err)
			http.Error(w, "transcript load failed", http.StatusInternalServerError)
			return
		}
		writeJSON(r.Context(), w, http.StatusOK, info)
	}
}

func makeHealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(r.Context(), w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func makeVersionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(r.Context(), w, http.StatusOK, map[string]string{"version": version.Get()})
	}
}

func makeStateListHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(r.Context(), w, http.StatusOK, s.Sessions())
	}
}

func makeStateOneHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("session_id")
		if id == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		sess, ok := s.GetSession(id)
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		writeJSON(r.Context(), w, http.StatusOK, sess)
	}
}

func writeJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.WarnContext(ctx, "http: encode response failed", "err", err)
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(rw, r)
		dur := time.Since(start)
		level := slog.LevelDebug
		if rw.status >= 500 {
			level = slog.LevelError
		} else if rw.status >= 400 {
			level = slog.LevelWarn
		}
		slog.LogAttrs(r.Context(), level, "http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Duration("dur", dur),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

type envelope struct {
	SessionID      string `json:"session_id"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Model          string `json:"model"`
	PermissionMode string `json:"permission_mode"`
	AgentID        string `json:"agent_id"`
	AgentType      string `json:"agent_type"`
	TurnID         string `json:"turn_id"`
	ToolName       string `json:"tool_name"`
	ToolUseID      string `json:"tool_use_id"`
}

func makeHookHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Method != http.MethodPost {
			slog.WarnContext(ctx, "hook rejected: method not allowed",
				"method", r.Method, "path", r.URL.Path)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			slog.ErrorContext(ctx, "hook: read body", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		slog.DebugContext(ctx, "hook body received", "bytes", len(body))

		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			slog.WarnContext(ctx, "hook: unmarshal failed",
				"err", err, "bytes", len(body))
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		agent := strings.TrimSpace(r.Header.Get("X-Agent"))
		if agent == "" {
			agent = state.AgentUnidentified
		}
		receivedAt := time.Now().UTC().Format(time.RFC3339Nano)

		parsedAttrs := []slog.Attr{
			slog.String("agent", agent),
			slog.String("session", state.ShortID(env.SessionID)),
			slog.String("event", env.HookEventName),
			slog.String("turn", env.TurnID),
		}
		if env.ToolName != "" {
			parsedAttrs = append(parsedAttrs,
				slog.String("tool", env.ToolName),
				slog.String("tool_use", env.ToolUseID),
			)
		}
		parsedAttrs = append(parsedAttrs,
			slog.String("model", env.Model),
			slog.String("permission_mode", env.PermissionMode),
			slog.String("hook_agent_id", state.ShortID(env.AgentID)),
			slog.String("hook_agent_type", env.AgentType),
			slog.String("cwd", env.Cwd),
			slog.String("transcript", env.TranscriptPath),
		)
		slog.LogAttrs(ctx, slog.LevelDebug, "hook envelope parsed", parsedAttrs...)

		recordStart := time.Now()
		applied, err := s.RecordEvent(ctx, state.HookEvent{
			Agent:      agent,
			SessionID:  env.SessionID,
			Event:      env.HookEventName,
			TurnID:     env.TurnID,
			ReceivedAt: receivedAt,
		})
		if err != nil {
			slog.ErrorContext(ctx, "hook: record event failed",
				"agent", agent,
				"session", state.ShortID(env.SessionID),
				"event", env.HookEventName,
				"err", err,
			)
			http.Error(w, "record failed", http.StatusInternalServerError)
			return
		}
		if applied {
			// Prefer the agent the store ended up with: discovery may have
			// stamped a concrete value before this hook arrived, so the inbound
			// header (often "unidentified") is just a fallback. SessionEnd
			// deletes the row, so the inbound value is all we have left.
			loggedAgent := agent
			if env.HookEventName != state.EventSessionEnd {
				if sess, ok := s.GetSession(env.SessionID); ok && sess.Agent != "" {
					loggedAgent = sess.Agent
				}
			}
			recordedAttrs := []slog.Attr{
				slog.String("agent", loggedAgent),
				slog.String("event", env.HookEventName),
				slog.String("session", state.ShortID(env.SessionID)),
			}
			if env.ToolName != "" {
				recordedAttrs = append(recordedAttrs, slog.String("tool", env.ToolName))
			}
			recordedAttrs = append(recordedAttrs, slog.Duration("record_dur", time.Since(recordStart)))
			slog.LogAttrs(ctx, slog.LevelInfo, "hook recorded", recordedAttrs...)
		} else {
			slog.DebugContext(ctx, "hook ignored (no state change)",
				"agent", agent,
				"event", env.HookEventName,
				"session", state.ShortID(env.SessionID),
				"record_dur", time.Since(recordStart),
			)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
