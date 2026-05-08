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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/state"
)

func Handler(s *state.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", makeHookHandler(s))
	mux.HandleFunc("GET /state", makeStateListHandler(s))
	mux.HandleFunc("GET /state/{session_id}", makeStateOneHandler(s))
	return traceMiddleware(mux)
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
			slog.WarnContext(ctx, "hook: unmarshal failed (continuing with zero envelope)",
				"err", err, "bytes", len(body))
		}

		agent := strings.TrimSpace(r.Header.Get("X-Agent"))
		if agent == "" {
			agent = state.AgentUnidentified
		}
		receivedAt := time.Now().UTC().Format(time.RFC3339Nano)

		// Anchor this hook to the session's persisted trace so successive
		// hooks for the same session land in one trace tree. EnsureTrace
		// allocates IDs lazily on first sight (minting a real exported
		// session.start root via OTel) and is a no-op once stamped.
		traceHex, spanHex, traceErr := s.EnsureTrace(ctx, env.SessionID, agent, func() (string, string) {
			return logging.NewSessionRoot(ctx, env.SessionID, agent)
		})
		if traceErr != nil {
			slog.WarnContext(ctx, "hook: ensure trace failed",
				"session", state.ShortID(env.SessionID), "err", traceErr)
		}
		ctx = logging.ContextWithSessionTrace(ctx, traceHex, spanHex)
		ctx, span := logging.Start(ctx, "server.hook",
			attribute.String("agent", agent),
			attribute.String("session.id", env.SessionID),
			attribute.String("hook.event", env.HookEventName),
			attribute.String("turn.id", env.TurnID),
			attribute.String("tool.name", env.ToolName),
			attribute.String("tool.use_id", env.ToolUseID),
			attribute.String("model", env.Model),
			attribute.String("permission_mode", env.PermissionMode),
			attribute.String("hook.agent_id", env.AgentID),
			attribute.String("hook.agent_type", env.AgentType),
		)
		defer span.End()

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
			span.RecordError(err)
			span.SetStatus(codes.Error, "record event")
			http.Error(w, "record failed", http.StatusInternalServerError)
			return
		}
		span.SetAttributes(attribute.Bool("applied", applied))
		if applied {
			recordedAttrs := []slog.Attr{
				slog.String("agent", agent),
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

