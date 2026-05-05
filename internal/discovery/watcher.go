package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"

	"agent-status/internal/store"
)

// Watch monitors ~/.claude/sessions/*.json for write/create events and
// syncs idle status into the events table: when a file reports
// status=="idle" and our derived status is currently "active" or
// "waiting", a synthetic "Idle" event is appended. Runs until ctx is
// cancelled.
func Watch(ctx context.Context, db *sql.DB) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".claude", "sessions")
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

	// Initial sweep so already-idle sessions get synced without waiting
	// for the next on-disk update.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			processSessionFile(ctx, db, filepath.Join(dir, e.Name()))
		}
	}

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
				processSessionFile(ctx, db, event.Name)
			case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
				// File vanished: a session likely exited (including
				// non-clean exits that skip SessionEnd). Trigger a reap.
				if n, err := Reap(ctx, db); err != nil {
					log.Printf("watcher: reap after %s: %v", filepath.Base(event.Name), err)
				} else if n > 0 {
					log.Printf("watcher: reaped %d session(s) after file removal", n)
				}
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher: %v", err)
		}
	}
}

func processSessionFile(ctx context.Context, db *sql.DB, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("watcher: read %s: %v", filepath.Base(path), err)
		}
		return
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		// Mid-write race or unrelated file; ignore quietly.
		return
	}
	if sf.SessionID == "" {
		return
	}
	setStatus(sf.SessionID, sf.Status)
	if sf.Status != "idle" {
		return
	}
	inserted, err := store.SyncIdle(ctx, db, sf.SessionID)
	if err != nil {
		log.Printf("watcher: sync idle for %s: %v", sf.SessionID, err)
		return
	}
	if inserted {
		log.Printf("watcher: synced session %s to idle", short(sf.SessionID))
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
