package discovery

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

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

func processClaudeSessionFile(s *state.Store, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("watcher: read %s: %v", filepath.Base(path), err)
		}
		return
	}
	var sf claudeSessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		// Mid-write race or unrelated file; ignore quietly.
		return
	}
	if sf.SessionID == "" {
		return
	}
	applyClaudeSessionFile(s, sf)
}

// applyClaudeSessionFile registers sf with the state store and syncs its
// status. Returns true when the session was newly inserted.
func applyClaudeSessionFile(s *state.Store, sf claudeSessionFile) bool {
	var createdAt time.Time
	if sf.StartedAt > 0 {
		createdAt = time.UnixMilli(sf.StartedAt)
	}
	inserted, err := s.MarkDiscovered(state.AgentClaudeCode, sf.SessionID, createdAt)
	if err != nil {
		log.Printf("watcher: mark discovered %s: %v", state.ShortID(sf.SessionID), err)
	} else if inserted {
		log.Printf("watcher: discovered new session %s", state.ShortID(sf.SessionID))
	}
	changed, err := s.SetJSONLStatus(state.AgentClaudeCode, sf.SessionID, sf.Status)
	if err != nil {
		log.Printf("watcher: set jsonl status for %s: %v", state.ShortID(sf.SessionID), err)
		return inserted
	}
	if changed {
		log.Printf("watcher: session %s jsonl_status=%q", state.ShortID(sf.SessionID), sf.Status)
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
