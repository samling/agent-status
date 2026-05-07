// Package discovery orchestrates the per-agent discovery backends in the
// claudecode and codex subpackages: it registers each backend, runs an initial
// sweep at startup, and polls them on a 2-second tick.
package discovery

import (
	"context"

	"github.com/samling/agent-status/internal/discovery/claudecode"
	"github.com/samling/agent-status/internal/discovery/codex"
	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

// liveSource is one agent discovery backend.
type liveSource struct {
	agent      string
	scan       func() ([]source.LiveSession, int, error)
	watch      func(ctx context.Context, s *state.Store) error
	apply      func(ctx context.Context, s *state.Store, sess source.LiveSession) bool
	transcript func(sessionID string, meta source.SessionMeta) (source.TranscriptInfo, error)
}

func liveSources() []liveSource {
	return []liveSource{
		{
			agent:      state.AgentClaudeCode,
			scan:       claudecode.Scan,
			watch:      claudecode.Watch,
			apply:      claudecode.Apply,
			transcript: claudecode.Transcript,
		},
		{
			agent:      state.AgentCodex,
			scan:       codex.Scan,
			apply:      codex.Apply,
			transcript: codex.Transcript,
		},
	}
}

// LiveSessionMeta returns the UI-facing metadata for every currently-live
// session, keyed by session id, by fanning out scans across every backend.
func LiveSessionMeta() (map[string]source.SessionMeta, error) {
	out := map[string]source.SessionMeta{}
	type result struct {
		sessions []source.LiveSession
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

// LoadTranscript dispatches transcript loading to the registered backend for
// the given agent.
func LoadTranscript(sessionID, agent string, meta source.SessionMeta) (source.TranscriptInfo, error) {
	for _, src := range liveSources() {
		if src.agent == agent {
			return src.transcript(sessionID, meta)
		}
	}
	return source.TranscriptInfo{}, nil
}
