// Package logging configures slog and optional tracing.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds resolved logging settings.
type Config struct {
	Level   slog.Level // log level threshold
	Format  string     // "text" or "json"
	Output  io.Writer  // defaults to os.Stderr
	Service string     // OTel service.name; defaults to "agent-status"

	// Tracing. When TracesEnabled is false the rest of the trace fields
	// are ignored and the global TracerProvider stays NoOp.
	TracesEnabled  bool
	TracesExporter string // "stdout" | "otlp-http" | "otlp-grpc"

	// OTLP-only knobs (used when TracesExporter is otlp-*). Empty fields
	// fall through to the OTel SDK's environment-variable defaults
	// (OTEL_EXPORTER_OTLP_*), so users can still drive everything from
	// env if they prefer.
	OTLPEndpoint    string            // "host:port" or full URL (https://otel.example.com/v1/traces)
	OTLPInsecure    bool              // disable TLS / use h2c
	OTLPHeaders     map[string]string // outgoing request headers
	OTLPTimeout     time.Duration     // per-export deadline
	OTLPCompression string            // "" | "gzip" | "none"
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
	tracesDesc := "off"
	if cfg.TracesEnabled {
		tracesDesc = normExporter(cfg.TracesExporter)
	}
	slog.Debug("logging configured",
		"min_level", cfg.Level.String(),
		"format", normFormat(cfg.Format),
		"traces", tracesDesc,
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

// normExporter canonicalizes the trace exporter name. Empty input falls back
// to "stdout"; unknown values pass through unchanged so setupTracer can
// surface a precise error.
func normExporter(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "stdout":
		return "stdout"
	case "otlp", "otlp-http", "otlp_http", "http":
		return "otlp-http"
	case "otlp-grpc", "otlp_grpc", "grpc":
		return "otlp-grpc"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}
