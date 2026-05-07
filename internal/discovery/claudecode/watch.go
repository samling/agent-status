// Push-side discovery for Claude Code sessions: an fsnotify watcher on
// ~/.claude/sessions/ reacts to session JSON file create/write/remove events
// so the store reflects new sessions and reaped removals without waiting
// for the periodic Scan tick.
package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"

	"github.com/samling/agent-status/internal/state"
)

// Watch reacts to fsnotify events on the Claude sessions directory. Push-side
// of discovery; the periodic Scan is the backstop.
//
// No wrapper span is opened per fs event: applySessionFile parents its own
// span on the affected session's persisted trace, which is the granularity
// we want. Removes / renames are not session-scoped (we only know the
// filename, not the dying session id), so reap is logged via slog only.
func Watch(ctx context.Context, s *state.Store) error {
	dir, err := sessionsDir()
	if err != nil {
		return err
	}
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
	slog.InfoContext(ctx, "discovery: claude fsnotify watcher started", "dir", dir)

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			if filepath.Ext(event.Name) != ".json" {
				continue
			}
			switch {
			case event.Op&(fsnotify.Write|fsnotify.Create) != 0:
				slog.DebugContext(ctx, "discovery: claude file changed",
					"file", filepath.Base(event.Name), "op", event.Op.String())
				processFile(ctx, s, event.Name)
			case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
				// Reap only claude-code; the poll loop is the backstop.
				slog.DebugContext(ctx, "discovery: claude file removed, reaping",
					"file", filepath.Base(event.Name), "op", event.Op.String())
				sessions, _, scanErr := Scan()
				if scanErr != nil {
					slog.WarnContext(ctx, "discovery: claude rescan after remove failed",
						"file", filepath.Base(event.Name), "err", scanErr)
					break
				}
				aliveSet := make(map[string]bool, len(sessions))
				for _, sess := range sessions {
					aliveSet[sess.SessionID] = true
				}
				n, reapErr := s.ReapAbsentForAgent(ctx, state.AgentClaudeCode, aliveSet)
				if reapErr != nil {
					slog.WarnContext(ctx, "discovery: reap after file removal failed",
						"file", filepath.Base(event.Name), "err", reapErr)
				} else if n > 0 {
					slog.InfoContext(ctx, "discovery: reaped after file removal",
						"file", filepath.Base(event.Name), "n", n)
				}
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.WarnContext(ctx, "discovery: claude fsnotify error", "err", err)
		}
	}
}

func processFile(ctx context.Context, s *state.Store, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(ctx, "discovery: read session file",
				"file", filepath.Base(path), "err", err)
		}
		return
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		// Mid-write race or unrelated file; ignore quietly.
		slog.DebugContext(ctx, "discovery: unparseable session file (likely mid-write)",
			"file", filepath.Base(path), "err", err)
		return
	}
	if sf.SessionID == "" {
		return
	}
	applySessionFile(ctx, s, sf)
}
