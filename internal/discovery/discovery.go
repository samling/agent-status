package discovery

import (
	"time"

	"github.com/samling/agent-status/internal/state"
)

// SessionMeta is the live per-session metadata discovered from an agent's
// local state files.
type SessionMeta struct {
	Agent      string
	PID        int
	Entrypoint string
	Cwd        string
	Version    string
	Model      string
	Path       string
	UpdatedAt  time.Time
}

type liveAgentSession struct {
	Agent        string
	SessionID    string
	StartedAt    time.Time
	Event        string
	EventAt      time.Time
	EngineStatus string
	Meta         SessionMeta
}

type liveSource struct {
	agent string
	scan  func() ([]liveAgentSession, int, error)
}

func liveSources() []liveSource {
	return []liveSource{
		{agent: state.AgentClaudeCode, scan: scanClaudeLive},
		{agent: state.AgentCodex, scan: scanCodexLive},
	}
}

// LiveSessionMeta returns a map of session_id -> SessionMeta for sessions
// currently alive on disk. Read-only; used by the UI to enrich rows with
// fields that are not part of persisted state.
func LiveSessionMeta() (map[string]SessionMeta, error) {
	out := map[string]SessionMeta{}
	type result struct {
		sessions []liveAgentSession
		err      error
	}
	sources := liveSources()
	ch := make(chan result, len(sources))
	for _, src := range sources {
		src := src
		go func() {
			sessions, _, err := src.scan()
			ch <- result{sessions: sessions, err: err}
		}()
	}
	var firstErr error
	for range sources {
		res := <-ch
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}
		for _, sess := range res.sessions {
			out[sess.SessionID] = sess.Meta
		}
	}
	return out, firstErr
}

// Reap removes any state entry whose session_id is no longer backed by a
// live session file. Returns the count removed.
func Reap(s *state.Store) (int, error) {
	type result struct {
		agent    string
		sessions []liveAgentSession
		err      error
	}
	sources := liveSources()
	ch := make(chan result, len(sources))
	for _, src := range sources {
		src := src
		go func() {
			sessions, _, err := src.scan()
			ch <- result{agent: src.agent, sessions: sessions, err: err}
		}()
	}
	total := 0
	var firstErr error
	for range sources {
		res := <-ch
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		set := make(map[string]bool, len(res.sessions))
		for _, sess := range res.sessions {
			set[sess.SessionID] = true
		}
		n, err := s.ReapAbsentForAgent(res.agent, set)
		total += n
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return total, firstErr
}
