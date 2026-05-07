//go:build darwin

package focus

import "context"

// New is a macOS placeholder until SBO-7 lands.
func New(_ context.Context) (Focuser, error) {
	return nil, ErrUnsupportedPlatform
}
