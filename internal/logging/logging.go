// Package logging is the project's central logging and tracing setup.
//
// It wraps log/slog (level-gated structured logs) with optional
// OpenTelemetry tracing. Every slog record automatically carries the
// trace_id and span_id of the current span when one is in scope, so
// you can correlate a log line back to the request/poll/fire that
// produced it.
//
// Configuration sources (in precedence order):
//
//	1. Environment variables: LOG_LEVEL, LOG_FORMAT, LOG_TRACES.
//	2. Viper keys: log.level, log.format, log.traces.
//	3. Defaults: level=info, format=text, traces=off.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config holds the resolved logging configuration. Use Resolve to
// build one from env + viper.
type Config struct {
	Level   slog.Level // log level threshold
	Format  string     // "text" or "json"
	Traces  string     // "off" or "stdout"
	Output  io.Writer  // defaults to os.Stderr
	Service string     // OTel service.name; defaults to "agent-status"
}

// ParseLevel converts a string like "debug" or "DEBUG" to slog.Level.
// Unrecognised values fall back to Info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "trace":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}

// Redirect swaps the default slog handler to write to w, preserving
// the configured level/format/trace attribution. Intended for
// commands that take over the terminal (the TUI) and must not leak
// log output onto the screen. Returns a function that restores the
// previous handler.
func Redirect(w io.Writer, cfg Config) func() {
	prev := slog.Default()
	opts := &slog.HandlerOptions{Level: cfg.Level}
	var inner slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "json":
		inner = slog.NewJSONHandler(w, opts)
	default:
		inner = slog.NewTextHandler(w, opts)
	}
	slog.SetDefault(slog.New(&traceHandler{Handler: inner}))
	return func() { slog.SetDefault(prev) }
}

// Setup installs slog as the default logger and starts the OTel
// tracer if configured. The returned shutdown function flushes any
// pending spans; call it from main on exit.
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: cfg.Level}

	var inner slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "json":
		inner = slog.NewJSONHandler(out, opts)
	default:
		inner = slog.NewTextHandler(out, opts)
	}
	slog.SetDefault(slog.New(&traceHandler{Handler: inner}))

	shutdown, err := setupTracer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("logging: %w", err)
	}
	slog.Debug("logging configured",
		"min_level", cfg.Level.String(),
		"format", normFormat(cfg.Format),
		"traces", normTraces(cfg.Traces),
	)
	return shutdown, nil
}

func normFormat(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "json" {
		return "json"
	}
	return "text"
}

// normTraces collapses the user-facing exporter aliases into the
// canonical mode names buildSpanExporter understands:
//   - off                -> off
//   - stdout / on / true -> stdout (pretty-prints to stderr)
//   - otlp / otlp-http   -> otlp-http (default OTLP transport)
//   - otlp-grpc          -> otlp-grpc
//
// Anything else is passed through verbatim so buildSpanExporter can
// raise a clear error on it.
func normTraces(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off", "false", "no", "none":
		return "off"
	case "stdout", "on", "true", "yes":
		return "stdout"
	case "otlp", "otlp-http", "otlp_http", "http":
		return "otlp-http"
	case "otlp-grpc", "otlp_grpc", "grpc":
		return "otlp-grpc"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}
