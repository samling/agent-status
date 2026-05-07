package discovery

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/state"
)

// Watch polls every discovery source until ctx is cancelled.
func Watch(ctx context.Context, s *state.Store) error {
	sources := liveSources()

	for _, src := range sources {
		if src.watch == nil {
			continue
		}
		go func(src liveSource) {
			if err := src.watch(ctx, s); err != nil {
				slog.ErrorContext(ctx, "discovery: source watcher exited",
					"agent", src.agent, "err", err)
			}
		}(src)
	}

	// Populate state before hook traffic arrives.
	if scanned, alive, updated, err := syncDiscovered(ctx, s, "initial"); err != nil {
		slog.ErrorContext(ctx, "discovery: initial sweep failed", "err", err)
	} else {
		slog.InfoContext(ctx, "discovery: initial sweep",
			"scanned", scanned, "alive", alive, "updated", updated)
	}

	discover := time.NewTicker(2 * time.Second)
	defer discover.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "discovery: watcher stopped")
			return nil
		case <-discover.C:
			if scanned, alive, updated, err := syncDiscovered(ctx, s, "tick"); err != nil {
				slog.WarnContext(ctx, "discovery: poll error", "err", err)
			} else if updated > 0 {
				slog.InfoContext(ctx, "discovery: tick",
					"scanned", scanned, "alive", alive, "updated", updated)
			} else {
				slog.DebugContext(ctx, "discovery: tick (no changes)",
					"scanned", scanned, "alive", alive)
			}
		}
	}
}

func syncDiscovered(ctx context.Context, s *state.Store, reason string) (scanned, alive, updated int, err error) {
	ctx, span := logging.Start(ctx, "discovery.sync",
		attribute.String("reason", reason))
	defer span.End()

	type result struct {
		agent    string
		sessions []liveAgentSession
		scanned  int
		err      error
	}
	sources := liveSources()
	ch := make(chan result, len(sources))
	for _, src := range sources {
		go func(src liveSource) {
			start := time.Now()
			sessions, scanned, err := src.scan()
			if err != nil {
				slog.WarnContext(ctx, "discovery: source scan failed",
					"agent", src.agent, "scanned", scanned, "alive", len(sessions),
					"dur", time.Since(start), "err", err)
			} else {
				slog.DebugContext(ctx, "discovery: source scanned",
					"agent", src.agent, "scanned", scanned, "alive", len(sessions),
					"dur", time.Since(start))
			}
			ch <- result{agent: src.agent, sessions: sessions, scanned: scanned, err: err}
		}(src)
	}
	for range sources {
		res := <-ch
		if res.err != nil {
			if err == nil {
				err = res.err
			}
			continue
		}
		scanned += res.scanned
		alive += len(res.sessions)
		aliveSet := make(map[string]bool, len(res.sessions))
		for _, sess := range res.sessions {
			aliveSet[sess.SessionID] = true
			if applyLiveSession(ctx, s, sess) {
				updated++
			}
		}
		// Reap per-agent after successful scans so one source failure
		// cannot delete another source's rows.
		n, reapErr := s.ReapAbsentForAgent(ctx, res.agent, aliveSet)
		if reapErr != nil {
			slog.WarnContext(ctx, "discovery: inline reap failed",
				"agent", res.agent, "err", reapErr)
			if err == nil {
				err = reapErr
			}
			continue
		}
		if n > 0 {
			slog.InfoContext(ctx, "discovery: reaped stale sessions",
				"agent", res.agent, "n", n, "alive", len(res.sessions))
			updated += n
		}
	}
	span.SetAttributes(
		attribute.Int("scanned", scanned),
		attribute.Int("alive", alive),
		attribute.Int("updated", updated),
	)
	return scanned, alive, updated, err
}

func applyLiveSession(ctx context.Context, s *state.Store, sess liveAgentSession) bool {
	switch sess.Agent {
	case state.AgentClaudeCode:
		return applyClaudeSessionFile(ctx, s, claudeSessionFile{
			PID:        sess.Meta.PID,
			SessionID:  sess.SessionID,
			Entrypoint: sess.Meta.Entrypoint,
			Cwd:        sess.Meta.Cwd,
			Status:     sess.EngineStatus,
			Version:    sess.Meta.Version,
			StartedAt:  unixMilli(sess.StartedAt),
		})
	case state.AgentCodex:
		changed, err := s.ReconcileDiscovered(ctx, sess.Agent, sess.SessionID, sess.StartedAt, sess.Event)
		if err != nil {
			slog.WarnContext(ctx, "discovery: reconcile failed",
				"agent", sess.Agent, "session", state.ShortID(sess.SessionID), "err", err)
		} else if changed {
			slog.InfoContext(ctx, "discovery: session reconciled",
				"agent", sess.Agent, "session", state.ShortID(sess.SessionID),
				"event", sess.Event)
		}
		return changed
	default:
		changed, err := s.RecordObserved(ctx, sess.Agent, sess.SessionID, sess.StartedAt, sess.Event, sess.EventAt, sess.EngineStatus)
		if err != nil {
			slog.WarnContext(ctx, "discovery: record observed failed",
				"agent", sess.Agent, "session", state.ShortID(sess.SessionID), "err", err)
		}
		return changed
	}
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
