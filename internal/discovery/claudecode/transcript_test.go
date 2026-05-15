package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samling/agent-status/internal/discovery/source"
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
	if info.UserMessages != 2 || info.AgentMessages != 1 {
		t.Fatalf("messages = user:%d agent:%d, want 2/1", info.UserMessages, info.AgentMessages)
	}
}

func TestTranscriptUsesMetaPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.jsonl")
	data := `{"type":"user","timestamp":"2026-05-14T10:00:00Z","message":{"content":"child prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Transcript("synthetic-child", source.SessionMeta{Path: path})
	if err != nil {
		t.Fatalf("Transcript() error = %v", err)
	}
	if info.LastUserPrompt != "child prompt" {
		t.Fatalf("LastUserPrompt = %q, want child prompt", info.LastUserPrompt)
	}
}

func TestTranscriptMessagesIncludesToolResultBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := `{"type":"assistant","timestamp":"2026-05-14T10:00:10Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","timestamp":"2026-05-14T10:00:20Z","message":{"content":[{"type":"tool_result","content":"tests passed"}]}}
{"type":"assistant","timestamp":"2026-05-14T10:00:30Z","message":{"content":[{"type":"text","text":"done"}]}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	messages, err := TranscriptMessages("s1", source.SessionMeta{Path: path})
	if err != nil {
		t.Fatalf("TranscriptMessages() error = %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	if messages[0].ID != "1" || messages[0].Role != "tool_call" || !strings.Contains(messages[0].Preview, "Bash") {
		t.Fatalf("messages[0] = %#v, want tool call preview", messages[0])
	}
	if messages[1].ID != "2" || messages[1].Role != "tool_result" || !strings.Contains(messages[1].Preview, "tests passed") {
		t.Fatalf("messages[1] = %#v, want tool result preview", messages[1])
	}

	detail, err := TranscriptMessage("s1", source.SessionMeta{Path: path}, "1")
	if err != nil {
		t.Fatalf("TranscriptMessage() error = %v", err)
	}
	if !strings.Contains(detail.Text, "go test ./...") {
		t.Fatalf("detail.Text = %q, want tool input", detail.Text)
	}
	if !strings.Contains(detail.RawText, `"type": "assistant"`) || !strings.Contains(detail.RawText, "tool_use") {
		t.Fatalf("detail.RawText = %q, want raw assistant line", detail.RawText)
	}
}
