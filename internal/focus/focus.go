// Package focus brings external windows to the foreground for the
// agent-status TUI. The public surface is a single capability —
// PID(pid) — that walks the process ancestry and asks the appropriate
// per-OS Focuser to focus the matching window.
//
// Layout:
//
//   - focus.go              public types, errors, and the PID() entry point
//   - focus_linux.go        New() for Linux, with WSL-vs-compositor dispatch
//   - focus_darwin.go       New() for macOS (stub today)
//   - focus_unsupported.go  New() for everything else (always returns an error)
//   - niri.go / wsl.go      per-environment Focuser implementations
//   - proc.go / tmux.go     shared helpers (ancestry walk, tmux drilling)
package focus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Focuser brings a session's window to the foreground. One Focuser is
// selected per process by New() based on the runtime environment.
type Focuser interface {
	// Name is a short identifier used in status messages and logs.
	Name() string
	// Focus focuses the window matching target. Returns
	// ErrWindowNotFound when no candidate window is owned by any pid
	// in target.Ancestors; nil on success; another error on hard
	// IPC failures.
	Focus(ctx context.Context, target Target) error
}

// Target describes what to focus. Backends consume whichever fields
// they can: today every focus call is anchored on a session pid and
// its ancestry, but AppID/Title/WindowID exist for backends that
// match windows by class/title/handle (Hyprland, KWin, macOS bundle
// id) when they land.
type Target struct {
	PID       int
	Ancestors []int // process ancestry, top-down from PID; populated by PID()
	AppID     string
	Title     string
	WindowID  string
}

var (
	// ErrUnsupportedPlatform is returned by New on operating systems
	// where no integration has been wired up yet (everything outside
	// Linux/macOS today).
	ErrUnsupportedPlatform = errors.New("focus: unsupported platform")
	// ErrNoBackend is returned by New when the platform is supported
	// but no Focuser implementation matches the running environment
	// (e.g. Linux with an unrecognised compositor).
	ErrNoBackend = errors.New("focus: no usable backend detected")
	// ErrWindowNotFound is returned by Focuser.Focus when none of the
	// pids in Target.Ancestors own a window known to the backend.
	ErrWindowNotFound = errors.New("focus: window not found")
)

// defaultTimeout caps each focus invocation so a hung IPC call (niri
// socket, code shim, powershell.exe) can't freeze the TUI.
const defaultTimeout = 5 * time.Second

// PID is the high-level entry the TUI calls. It picks a Focuser for
// the current environment, walks the ancestry of pid, focuses the
// matching window, drills into a tmux pane if one exists, and returns
// a status string suitable for the UI footer.
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
