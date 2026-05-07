//go:build linux

package focus

import (
	"context"
	"os"
	"strings"
)

// New picks the first reachable Linux focus backend.
func New(ctx context.Context) (Focuser, error) {
	if isWSL() {
		return &wsl{}, nil
	}

	sessionType := os.Getenv("XDG_SESSION_TYPE")
	desktop := os.Getenv("XDG_CURRENT_DESKTOP")
	for _, c := range linuxCandidates(sessionType, desktop) {
		if c.Probe(ctx) == nil {
			return c, nil
		}
	}
	return nil, ErrNoBackend
}

func linuxCandidates(sessionType, desktop string) []probingFocuser {
	var c []probingFocuser
	if strings.EqualFold(desktop, "niri") || os.Getenv("NIRI_SOCKET") != "" {
		c = append(c, &niri{})
	}
	// Future compositors slot in here; see SBO-7.
	_ = sessionType // referenced by future X11 / Wayland generic candidates
	return c
}

type probingFocuser interface {
	Focuser
	Probe(ctx context.Context) error
}

// isWSL checks /proc first so stripped service envs still work.
func isWSL() bool {
	b, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	s := strings.ToLower(string(b))
	if strings.Contains(s, "microsoft") || strings.Contains(s, "wsl") {
		return true
	}
	return os.Getenv("WSL_INTEROP") != "" || os.Getenv("WSL_DISTRO_NAME") != ""
}
