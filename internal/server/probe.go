package server

import (
	"net"
	"time"
)

// Reachable returns true when something is listening on addr. Used by
// statusline and TUI templates to populate their "Connected" field
// without a JSON round trip; the server has no read-side HTTP surface,
// so a TCP dial is the cheapest liveness signal we can give the user.
//
// The check is intentionally weak: it proves the listener is up, not
// that the writer goroutines are healthy. That's the same guarantee
// the old GET /state probe gave (a successful 200 only meant the
// handler ran, not that hooks were being processed), expressed at a
// fraction of the cost.
func Reachable(addr string) bool {
	if addr == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
