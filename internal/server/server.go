// Package server hosts the only network-facing surface of agent-status:
// a single POST /hook endpoint that agent processes call to deliver
// hook events. Everything else (session list, live meta, transcripts,
// focus) is read directly from disk by clients on the same host. The
// server's role in the architecture is "single writer of state.json
// plus background watcher daemon"; readers are independent and atomic
// rename guarantees they see consistent snapshots.
package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/state"
)

func Handler(s *state.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", makeHookHandler(s))
	return traceMiddleware(mux)
}

// traceMiddleware extracts any incoming W3C traceparent header so a
// caller's span becomes the parent of the server-side span, then
// emits a log line per request with method+path+status+duration.
func traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := logging.ExtractHTTP(r)
		r = r.WithContext(ctx)
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
		slog.LogAttrs(ctx, level, "http",
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
	Agent               string `json:"agent"`
	SessionID           string `json:"session_id"`
	SessionIDCamel      string `json:"sessionId"`
	HookEventName       string `json:"hook_event_name"`
	HookEventNameCamel  string `json:"hookEventName"`
	TranscriptPath      string `json:"transcript_path"`
	TranscriptPathCamel string `json:"transcriptPath"`
	TurnID              string `json:"turn_id"`
	TurnIDCamel         string `json:"turnId"`
	ToolName            string `json:"tool_name"`
	ToolNameCamel       string `json:"toolName"`
}

func (e envelope) sessionID() string {
	if e.SessionID != "" {
		return e.SessionID
	}
	return e.SessionIDCamel
}

func (e envelope) hookEventName() string {
	if e.HookEventName != "" {
		return e.HookEventName
	}
	return e.HookEventNameCamel
}

func (e envelope) transcriptPath() string {
	if e.TranscriptPath != "" {
		return e.TranscriptPath
	}
	return e.TranscriptPathCamel
}

func (e envelope) turnID() string {
	if e.TurnID != "" {
		return e.TurnID
	}
	return e.TurnIDCamel
}

func (e envelope) toolName() string {
	if e.ToolName != "" {
		return e.ToolName
	}
	return e.ToolNameCamel
}

func makeHookHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := logging.Start(r.Context(), "server.hook")
		defer span.End()

		if r.Method != http.MethodPost {
			slog.WarnContext(ctx, "hook rejected: method not allowed",
				"method", r.Method, "path", r.URL.Path)
			span.SetStatus(codes.Error, "method not allowed")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			slog.ErrorContext(ctx, "hook: read body", "err", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "read body")
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		slog.DebugContext(ctx, "hook body received", "bytes", len(body))

		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			slog.WarnContext(ctx, "hook: unmarshal failed (continuing with zero envelope)",
				"err", err, "bytes", len(body))
		}

		agent := inferAgent(env, r.URL.Query().Get("agent"))
		sessionID := env.sessionID()
		rawEvent := env.hookEventName()
		event := state.NormalizeHookEvent(agent, rawEvent)
		receivedAt := time.Now().UTC().Format(time.RFC3339Nano)

		span.SetAttributes(
			attribute.String("agent", agent),
			attribute.String("session_id", sessionID),
			attribute.String("hook_event", event),
			attribute.String("hook_event_raw", rawEvent),
			attribute.String("turn_id", env.turnID()),
			attribute.String("tool", env.toolName()),
		)

		slog.DebugContext(ctx, "hook envelope parsed",
			"agent", agent,
			"session", state.ShortID(sessionID),
			"event_raw", rawEvent,
			"event_norm", event,
			"turn", env.turnID(),
			"tool", env.toolName(),
			"transcript", env.transcriptPath(),
		)

		recordStart := time.Now()
		applied, err := s.RecordEvent(ctx, agent, sessionID, event, env.turnID(), receivedAt)
		if err != nil {
			slog.ErrorContext(ctx, "hook: record event failed",
				"agent", agent,
				"session", state.ShortID(sessionID),
				"event", event,
				"err", err,
			)
			span.RecordError(err)
			span.SetStatus(codes.Error, "record event")
			http.Error(w, "record failed", http.StatusInternalServerError)
			return
		}
		span.SetAttributes(attribute.Bool("applied", applied))
		if applied {
			slog.InfoContext(ctx, "hook recorded",
				"agent", agent,
				"event", event,
				"session", state.ShortID(sessionID),
				"tool", env.toolName(),
				"record_dur", time.Since(recordStart),
			)
		} else {
			// No-op writes happen when the session was already reaped
			// (SessionEnd arriving after the file watcher dropped the
			// row), when an event is for an unknown session, or when a
			// turn-idle event repeats inside the same turn. Stay at
			// DEBUG so INFO reflects only state-changing hooks.
			slog.DebugContext(ctx, "hook ignored (no state change)",
				"agent", agent,
				"event", event,
				"session", state.ShortID(sessionID),
				"record_dur", time.Since(recordStart),
			)
		}

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
	if isCodexTranscriptPath(env.transcriptPath()) {
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
