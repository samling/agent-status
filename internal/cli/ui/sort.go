package ui

import (
	"sort"

	"github.com/samling/agent-status/internal/sessionview"
)

type sortMode int

const (
	sortActivity sortMode = iota
	sortCreated
	sortStatus
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

var sortCycle = []sortMode{sortActivity, sortCreated, sortStatus}

func (m sortMode) next() sortMode {
	for i, s := range sortCycle {
		if s == m {
			return sortCycle[(i+1)%len(sortCycle)]
		}
	}
	return sortCycle[0]
}

func sortCards(cards []sessionview.SessionCard, mode sortMode, previousOrder map[string]int) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		switch mode {
		case sortCreated:
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt > b.FirstSeenAt
			}
		case sortStatus:
			ra, rb := statusRank(a.Status), statusRank(b.Status)
			if ra != rb {
				return ra < rb
			}
			if ai, bi, ok := previousIndexes(previousOrder, a.SessionID, b.SessionID); ok {
				return ai < bi
			}
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt < b.FirstSeenAt
			}
		case sortActivity:
			ra, rb := statusRank(a.Status), statusRank(b.Status)
			if ra != rb {
				return ra < rb
			}
			if a.StatusAt != b.StatusAt {
				return a.StatusAt > b.StatusAt
			}
		default:
			ra, rb := statusRank(a.Status), statusRank(b.Status)
			if ra != rb {
				return ra < rb
			}
			if a.StatusAt != b.StatusAt {
				return a.StatusAt > b.StatusAt
			}
		}
		return a.SessionID < b.SessionID
	})
}

func cardOrder(cards []sessionview.SessionCard) map[string]int {
	out := make(map[string]int, len(cards))
	for i, card := range cards {
		out[card.SessionID] = i
	}
	return out
}

func previousIndexes(order map[string]int, a, b string) (int, int, bool) {
	ai, aok := order[a]
	bi, bok := order[b]
	switch {
	case aok && bok && ai != bi:
		return ai, bi, true
	case aok != bok:
		if aok {
			return 0, 1, true
		}
		return 1, 0, true
	default:
		return 0, 0, false
	}
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
