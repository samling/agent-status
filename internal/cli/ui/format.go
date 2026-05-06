package ui

import (
	"fmt"
	"strings"
	"time"
)

// collapseWS replaces any run of whitespace with a single space.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncate returns s clipped to max runes, with a trailing "..." when
// truncation actually happens.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

// humanTokens formats a token count with K/M/B suffixes.
func humanTokens(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
}

// shortPath truncates a path with an "..." prefix if it exceeds max,
// keeping the trailing portion (project basename, etc.) which is the
// most informative end of a working directory. ASCII ellipsis is used
// so the resulting byte length matches the visual width and printf
// padding stays correct.
func shortPath(p string, max int) string {
	if len(p) <= max {
		return p
	}
	if max <= 3 {
		return p[len(p)-max:]
	}
	return "..." + p[len(p)-max+3:]
}

// absTime renders t as a local 19-char "YYYY-MM-DD HH:MM:SS" string,
// falling back to "-" on the zero value.
func absTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// relTime renders t as a coarse "Ns/m/h/d ago" string, falling back to
// "-" on the zero value.
func relTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Second:
		return "<1s ago"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// lineCount returns the number of visual rows in s (a string with no
// trailing newline counts its last line).
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
