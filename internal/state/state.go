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
	"strings"
	"sync"
	"time"
)

// Session is the unit of state, both on disk (as values in the
// per-session map keyed by session id) and at the API surface.
// SessionID and Status are derived: SessionID is filled from the map
// key, and Status is computed by deriveStatus, both inside materialize.
// They're tagged omitempty and the mutators never populate them on the
// in-memory map values, so persist() omits them — derived state stays
// out of the file. Consumer output (server /state, `state --json`) goes
// through materialize, which sets them, so the wire shape is unchanged.
type Session struct {
	SessionID   string `json:"session_id,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Status      string `json:"status,omitempty"`
	FirstSeenAt string `json:"first_seen_at"`
	LastEvent   string `json:"last_event"`
	LastEventAt string `json:"last_event_at"`
	TurnID      string `json:"turn_id,omitempty"`
	JSONLStatus string `json:"jsonl_status"` // last observed engine status ("idle"|"busy") when the agent exposes one
	StatusAt    string `json:"status_at"`    // when derived status last transitioned

	// Parsed copies of FirstSeenAt and StatusAt, set by materialize so
	// renderers don't re-parse RFC3339Nano strings on every frame.
	// json:"-" keeps them off both the wire and the disk.
	FirstSeenTime time.Time `json:"-"`
	StatusTime    time.Time `json:"-"`
}

type Store struct {
	path     string
	mu       sync.Mutex
	sessions map[string]Session
}

const (
	AgentClaudeCode = "claude-code"
	AgentCodex      = "codex"
)

func NormalizeAgent(agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return AgentClaudeCode
	}
	return agent
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{path: path, sessions: map[string]Session{}}
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
	var m map[string]Session
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
func (s *Store) RecordEvent(agent, sessionID, event, turnID, receivedAt string) error {
	if sessionID == "" {
		return nil
	}
	agent = NormalizeAgent(agent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if isTerminal(event) {
		r, ok := s.sessions[sessionID]
		if !ok {
			return nil
		}
		if NormalizeAgent(r.Agent) != agent {
			return nil
		}
		delete(s.sessions, sessionID)
		return s.persist()
	}
	r, existed := s.sessions[sessionID]
	if existed && shouldIgnoreHookEvent(r, event, turnID) {
		return nil
	}
	prevStatus := deriveStatus(r)
	if !existed {
		r.FirstSeenAt = receivedAt
		r.StatusAt = receivedAt
	}
	r.Agent = agent
	r.LastEvent = event
	r.LastEventAt = receivedAt
	if turnID != "" {
		r.TurnID = turnID
	}
	if existed && deriveStatus(r) != prevStatus {
		r.StatusAt = receivedAt
	}
	s.sessions[sessionID] = r
	return s.persist()
}

// SetJSONLStatus records the latest "status" field read from a session's
// JSONL file. Empty string means "no info" and is treated as such by
// deriveStatus. Returns true if the entry actually changed.
func (s *Store) SetJSONLStatus(agent, sessionID, jsonlStatus string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	agent = NormalizeAgent(agent)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.sessions[sessionID]
	if !ok {
		// We don't know about this session yet. Wait for discovery or a
		// hook to register it before tracking JSONL status.
		return false, nil
	}
	if r.JSONLStatus == jsonlStatus && NormalizeAgent(r.Agent) == agent {
		return false, nil
	}
	prevStatus := deriveStatus(r)
	r.Agent = agent
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
func (s *Store) MarkDiscovered(agent, sessionID string, createdAt time.Time) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	agent = NormalizeAgent(agent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; ok {
		return false, nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)
	s.sessions[sessionID] = Session{
		Agent:       agent,
		FirstSeenAt: ts,
		LastEvent:   "Discovered",
		LastEventAt: ts,
		StatusAt:    ts,
	}
	return true, s.persist()
}

// ReconcileDiscovered registers a session when absent and reconciles
// durable identity metadata when present. It intentionally does not
// update LastEvent or JSONLStatus, so database polling cannot clobber
// hook-derived active/idle state.
func (s *Store) ReconcileDiscovered(agent, sessionID string, createdAt time.Time) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	agent = NormalizeAgent(agent)
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)

	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.sessions[sessionID]
	if !ok {
		s.sessions[sessionID] = Session{
			Agent:       agent,
			FirstSeenAt: ts,
			LastEvent:   "Discovered",
			LastEventAt: ts,
			StatusAt:    ts,
		}
		return true, s.persist()
	}

	changed := false
	if NormalizeAgent(r.Agent) != agent {
		r.Agent = agent
		changed = true
	}
	if r.FirstSeenAt == "" || ts < r.FirstSeenAt {
		r.FirstSeenAt = ts
		changed = true
	}
	if r.StatusAt == "" {
		r.StatusAt = r.LastEventAt
		if r.StatusAt == "" {
			r.StatusAt = ts
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	s.sessions[sessionID] = r
	return true, s.persist()
}

// RecordObserved upserts a session from a read-only agent state source,
// preserving the original creation time and using the source's own event
// timestamp when it is available.
func (s *Store) RecordObserved(agent, sessionID string, createdAt time.Time, event string, eventAt time.Time, engineStatus string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	agent = NormalizeAgent(agent)
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	if eventAt.IsZero() {
		eventAt = now
	}
	createdTS := createdAt.UTC().Format(time.RFC3339Nano)
	eventTS := eventAt.UTC().Format(time.RFC3339Nano)

	s.mu.Lock()
	defer s.mu.Unlock()

	r, existed := s.sessions[sessionID]
	prevStatus := deriveStatus(r)
	changed := false
	if !existed {
		r.FirstSeenAt = createdTS
		r.StatusAt = eventTS
		changed = true
	}
	if NormalizeAgent(r.Agent) != agent {
		r.Agent = agent
		changed = true
	}
	if r.FirstSeenAt == "" || createdTS < r.FirstSeenAt {
		r.FirstSeenAt = createdTS
		changed = true
	}
	if event != "" && (r.LastEvent != event || r.LastEventAt == "" || eventTS > r.LastEventAt) {
		r.LastEvent = event
		r.LastEventAt = eventTS
		changed = true
	}
	if engineStatus != "" && r.JSONLStatus != engineStatus {
		r.JSONLStatus = engineStatus
		changed = true
	}
	if r.StatusAt == "" {
		r.StatusAt = eventTS
		changed = true
	}
	if existed && deriveStatus(r) != prevStatus {
		r.StatusAt = eventTS
		changed = true
	}
	if !changed {
		return false, nil
	}
	s.sessions[sessionID] = r
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

// ReapAbsentForAgent is the provider-scoped variant of ReapAbsent. It
// lets discovery sources fail independently without one missing scan
// deleting another agent's rows.
func (s *Store) ReapAbsentForAgent(agent string, alive map[string]bool) (int, error) {
	agent = NormalizeAgent(agent)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, r := range s.sessions {
		if NormalizeAgent(r.Agent) != agent {
			continue
		}
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
	var m map[string]Session
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return materialize(m), nil
}

// materialize fills the derived fields on each entry (SessionID,
// Status, parsed timestamps) and returns a sorted slice for consumers.
func materialize(m map[string]Session) []Session {
	out := make([]Session, 0, len(m))
	for id, s := range m {
		s.SessionID = id
		s.Agent = NormalizeAgent(s.Agent)
		s.Status = deriveStatus(s)
		s.FirstSeenTime, _ = time.Parse(time.RFC3339Nano, s.FirstSeenAt)
		s.StatusTime, _ = time.Parse(time.RFC3339Nano, s.StatusAt)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StatusAt != out[j].StatusAt {
			return out[i].StatusAt > out[j].StatusAt
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// deriveStatus reduces a session entry into a coarse status. JSONL=idle
// is treated as authoritative even over a recent Notification or
// PermissionRequest — once Claude's engine has moved back to idle, the
// user has either resolved or ignored the prompt and we follow the
// engine's self-report. JSONL=busy paired with a user-attention event
// stays "waiting" (engine working but user input still pending).
func deriveStatus(r Session) string {
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
	// Case-insensitive: Claude Code's hook payloads have varied
	// between "SessionEnd" and lowercase variants in past versions,
	// and we'd rather drop the entry than keep it around as a
	// stale "active" row when the casing doesn't line up.
	return strings.EqualFold(event, "SessionEnd")
}

func shouldIgnoreHookEvent(r Session, event, turnID string) bool {
	if turnID == "" || r.TurnID == "" || turnID != r.TurnID {
		return false
	}
	if !isTurnIdleEvent(r.LastEvent) {
		return false
	}
	return true
}

func isTurnIdleEvent(event string) bool {
	return event == "Stop" || event == "StopFailure"
}

// ShortID returns a display-friendly truncation of a session id (first 8
// chars), or "?" when empty. Shared so logs and UI render ids identically.
func ShortID(id string) string {
	if id == "" {
		return "?"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
