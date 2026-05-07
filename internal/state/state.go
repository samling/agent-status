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
	"strings"
	"sync"
	"time"
)

// Session is stored by session id; SessionID and Status are derived on read.
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

// RecordEvent applies one hook event to a live session.
func (s *Store) RecordEvent(ctx context.Context, agent, sessionID, event, turnID, receivedAt string) (applied bool, err error) {
	if sessionID == "" {
		slog.DebugContext(ctx, "RecordEvent: empty session_id, dropping", "agent", agent, "event", event)
		return false, nil
	}
	agent = NormalizeAgent(agent)
	event = NormalizeHookEvent(agent, event)
	s.mu.Lock()
	defer s.mu.Unlock()
	if isTerminal(event) {
		r, ok := s.sessions[sessionID]
		if !ok {
			slog.DebugContext(ctx, "RecordEvent: terminal for unknown session",
				"agent", agent, "session", ShortID(sessionID), "event", event)
			return false, nil
		}
		if NormalizeAgent(r.Agent) != agent {
			slog.DebugContext(ctx, "RecordEvent: terminal agent mismatch, ignoring",
				"agent", agent, "stored_agent", r.Agent,
				"session", ShortID(sessionID), "event", event)
			return false, nil
		}
		delete(s.sessions, sessionID)
		slog.InfoContext(ctx, "session terminated",
			"agent", agent, "session", ShortID(sessionID), "event", event)
		if err := s.persist(ctx); err != nil {
			return false, err
		}
		return true, nil
	}
	r, existed := s.sessions[sessionID]
	if existed && shouldIgnoreHookEvent(r, event, turnID) {
		slog.DebugContext(ctx, "RecordEvent: ignoring redundant turn event",
			"session", ShortID(sessionID), "event", event,
			"prev_event", r.LastEvent, "turn", turnID)
		return false, nil
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
	newStatus := deriveStatus(r)
	if existed && newStatus != prevStatus {
		r.StatusAt = receivedAt
	}
	s.sessions[sessionID] = r
	if existed {
		slog.DebugContext(ctx, "RecordEvent: applied",
			"agent", agent, "session", ShortID(sessionID), "event", event,
			"prev_status", prevStatus, "new_status", newStatus)
	} else {
		slog.DebugContext(ctx, "RecordEvent: applied (new session)",
			"agent", agent, "session", ShortID(sessionID), "event", event,
			"status", newStatus)
	}
	if err := s.persist(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func NormalizeHookEvent(agent, event string) string {
	if NormalizeAgent(agent) == AgentCodex && event == "Stop" {
		return "TurnComplete"
	}
	return event
}

// ApplyDiscovered upserts discovery status in one locked write.
func (s *Store) ApplyDiscovered(ctx context.Context, agent, sessionID, jsonlStatus string, createdAt time.Time) (inserted, jsonlChanged, transitioned bool, err error) {
	if sessionID == "" {
		return false, false, false, nil
	}
	agent = NormalizeAgent(agent)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, existed := s.sessions[sessionID]
	if !existed {
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		ts := createdAt.UTC().Format(time.RFC3339Nano)
		r = Session{
			Agent:       agent,
			FirstSeenAt: ts,
			LastEvent:   "Discovered",
			LastEventAt: ts,
			StatusAt:    ts,
		}
		inserted = true
	}

	prevStatus := deriveStatus(r)
	if r.JSONLStatus != jsonlStatus || NormalizeAgent(r.Agent) != agent {
		r.Agent = agent
		r.JSONLStatus = jsonlStatus
		jsonlChanged = true
	}
	newStatus := deriveStatus(r)
	transitioned = !inserted && jsonlChanged && newStatus != prevStatus
	if transitioned {
		r.StatusAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	if !inserted && !jsonlChanged {
		return false, false, false, nil
	}

	s.sessions[sessionID] = r
	if inserted {
		slog.DebugContext(ctx, "ApplyDiscovered: inserted",
			"agent", agent, "session", ShortID(sessionID),
			"created_at", r.FirstSeenAt, "jsonl_status", jsonlStatus)
	} else {
		slog.DebugContext(ctx, "ApplyDiscovered: jsonl applied",
			"agent", agent, "session", ShortID(sessionID),
			"jsonl_status", jsonlStatus,
			"prev_status", prevStatus, "new_status", newStatus,
			"transitioned", transitioned)
	}
	if perr := s.persist(ctx); perr != nil {
		return inserted, jsonlChanged, transitioned, perr
	}
	return inserted, jsonlChanged, transitioned, nil
}

// ReconcileDiscovered updates durable metadata without clobbering hook state.
func (s *Store) ReconcileDiscovered(ctx context.Context, agent, sessionID string, createdAt time.Time, insertEvent string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	agent = NormalizeAgent(agent)
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if insertEvent == "" {
		insertEvent = "Discovered"
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)

	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.sessions[sessionID]
	if !ok {
		s.sessions[sessionID] = Session{
			Agent:       agent,
			FirstSeenAt: ts,
			LastEvent:   insertEvent,
			LastEventAt: ts,
			StatusAt:    ts,
		}
		slog.DebugContext(ctx, "ReconcileDiscovered: inserted",
			"agent", agent, "session", ShortID(sessionID),
			"created_at", ts, "event", insertEvent)
		return true, s.persist(ctx)
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
	slog.DebugContext(ctx, "ReconcileDiscovered: updated",
		"agent", agent, "session", ShortID(sessionID))
	return true, s.persist(ctx)
}

// RecordObserved upserts a session from a read-only agent source.
func (s *Store) RecordObserved(ctx context.Context, agent, sessionID string, createdAt time.Time, event string, eventAt time.Time, engineStatus string) (bool, error) {
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
	slog.DebugContext(ctx, "RecordObserved: applied",
		"agent", agent, "session", ShortID(sessionID),
		"event", event, "engine_status", engineStatus,
		"new", !existed, "prev_status", prevStatus,
		"new_status", deriveStatus(r))
	return true, s.persist(ctx)
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

// deriveStatus reduces hook and engine state to active/waiting/idle.
func deriveStatus(r Session) string {
	// PermissionRequest is idle at the engine level but still user-blocked.
	if r.LastEvent == "PermissionRequest" {
		return "waiting"
	}
	// Engine idle clears earlier user-attention events except permissions.
	if r.JSONLStatus == "idle" {
		return "idle"
	}
	if r.LastEvent == "Notification" {
		return "waiting"
	}
	if r.JSONLStatus == "busy" {
		return "active"
	}
	switch r.LastEvent {
	case "SessionStart", "Stop", "StopFailure", "TurnComplete", "Discovered":
		return "idle"
	default:
		return "active"
	}
}

func isTerminal(event string) bool {
	// Hook casing has varied across Claude Code versions.
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
	return event == "Stop" || event == "StopFailure" || event == "TurnComplete"
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
