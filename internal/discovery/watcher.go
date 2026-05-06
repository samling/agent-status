package discovery

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/samling/agent-status/internal/state"
)

// Watch monitors Claude session-file events and polls all registered
// discovery sources into our state file. Removal/rename triggers a reap.
// Runs until ctx is cancelled.
func Watch(ctx context.Context, s *state.Store) error {
	dir, err := claudeSessionsDir()
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

	// Initial sweep: register every live session and sync its on-disk
	// status so the state file is populated before any hooks fire.
	// Filtered through each provider's liveness check so dead-PID
	// stragglers from prior crashes don't get inserted.
	if scanned, alive, updated, err := syncDiscovered(s); err != nil {
		log.Printf("watcher: initial sweep: %v", err)
	} else {
		log.Printf("discovery: scanned=%d alive=%d updated=%d", scanned, alive, updated)
	}

	discover := time.NewTicker(2 * time.Second)
	defer discover.Stop()

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
		case <-discover.C:
			if scanned, alive, updated, err := syncDiscovered(s); err != nil {
				log.Printf("watcher: discovery poll: %v", err)
			} else if updated > 0 {
				log.Printf("discovery: scanned=%d alive=%d updated=%d", scanned, alive, updated)
			}
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
				processClaudeSessionFile(s, event.Name)
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

func syncDiscovered(s *state.Store) (scanned, alive, updated int, err error) {
	type result struct {
		sessions []liveAgentSession
		scanned  int
		err      error
	}
	sources := liveSources()
	ch := make(chan result, len(sources))
	for _, src := range sources {
		src := src
		go func() {
			sessions, scanned, err := src.scan()
			ch <- result{sessions: sessions, scanned: scanned, err: err}
		}()
	}
	for range sources {
		res := <-ch
		if res.err != nil {
			if err == nil {
				err = res.err
			}
			continue
		}
		scanned += res.scanned
		alive += len(res.sessions)
		for _, sess := range res.sessions {
			if applyLiveSession(s, sess) {
				updated++
			}
		}
	}
	return scanned, alive, updated, err
}

func applyLiveSession(s *state.Store, sess liveAgentSession) bool {
	switch sess.Agent {
	case state.AgentClaudeCode:
		return applyClaudeSessionFile(s, claudeSessionFile{
			PID:        sess.Meta.PID,
			SessionID:  sess.SessionID,
			Entrypoint: sess.Meta.Entrypoint,
			Cwd:        sess.Meta.Cwd,
			Status:     sess.EngineStatus,
			Version:    sess.Meta.Version,
			StartedAt:  unixMilli(sess.StartedAt),
		})
	case state.AgentCodex:
		changed, err := s.ReconcileDiscovered(sess.Agent, sess.SessionID, sess.StartedAt)
		if err != nil {
			log.Printf("watcher: reconcile discovered %s %s: %v", sess.Agent, state.ShortID(sess.SessionID), err)
		} else if changed {
			log.Printf("watcher: reconciled %s session %s", sess.Agent, state.ShortID(sess.SessionID))
		}
		return changed
	default:
		changed, err := s.RecordObserved(sess.Agent, sess.SessionID, sess.StartedAt, sess.Event, sess.EventAt, sess.EngineStatus)
		if err != nil {
			log.Printf("watcher: record observed %s %s: %v", sess.Agent, state.ShortID(sess.SessionID), err)
		}
		return changed
	}
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
