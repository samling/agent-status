package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"agent-status/internal/state"
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

// Result reports the outcome of a discovery scan.
type Result struct {
	Scanned  int // session files seen on disk
	Alive    int // session files whose PID is still alive
	Inserted int // sessions newly added to state
}

// Run scans ~/.claude/sessions/*.json for live Claude Code sessions and
// registers any session_id not yet present in state with a Discovered
// marker. Existing sessions are not touched.
func Run(ctx context.Context, s *state.Store) (Result, error) {
	var r Result
	alive, scanned, err := walkAlive()
	if err != nil {
		return r, err
	}
	r.Scanned = scanned
	r.Alive = len(alive)
	for _, sf := range alive {
		var createdAt time.Time
		if sf.StartedAt > 0 {
			createdAt = time.UnixMilli(sf.StartedAt)
		}
		inserted, err := s.MarkDiscovered(sf.SessionID, createdAt)
		if err != nil {
			return r, err
		}
		if inserted {
			r.Inserted++
		}
	}
	return r, nil
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
func Reap(ctx context.Context, s *state.Store) (int, error) {
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
// scanned counts every parseable file regardless of liveness.
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
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var sf sessionFile
		if err := json.Unmarshal(b, &sf); err != nil {
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
	return alive, scanned, nil
}

func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
