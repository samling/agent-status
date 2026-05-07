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

// Session is stored by session id. SessionID and the parsed time fields are
// filled in on read. The displayed status is computed via DeriveStatus and
// never stored on the type.
type Session struct {
	SessionID    string `json:"session_id,omitempty"`
	Agent        string `json:"agent,omitempty"`
	FirstSeenAt  string `json:"first_seen_at"`
	LastEvent    string `json:"last_event"`
	LastEventAt  string `json:"last_event_at"`
	TurnID       string `json:"turn_id,omitempty"`
	EngineStatus string `json:"engine_status"` // most recent self-reported engine status ("idle"|"busy") when the agent exposes one
	StatusAt     string `json:"status_at"`     // when derived status last transitioned

	// TraceID/RootSpanID anchor a long-lived OTel trace per session. They are
	// allocated lazily on first sight (hook or discovery), persisted, and
	// reused as the parent context for every subsequent span concerning this
	// session. Stored as W3C-format hex strings (32 / 16 chars).
	TraceID    string `json:"trace_id,omitempty"`
	RootSpanID string `json:"root_span_id,omitempty"`

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
	prevStatus := DeriveStatus(sess)
	prevAgent := sess.Agent
	// First sight, OR upgrading the bare trace placeholder created by
	// EnsureTrace (which sets only SessionID / TraceID / RootSpanID / Agent
	// and leaves FirstSeenAt empty).
	if !existed || sess.FirstSeenAt == "" {
		sess.FirstSeenAt = e.ReceivedAt
	}
	if !existed || sess.StatusAt == "" {
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
	if err := s.persist(ctx); err != nil {
		return false, err
	}
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

// InsertSession inserts sess under sess.SessionID on first sight. A row
// holding only the trace placeholder fields (no LastEvent yet) counts as
// "not yet inserted" and gets promoted with the input data; any persisted
// TraceID/RootSpanID are preserved so the session's long-lived trace is
// unbroken.
//
// Returns (true, nil) when the row went from absent/placeholder to populated,
// (false, nil) if a fully-populated row already existed (no-op), or an error
// if persist failed.
func (s *Store) InsertSession(ctx context.Context, sess Session) (bool, error) {
	if sess.SessionID == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.sessions[sess.SessionID]
	if exists && existing.LastEvent != "" {
		return false, nil
	}
	if existing.TraceID != "" {
		sess.TraceID = existing.TraceID
	}
	if existing.RootSpanID != "" {
		sess.RootSpanID = existing.RootSpanID
	}
	s.sessions[sess.SessionID] = sess
	if err := s.persist(ctx); err != nil {
		return false, err
	}
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
	defer s.mu.Unlock()
	sess, exists := s.sessions[sessionID]
	if !exists {
		return false, nil
	}
	if !mutate(&sess) {
		return false, nil
	}
	s.sessions[sessionID] = sess
	if err := s.persist(ctx); err != nil {
		return false, err
	}
	slog.DebugContext(ctx, "UpdateSession: applied",
		"agent", sess.Agent, "session", ShortID(sessionID))
	return true, nil
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

// TraceMinter returns a fresh (TraceID, RootSpanID) hex pair. Implementations
// are expected to back the returned IDs with a real exported root span so
// downstream parent references resolve in trace UIs (otherwise Jaeger / Tempo
// warn about invalid parent span IDs).
type TraceMinter func() (traceIDHex, rootSpanIDHex string)

// EnsureTrace returns the persisted W3C trace IDs for sessionID, calling
// mint on first sight to allocate them and persisting the result before any
// concurrent caller can race in with its own mint. agent is stamped into a
// brand-new row so the sparse pre-event placeholder is still reapable
// per-agent.
//
// If mint is nil, or returns empty strings (e.g. NoOp tracer), the row is
// re-checked on every call and never persists trace IDs. That keeps Jaeger
// quiet when tracing is disabled and lets a later run with tracing enabled
// mint a real root.
//
// On success returned hex strings are suitable for trace.TraceIDFromHex /
// trace.SpanIDFromHex.
func (s *Store) EnsureTrace(ctx context.Context, sessionID, agent string, mint TraceMinter) (traceID, rootSpanID string, err error) {
	if sessionID == "" {
		return "", "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, existed := s.sessions[sessionID]
	if !existed {
		sess.SessionID = sessionID
	}
	if sess.TraceID != "" && sess.RootSpanID != "" {
		// Already minted on a previous EnsureTrace; just refine Agent if
		// this is the placeholder row's first sighting.
		var changed bool
		if !existed && sess.Agent == "" && agent != "" {
			sess.Agent = agent
			changed = true
		}
		if changed {
			s.sessions[sessionID] = sess
			if err := s.persist(ctx); err != nil {
				return "", "", err
			}
		}
		return sess.TraceID, sess.RootSpanID, nil
	}

	var newTrace, newSpan string
	if mint != nil {
		newTrace, newSpan = mint()
	}
	if newTrace == "" || newSpan == "" {
		// Tracing disabled (NoOp tracer) or mint refused. Don't persist
		// placeholder IDs; the row will be re-checked next call and a real
		// root will be minted as soon as tracing is configured. Still stamp
		// the agent so the sparse row is reapable.
		var changed bool
		if !existed && sess.Agent == "" && agent != "" {
			sess.Agent = agent
			changed = true
		}
		if changed {
			s.sessions[sessionID] = sess
			if err := s.persist(ctx); err != nil {
				return "", "", err
			}
		}
		return "", "", nil
	}

	sess.TraceID = newTrace
	sess.RootSpanID = newSpan
	if !existed && sess.Agent == "" && agent != "" {
		sess.Agent = agent
	}
	s.sessions[sessionID] = sess
	if err := s.persist(ctx); err != nil {
		return "", "", err
	}
	return sess.TraceID, sess.RootSpanID, nil
}

// Sessions returns the current state, newest event first.
func (s *Store) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedSessions(s.sessions)
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
	return sortedSessions(m), nil
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
