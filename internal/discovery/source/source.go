// Package source defines the shared types passed between discovery
// orchestration (parent discovery package) and per-agent discovery backends
// (claudecode, codex).
package source

import "time"

// LiveSession is the normalized output of one per-agent scan, used by the
// discovery loop to upsert state and reap stale rows.
type LiveSession struct {
	Agent        string
	SessionID    string
	StartedAt    time.Time
	Event        string
	EventAt      time.Time
	EngineStatus string
	Meta         SessionMeta
}

// SessionMeta is the UI-facing metadata blob for a live session, sourced
// from agent-owned files (transcripts, SQLite, JSON session files).
type SessionMeta struct {
	PID        int
	Entrypoint string
	Cwd        string
	Version    string
	Model      string
	Path       string
	UpdatedAt  time.Time
}
