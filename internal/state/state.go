// Package state persists live sessions as an atomically replaced JSON file.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Session is stored by session id; SessionID and Status are derived on read.
type Session struct {
	SessionID    string `json:"session_id,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Status       string `json:"status,omitempty"`
	FirstSeenAt  string `json:"first_seen_at"`
	LastEvent    string `json:"last_event"`
	LastEventAt  string `json:"last_event_at"`
	TurnID       string `json:"turn_id,omitempty"`
	EngineStatus string `json:"engine_status"` // most recent self-reported engine status ("idle"|"busy") when the agent exposes one
	StatusAt     string `json:"status_at"`     // when derived status last transitioned

	// Parsed timestamps for renderers; omitted from JSON.
	FirstSeenTime time.Time `json:"-"`
	StatusTime    time.Time `json:"-"`
}

type Store struct {
	path     string
	mu       sync.Mutex
	sessions map[string]Session
}

const (
	AgentClaudeCode   = "claude-code"
	AgentCodex        = "codex"
	AgentUnidentified = "unidentified"
)

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
	slog.Debug("state opened", "path", path, "sessions", len(s.sessions))
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

func (s *Store) persist(ctx context.Context) error {
	start := time.Now()
	b, err := json.MarshalIndent(s.sessions, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	slog.LogAttrs(ctx, slog.LevelDebug, "state persisted",
		slog.Int("sessions", len(s.sessions)),
		slog.Int("bytes", len(b)),
		slog.Duration("dur", time.Since(start)),
	)
	return nil
}

// HookEvent is one inbound hook payload, normalized for ingestion by RecordEvent.
type HookEvent struct {
	Agent      string
	SessionID  string
	Event      string
	TurnID     string
	ReceivedAt string
}

// RecordEvent applies one hook event to a live session.
func (s *Store) RecordEvent(ctx context.Context, e HookEvent) (applied bool, err error) {
	if e.SessionID == "" {
		slog.DebugContext(ctx, "RecordEvent: empty session_id, dropping", "agent", e.Agent, "event", e.Event)
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Event == "SessionEnd" {
		if _, ok := s.sessions[e.SessionID]; !ok {
			return false, nil
		}
		delete(s.sessions, e.SessionID)
		if err := s.persist(ctx); err != nil {
			return false, err
		}
		slog.InfoContext(ctx, "session terminated",
			"agent", e.Agent, "session", ShortID(e.SessionID))
		return true, nil
	}
	sess, existed := s.sessions[e.SessionID]
	// if an event comes in during the agent's turn after a stop event,
	// ignore it so that we don't clobber our idle status
	if existed && e.TurnID != "" && e.TurnID == sess.TurnID &&
		(sess.LastEvent == "Stop" || sess.LastEvent == "StopFailure") {
		slog.DebugContext(ctx, "RecordEvent: ignoring late event for concluded turn",
			"session", ShortID(e.SessionID), "event", e.Event,
			"prev_event", sess.LastEvent, "turn", e.TurnID)
		return false, nil
	}
	prevStatus := deriveStatus(sess)
	if !existed {
		sess.Agent = e.Agent
		sess.FirstSeenAt = e.ReceivedAt
		sess.StatusAt = e.ReceivedAt
	}
	sess.LastEvent = e.Event
	sess.LastEventAt = e.ReceivedAt
	if e.TurnID != "" {
		sess.TurnID = e.TurnID
	}
	newStatus := deriveStatus(sess)
	if existed && newStatus != prevStatus {
		sess.StatusAt = e.ReceivedAt
	}
	s.sessions[e.SessionID] = sess
	if err := s.persist(ctx); err != nil {
		return false, err
	}
	if existed {
		slog.DebugContext(ctx, "RecordEvent: applied",
			"agent", sess.Agent, "session", ShortID(e.SessionID), "event", e.Event,
			"prev_status", prevStatus, "new_status", newStatus)
	} else {
		slog.DebugContext(ctx, "RecordEvent: applied (new session)",
			"agent", sess.Agent, "session", ShortID(e.SessionID), "event", e.Event,
			"status", newStatus)
	}
	return true, nil
}

// ApplyDiscovered upserts discovery status in one locked write.
func (s *Store) ApplyDiscovered(ctx context.Context, agent, sessionID, engineStatus string, createdAt time.Time) (inserted, engineChanged, transitioned bool, err error) {
	if sessionID == "" {
		return false, false, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, existed := s.sessions[sessionID]
	if !existed {
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		ts := createdAt.UTC().Format(time.RFC3339Nano)
		sess = Session{
			Agent:       agent,
			FirstSeenAt: ts,
			LastEvent:   "Discovered",
			LastEventAt: ts,
			StatusAt:    ts,
		}
		inserted = true
	}

	prevStatus := deriveStatus(sess)
	if sess.EngineStatus != engineStatus || sess.Agent != agent {
		sess.Agent = agent
		sess.EngineStatus = engineStatus
		engineChanged = true
	}
	newStatus := deriveStatus(sess)
	transitioned = !inserted && engineChanged && newStatus != prevStatus
	if transitioned {
		sess.StatusAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	if !inserted && !engineChanged {
		return false, false, false, nil
	}

	s.sessions[sessionID] = sess
	if perr := s.persist(ctx); perr != nil {
		return inserted, engineChanged, transitioned, perr
	}
	if inserted {
		slog.DebugContext(ctx, "ApplyDiscovered: inserted",
			"agent", agent, "session", ShortID(sessionID),
			"created_at", sess.FirstSeenAt, "engine_status", engineStatus)
	} else {
		slog.DebugContext(ctx, "ApplyDiscovered: engine status applied",
			"agent", agent, "session", ShortID(sessionID),
			"engine_status", engineStatus,
			"prev_status", prevStatus, "new_status", newStatus,
			"transitioned", transitioned)
	}
	return inserted, engineChanged, transitioned, nil
}

// ReconcileDiscovered updates durable metadata without clobbering hook state.
func (s *Store) ReconcileDiscovered(ctx context.Context, agent, sessionID string, createdAt time.Time, insertEvent string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if insertEvent == "" {
		insertEvent = "Discovered"
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		s.sessions[sessionID] = Session{
			Agent:       agent,
			FirstSeenAt: ts,
			LastEvent:   insertEvent,
			LastEventAt: ts,
			StatusAt:    ts,
		}
		if err := s.persist(ctx); err != nil {
			return false, err
		}
		slog.DebugContext(ctx, "ReconcileDiscovered: inserted",
			"agent", agent, "session", ShortID(sessionID),
			"created_at", ts, "event", insertEvent)
		return true, nil
	}

	changed := false
	if sess.Agent != agent {
		sess.Agent = agent
		changed = true
	}
	if sess.FirstSeenAt == "" || ts < sess.FirstSeenAt {
		sess.FirstSeenAt = ts
		changed = true
	}
	if sess.StatusAt == "" {
		sess.StatusAt = sess.LastEventAt
		if sess.StatusAt == "" {
			sess.StatusAt = ts
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	s.sessions[sessionID] = sess
	if err := s.persist(ctx); err != nil {
		return false, err
	}
	slog.DebugContext(ctx, "ReconcileDiscovered: updated",
		"agent", agent, "session", ShortID(sessionID))
	return true, nil
}

// RecordObserved upserts a session from a read-only agent source.
func (s *Store) RecordObserved(ctx context.Context, agent, sessionID string, createdAt time.Time, event string, eventAt time.Time, engineStatus string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
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

	sess, existed := s.sessions[sessionID]
	prevStatus := deriveStatus(sess)
	changed := false
	if !existed {
		sess.FirstSeenAt = createdTS
		sess.StatusAt = eventTS
		changed = true
	}
	if sess.Agent != agent {
		sess.Agent = agent
		changed = true
	}
	if sess.FirstSeenAt == "" || createdTS < sess.FirstSeenAt {
		sess.FirstSeenAt = createdTS
		changed = true
	}
	if event != "" && (sess.LastEvent != event || sess.LastEventAt == "" || eventTS > sess.LastEventAt) {
		sess.LastEvent = event
		sess.LastEventAt = eventTS
		changed = true
	}
	if engineStatus != "" && sess.EngineStatus != engineStatus {
		sess.EngineStatus = engineStatus
		changed = true
	}
	if sess.StatusAt == "" {
		sess.StatusAt = eventTS
		changed = true
	}
	if existed && deriveStatus(sess) != prevStatus {
		sess.StatusAt = eventTS
		changed = true
	}
	if !changed {
		return false, nil
	}
	s.sessions[sessionID] = sess
	if err := s.persist(ctx); err != nil {
		return false, err
	}
	slog.DebugContext(ctx, "RecordObserved: applied",
		"agent", agent, "session", ShortID(sessionID),
		"event", event, "engine_status", engineStatus,
		"new", !existed, "prev_status", prevStatus,
		"new_status", deriveStatus(sess))
	return true, nil
}

// ReapAbsent drops sessions not present in alive.
func (s *Store) ReapAbsent(ctx context.Context, alive map[string]bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id := range s.sessions {
		if !alive[id] {
			delete(s.sessions, id)
			n++
			slog.DebugContext(ctx, "ReapAbsent: dropping",
				"session", ShortID(id))
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.persist(ctx)
}

// ReapAbsentForAgent scopes reaping to one discovery source.
func (s *Store) ReapAbsentForAgent(ctx context.Context, agent string, alive map[string]bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, sess := range s.sessions {
		if sess.Agent != agent {
			continue
		}
		if !alive[id] {
			delete(s.sessions, id)
			n++
			slog.DebugContext(ctx, "ReapAbsentForAgent: dropping",
				"agent", agent, "session", ShortID(id))
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.persist(ctx)
}

// Sessions returns the current state, newest event first.
func (s *Store) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return materialize(s.sessions)
}

// Load reads state for separate read-only processes.
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

// materialize fills derived fields and returns newest status changes first.
func materialize(m map[string]Session) []Session {
	out := make([]Session, 0, len(m))
	for id, s := range m {
		s.SessionID = id
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

// deriveStatus reduces various states to one of {active, waiting, idle}:
//   - LastEvent: the most recent hook event we saw for this session
//   - EngineStatus: the agent's self-reported engine status ("idle"/"busy"), when available
//
// Here we account for some asymmetry in what we want to show versus
// what the agent engine (codex, claude-code, etc.) itself reports for its current state.
func deriveStatus(sess Session) string {
	// 1. User is blocked on a permission prompt, regardless of engine state.
	if sess.LastEvent == "PermissionRequest" {
		return "waiting"
	}
	// 2. Engine signal (when present) is authoritative for idle, overriding
	//    any in-flight hook event below.
	if sess.EngineStatus == "idle" {
		return "idle"
	}
	// 3. User attention requested and engine isn't idle.
	if sess.LastEvent == "Notification" {
		return "waiting"
	}
	// 4. Engine signal authoritative for busy.
	if sess.EngineStatus == "busy" {
		return "active"
	}
	// 5. No engine signal: fall back to the hook event.
	switch sess.LastEvent {
	case "SessionStart", "Stop", "StopFailure", "Discovered":
		return "idle"
	default:
		return "active"
	}
}

// ShortID returns the first 8 chars of a session id, or "?".
func ShortID(id string) string {
	if id == "" {
		return "?"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
