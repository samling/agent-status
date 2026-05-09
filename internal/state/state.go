// Package state persists live sessions as an atomically replaced JSON file.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Session is stored by session id. SessionID and the parsed time fields are
// filled in on read. The displayed status is computed via DeriveStatus and
// never stored on the type.
type Session struct {
	SessionID    string `json:"session_id,omitempty"`
	Agent        string `json:"agent,omitempty"`
	PID          int    `json:"pid,omitempty"` // OS PID of the agent process, populated by discovery; 0 when unknown
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
	path string
	// mu protects sessions; writeMu serializes file writes so callers can
	// release mu (and unblock readers) while a persist is in flight.
	mu       sync.Mutex
	writeMu  sync.Mutex
	sessions map[string]Session
	// dirty signals the debounced flusher in Run that there is unwritten
	// state. Buffered cap 1; markDirty coalesces concurrent writes.
	dirty chan struct{}
}

const (
	AgentClaudeCode   = "claude-code"
	AgentCodex        = "codex"
	AgentUnidentified = "unidentified"
)

// Event names normalized into LastEvent. The first group is load-bearing —
// DeriveStatus and RecordEvent treat these specifically. The second group is
// passed through from hook payloads and surfaced unchanged.
const (
	EventSessionStart      = "SessionStart"
	EventSessionEnd        = "SessionEnd"
	EventStop              = "Stop"
	EventStopFailure       = "StopFailure"
	EventPermissionRequest = "PermissionRequest"
	EventNotification      = "Notification"
	EventDiscovered        = "Discovered"

	EventUserPromptSubmit = "UserPromptSubmit"
	EventPreToolUse       = "PreToolUse"
	EventPostToolUse      = "PostToolUse"
)

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{
		path:     path,
		sessions: map[string]Session{},
		dirty:    make(chan struct{}, 1),
	}
	if err := s.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	slog.Debug("state opened", "path", path, "sessions", len(s.sessions))
	return s, nil
}

// Run drives the debounced state-file flusher until ctx is cancelled. It
// must be called once per store (typically by the daemon); without it, no
// mutation is ever written to disk. On ctx.Done it performs a final flush so
// in-memory updates aren't lost on shutdown.
//
// Tests that exercise in-memory state only do not need to call Run.
func (s *Store) Run(ctx context.Context) {
	const debounce = 250 * time.Millisecond

	flush := func() {
		s.mu.Lock()
		snap := s.cloneSessionsLocked()
		s.mu.Unlock()
		if err := s.persist(ctx, snap); err != nil {
			slog.WarnContext(ctx, "state: flush failed", "err", err)
		}
	}

	var (
		timer   *time.Timer
		timerC  <-chan time.Time
		pending bool
	)
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			// Final flush captures any signal that arrived after the last
			// timer fire, plus any in-memory mutation that hadn't yet been
			// flushed.
			flush()
			return
		case <-s.dirty:
			if !pending {
				pending = true
				timer = time.NewTimer(debounce)
				timerC = timer.C
			}
		case <-timerC:
			timer = nil
			timerC = nil
			pending = false
			flush()
		}
	}
}

// markDirty signals the flusher that state has changed. Non-blocking; if a
// flush is already pending, the new signal coalesces with it.
func (s *Store) markDirty() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
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
		corruptPath := s.path + ".corrupt-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if renameErr := os.Rename(s.path, corruptPath); renameErr != nil {
			return renameErr
		}
		slog.Warn("state: corrupt file quarantined",
			"path", s.path,
			"corrupt_path", corruptPath,
			"err", err)
		return nil
	}
	if m != nil {
		s.sessions = m
	}
	return nil
}

// cloneSessionsLocked returns a shallow copy of the sessions map. Caller must
// hold s.mu.
func (s *Store) cloneSessionsLocked() map[string]Session {
	out := make(map[string]Session, len(s.sessions))
	maps.Copy(out, s.sessions)
	return out
}

// persist marshals snapshot and atomically replaces the state file. It does
// not touch s.mu, so readers (Sessions, GetSession) aren't blocked on
// filesystem I/O. writeMu serializes concurrent persists so they don't race
// on the tmp-file rename.
func (s *Store) persist(ctx context.Context, snapshot map[string]Session) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	start := time.Now()
	b, err := json.MarshalIndent(snapshot, "", "  ")
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
		slog.Int("sessions", len(snapshot)),
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
	if e.Event == EventSessionEnd {
		if _, ok := s.sessions[e.SessionID]; !ok {
			s.mu.Unlock()
			return false, nil
		}
		delete(s.sessions, e.SessionID)
		s.mu.Unlock()
		s.markDirty()
		slog.InfoContext(ctx, "session terminated",
			"agent", e.Agent, "session", ShortID(e.SessionID))
		return true, nil
	}
	sess, existed := s.sessions[e.SessionID]
	// if an event comes in during the agent's turn after a stop event,
	// ignore it so that we don't clobber our idle status
	if existed && e.TurnID != "" && e.TurnID == sess.TurnID &&
		(sess.LastEvent == EventStop || sess.LastEvent == EventStopFailure) {
		s.mu.Unlock()
		slog.DebugContext(ctx, "RecordEvent: ignoring late event for concluded turn",
			"session", ShortID(e.SessionID), "event", e.Event,
			"prev_event", sess.LastEvent, "turn", e.TurnID)
		return false, nil
	}
	prevStatus := DeriveStatus(sess)
	prevAgent := sess.Agent
	if !existed {
		sess.FirstSeenAt = e.ReceivedAt
		sess.StatusAt = e.ReceivedAt
	}
	// More-specific agent labels win. AgentUnidentified is a placeholder used
	// when the hook script doesn't supply an X-Agent header; never let it
	// overwrite a concrete value that another source (discovery, or an earlier
	// hook with a real header) already stamped.
	if e.Agent != AgentUnidentified || sess.Agent == "" {
		sess.Agent = e.Agent
	}
	sess.LastEvent = e.Event
	sess.LastEventAt = e.ReceivedAt
	if e.TurnID != "" {
		sess.TurnID = e.TurnID
	}
	newStatus := DeriveStatus(sess)
	if existed && newStatus != prevStatus {
		sess.StatusAt = e.ReceivedAt
	}
	s.sessions[e.SessionID] = sess
	s.mu.Unlock()
	s.markDirty()
	if existed && prevAgent != sess.Agent {
		slog.InfoContext(ctx, "RecordEvent: agent identified",
			"session", ShortID(e.SessionID),
			"from", prevAgent, "to", sess.Agent,
			"event", e.Event)
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

// InsertSession inserts sess under sess.SessionID on first sight.
//
// Returns (true, nil) when a new row was written, (false, nil) if a row
// already existed (no-op), or an error if persist failed.
func (s *Store) InsertSession(ctx context.Context, sess Session) (bool, error) {
	if sess.SessionID == "" {
		return false, nil
	}
	s.mu.Lock()
	if _, exists := s.sessions[sess.SessionID]; exists {
		s.mu.Unlock()
		return false, nil
	}
	s.sessions[sess.SessionID] = sess
	s.mu.Unlock()
	s.markDirty()
	slog.DebugContext(ctx, "InsertSession: inserted",
		"agent", sess.Agent, "session", ShortID(sess.SessionID),
		"event", sess.LastEvent)
	return true, nil
}

// UpdateSession applies mutate to the session under sessionID, atomically
// under the store lock. mutate may modify the passed *Session in place;
// it returns true to commit the changes, false to discard them.
//
// Returns (true, nil) if a write happened, (false, nil) if the session
// doesn't exist or mutate returned false.
func (s *Store) UpdateSession(ctx context.Context, sessionID string, mutate func(sess *Session) bool) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	s.mu.Lock()
	sess, exists := s.sessions[sessionID]
	if !exists {
		s.mu.Unlock()
		return false, nil
	}
	if !mutate(&sess) {
		s.mu.Unlock()
		return false, nil
	}
	s.sessions[sessionID] = sess
	s.mu.Unlock()
	s.markDirty()
	slog.DebugContext(ctx, "UpdateSession: applied",
		"agent", sess.Agent, "session", ShortID(sessionID))
	return true, nil
}

// ReapAbsentForAgent scopes reaping to one discovery source.
func (s *Store) ReapAbsentForAgent(ctx context.Context, agent string, alive map[string]bool) (int, error) {
	s.mu.Lock()
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
		s.mu.Unlock()
		return 0, nil
	}
	s.mu.Unlock()
	s.markDirty()
	return n, nil
}

// Sessions returns the current state, newest event first.
func (s *Store) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedSessions(s.sessions)
}

// GetSession returns the session for id and whether it was found. The returned
// Session has SessionID and parsed time fields filled in, matching what
// Sessions() yields.
func (s *Store) GetSession(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, false
	}
	sess.SessionID = id
	sess.FirstSeenTime, _ = time.Parse(time.RFC3339Nano, sess.FirstSeenAt)
	sess.StatusTime, _ = time.Parse(time.RFC3339Nano, sess.StatusAt)
	return sess, true
}

// sortedSessions fills derived fields and returns newest status changes first.
func sortedSessions(m map[string]Session) []Session {
	out := make([]Session, 0, len(m))
	for id, s := range m {
		s.SessionID = id
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

// DeriveStatus reduces various states to one of {active, waiting, idle}:
//   - LastEvent: the most recent hook event we saw for this session
//   - EngineStatus: the agent's self-reported engine status ("idle"/"busy"), when available
//
// Here we account for some asymmetry in what we want to show versus
// what the agent engine (codex, claude-code, etc.) itself reports for its current state.
func DeriveStatus(sess Session) string {
	// 1. User is blocked on a permission prompt, regardless of engine state.
	if sess.LastEvent == EventPermissionRequest {
		return "waiting"
	}
	// 2. Engine signal (when present) is authoritative for idle, overriding
	//    any in-flight hook event below.
	if sess.EngineStatus == "idle" {
		return "idle"
	}
	// 3. User attention requested and engine isn't idle.
	if sess.LastEvent == EventNotification {
		return "waiting"
	}
	// 4. Engine signal authoritative for busy.
	if sess.EngineStatus == "busy" {
		return "active"
	}
	// 5. No engine signal: fall back to the hook event.
	switch sess.LastEvent {
	case EventSessionStart, EventStop, EventStopFailure, EventDiscovered:
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
