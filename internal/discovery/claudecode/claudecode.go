// Package claudecode is the Claude Code discovery backend: it reads the
// session JSON files Claude Code writes under ~/.claude/sessions/ and
// translates them into the shared source.LiveSession shape.
package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.opentelemetry.io/otel/attribute"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/state"
)

type sessionFile struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	StartedAt  int64  `json:"startedAt"` // Unix milliseconds
	Entrypoint string `json:"entrypoint"`
	Cwd        string `json:"cwd"`
	Status     string `json:"status"`  // "idle"|"busy"; absent for non-cli entrypoints
	Version    string `json:"version"` // Claude Code version string, e.g. "2.1.128"
}

func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "sessions"), nil
}

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

// Scan returns the currently-live Claude sessions (PID-alive sessions whose
// JSON file is present under ~/.claude/sessions/).
func Scan() ([]source.LiveSession, int, error) {
	files, scanned, err := walkAlive()
	if err != nil {
		return nil, scanned, err
	}
	out := make([]source.LiveSession, 0, len(files))
	for _, sf := range files {
		var startedAt time.Time
		if sf.StartedAt > 0 {
			startedAt = time.UnixMilli(sf.StartedAt)
		}
		out = append(out, source.LiveSession{
			Agent:        state.AgentClaudeCode,
			SessionID:    sf.SessionID,
			StartedAt:    startedAt,
			Event:        "Discovered",
			EngineStatus: sf.Status,
			Meta: source.SessionMeta{
				PID:        sf.PID,
				Entrypoint: sf.Entrypoint,
				Cwd:        sf.Cwd,
				Version:    sf.Version,
			},
		})
	}
	return out, scanned, nil
}

func walkAlive() (alive []sessionFile, scanned int, err error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		seen[path] = struct{}{}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		sf, ok := loadSessionFile(path, fi.ModTime(), fi.Size())
		if !ok {
			continue
		}
		if sf.SessionID == "" || sf.PID <= 0 {
			continue
		}
		scanned++
		if !source.PIDAlive(sf.PID) {
			continue
		}
		alive = append(alive, sf)
	}
	pruneWalkCache(seen)
	return alive, scanned, nil
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

// Apply adapts a scanned source.LiveSession into the claude-specific upsert
// path.
func Apply(ctx context.Context, s *state.Store, sess source.LiveSession) bool {
	return applySessionFile(ctx, s, sessionFile{
		PID:        sess.Meta.PID,
		SessionID:  sess.SessionID,
		Entrypoint: sess.Meta.Entrypoint,
		Cwd:        sess.Meta.Cwd,
		Status:     sess.EngineStatus,
		Version:    sess.Meta.Version,
		StartedAt:  unixMilli(sess.StartedAt),
	})
}

// applySessionFile upserts a parsed Claude session JSON into the store.
//
// On first sight: insert a row stamped "Discovered" with the engine status
// (idle/busy) read from the file.
//
// On already-known rows: refresh Agent and EngineStatus only. If the refresh
// causes the derived status to flip, bump StatusAt to mark the transition.
// Hook-driven LastEvent / LastEventAt / TurnID are deliberately untouched so
// they aren't clobbered by a later poll.
func applySessionFile(ctx context.Context, s *state.Store, sf sessionFile) bool {
	traceHex, spanHex, traceErr := s.EnsureTrace(ctx, sf.SessionID, state.AgentClaudeCode, func() (string, string) {
		return logging.NewSessionRoot(ctx, sf.SessionID, state.AgentClaudeCode)
	})
	if traceErr != nil {
		slog.WarnContext(ctx, "discovery: ensure trace failed",
			"agent", state.AgentClaudeCode, "session", state.ShortID(sf.SessionID), "err", traceErr)
	}
	ctx = logging.ContextWithSessionTrace(ctx, traceHex, spanHex)

	// Avoid opening a span up front: every Watch fsnotify Write and every
	// 2s sweep re-applies each alive session, and steady state is "no
	// change". Do the work, then emit a span only when state actually
	// shifted, backdated to when the work began.
	start := time.Now()

	createdAt := start
	if sf.StartedAt > 0 {
		createdAt = time.UnixMilli(sf.StartedAt)
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)

	inserted, err := s.InsertSession(ctx, state.Session{
		SessionID:    sf.SessionID,
		Agent:        state.AgentClaudeCode,
		FirstSeenAt:  ts,
		LastEvent:    "Discovered",
		LastEventAt:  ts,
		StatusAt:     ts,
		EngineStatus: sf.Status,
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: claude-code insert failed",
			"session", state.ShortID(sf.SessionID), "err", err)
		return false
	}
	if inserted {
		_, span := logging.StartAt(ctx, "discovery.apply", start,
			attribute.String("agent", state.AgentClaudeCode),
			attribute.String("session.id", sf.SessionID),
			attribute.String("engine_status", sf.Status),
			attribute.Bool("inserted", true),
		)
		span.End()
		slog.InfoContext(ctx, "discovery: new claude-code session",
			"session", state.ShortID(sf.SessionID),
			"pid", sf.PID, "entrypoint", sf.Entrypoint, "version", sf.Version,
			"engine_status", sf.Status)
		return true
	}

	var (
		transitioned bool
		identified   bool
		priorAgent   string
	)
	engineChanged, err := s.UpdateSession(ctx, sf.SessionID, func(stored *state.Session) bool {
		var changed bool
		// Identify (or re-identify) Agent if a hook stamped it before us with
		// the placeholder. We never downgrade a concrete label.
		if stored.Agent == "" || stored.Agent == state.AgentUnidentified {
			priorAgent = stored.Agent
			stored.Agent = state.AgentClaudeCode
			identified = true
			changed = true
		}
		if stored.EngineStatus != sf.Status {
			prevStatus := state.DeriveStatus(*stored)
			stored.EngineStatus = sf.Status
			if state.DeriveStatus(*stored) != prevStatus {
				stored.StatusAt = time.Now().UTC().Format(time.RFC3339Nano)
				transitioned = true
			}
			changed = true
		}
		return changed
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: claude-code refine failed",
			"session", state.ShortID(sf.SessionID), "err", err)
		return false
	}
	if engineChanged {
		_, span := logging.StartAt(ctx, "discovery.apply", start,
			attribute.String("agent", state.AgentClaudeCode),
			attribute.String("session.id", sf.SessionID),
			attribute.String("engine_status", sf.Status),
			attribute.Bool("refined", true),
			attribute.Bool("identified", identified),
			attribute.Bool("transitioned", transitioned),
		)
		span.End()
	}
	if identified {
		slog.InfoContext(ctx, "discovery: agent identified",
			"session", state.ShortID(sf.SessionID),
			"from", priorAgent, "to", state.AgentClaudeCode)
	}
	switch {
	case transitioned:
		slog.InfoContext(ctx, "discovery: claude-code status transitioned",
			"session", state.ShortID(sf.SessionID), "engine_status", sf.Status)
	case engineChanged:
		slog.DebugContext(ctx, "discovery: claude-code engine_status recorded",
			"session", state.ShortID(sf.SessionID), "engine_status", sf.Status)
	}
	return false
}

type cachedSessionFile struct {
	mtime time.Time
	size  int64
	sf    sessionFile
}

var (
	walkCacheMu sync.Mutex
	walkCache   = map[string]cachedSessionFile{}
)

func loadSessionFile(path string, mtime time.Time, size int64) (sessionFile, bool) {
	walkCacheMu.Lock()
	cached, ok := walkCache[path]
	walkCacheMu.Unlock()
	if ok && cached.mtime.Equal(mtime) && cached.size == size {
		return cached.sf, true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, false
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return sessionFile{}, false
	}
	walkCacheMu.Lock()
	walkCache[path] = cachedSessionFile{mtime: mtime, size: size, sf: sf}
	walkCacheMu.Unlock()
	return sf, true
}

func pruneWalkCache(seen map[string]struct{}) {
	walkCacheMu.Lock()
	defer walkCacheMu.Unlock()
	for path := range walkCache {
		if _, ok := seen[path]; !ok {
			delete(walkCache, path)
		}
	}
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
