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

	"github.com/samling/agent-status/internal/state"
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

	// Initial sweep: register every live session and sync its on-disk
	// JSONL status so the state file is populated before any hooks fire.
	// Filtered through walkAlive so dead-PID stragglers (uncleaned
	// session files from prior crashes) don't get inserted.
	if alive, scanned, err := walkAlive(); err != nil {
		log.Printf("watcher: initial sweep: %v", err)
	} else {
		inserted := 0
		for _, sf := range alive {
			if applySessionFile(s, sf) {
				inserted++
			}
		}
		log.Printf("discovery: scanned=%d alive=%d inserted=%d", scanned, len(alive), inserted)
	}

	// Periodic reap as a safety net for dead sessions whose file
	// removal we missed (fsnotify drops events under load, the
	// process crashed without unlinking, the SessionEnd hook had a
	// case mismatch, etc.). Reap is cheap — one ReadDir + a stat
	// per file plus a kill -0 per pid — so polling every 30s costs
	// nothing and prevents stale rows from lingering in the TUI.
	reap := time.NewTicker(30 * time.Second)
	defer reap.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-reap.C:
			if n, err := Reap(s); err != nil {
				log.Printf("watcher: periodic reap: %v", err)
			} else if n > 0 {
				log.Printf("watcher: periodic reap removed %d stale session(s)", n)
			}
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
	applySessionFile(s, sf)
}

// applySessionFile registers sf with the state store and syncs its JSONL
// status. Returns true when the session was newly inserted. Shared by
// the initial sweep (which feeds it parsed entries from walkAlive) and
// the per-event handler.
func applySessionFile(s *state.Store, sf sessionFile) bool {
	var createdAt time.Time
	if sf.StartedAt > 0 {
		createdAt = time.UnixMilli(sf.StartedAt)
	}
	inserted, err := s.MarkDiscovered(sf.SessionID, createdAt)
	if err != nil {
		log.Printf("watcher: mark discovered %s: %v", state.ShortID(sf.SessionID), err)
	} else if inserted {
		log.Printf("watcher: discovered new session %s", state.ShortID(sf.SessionID))
	}
	changed, err := s.SetJSONLStatus(sf.SessionID, sf.Status)
	if err != nil {
		log.Printf("watcher: set jsonl status for %s: %v", state.ShortID(sf.SessionID), err)
		return inserted
	}
	if changed {
		log.Printf("watcher: session %s jsonl_status=%q", state.ShortID(sf.SessionID), sf.Status)
	}
	return inserted
}
