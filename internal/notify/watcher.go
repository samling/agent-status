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

// Config controls notification timing and templates.
type Config struct {
	// InitialDelay starts on a fresh 0->1 waiting transition.
	InitialDelay time.Duration

	// RepeatInterval is disabled when zero.
	RepeatInterval time.Duration

	TitleTemplate string
	BodyTemplate  string

	// Activation adds an action button and callback.
	Activation *Activation
}

// Activation describes an optional action button.
type Activation struct {
	Label      string
	OnActivate func(ctx context.Context)
}

// TemplateData is exposed to user notification templates.
type TemplateData struct {
	Total           int
	Active          int
	Waiting         int
	Idle            int
	Sessions        []state.Session
	WaitingSessions []state.Session
	First           *state.Session
}

// Watcher polls state and fires notifications.
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

// Run sends on the 0->1 waiting edge, then repeats while waiting remains.
func (w *Watcher) Run(ctx context.Context) {
	sample := time.NewTicker(1 * time.Second)
	defer sample.Stop()

	var (
		initialTimer *time.Timer
		repeatTimer  *time.Timer
		prevWaiting  int
	)

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
		if state.DeriveStatus(s) == "waiting" {
			n++
		}
	}
	return n
}

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
		switch state.DeriveStatus(s) {
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
