// Package state holds per-session status as a single point-in-time JSON
// file. The server is the sole writer; readers (UI, `state` subcommand)
// load the file directly. Writes use a temp-file + rename so readers
// always see a consistent snapshot.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// record is the persisted per-session payload. Status is derived at read
// time from LastEvent and JSONLStatus together.
type record struct {
	FirstSeenAt string `json:"first_seen_at"`
	LastEvent   string `json:"last_event"`
	LastEventAt string `json:"last_event_at"`
	JSONLStatus string `json:"jsonl_status"` // last value of the "status" field in ~/.claude/sessions/<pid>.json
	StatusAt    string `json:"status_at"`    // when derived status last transitioned
}

// Session is the materialized view returned to consumers.
type Session struct {
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	FirstSeenAt string `json:"first_seen_at"`
	LastEvent   string `json:"last_event"`
	LastEventAt string `json:"last_event_at"`
	JSONLStatus string `json:"jsonl_status"`
	StatusAt    string `json:"status_at"`
}

type Store struct {
	path     string
	mu       sync.Mutex
	sessions map[string]record
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{path: path, sessions: map[string]record{}}
	if err := s.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var m map[string]record
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m != nil {
		s.sessions = m
	}
	return nil
}

func (s *Store) persist() error {
	b, err := json.MarshalIndent(s.sessions, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// RecordEvent updates last_event / last_event_at for sessionID, setting
// first_seen_at on first sight. Terminal events (SessionEnd) drop the
// entry entirely; the UI shows only currently-live sessions.
func (s *Store) RecordEvent(sessionID, event, receivedAt string) error {
	if sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if isTerminal(event) {
		if _, ok := s.sessions[sessionID]; !ok {
			return nil
		}
		delete(s.sessions, sessionID)
		return s.persist()
	}
	r, existed := s.sessions[sessionID]
	prevStatus := deriveStatus(r)
	if !existed {
		r.FirstSeenAt = receivedAt
		r.StatusAt = receivedAt
	}
	r.LastEvent = event
	r.LastEventAt = receivedAt
	if existed && deriveStatus(r) != prevStatus {
		r.StatusAt = receivedAt
	}
	s.sessions[sessionID] = r
	return s.persist()
}

// SetJSONLStatus records the latest "status" field read from a session's
// JSONL file. Empty string means "no info" and is treated as such by
// deriveStatus. Returns true if the entry actually changed.
func (s *Store) SetJSONLStatus(sessionID, jsonlStatus string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.sessions[sessionID]
	if !ok {
		// We don't know about this session yet. Wait for discovery or a
		// hook to register it before tracking JSONL status.
		return false, nil
	}
	if r.JSONLStatus == jsonlStatus {
		return false, nil
	}
	prevStatus := deriveStatus(r)
	r.JSONLStatus = jsonlStatus
	if deriveStatus(r) != prevStatus {
		r.StatusAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	s.sessions[sessionID] = r
	return true, s.persist()
}

// MarkDiscovered registers a session only if it is not already present.
// createdAt is the session's actual start time when known; pass zero for
// the current time.
func (s *Store) MarkDiscovered(sessionID string, createdAt time.Time) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; ok {
		return false, nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)
	s.sessions[sessionID] = record{
		FirstSeenAt: ts,
		LastEvent:   "Discovered",
		LastEventAt: ts,
		StatusAt:    ts,
	}
	return true, s.persist()
}

// ReapAbsent drops any session whose id is not in alive. Returns the
// number of entries removed.
func (s *Store) ReapAbsent(alive map[string]bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id := range s.sessions {
		if !alive[id] {
			delete(s.sessions, id)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.persist()
}

// Sessions returns the current state, newest event first.
func (s *Store) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return materialize(s.sessions)
}

// Load reads a state file without opening it for writes. Intended for
// read-only consumers in separate processes.
func Load(path string) ([]Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var m map[string]record
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return materialize(m), nil
}

func materialize(m map[string]record) []Session {
	out := make([]Session, 0, len(m))
	for id, r := range m {
		out = append(out, Session{
			SessionID:   id,
			Status:      deriveStatus(r),
			FirstSeenAt: r.FirstSeenAt,
			LastEvent:   r.LastEvent,
			LastEventAt: r.LastEventAt,
			JSONLStatus: r.JSONLStatus,
			StatusAt:    r.StatusAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StatusAt != out[j].StatusAt {
			return out[i].StatusAt > out[j].StatusAt
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// deriveStatus reduces a session record into a coarse status. JSONL=idle
// is treated as authoritative even over a recent Notification or
// PermissionRequest — once Claude's engine has moved back to idle, the
// user has either resolved or ignored the prompt and we follow the
// engine's self-report. JSONL=busy paired with a user-attention event
// stays "waiting" (engine working but user input still pending).
func deriveStatus(r record) string {
	if r.JSONLStatus == "idle" {
		return "idle"
	}
	switch r.LastEvent {
	case "Notification", "PermissionRequest":
		return "waiting"
	}
	if r.JSONLStatus == "busy" {
		return "active"
	}
	switch r.LastEvent {
	case "SessionStart", "Stop", "StopFailure", "Discovered":
		return "idle"
	default:
		return "active"
	}
}

func isTerminal(event string) bool {
	return event == "SessionEnd"
}
