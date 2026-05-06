//go:build !linux && !darwin

package notify

// New is the catch-all stub for operating systems we don't yet
// integrate with (FreeBSD, Windows native, etc.). Always returns
// ErrUnsupportedPlatform.
func New() (Notifier, error) {
	return nil, ErrUnsupportedPlatform
}
