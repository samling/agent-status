package discovery

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

	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/state"
)

type claudeSessionFile struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	StartedAt  int64  `json:"startedAt"` // Unix milliseconds; absent on some entrypoints
	Entrypoint string `json:"entrypoint"`
	Cwd        string `json:"cwd"`
	Status     string `json:"status"`  // "idle"|"busy"; absent for non-cli entrypoints
	Version    string `json:"version"` // Claude Code version string, e.g. "2.1.128"
}

func claudeSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "sessions"), nil
}

// watchClaudeFiles is claude-code's fast-path: an fsnotify watcher on
// ~/.claude/sessions/ so per-session JSON updates land in state within
// milliseconds instead of waiting up to one 2s poll. File removal
// triggers a global reap so a session that exits without firing
// SessionEnd doesn't linger. This is wired in as the watch field of
// the claude-code liveSource (see liveSources in discovery.go); the
// poll loop in Watch supervises it.
func watchClaudeFiles(ctx context.Context, s *state.Store) error {
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
			eventCtx, span := logging.Start(ctx, "discovery.fs_event",
				attribute.String("agent", state.AgentClaudeCode),
				attribute.String("file", filepath.Base(event.Name)),
				attribute.String("op", event.Op.String()),
			)
			switch {
			case event.Op&(fsnotify.Write|fsnotify.Create) != 0:
				slog.DebugContext(eventCtx, "discovery: claude file changed",
					"file", filepath.Base(event.Name), "op", event.Op.String())
				processClaudeSessionFile(eventCtx, s, event.Name)
			case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
				// File vanished: a claude session likely exited (incl.
				// non-clean exits that skip SessionEnd). Reap claude
				// only — a global Reap would also re-scan codex SQLite
				// for nothing. The next 2s poll's inline reap is the
				// backstop if this fast path errors.
				slog.DebugContext(eventCtx, "discovery: claude file removed, reaping",
					"file", filepath.Base(event.Name), "op", event.Op.String())
				sessions, _, scanErr := scanClaudeLive()
				if scanErr != nil {
					slog.WarnContext(eventCtx, "discovery: claude rescan after remove failed",
						"file", filepath.Base(event.Name), "err", scanErr)
					break
				}
				aliveSet := make(map[string]bool, len(sessions))
				for _, sess := range sessions {
					aliveSet[sess.SessionID] = true
				}
				n, reapErr := s.ReapAbsentForAgent(eventCtx, state.AgentClaudeCode, aliveSet)
				if reapErr != nil {
					slog.WarnContext(eventCtx, "discovery: reap after file removal failed",
						"file", filepath.Base(event.Name), "err", reapErr)
				} else if n > 0 {
					slog.InfoContext(eventCtx, "discovery: reaped after file removal",
						"file", filepath.Base(event.Name), "n", n)
				}
			}
			span.End()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.WarnContext(ctx, "discovery: claude fsnotify error", "err", err)
		}
	}
}

func scanClaudeLive() ([]liveAgentSession, int, error) {
	files, scanned, err := walkClaudeAlive()
	if err != nil {
		return nil, scanned, err
	}
	out := make([]liveAgentSession, 0, len(files))
	for _, sf := range files {
		var startedAt time.Time
		if sf.StartedAt > 0 {
			startedAt = time.UnixMilli(sf.StartedAt)
		}
		out = append(out, liveAgentSession{
			Agent:        state.AgentClaudeCode,
			SessionID:    sf.SessionID,
			StartedAt:    startedAt,
			Event:        "Discovered",
			EngineStatus: sf.Status,
			Meta: SessionMeta{
				Agent:      state.AgentClaudeCode,
				PID:        sf.PID,
				Entrypoint: sf.Entrypoint,
				Cwd:        sf.Cwd,
				Version:    sf.Version,
			},
		})
	}
	return out, scanned, nil
}

// walkClaudeAlive returns every parsed Claude session file whose PID is
// still alive. scanned counts every parseable file regardless of
// liveness. Parsed entries are cached keyed on (path, mtime, size) so
// polling doesn't re-read every file under ~/.claude/sessions/ on each
// refresh; pidAlive is checked unconditionally because liveness can
// flip between polls without touching the file.
func walkClaudeAlive() (alive []claudeSessionFile, scanned int, err error) {
	dir, err := claudeSessionsDir()
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
		sf, ok := loadClaudeSessionFile(path, fi.ModTime(), fi.Size())
		if !ok {
			continue
		}
		if sf.SessionID == "" || sf.PID <= 0 {
			continue
		}
		scanned++
		if !pidAlive(sf.PID) {
			continue
		}
		alive = append(alive, sf)
	}
	pruneClaudeWalkCache(seen)
	return alive, scanned, nil
}

func processClaudeSessionFile(ctx context.Context, s *state.Store, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(ctx, "discovery: read session file",
				"file", filepath.Base(path), "err", err)
		}
		return
	}
	var sf claudeSessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		// Mid-write race or unrelated file; ignore quietly.
		slog.DebugContext(ctx, "discovery: unparseable session file (likely mid-write)",
			"file", filepath.Base(path), "err", err)
		return
	}
	if sf.SessionID == "" {
		return
	}
	applyClaudeSessionFile(ctx, s, sf)
}

// applyClaudeSessionFile registers sf with the state store and syncs its
// status in a single critical section. Returns true when the session
// was newly inserted.
func applyClaudeSessionFile(ctx context.Context, s *state.Store, sf claudeSessionFile) bool {
	var createdAt time.Time
	if sf.StartedAt > 0 {
		createdAt = time.UnixMilli(sf.StartedAt)
	}
	inserted, jsonlChanged, transitioned, err := s.ApplyDiscovered(
		ctx, state.AgentClaudeCode, sf.SessionID, sf.Status, createdAt,
	)
	if err != nil {
		slog.WarnContext(ctx, "discovery: apply discovered failed",
			"session", state.ShortID(sf.SessionID), "err", err)
		return inserted
	}
	switch {
	case inserted:
		slog.InfoContext(ctx, "discovery: new claude-code session",
			"session", state.ShortID(sf.SessionID),
			"pid", sf.PID, "entrypoint", sf.Entrypoint, "version", sf.Version,
			"jsonl_status", sf.Status)
	case transitioned:
		// Real status change (e.g. idle -> active): worth INFO so a
		// human reading the log sees the session move on.
		slog.InfoContext(ctx, "discovery: claude-code status transitioned",
			"session", state.ShortID(sf.SessionID), "jsonl_status", sf.Status)
	case jsonlChanged:
		// First observation of a JSONL status (or a same-derived-state
		// flip): bookkeeping only, keep at DEBUG so INFO reflects real
		// state changes.
		slog.DebugContext(ctx, "discovery: claude-code jsonl_status recorded",
			"session", state.ShortID(sf.SessionID), "jsonl_status", sf.Status)
	}
	return inserted
}

type cachedClaudeSessionFile struct {
	mtime time.Time
	size  int64
	sf    claudeSessionFile
}

var (
	claudeWalkCacheMu sync.Mutex
	claudeWalkCache   = map[string]cachedClaudeSessionFile{}
)

// loadClaudeSessionFile returns the parsed session file at path, using
// the (mtime, size) cache when present. The mutex is held only for
// cache reads/writes; I/O happens unlocked, so concurrent callers may
// race to re-parse the same file on a miss but that's harmless.
func loadClaudeSessionFile(path string, mtime time.Time, size int64) (claudeSessionFile, bool) {
	claudeWalkCacheMu.Lock()
	cached, ok := claudeWalkCache[path]
	claudeWalkCacheMu.Unlock()
	if ok && cached.mtime.Equal(mtime) && cached.size == size {
		return cached.sf, true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return claudeSessionFile{}, false
	}
	var sf claudeSessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return claudeSessionFile{}, false
	}
	claudeWalkCacheMu.Lock()
	claudeWalkCache[path] = cachedClaudeSessionFile{mtime: mtime, size: size, sf: sf}
	claudeWalkCacheMu.Unlock()
	return sf, true
}

func pruneClaudeWalkCache(seen map[string]struct{}) {
	claudeWalkCacheMu.Lock()
	defer claudeWalkCacheMu.Unlock()
	for path := range claudeWalkCache {
		if _, ok := seen[path]; !ok {
			delete(claudeWalkCache, path)
		}
	}
}
