package ui

import (
	"testing"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.1.128", "2.1.120", 1},
		{"2.1.120", "2.1.128", -1},
		{"2.1.128", "2.1.128", 0},
		{"2.1.128", "2.1.10", 1},
		{"2.1.10", "2.1.128", -1},
		{"0.128.0", "0.99.0", 1},
		{"2.1", "2.1.0", 0},
		{"2.1.1", "2.1", 1},
		{"", "1.0.0", -1},
		{"1.0.0", "", 1},
		{"", "", 0},
		// Pre-release: a release version outranks the same version with a
		// pre-release tag, per SemVer 2.0.0.
		{"2.1.128", "2.1.128-rc1", 1},
		{"2.1.128-rc1", "2.1.128", -1},
		// Pre-release identifier ordering: numeric is less than alphanumeric;
		// numeric identifiers compare numerically (so rc.10 > rc.2).
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-rc.10", "1.0.0-rc.2", 1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		// Shorter pre-release identifier set is lower precedence when all
		// leading identifiers are equal.
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		// Build metadata is ignored for ordering.
		{"1.0.0+abc", "1.0.0+xyz", 0},
		{"1.0.0-rc1+build1", "1.0.0-rc1+build2", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMaxVersionByAgent(t *testing.T) {
	sessions := []state.Session{
		{SessionID: "a1", Agent: "claude-code"},
		{SessionID: "a2", Agent: "claude-code"},
		{SessionID: "a3", Agent: "claude-code"},
		{SessionID: "b1", Agent: "codex"},
		{SessionID: "b2", Agent: "codex"},
		{SessionID: "c1", Agent: "claude-code"},
	}
	meta := map[string]source.SessionMeta{
		"a1": {Version: "2.1.120"},
		"a2": {Version: "2.1.128"},
		"a3": {Version: "2.1.115"},
		"b1": {Version: "0.128.0"},
		"b2": {Version: "0.99.0"},
		"c1": {Version: ""},
	}
	got := maxVersionByAgent(sessions, meta)
	if got["claude-code"] != "2.1.128" {
		t.Errorf("claude-code max = %q, want 2.1.128", got["claude-code"])
	}
	if got["codex"] != "0.128.0" {
		t.Errorf("codex max = %q, want 0.128.0", got["codex"])
	}
}

func TestMaxVersionByAgent_AllEmpty(t *testing.T) {
	sessions := []state.Session{
		{SessionID: "a1", Agent: "claude-code"},
	}
	meta := map[string]source.SessionMeta{
		"a1": {Version: ""},
	}
	got := maxVersionByAgent(sessions, meta)
	if _, ok := got["claude-code"]; ok {
		t.Errorf("agent with no version should not appear in max map, got %v", got)
	}
}
