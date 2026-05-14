package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTranscriptCapturesConversationPreviews(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := `{"type":"user","timestamp":"2026-05-14T10:00:00Z","message":{"content":[{"type":"text","text":"hello\nfrom user"}]}}
{"type":"assistant","timestamp":"2026-05-14T10:00:10Z","message":{"model":"claude-sonnet","content":[{"type":"text","text":"hello from assistant"}],"usage":{"input_tokens":10,"output_tokens":5}}}
{"type":"user","timestamp":"2026-05-14T10:01:00Z","message":{"content":"second user message"}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := parseTranscript(path)
	if err != nil {
		t.Fatalf("parseTranscript() error = %v", err)
	}
	if len(info.RecentMessages) != 3 {
		t.Fatalf("len(RecentMessages) = %d, want 3", len(info.RecentMessages))
	}
	if info.RecentMessages[0].Role != "user" || info.RecentMessages[0].Text != "hello from user" {
		t.Fatalf("first message = %#v", info.RecentMessages[0])
	}
	if info.RecentMessages[1].Role != "assistant" || info.RecentMessages[1].Text != "hello from assistant" {
		t.Fatalf("second message = %#v", info.RecentMessages[1])
	}
	if info.LastUserPrompt != "second user message" {
		t.Fatalf("LastUserPrompt = %q, want second user message", info.LastUserPrompt)
	}
}
