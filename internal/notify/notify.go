// Package notify delivers desktop notifications for waiting sessions.
package notify

import (
	"context"
	"errors"
)

// Notifier wraps a platform notification backend.
type Notifier interface {
	Name() string
	// Notify returns activated action IDs, then closes when the notification ends.
	Notify(ctx context.Context, n Notification) (<-chan string, error)
}

// Notification is a rendered outbound message.
type Notification struct {
	Title   string
	Body    string
	Actions []Action
}

// Action is one button on a notification.
type Action struct {
	ID    string
	Label string
}

// ErrUnsupportedPlatform means there is no backend for this OS yet.
var ErrUnsupportedPlatform = errors.New("notify: unsupported platform")
