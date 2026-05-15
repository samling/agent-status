package ui

import (
	"testing"

	"github.com/samling/agent-status/internal/sessionview"
)

func TestStatusSortPreservesPreviousOrderWithinSameStatus(t *testing.T) {
	cards := []sessionview.SessionCard{
		{SessionID: "third", Status: "idle", FirstSeenAt: "2026-05-14T10:00:00Z"},
		{SessionID: "first", Status: "idle", FirstSeenAt: "2026-05-14T10:02:00Z"},
		{SessionID: "second", Status: "idle", FirstSeenAt: "2026-05-14T10:01:00Z"},
	}
	previousOrder := map[string]int{
		"first":  0,
		"second": 1,
		"third":  2,
	}

	sortCards(cards, sortStatus, previousOrder)

	for i, want := range []string{"first", "second", "third"} {
		if cards[i].SessionID != want {
			t.Fatalf("cards[%d] = %q, want %q; cards=%#v", i, cards[i].SessionID, want, cards)
		}
	}
}

