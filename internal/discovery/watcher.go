package discovery

import (
	"context"
	"log/slog"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

// Watch polls every discovery source until ctx is cancelled.
func Watch(ctx context.Context, s *state.Store) error {
	sources := liveSources()

	for _, src := range sources {
		if src.watch == nil {
			continue
		}
		// Start filewatchers per source (agent type) for sources that expose one.
		// These react to push-side filesystem events for session creation/deletion;
		// the periodic syncDiscovered tick is the backstop.
		go func(src liveSource) {
			if err := src.watch(ctx, s); err != nil {
				slog.ErrorContext(ctx, "discovery: source watcher exited",
					"agent", src.agent, "err", err)
			}
		}(src)
	}

	// Initial sweep of existing on-disk sessions; watchers (already armed above)
	// handle anything that changes from this point on.
	if scanned, alive, updated, err := syncDiscovered(ctx, s, sources); err != nil {
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
			if scanned, alive, updated, err := syncDiscovered(ctx, s, sources); err != nil {
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

// syncDiscovered fans out scans across every source concurrently, applies each
// returned session through that source's apply func, and reaps any store rows
// not present in the alive set.
//
// No span is opened for the tick itself: each apply that touches a session
// emits its own span parented to that session's persisted trace, which is the
// granularity we want for the trace UI. The tick is logged via slog only.
func syncDiscovered(ctx context.Context, s *state.Store, sources []liveSource) (scanned, alive, updated int, err error) {
	type result struct {
		src      liveSource
		sessions []source.LiveSession
		scanned  int
		err      error
	}
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
			ch <- result{src: src, sessions: sessions, scanned: scanned, err: err}
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
			if res.src.apply(ctx, s, sess) {
				updated++
			}
		}
		// Reap per-agent after successful scans so one source failure
		// cannot delete another source's rows.
		n, reapErr := s.ReapAbsentForAgent(ctx, res.src.agent, aliveSet)
		if reapErr != nil {
			slog.WarnContext(ctx, "discovery: inline reap failed",
				"agent", res.src.agent, "err", reapErr)
			if err == nil {
				err = reapErr
			}
			continue
		}
		if n > 0 {
			slog.InfoContext(ctx, "discovery: reaped stale sessions",
				"agent", res.src.agent, "n", n, "alive", len(res.sessions))
			updated += n
		}
	}
	return scanned, alive, updated, err
}
