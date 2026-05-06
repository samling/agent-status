package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"agent-status/internal/state"
)

// Watch monitors ~/.claude/sessions/*.json for write/create events and
// syncs the on-disk JSONL status into our state file: when a file
// reports status=="idle" and our derived status is currently "active"
// or "waiting", we flip the entry to idle. Removal/rename triggers a
// reap. Runs until ctx is cancelled.
func Watch(ctx context.Context, s *state.Store) error {
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
			processSessionFile(s, filepath.Join(dir, e.Name()))
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
				processSessionFile(s, event.Name)
			case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
				// File vanished: a session likely exited (including
				// non-clean exits that skip SessionEnd). Trigger a reap.
				if n, err := Reap(s); err != nil {
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

func processSessionFile(s *state.Store, path string) {
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
	// Pick up sessions that started after the server did so they show
	// up immediately, without waiting for a hook to fire.
	var createdAt time.Time
	if sf.StartedAt > 0 {
		createdAt = time.UnixMilli(sf.StartedAt)
	}
	if inserted, err := s.MarkDiscovered(sf.SessionID, createdAt); err != nil {
		log.Printf("watcher: mark discovered %s: %v", state.ShortID(sf.SessionID), err)
	} else if inserted {
		log.Printf("watcher: discovered new session %s", state.ShortID(sf.SessionID))
	}
	changed, err := s.SetJSONLStatus(sf.SessionID, sf.Status)
	if err != nil {
		log.Printf("watcher: set jsonl status for %s: %v", state.ShortID(sf.SessionID), err)
		return
	}
	if changed {
		log.Printf("watcher: session %s jsonl_status=%q", state.ShortID(sf.SessionID), sf.Status)
	}
}
