package discovery

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/samling/agent-status/internal/state"
)

type sessionFile struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	StartedAt  int64  `json:"startedAt"` // Unix milliseconds; absent on some entrypoints
	Entrypoint string `json:"entrypoint"`
	Cwd        string `json:"cwd"`
	Status     string `json:"status"`  // "idle"|"busy"; absent for non-cli entrypoints
	Version    string `json:"version"` // Claude Code version string, e.g. "2.1.128"
}

// SessionMeta is the per-session metadata available from ~/.claude/sessions/.
type SessionMeta struct {
	PID        int
	Entrypoint string
	Cwd        string
	Version    string
}

// LiveSessionMeta returns a map of session_id -> SessionMeta for sessions
// currently alive on disk. Read-only; used by the UI to enrich rows with
// fields that are not part of persisted state.
func LiveSessionMeta() (map[string]SessionMeta, error) {
	out := map[string]SessionMeta{}
	alive, _, err := walkAlive()
	if err != nil {
		return out, err
	}
	for _, sf := range alive {
		out[sf.SessionID] = SessionMeta{PID: sf.PID, Entrypoint: sf.Entrypoint, Cwd: sf.Cwd, Version: sf.Version}
	}
	return out, nil
}

// Reap removes any state entry whose session_id is no longer backed by a
// live session file. Returns the count removed.
func Reap(s *state.Store) (int, error) {
	alive, _, err := walkAlive()
	if err != nil {
		return 0, err
	}
	set := make(map[string]bool, len(alive))
	for _, sf := range alive {
		set[sf.SessionID] = true
	}
	return s.ReapAbsent(set)
}

// walkAlive returns every parsed session file whose PID is still alive.
// scanned counts every parseable file regardless of liveness. Parsed
// entries are cached keyed on (path, mtime, size) so the UI's per-tick
// poll doesn't re-read every file under ~/.claude/sessions/ on each
// refresh; pidAlive is checked unconditionally because liveness can
// flip between polls without touching the file.
func walkAlive() (alive []sessionFile, scanned int, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, 0, err
	}
	dir := filepath.Join(home, ".claude", "sessions")
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
		if !pidAlive(sf.PID) {
			continue
		}
		alive = append(alive, sf)
	}
	pruneWalkCache(seen)
	return alive, scanned, nil
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

// loadSessionFile returns the parsed sessionFile at path, using the
// (mtime, size) cache when present. The mutex is held only for cache
// reads/writes; I/O happens unlocked, so concurrent callers may race to
// re-parse the same file on a miss but that's harmless.
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

func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
