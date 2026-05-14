package ui

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

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

// shortPath shrinks a filesystem path to fit in max columns by (in order):
// substituting $HOME with "~", dropping leading segments and prefixing with
// ".../", or falling back to a truncated basename. The goal is to keep the
// trailing segment (typically the project name) readable rather than smearing
// the middle of an irrelevant prefix into view.
func shortPath(p string, max int) string {
	if p == "" || max <= 0 {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		rest := p[len(home):]
		if rest == "" || rest[0] == '/' {
			p = "~" + rest
		}
	}
	if len(p) <= max {
		return p
	}
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		candidate := ".../" + strings.Join(parts[i:], "/")
		if len(candidate) <= max {
			return candidate
		}
	}
	base := parts[len(parts)-1]
	if len(base) <= max {
		return base
	}
	if max <= 3 {
		return base[len(base)-max:]
	}
	return base[:max-3] + "..."
}

func absTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

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
