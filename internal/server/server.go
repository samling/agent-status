// Package server hosts the POST /hook collector.
package server

import (
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
	return traceMiddleware(mux)
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

		agent := strings.TrimSpace(r.Header.Get("X-Agent"))
		if agent == "" {
			agent = state.AgentUnidentified
		}
		receivedAt := time.Now().UTC().Format(time.RFC3339Nano)

		span.SetAttributes(
			attribute.String("agent", agent),
			attribute.String("session_id", env.SessionID),
			attribute.String("hook_event", env.HookEventName),
			attribute.String("turn_id", env.TurnID),
			attribute.String("tool", env.ToolName),
			attribute.String("tool_use_id", env.ToolUseID),
			attribute.String("model", env.Model),
			attribute.String("permission_mode", env.PermissionMode),
			attribute.String("agent_id", env.AgentID),
			attribute.String("agent_type", env.AgentType),
		)

		slog.DebugContext(ctx, "hook envelope parsed",
			"agent", agent,
			"session", state.ShortID(env.SessionID),
			"event", env.HookEventName,
			"turn", env.TurnID,
			"tool", env.ToolName,
			"tool_use", env.ToolUseID,
			"model", env.Model,
			"permission_mode", env.PermissionMode,
			"hook_agent_id", state.ShortID(env.AgentID),
			"hook_agent_type", env.AgentType,
			"cwd", env.Cwd,
			"transcript", env.TranscriptPath,
		)

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
			slog.InfoContext(ctx, "hook recorded",
				"agent", agent,
				"event", env.HookEventName,
				"session", state.ShortID(env.SessionID),
				"tool", env.ToolName,
				"record_dur", time.Since(recordStart),
			)
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

