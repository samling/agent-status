package discovery

import "sync"

// statusCache holds the last-known JSONL status ("idle"|"busy"|"") for
// each session_id, keyed by sessionID. The watcher populates it on every
// file event; consumers (e.g. the server's hook handler) read it to
// decide whether to sync state without doing extra disk I/O.
var (
	statusMu sync.RWMutex
	statuses = map[string]string{}
)

// setStatus updates the cached status for a session. An empty status
// removes the entry, treating it as "no status reported."
func setStatus(sessionID, status string) {
	statusMu.Lock()
	defer statusMu.Unlock()
	if status == "" {
		delete(statuses, sessionID)
		return
	}
	statuses[sessionID] = status
}

// Status returns the last-known JSONL status for sessionID, if known.
// The boolean reports whether an entry exists (not whether it's "idle").
func Status(sessionID string) (string, bool) {
	statusMu.RLock()
	defer statusMu.RUnlock()
	s, ok := statuses[sessionID]
	return s, ok
}
