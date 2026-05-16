package opencode

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/samling/agent-status/internal/state"
)

// Watch reacts to Opencode SQLite database changes and debounces rescans so
// discovery does not read the database while Opencode is still writing.
func Watch(ctx context.Context, s *state.Store) error {
	path, err := dbPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
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
	slog.InfoContext(ctx, "discovery: opencode fsnotify watcher started", "dir", dir)

	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			base := filepath.Base(event.Name)
			if base != "opencode.db" && base != "opencode.db-wal" && base != "opencode.db-shm" {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(150 * time.Millisecond)
				timerC = timer.C
				continue
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(150 * time.Millisecond)
		case <-timerC:
			timerC = nil
			timer = nil
			sessions, _, scanErr := Scan()
			if scanErr != nil {
				slog.WarnContext(ctx, "discovery: opencode scan after fsnotify failed", "err", scanErr)
				continue
			}
			for _, sess := range sessions {
				Apply(ctx, s, sess)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.WarnContext(ctx, "discovery: opencode fsnotify error", "err", err)
		}
	}
}
