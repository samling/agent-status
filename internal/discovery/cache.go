package discovery

import (
	"maps"
	"sync"

	"github.com/samling/agent-status/internal/discovery/source"
)

// metaCache holds the most recently observed SessionMeta for every live
// session, refreshed by syncDiscovered after each scan tick. Out-of-process
// readers (TUI, statusline, etc.) consume this snapshot via the daemon's
// /meta HTTP endpoint instead of running their own filesystem scans.
var (
	metaMu    sync.RWMutex
	metaState = map[string]source.SessionMeta{}
)

func setMetaSnapshot(snapshot map[string]source.SessionMeta) {
	metaMu.Lock()
	metaState = snapshot
	metaMu.Unlock()
}

// LatestMeta returns a copy of the most recent SessionMeta snapshot.
func LatestMeta() map[string]source.SessionMeta {
	metaMu.RLock()
	defer metaMu.RUnlock()
	out := make(map[string]source.SessionMeta, len(metaState))
	maps.Copy(out, metaState)
	return out
}
