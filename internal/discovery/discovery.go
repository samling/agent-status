package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"agent-status/internal/store"
)

type sessionFile struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	StartedAt  int64  `json:"startedAt"` // Unix milliseconds; absent on some entrypoints
	Entrypoint string `json:"entrypoint"`
	Cwd        string `json:"cwd"`
	Status     string `json:"status"` // "idle"|"busy"; absent for non-cli entrypoints
}

// SessionMeta is the per-session metadata available from ~/.claude/sessions/.
type SessionMeta struct {
	PID        int
	Entrypoint string
	Cwd        string
}

// Result reports the outcome of a discovery scan.
type Result struct {
	Scanned  int // session files seen on disk
	Alive    int // session files whose PID is still alive
	Inserted int // sessions newly added to the events table
}

// Run scans ~/.claude/sessions/*.json for live Claude Code sessions and
// records a synthetic "Discovered" event for any session_id that has no
// events yet. Existing sessions are not touched.
func Run(ctx context.Context, db *sql.DB) (Result, error) {
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
		inserted, err := store.DiscoverSession(ctx, db, sf.SessionID, createdAt)
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
// currently alive on disk. Read-only; intended for the UI to enrich
// DB-derived rows with fields not stored in the events table.
func LiveSessionMeta() (map[string]SessionMeta, error) {
	out := map[string]SessionMeta{}
	alive, _, err := walkAlive()
	if err != nil {
		return out, err
	}
	for _, sf := range alive {
		out[sf.SessionID] = SessionMeta{PID: sf.PID, Entrypoint: sf.Entrypoint, Cwd: sf.Cwd}
	}
	return out, nil
}

// Reap inserts a synthetic "Reaped" event for any DB session whose PID is no
// longer alive (or whose ~/.claude/sessions/<pid>.json no longer exists).
// Idempotent: a session already ended is left alone. Returns count reaped.
func Reap(ctx context.Context, db *sql.DB) (int, error) {
	alive, _, err := walkAlive()
	if err != nil {
		return 0, err
	}
	set := make(map[string]bool, len(alive))
	for _, sf := range alive {
		set[sf.SessionID] = true
	}
	return store.ReapAbsent(ctx, db, set)
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
