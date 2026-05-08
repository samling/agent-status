package notify

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/state"
)

// Config controls notification timing and templates. Notifications fire
// per-session: each waiting session has its own initial+repeat schedule
// and gets its own toast.
type Config struct {
	// InitialDelay is how long a session must remain in the waiting state
	// before its first notification fires.
	InitialDelay time.Duration

	// RepeatInterval is the cadence at which repeats fire while the
	// session stays waiting. Zero disables repeats (one-shot per
	// waiting episode).
	RepeatInterval time.Duration

	TitleTemplate string
	BodyTemplate  string

	// Activation, when non-nil, attaches a clickable action to every
	// notification and dispatches OnActivate with the session id when
	// the user clicks.
	Activation *Activation
}

// Activation describes the per-notification action button.
type Activation struct {
	Label      string
	OnActivate func(ctx context.Context, sessionID string)
}

// TemplateData is exposed to user notification templates. Each
// notification renders against a single waiting session.
type TemplateData struct {
	Session state.Session
	Status  string
}

// Watcher polls state and fires per-session notifications on transitions.
type Watcher struct {
	cfg     Config
	backend Notifier
	store   *state.Store
	title   *template.Template
	body    *template.Template
}

// NewWatcher builds a Watcher with the platform Notifier.
func NewWatcher(cfg Config, store *state.Store) (*Watcher, error) {
	backend, err := New()
	if err != nil {
		return nil, err
	}
	return newWatcherWithBackend(cfg, store, backend)
}

// newWatcherWithBackend is the shared constructor that accepts an injected
// Notifier. Used by NewWatcher for the platform backend and by tests for a
// fake one.
func newWatcherWithBackend(cfg Config, store *state.Store, backend Notifier) (*Watcher, error) {
	title, err := template.New("title").Parse(cfg.TitleTemplate)
	if err != nil {
		return nil, fmt.Errorf("notify: parse title template: %w", err)
	}
	body, err := template.New("body").Parse(cfg.BodyTemplate)
	if err != nil {
		return nil, fmt.Errorf("notify: parse body template: %w", err)
	}
	return &Watcher{cfg: cfg, backend: backend, store: store, title: title, body: body}, nil
}

// Backend returns the selected Notifier.
func (w *Watcher) Backend() Notifier { return w.backend }

// sessionTimer tracks the per-session firing schedule.
type sessionTimer struct {
	// nextFireAt is the time the next notification for this session is
	// due. Set on transition into waiting (now + InitialDelay) and bumped
	// each time we fire (now + RepeatInterval, or far-future if no repeat).
	nextFireAt time.Time
	everFired  bool
}

// Run samples state once per second; for each session in the waiting set
// we maintain its own initial+repeat schedule. Sessions that leave waiting
// drop their schedule; re-entering waiting later starts a fresh one.
func (w *Watcher) Run(ctx context.Context) {
	sample := time.NewTicker(1 * time.Second)
	defer sample.Stop()

	timers := map[string]*sessionTimer{}

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "notify: watcher stopped")
			return
		case now := <-sample.C:
			w.tick(ctx, now, timers)
		}
	}
}

// tick runs one iteration of the watcher loop. Exported via package-internal
// testing to drive the state machine with synthetic times.
func (w *Watcher) tick(ctx context.Context, now time.Time, timers map[string]*sessionTimer) {
	waiting := w.waitingSessions()

	// Drop schedules for sessions that left waiting. Re-entry later treats
	// them as new (fresh InitialDelay).
	for id := range timers {
		if _, ok := waiting[id]; !ok {
			delete(timers, id)
			slog.DebugContext(ctx, "notify: session left waiting, dropped schedule",
				"session", state.ShortID(id))
		}
	}

	for id, sess := range waiting {
		st, exists := timers[id]
		if !exists {
			st = &sessionTimer{nextFireAt: now.Add(w.cfg.InitialDelay)}
			timers[id] = st
			slog.DebugContext(ctx, "notify: session entered waiting, armed initial timer",
				"session", state.ShortID(id),
				"delay", w.cfg.InitialDelay)
			continue
		}
		if now.Before(st.nextFireAt) {
			continue
		}
		reason := "initial"
		if st.everFired {
			reason = "repeat"
		}
		if err := w.fire(ctx, reason, sess); err != nil {
			slog.ErrorContext(ctx, "notify: fire failed",
				"session", state.ShortID(id), "reason", reason, "err", err)
		}
		st.everFired = true
		if w.cfg.RepeatInterval > 0 {
			st.nextFireAt = now.Add(w.cfg.RepeatInterval)
		} else {
			// One-shot: schedule far enough out that we won't re-fire
			// until the session leaves and re-enters waiting.
			st.nextFireAt = now.Add(100 * 365 * 24 * time.Hour)
		}
	}
}

// waitingSessions returns the current waiting set keyed by session id.
func (w *Watcher) waitingSessions() map[string]state.Session {
	out := map[string]state.Session{}
	for _, s := range w.store.Sessions() {
		if state.DeriveStatus(s) == "waiting" {
			out[s.SessionID] = s
		}
	}
	return out
}

// fire renders templates against sess and dispatches one notification.
// When Activation is configured, the click handler runs OnActivate with
// the session's id so the daemon can exec the focus subcommand.
func (w *Watcher) fire(ctx context.Context, reason string, sess state.Session) error {
	ctx, span := logging.Start(ctx, "notify.fire",
		attribute.String("reason", reason),
		attribute.String("session.id", sess.SessionID),
		attribute.String("agent", sess.Agent))
	defer span.End()

	data := TemplateData{Session: sess, Status: state.DeriveStatus(sess)}

	var titleBuf, bodyBuf bytes.Buffer
	if err := w.title.Execute(&titleBuf, data); err != nil {
		return fmt.Errorf("render title: %w", err)
	}
	if err := w.body.Execute(&bodyBuf, data); err != nil {
		return fmt.Errorf("render body: %w", err)
	}

	note := Notification{Title: titleBuf.String(), Body: bodyBuf.String()}
	if w.cfg.Activation != nil {
		note.Actions = []Action{{ID: "focus", Label: w.cfg.Activation.Label}}
	}
	slog.DebugContext(ctx, "notify: dispatching",
		"backend", w.backend.Name(),
		"reason", reason,
		"session", state.ShortID(sess.SessionID),
		"title", note.Title,
		"body", note.Body,
		"actions", len(note.Actions))
	ch, err := w.backend.Notify(ctx, note)
	if err != nil {
		return err
	}
	if w.cfg.Activation != nil {
		sessionID := sess.SessionID
		go func() {
			for range ch {
				slog.DebugContext(ctx, "notify: activation clicked",
					"session", state.ShortID(sessionID))
				w.cfg.Activation.OnActivate(ctx, sessionID)
			}
		}()
	}
	return nil
}
