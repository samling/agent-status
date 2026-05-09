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

// sources is the registry of live agent backends. Treat as read-only after
// init; the slice and its function fields are shared by every caller.
var sources = []liveSource{
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
		watch:      codex.Watch,
		apply:      codex.Apply,
		transcript: codex.Transcript,
	},
}

// LoadTranscript dispatches transcript loading to the registered backend for
// the given agent.
func LoadTranscript(sessionID, agent string, meta source.SessionMeta) (source.TranscriptInfo, error) {
	for _, src := range sources {
		if src.agent == agent {
			return src.transcript(sessionID, meta)
		}
	}
	return source.TranscriptInfo{}, nil
}
