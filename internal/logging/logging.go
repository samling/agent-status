// Package logging configures slog.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config holds resolved logging settings.
type Config struct {
	Level  slog.Level // log level threshold
	Format string     // "text" or "json"
	Output io.Writer  // defaults to os.Stderr
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

// Silence installs slog.DiscardHandler as the default until restore runs.
// Unlike Redirect to io.Discard, the discard handler reports Enabled=false
// so logger calls short-circuit before any formatting work.
func Silence() func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	return func() { slog.SetDefault(prev) }
}

// Redirect sends default slog output to w until the returned restore runs.
func Redirect(w io.Writer, cfg Config) func() {
	prev := slog.Default()
	opts := &slog.HandlerOptions{Level: cfg.Level}
	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	default:
		h = slog.NewTextHandler(w, opts)
	}
	slog.SetDefault(slog.New(h))
	return func() { slog.SetDefault(prev) }
}

// Setup installs slog and returns a no-op shutdown for symmetry with callers
// that previously also flushed span exporters.
func Setup(_ context.Context, cfg Config) (func(context.Context) error, error) {
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: cfg.Level}

	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "json":
		h = slog.NewJSONHandler(out, opts)
	default:
		h = slog.NewTextHandler(out, opts)
	}
	slog.SetDefault(slog.New(h))

	slog.Debug("logging configured",
		"min_level", cfg.Level.String(),
		"format", normFormat(cfg.Format),
	)
	return func(context.Context) error { return nil }, nil
}

func normFormat(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "json" {
		return "json"
	}
	return "text"
}
