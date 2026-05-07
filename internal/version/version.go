// Package version resolves the build version.
package version

import "runtime/debug"

// Version is set by release builds via -ldflags.
var Version = ""

// Get returns Version, build metadata, or "dev".
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
