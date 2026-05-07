package source

import (
	"os"
	"syscall"
)

// PIDAlive reports whether a process is still running.
func PIDAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
