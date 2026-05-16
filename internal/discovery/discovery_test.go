package discovery

import (
	"testing"

	"github.com/samling/agent-status/internal/state"
)

func TestSourcesIncludesOpencode(t *testing.T) {
	for _, src := range sources {
		if src.agent != state.AgentOpencode {
			continue
		}
		if src.scan == nil {
			t.Fatal("opencode scan is nil")
		}
		if src.watch == nil {
			t.Fatal("opencode watch is nil")
		}
		if src.apply == nil {
			t.Fatal("opencode apply is nil")
		}
		if src.transcript == nil {
			t.Fatal("opencode transcript is nil")
		}
		if src.messages == nil {
			t.Fatal("opencode messages is nil")
		}
		if src.message == nil {
			t.Fatal("opencode message is nil")
		}
		return
	}
	t.Fatal("opencode source not registered")
}
