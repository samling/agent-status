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

func TestDefaultSortModeIsActivity(t *testing.T) {
	var mode sortMode

	if mode != sortActivity {
		t.Fatalf("default sortMode = %v, want sortActivity", mode)
	}
}

func TestActivitySortRanksLiveStatusBeforeIdleThenActivity(t *testing.T) {
	cards := []sessionview.SessionCard{
		{SessionID: "idle-new", Status: "idle", StatusAt: "2026-05-14T10:03:00Z"},
		{SessionID: "active-old", Status: "active", StatusAt: "2026-05-14T10:01:00Z"},
		{SessionID: "active-new", Status: "active", StatusAt: "2026-05-14T10:02:00Z"},
	}

	sortCards(cards, sortActivity, nil)

	for i, want := range []string{"active-new", "active-old", "idle-new"} {
		if cards[i].SessionID != want {
			t.Fatalf("cards[%d] = %q, want %q; cards=%#v", i, cards[i].SessionID, want, cards)
		}
	}
}
