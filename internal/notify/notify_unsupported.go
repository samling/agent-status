//go:build !linux && !darwin

package notify

// New is the unsupported-platform stub.
func New() (Notifier, error) {
	return nil, ErrUnsupportedPlatform
}
