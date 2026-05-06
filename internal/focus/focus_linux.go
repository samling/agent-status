//go:build linux

package focus

import (
	"context"
	"os"
	"strings"
)

// New picks a Focuser for the current Linux environment. WSL is
// checked first because /proc/version is the only thing that
// distinguishes it from native Linux at the kernel level, and a WSL
// session needs an entirely different backend (focus a host-side
// window via a per-host strategy, not a local compositor).
//
// On native Linux we build a candidate list from XDG hints and
// well-known per-compositor environment variables, then run each
// candidate's Probe to confirm it can actually reach its IPC. The
// first one that probes cleanly wins.
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

// linuxCandidates returns Focusers in preferred order for the current
// Linux session. Ordering is most-specific-first so an env-var match
// (NIRI_SOCKET, HYPRLAND_INSTANCE_SIGNATURE, SWAYSOCK, ...) outranks
// a generic Wayland fallback. Future compositors plug in here.
func linuxCandidates(sessionType, desktop string) []probingFocuser {
	var c []probingFocuser
	if strings.EqualFold(desktop, "niri") || os.Getenv("NIRI_SOCKET") != "" {
		c = append(c, &niri{})
	}
	// Hyprland (HYPRLAND_INSTANCE_SIGNATURE), Sway (SWAYSOCK),
	// KDE/KWin (XDG_CURRENT_DESKTOP), GNOME, X11 (DISPLAY) all slot
	// in here — see SBO-7.
	_ = sessionType // referenced by future X11 / Wayland generic candidates
	return c
}

// probingFocuser is a Focuser plus a runtime reachability test. Probe
// is intentionally not on the public Focuser interface — callers don't
// want to know about it; only the factory does, when filtering
// candidates.
type probingFocuser interface {
	Focuser
	Probe(ctx context.Context) error
}

// isWSL returns true when /proc/version is from a WSL kernel. The
// WSL_INTEROP / WSL_DISTRO_NAME env vars are reliable signals too,
// but they're not present in stripped-environment service launches
// (systemd user units, etc.), so we check the kernel banner first.
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
