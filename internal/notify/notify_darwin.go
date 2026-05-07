//go:build darwin

package notify

// New is a macOS placeholder.
func New() (Notifier, error) {
	return nil, ErrUnsupportedPlatform
}
