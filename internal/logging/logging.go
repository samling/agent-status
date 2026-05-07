// Package logging configures slog and optional tracing.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config holds resolved logging settings.
type Config struct {
	Level   slog.Level // log level threshold
	Format  string     // "text" or "json"
	Traces  string     // "off" or "stdout"
	Output  io.Writer  // defaults to os.Stderr
	Service string     // OTel service.name; defaults to "agent-status"
}

// ParseLevel converts user input to slog.Level.
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

// Redirect sends default slog output to w until the returned restore runs.
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

// Setup installs slog and tracing, returning a span-flush shutdown.
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

// normTraces canonicalizes user-facing exporter aliases.
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
