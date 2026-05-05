package focus

// Compositor focuses an OS window owned by one of the PIDs in a process
// ancestry. Implementations are expected to be cheap to construct and to
// short-circuit via Available() when their compositor isn't running.
type Compositor interface {
	// Name is a short identifier used in status messages and debug logs.
	Name() string
	// Available returns true if this compositor's IPC is reachable on
	// the current host (binary present, socket open, etc.).
	Available() bool
	// Focus searches the ancestor list for a PID this compositor knows
	// about as a window owner and focuses that window. Returns a status
	// message, whether a window was found, and any hard error from the
	// compositor's IPC.
	Focus(ancestors []int) (msg string, found bool, err error)
}

// compositors returns the registered compositor implementations in the
// order they should be tried. The first one whose Available() returns
// true gets to attempt the focus.
func compositors() []Compositor {
	return []Compositor{Niri{}}
}
