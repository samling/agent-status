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

// Config controls when and what the Watcher fires.
type Config struct {
	// InitialDelay is the delay between the first 0→1 waiting
	// transition and the first notification. Restarted only on
	// fresh 0→1 transitions, so additional sessions joining the
	// waiting bucket don't bump it.
	InitialDelay time.Duration

	// RepeatInterval is the gap between subsequent notifications
	// while at least one session remains waiting. Set to 0 to fire
	// only once per 0→1 episode.
	RepeatInterval time.Duration

	// TitleTemplate and BodyTemplate are Go text/template strings
	// rendered against TemplateData on each fire.
	TitleTemplate string
	BodyTemplate  string

	// Activation, if non-nil, attaches an action button to every
	// notification. The notify package never decides what an
	// activation means; it just forwards clicks to OnActivate so
	// the caller can call into the focus API (or anything else).
	Activation *Activation
}

// Activation describes the optional action button. The notify package
// has no opinion on what activation means: callers wire OnActivate to
// whatever effect they want (today, picking the freshest waiting
// session and calling focus.PID on its live PID).
type Activation struct {
	// Label is the user-visible button text (e.g. "Focus").
	Label string
	// OnActivate is invoked when the user clicks the action button.
	// It runs in a goroutine owned by the Watcher; the ctx is the
	// Watcher's Run ctx, so a shutdown will cancel in-flight calls.
	OnActivate func(ctx context.Context)
}

// TemplateData is what user-supplied templates render against.
// Stable field set; new fields can be added without breaking
// existing templates.
type TemplateData struct {
	Total           int
	Active          int
	Waiting         int // count of waiting sessions
	Idle            int
	Sessions        []state.Session // every live session
	WaitingSessions []state.Session // just the waiting ones
	First           *state.Session  // first waiting session (or nil)
}

// Watcher polls the state Store on a 1-second cadence, tracks the
// waiting count, and fires the Notifier per the Config rules.
type Watcher struct {
	cfg     Config
	backend Notifier
	store   *state.Store
	title   *template.Template
	body    *template.Template
}

// NewWatcher builds a Watcher backed by the platform's Notifier.
// Returns an error if the Config templates don't parse, or if no
// Notifier is available (no daemon installed, unsupported OS, ...).
func NewWatcher(cfg Config, store *state.Store) (*Watcher, error) {
	backend, err := New()
	if err != nil {
		return nil, err
	}
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

// Backend returns the underlying Notifier. Useful for logging which
// daemon was selected.
func (w *Watcher) Backend() Notifier { return w.backend }

// Run polls every second until ctx is cancelled. Behavior:
//
//   - When waiting transitions 0 → 1+, start the InitialDelay timer.
//   - When InitialDelay fires, send a notification and start the
//     RepeatInterval timer.
//   - On each RepeatInterval tick, re-check the waiting count: send
//     another notification if still > 0, otherwise stop.
//   - When waiting transitions 1+ → 0 at any point, cancel both
//     timers and reset.
//
// Sessions joining the waiting bucket while we're already in a
// pending or repeating cycle do NOT reset timers — only the 0 → 1+
// edge starts a new cycle.
func (w *Watcher) Run(ctx context.Context) {
	sample := time.NewTicker(1 * time.Second)
	defer sample.Stop()

	var (
		initialTimer *time.Timer
		repeatTimer  *time.Timer
		prevWaiting  int
	)

	// nilOrChan returns timer.C, or nil when timer is nil. Selecting
	// on a nil channel blocks forever, which is exactly what we want
	// when the timer is inactive — that case in the select is
	// effectively skipped.
	chanOf := func(t *time.Timer) <-chan time.Time {
		if t == nil {
			return nil
		}
		return t.C
	}
	stopAll := func() {
		if initialTimer != nil {
			initialTimer.Stop()
			initialTimer = nil
		}
		if repeatTimer != nil {
			repeatTimer.Stop()
			repeatTimer = nil
		}
	}

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "notify: watcher stopped")
			stopAll()
			return

		case <-sample.C:
			waiting := w.countWaiting()
			switch {
			case waiting == 0 && prevWaiting > 0:
				slog.DebugContext(ctx, "notify: waiting cleared, stopping timers",
					"prev_waiting", prevWaiting)
				stopAll()
			case waiting > 0 && prevWaiting == 0:
				// Fresh 0 → 1+ transition: start the initial timer.
				// (If existing timers somehow still exist, stop
				// them first to be safe.)
				slog.DebugContext(ctx, "notify: 0->1+ transition, arming initial timer",
					"waiting", waiting, "delay", w.cfg.InitialDelay)
				stopAll()
				initialTimer = time.NewTimer(w.cfg.InitialDelay)
			}
			prevWaiting = waiting

		case <-chanOf(initialTimer):
			initialTimer = nil
			waiting := w.countWaiting()
			slog.InfoContext(ctx, "notify: initial timer fired",
				"waiting", waiting)
			if err := w.fire(ctx, "initial"); err != nil {
				slog.ErrorContext(ctx, "notify: fire initial failed", "err", err)
			}
			if w.cfg.RepeatInterval > 0 {
				repeatTimer = time.NewTimer(w.cfg.RepeatInterval)
			}

		case <-chanOf(repeatTimer):
			repeatTimer = nil
			waiting := w.countWaiting()
			if waiting == 0 {
				slog.DebugContext(ctx, "notify: repeat fired but waiting=0, skipping")
				continue
			}
			slog.InfoContext(ctx, "notify: repeat timer fired", "waiting", waiting)
			if err := w.fire(ctx, "repeat"); err != nil {
				slog.ErrorContext(ctx, "notify: fire repeat failed", "err", err)
			}
			repeatTimer = time.NewTimer(w.cfg.RepeatInterval)
		}
	}
}

func (w *Watcher) countWaiting() int {
	n := 0
	for _, s := range w.store.Sessions() {
		if s.Status == "waiting" {
			n++
		}
	}
	return n
}

// fire renders the title/body templates against the current state
// and hands them to the backend. When Activation is configured, an
// action button is attached and clicks are dispatched to a goroutine
// that calls OnActivate; the goroutine exits when the backend closes
// the activations channel (notification dismissed, expired, or
// activated).
func (w *Watcher) fire(ctx context.Context, reason string) error {
	ctx, span := logging.Start(ctx, "notify.fire",
		attribute.String("reason", reason))
	defer span.End()

	sessions := w.store.Sessions()
	data := TemplateData{
		Total:    len(sessions),
		Sessions: sessions,
	}
	for i := range sessions {
		s := sessions[i]
		switch s.Status {
		case "waiting":
			data.Waiting++
			data.WaitingSessions = append(data.WaitingSessions, s)
			if data.First == nil {
				data.First = &data.WaitingSessions[len(data.WaitingSessions)-1]
			}
		case "active":
			data.Active++
		case "idle":
			data.Idle++
		}
	}
	span.SetAttributes(
		attribute.Int("waiting", data.Waiting),
		attribute.Int("active", data.Active),
		attribute.Int("idle", data.Idle),
	)

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
		"title", note.Title,
		"body", note.Body,
		"actions", len(note.Actions))
	ch, err := w.backend.Notify(ctx, note)
	if err != nil {
		return err
	}
	if w.cfg.Activation != nil {
		go func() {
			for range ch {
				slog.DebugContext(ctx, "notify: activation clicked")
				w.cfg.Activation.OnActivate(ctx)
			}
		}()
	}
	return nil
}
