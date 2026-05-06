//go:build !linux && !darwin

package focus

import "context"

// New is the catch-all stub for operating systems we don't yet
// integrate with (FreeBSD, Windows native, etc.). It always returns
// ErrUnsupportedPlatform so callers can render a clean footer message
// rather than blow up.
func New(_ context.Context) (Focuser, error) {
	return nil, ErrUnsupportedPlatform
}
