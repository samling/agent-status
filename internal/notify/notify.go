// Package notify delivers desktop notifications when sessions need
// the user's attention. The public surface is a Notifier interface
// plus a Watcher that drives notifications based on session state
// transitions; the per-OS backend is selected via build-tagged
// New() implementations, mirroring the focus package layout.
//
// Layout:
//
//   - notify.go              public types, errors, the Notifier interface
//   - notify_linux.go        New() returning a notify-send backend
//   - notify_darwin.go       New() stub for macOS (pending)
//   - notify_unsupported.go  New() stub for everything else
//   - watcher.go             Config + Watcher: the timer state machine
//                            that decides when to call Notifier.Notify
package notify

import (
	"context"
	"errors"
)

// Notifier delivers a notification via the platform's notification
// daemon. One Notifier is selected per process by New() based on the
// runtime environment.
type Notifier interface {
	// Name is a short identifier used in status messages and logs.
	Name() string
	// Notify delivers n via the platform daemon. The returned
	// channel emits action IDs as the user activates them and is
	// closed when the notification has been dismissed, expired, or
	// the underlying process exits. Notifications without
	// actions return an already-closed channel.
	//
	// Notify itself is non-blocking: any waiting on the
	// notification's lifecycle happens in the goroutine that owns
	// the returned channel.
	Notify(ctx context.Context, n Notification) (<-chan string, error)
}

// Notification is the shape of an outbound message. Title and Body
// are already rendered (template substitution happens in the
// Watcher), so backends can pass them straight through. Actions, if
// any, are surfaced as buttons on the notification; the activated
// ID flows back via the channel returned from Notify.
type Notification struct {
	Title   string
	Body    string
	Actions []Action
}

// Action is one button on a notification.
type Action struct {
	// ID is the value emitted via the activation channel when the
	// user clicks this action. Backends use it as the action's
	// internal name (notify-send's --action=ID=Label form).
	ID string
	// Label is the user-visible button text.
	Label string
}

// ErrUnsupportedPlatform is returned by New on operating systems
// where no notification integration has been wired up yet.
var ErrUnsupportedPlatform = errors.New("notify: unsupported platform")
