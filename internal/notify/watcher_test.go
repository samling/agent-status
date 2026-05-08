package notify

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/samling/agent-status/internal/state"
)

// fakeNotifier records every Notify call and returns an immediately-closed
// channel so the watcher's per-fire goroutine exits cleanly.
type fakeNotifier struct {
	mu            sync.Mutex
	notifications []Notification
}

func (*fakeNotifier) Name() string { return "fake" }

func (f *fakeNotifier) Notify(_ context.Context, n Notification) (<-chan string, error) {
	f.mu.Lock()
	f.notifications = append(f.notifications, n)
	f.mu.Unlock()
	ch := make(chan string)
	close(ch)
	return ch, nil
}

func (f *fakeNotifier) snapshot() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Notification, len(f.notifications))
	copy(out, f.notifications)
	return out
}

// markWaiting records a session in store with LastEvent="Notification" so
// DeriveStatus returns "waiting".
func markWaiting(t *testing.T, store *state.Store, id, agent string) {
	t.Helper()
	if _, err := store.InsertSession(context.Background(), state.Session{
		SessionID:   id,
		Agent:       agent,
		PID:         1234,
		FirstSeenAt: "2026-05-08T00:00:00Z",
		LastEvent:   "Notification",
		LastEventAt: "2026-05-08T00:00:00Z",
		StatusAt:    "2026-05-08T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
}

// makeWaitingNotIdle moves an existing waiting session out of waiting by
// setting EngineStatus="idle" (DeriveStatus's idle short-circuit).
func makeNotWaiting(t *testing.T, store *state.Store, id string) {
	t.Helper()
	if _, err := store.UpdateSession(context.Background(), id, func(s *state.Session) bool {
		s.EngineStatus = "idle"
		return true
	}); err != nil {
		t.Fatal(err)
	}
}

func reenterWaiting(t *testing.T, store *state.Store, id string) {
	t.Helper()
	if _, err := store.UpdateSession(context.Background(), id, func(s *state.Session) bool {
		s.EngineStatus = ""
		s.LastEvent = "Notification"
		return true
	}); err != nil {
		t.Fatal(err)
	}
}

func newTestWatcher(t *testing.T, cfg Config, store *state.Store) (*Watcher, *fakeNotifier) {
	t.Helper()
	if cfg.TitleTemplate == "" {
		cfg.TitleTemplate = "agent-status"
	}
	if cfg.BodyTemplate == "" {
		cfg.BodyTemplate = "{{.Session.Agent}} session waiting for input"
	}
	fake := &fakeNotifier{}
	w, err := newWatcherWithBackend(cfg, store, fake)
	if err != nil {
		t.Fatal(err)
	}
	return w, fake
}

func TestTickFiresInitialAfterDelay(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	markWaiting(t, store, "session-a", state.AgentClaudeCode)

	w, fake := newTestWatcher(t, Config{
		InitialDelay:   5 * time.Second,
		RepeatInterval: 0,
	}, store)

	ctx := context.Background()
	timers := map[string]*sessionTimer{}
	t0 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)

	// First tick: arms the initial timer; no fire yet.
	w.tick(ctx, t0, timers)
	if got := len(fake.snapshot()); got != 0 {
		t.Fatalf("expected no fire on entering waiting, got %d", got)
	}
	if _, ok := timers["session-a"]; !ok {
		t.Fatalf("expected timer for session-a, got %v", timers)
	}

	// Tick before delay expires: still no fire.
	w.tick(ctx, t0.Add(3*time.Second), timers)
	if got := len(fake.snapshot()); got != 0 {
		t.Fatalf("expected no fire before InitialDelay elapses, got %d", got)
	}

	// Tick after delay expires: fires once.
	w.tick(ctx, t0.Add(6*time.Second), timers)
	notes := fake.snapshot()
	if len(notes) != 1 {
		t.Fatalf("expected 1 fire after InitialDelay elapses, got %d", len(notes))
	}
	if want := "claude-code session waiting for input"; notes[0].Body != want {
		t.Fatalf("body = %q, want %q", notes[0].Body, want)
	}
}

func TestTickRepeatsWhileWaiting(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	markWaiting(t, store, "session-a", state.AgentCodex)

	w, fake := newTestWatcher(t, Config{
		InitialDelay:   1 * time.Second,
		RepeatInterval: 10 * time.Second,
	}, store)

	ctx := context.Background()
	timers := map[string]*sessionTimer{}
	t0 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)

	w.tick(ctx, t0, timers)                       // arm
	w.tick(ctx, t0.Add(2*time.Second), timers)    // initial fire
	w.tick(ctx, t0.Add(5*time.Second), timers)    // before repeat
	w.tick(ctx, t0.Add(13*time.Second), timers)   // first repeat (12s + initial 2s = 14s window)
	w.tick(ctx, t0.Add(23*time.Second), timers)   // second repeat

	if got := len(fake.snapshot()); got != 3 {
		t.Fatalf("expected 3 fires (initial + 2 repeats), got %d", got)
	}
}

func TestTickStopsWhenSessionLeavesWaiting(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	markWaiting(t, store, "session-a", state.AgentClaudeCode)

	w, fake := newTestWatcher(t, Config{
		InitialDelay:   1 * time.Second,
		RepeatInterval: 10 * time.Second,
	}, store)

	ctx := context.Background()
	timers := map[string]*sessionTimer{}
	t0 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)

	w.tick(ctx, t0, timers)                    // arm
	w.tick(ctx, t0.Add(2*time.Second), timers) // initial fire (1)

	makeNotWaiting(t, store, "session-a")
	w.tick(ctx, t0.Add(13*time.Second), timers) // session no longer waiting

	if _, exists := timers["session-a"]; exists {
		t.Fatalf("expected timer to be dropped when session left waiting")
	}
	if got := len(fake.snapshot()); got != 1 {
		t.Fatalf("expected only the 1 initial fire, got %d", got)
	}
}

func TestTickReentryGetsFreshInitialDelay(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	markWaiting(t, store, "session-a", state.AgentClaudeCode)

	w, fake := newTestWatcher(t, Config{
		InitialDelay:   5 * time.Second,
		RepeatInterval: 0,
	}, store)

	ctx := context.Background()
	timers := map[string]*sessionTimer{}
	t0 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)

	w.tick(ctx, t0, timers)                       // arm
	w.tick(ctx, t0.Add(6*time.Second), timers)    // initial fire
	makeNotWaiting(t, store, "session-a")
	w.tick(ctx, t0.Add(7*time.Second), timers)    // schedule cleared

	reenterWaiting(t, store, "session-a")
	w.tick(ctx, t0.Add(8*time.Second), timers)    // re-armed at 8s, fires at 13s
	w.tick(ctx, t0.Add(10*time.Second), timers)   // before re-armed delay
	if got := len(fake.snapshot()); got != 1 {
		t.Fatalf("expected only the original fire, got %d before re-armed delay elapsed", got)
	}
	w.tick(ctx, t0.Add(14*time.Second), timers)   // after re-armed delay
	if got := len(fake.snapshot()); got != 2 {
		t.Fatalf("expected 2 fires (one per waiting episode), got %d", got)
	}
}

func TestTickFiresOncePerSession(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	markWaiting(t, store, "session-a", state.AgentClaudeCode)
	markWaiting(t, store, "session-b", state.AgentCodex)

	w, fake := newTestWatcher(t, Config{
		InitialDelay:   1 * time.Second,
		RepeatInterval: 0,
	}, store)

	ctx := context.Background()
	timers := map[string]*sessionTimer{}
	t0 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)

	w.tick(ctx, t0, timers)                    // arm both
	w.tick(ctx, t0.Add(2*time.Second), timers) // both fire

	notes := fake.snapshot()
	if len(notes) != 2 {
		t.Fatalf("expected 2 fires (one per session), got %d", len(notes))
	}
	bodies := map[string]bool{notes[0].Body: true, notes[1].Body: true}
	if !bodies["claude-code session waiting for input"] || !bodies["codex session waiting for input"] {
		t.Fatalf("expected one fire per session, got %v", bodies)
	}
}

func TestTickFireAttachesActionWhenActivationConfigured(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	markWaiting(t, store, "session-a", state.AgentClaudeCode)

	var clicked []string
	cfg := Config{
		InitialDelay:   1 * time.Second,
		RepeatInterval: 0,
		Activation: &Activation{
			Label: "Focus",
			OnActivate: func(_ context.Context, id string) {
				clicked = append(clicked, id)
			},
		},
	}
	w, fake := newTestWatcher(t, cfg, store)

	ctx := context.Background()
	timers := map[string]*sessionTimer{}
	t0 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)

	w.tick(ctx, t0, timers)
	w.tick(ctx, t0.Add(2*time.Second), timers)

	notes := fake.snapshot()
	if len(notes) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(notes))
	}
	if len(notes[0].Actions) != 1 || notes[0].Actions[0].Label != "Focus" {
		t.Fatalf("expected one Focus action, got %+v", notes[0].Actions)
	}
	// fakeNotifier closes the channel synchronously, so the activation goroutine
	// exits immediately and never sees a click. clicked stays empty here; the
	// presence of the Action on the notification is the dispatch contract.
	_ = clicked
}
