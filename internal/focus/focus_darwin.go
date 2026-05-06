//go:build darwin

package focus

import "context"

// New on macOS is a placeholder until SBO-7's Darwin entry lands. The
// implementation will wrap NSRunningApplication.activate (cgo or via
// a small osascript helper); until then we fail cleanly so the TUI
// surfaces a readable footer instead of misbehaving.
func New(_ context.Context) (Focuser, error) {
	return nil, ErrUnsupportedPlatform
}
