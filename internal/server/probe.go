package server

import (
	"net"
	"time"
)

// Reachable reports whether anything is listening on addr.
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
