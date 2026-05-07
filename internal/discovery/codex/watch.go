// Push-side discovery for Codex sessions: watch ~/.codex/shell_snapshots/
// for new <sessionID>.<nanoTs>.sh files via fsnotify and dispatch each
// through Apply the moment the snapshot lands. Codex drops this snapshot
// at session open, before any turns, so it's the earliest signal we get
// that a fresh Codex CLI has started up.
//
// Periodic Scan (2-second tick) is the backstop and the source of metadata
// enrichment: it reads state_*.sqlite + recent rollout JSONLs and refines
// the row with cwd/model/version/git_branch shortly after the user takes
// their first turn (which is when Codex actually flushes that data).
package codex

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

// Watch reacts to CREATE events on shell-snapshot files under
// ~/.codex/shell_snapshots/. The directory is flat (no date partitioning),
// so a single kernel watch covers it. End-of-session is not handled here;
// the PID reaper covers it.
func Watch(ctx context.Context, s *state.Store) error {
	home, err := homeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "shell_snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Add(dir); err != nil {
		return err
	}
	slog.InfoContext(ctx, "discovery: codex fsnotify watcher started", "dir", dir)

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create == 0 {
				continue
			}
			if !isShellSnapshotPath(ev.Name) {
				continue
			}
			applySessionFromShellSnapshot(ctx, s, ev.Name)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.WarnContext(ctx, "discovery: codex fsnotify error", "err", err)
		}
	}
}

// isShellSnapshotPath reports whether path is a Codex shell-snapshot file:
// <UUID>.<nanoTimestamp>.sh.
func isShellSnapshotPath(path string) bool {
	base := filepath.Base(path)
	if filepath.Ext(base) != ".sh" {
		return false
	}
	parts := strings.SplitN(base, ".", 3)
	if len(parts) != 3 {
		return false
	}
	return looksLikeUUID(parts[0])
}

// threadIDFromShellSnapshotPath extracts the session UUID from a shell
// snapshot filename. Returns "" if the filename doesn't match the expected
// shape.
func threadIDFromShellSnapshotPath(path string) string {
	base := filepath.Base(path)
	id, _, ok := strings.Cut(base, ".")
	if !ok || !looksLikeUUID(id) {
		return ""
	}
	return id
}

// shellSnapshotTimestamp parses the nanosecond Unix timestamp embedded in a
// shell snapshot filename: <UUID>.<nanoTimestamp>.sh. Returns the zero time
// if it can't be parsed; callers should fall back to file mtime / now.
func shellSnapshotTimestamp(path string) time.Time {
	base := strings.TrimSuffix(filepath.Base(path), ".sh")
	i := strings.LastIndexByte(base, '.')
	if i < 0 {
		return time.Time{}
	}
	nanos, err := strconv.ParseInt(base[i+1:], 10, 64)
	if err != nil || nanos <= 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

func looksLikeUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}

// applySessionFromShellSnapshot dispatches a minimal LiveSession synthesized
// from the shell-snapshot filename: just the session ID, "cli" entrypoint,
// and the start timestamp from the filename (or mtime as fallback). Codex
// hasn't written rollout/state metadata yet at this point, so cwd/version/
// model are left empty for later refinement by Scan.
func applySessionFromShellSnapshot(ctx context.Context, s *state.Store, path string) {
	id := threadIDFromShellSnapshotPath(path)
	if id == "" {
		return
	}
	startedAt := shellSnapshotTimestamp(path)
	if startedAt.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			startedAt = fi.ModTime()
		} else {
			startedAt = time.Now()
		}
	}
	sess := source.LiveSession{
		Agent:     state.AgentCodex,
		SessionID: id,
		StartedAt: startedAt,
		Event:     "SessionStart",
		EventAt:   startedAt,
		Meta: source.SessionMeta{
			Entrypoint: "cli",
			UpdatedAt:  startedAt,
		},
	}
	if Apply(ctx, s, sess) {
		slog.InfoContext(ctx, "discovery: codex session via shell_snapshot",
			"session", state.ShortID(id))
	}
}
