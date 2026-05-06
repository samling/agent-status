//go:build darwin

package notify

// New on macOS is a placeholder. Future implementation will shell to
// `osascript -e 'display notification ...'` or use the native
// UserNotifications framework via a small Swift/Objective-C helper.
func New() (Notifier, error) {
	return nil, ErrUnsupportedPlatform
}
