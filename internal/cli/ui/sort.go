package ui

import (
	"sort"

	"github.com/samling/agent-status/internal/state"
)

type sortMode int

const (
	sortStatus sortMode = iota
	sortActivity
	sortCreated
)

func (m sortMode) String() string {
	switch m {
	case sortActivity:
		return "activity"
	case sortCreated:
		return "created"
	case sortStatus:
		return "status"
	}
	return "?"
}

var sortCycle = []sortMode{sortStatus, sortActivity, sortCreated}

func (m sortMode) next() sortMode {
	for i, s := range sortCycle {
		if s == m {
			return sortCycle[(i+1)%len(sortCycle)]
		}
	}
	return sortCycle[0]
}

func sortSessions(ss []state.Session, mode sortMode) {
	sort.SliceStable(ss, func(i, j int) bool {
		a, b := ss[i], ss[j]
		switch mode {
		case sortCreated:
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt > b.FirstSeenAt
			}
		case sortStatus:
			ra, rb := statusRank(state.DeriveStatus(a)), statusRank(state.DeriveStatus(b))
			if ra != rb {
				return ra < rb
			}
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt < b.FirstSeenAt
			}
		default: // sortActivity
			if a.StatusAt != b.StatusAt {
				return a.StatusAt > b.StatusAt
			}
		}
		return a.SessionID < b.SessionID
	})
}

func statusRank(status string) int {
	switch status {
	case "waiting":
		return 0
	case "active":
		return 1
	case "idle":
		return 2
	}
	return 3
}
