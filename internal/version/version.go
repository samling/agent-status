// Package version exposes the build's version string. The string is
// resolved at runtime in this order:
//
//  1. The Version package var, populated at build time via -ldflags
//     "-X github.com/samling/agent-status/internal/version.Version=v1.2.3".
//     This is the path the Makefile and release workflow take.
//  2. runtime/debug.ReadBuildInfo: returns the module version for
//     `go install` builds, or the VCS revision (with a "-dirty"
//     suffix when the working tree had uncommitted changes) for
//     plain `go build` runs that include VCS stamping.
//  3. The literal "dev" fallback, when neither of the above
//     produced anything useful.
package version

import "runtime/debug"

// Version is the override path: empty unless set via -ldflags.
var Version = ""

// Get returns the resolved version string. Always returns a non-empty
// value.
func Get() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		var rev, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev != "" {
			short := rev
			if len(short) > 7 {
				short = short[:7]
			}
			if modified == "true" {
				return short + "-dirty"
			}
			return short
		}
	}
	return "dev"
}
