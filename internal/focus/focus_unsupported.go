//go:build !linux && !darwin

package focus

import "context"

// New is the unsupported-platform stub.
func New(_ context.Context) (Focuser, error) {
	return nil, ErrUnsupportedPlatform
}
