package discovery

import (
	"context"
	"log/slog"
	"time"

	"github.com/samling/agent-status/internal/state"
)

// SessionMeta is live metadata from agent-owned files.
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

// liveSource is one agent discovery backend.
type liveSource struct {
	agent string
	scan  func() ([]liveAgentSession, int, error)
	watch func(ctx context.Context, s *state.Store) error
}

func liveSources() []liveSource {
	return []liveSource{
		{agent: state.AgentClaudeCode, scan: scanClaudeLive, watch: watchClaudeFiles},
		{agent: state.AgentCodex, scan: scanCodexLive},
	}
}

// LiveSessionMeta returns live metadata keyed by session id.
func LiveSessionMeta() (map[string]SessionMeta, error) {
	out := map[string]SessionMeta{}
	type result struct {
		sessions []liveAgentSession
		err      error
	}
	sources := liveSources()
	ch := make(chan result, len(sources))
	for _, src := range sources {
		go func(src liveSource) {
			sessions, _, err := src.scan()
			ch <- result{sessions: sessions, err: err}
		}(src)
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

// Reap drops state rows no longer backed by any live source.
func Reap(ctx context.Context, s *state.Store) (int, error) {
	type result struct {
		agent    string
		sessions []liveAgentSession
		err      error
	}
	sources := liveSources()
	ch := make(chan result, len(sources))
	for _, src := range sources {
		go func(src liveSource) {
			sessions, _, err := src.scan()
			ch <- result{agent: src.agent, sessions: sessions, err: err}
		}(src)
	}
	total := 0
	var firstErr error
	for range sources {
		res := <-ch
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			slog.WarnContext(ctx, "discovery.Reap: source scan failed",
				"agent", res.agent, "err", res.err)
			continue
		}
		set := make(map[string]bool, len(res.sessions))
		for _, sess := range res.sessions {
			set[sess.SessionID] = true
		}
		n, err := s.ReapAbsentForAgent(ctx, res.agent, set)
		total += n
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if n > 0 {
			slog.InfoContext(ctx, "discovery.Reap: dropped stale sessions",
				"agent", res.agent, "n", n, "alive", len(res.sessions))
		}
	}
	return total, firstErr
}
