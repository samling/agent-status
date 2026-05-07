// Package focus brings an agent session's window to the foreground.
package focus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Focuser brings a session's window to the foreground.
type Focuser interface {
	Name() string
	// Focus returns ErrWindowNotFound when no ancestor owns a matching window.
	Focus(ctx context.Context, target Target) error
}

// Target describes the process/window to focus.
type Target struct {
	PID       int
	Ancestors []int
	AppID     string
	Title     string
	WindowID  string
}

var (
	ErrUnsupportedPlatform = errors.New("focus: unsupported platform")
	ErrNoBackend           = errors.New("focus: no usable backend detected")
	ErrWindowNotFound      = errors.New("focus: window not found")
)

// defaultTimeout keeps backend IPC from hanging the TUI.
const defaultTimeout = 5 * time.Second

// PID focuses the window and tmux pane associated with pid.
func PID(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	f, err := New(ctx)
	if err != nil {
		slog.DebugContext(ctx, "focus.PID: no backend", "pid", pid, "err", err)
		return "", err
	}

	ancestors := walkAncestors(pid)
	slog.DebugContext(ctx, "focus.PID: ancestry resolved",
		"pid", pid, "backend", f.Name(), "ancestors", ancestors)

	winErr := f.Focus(ctx, Target{PID: pid, Ancestors: ancestors})
	slog.DebugContext(ctx, "focus.PID: window focus result",
		"pid", pid, "backend", f.Name(), "err", winErr)

	paneMsg, paneErr := findAndFocusTmuxPane(ctx, ancestors)
	if paneErr != nil {
		slog.DebugContext(ctx, "focus.PID: tmux pane error", "err", paneErr)
		return "", paneErr
	}
	if paneMsg != "" {
		slog.DebugContext(ctx, "focus.PID: tmux pane focused", "msg", paneMsg)
	}

	switch {
	case winErr == nil && paneMsg != "":
		return "Focused window and tmux pane", nil
	case winErr == nil:
		return fmt.Sprintf("Focused via %s", f.Name()), nil
	case errors.Is(winErr, ErrWindowNotFound) && paneMsg != "":
		return paneMsg, nil
	case errors.Is(winErr, ErrWindowNotFound):
		return "Couldn't find a window or pane for this session", nil
	default:
		return "", fmt.Errorf("%s: %w", f.Name(), winErr)
	}
}
