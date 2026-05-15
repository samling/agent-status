package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samling/agent-status/internal/discovery/source"
)

func TestParseTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"type":"session_meta","timestamp":"2026-05-14T10:00:00Z","payload":{"cli_version":"0.128.0","git":{"branch":"feature"}}}
{"type":"turn_context","timestamp":"2026-05-14T10:00:01Z","payload":{"model":"gpt-5.5"}}
{"type":"response_item","timestamp":"2026-05-14T10:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build this"}]}}
{"type":"response_item","timestamp":"2026-05-14T10:00:03Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
{"type":"event_msg","timestamp":"2026-05-14T10:00:04Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":400,"output_tokens":250}}}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := parseTranscript(path)
	if err != nil {
		t.Fatalf("parseTranscript() error = %v", err)
	}
	if info.Version != "0.128.0" {
		t.Fatalf("Version = %q, want 0.128.0", info.Version)
	}
	if info.GitBranch != "feature" {
		t.Fatalf("GitBranch = %q, want feature", info.GitBranch)
	}
	if info.Model != "gpt-5.5" {
		t.Fatalf("Model = %q, want gpt-5.5", info.Model)
	}
	if info.LastUserPrompt != "build this" {
		t.Fatalf("LastUserPrompt = %q, want build this", info.LastUserPrompt)
	}
	if len(info.RecentMessages) != 2 {
		t.Fatalf("len(RecentMessages) = %d, want 2", len(info.RecentMessages))
	}
	if info.RecentMessages[0].Role != "user" || info.RecentMessages[0].Text != "build this" {
		t.Fatalf("first message = %#v", info.RecentMessages[0])
	}
	if info.RecentMessages[1].Role != "assistant" || info.RecentMessages[1].Text != "ok" {
		t.Fatalf("second message = %#v", info.RecentMessages[1])
	}
	if info.TurnCount != 1 {
		t.Fatalf("TurnCount = %d, want 1", info.TurnCount)
	}
	if info.UserMessages != 1 || info.AgentMessages != 1 {
		t.Fatalf("messages = user:%d agent:%d, want 1/1", info.UserMessages, info.AgentMessages)
	}
	if info.InputTokens != 1000 || info.CacheReadTokens != 400 || info.OutputTokens != 250 {
		t.Fatalf("tokens = in:%d cache:%d out:%d, want 1000/400/250", info.InputTokens, info.CacheReadTokens, info.OutputTokens)
	}
}

func TestTranscriptMessagesIncludesToolItemsAndStableIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"type":"session_meta","timestamp":"2026-05-14T10:00:00Z","payload":{"cli_version":"0.128.0"}}
{"type":"response_item","timestamp":"2026-05-14T10:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build this"}]}}
{"type":"response_item","timestamp":"2026-05-14T10:00:03Z","payload":{"type":"function_call","name":"shell","arguments":"{\"cmd\":\"go test ./...\"}"}}
{"type":"response_item","timestamp":"2026-05-14T10:00:04Z","payload":{"type":"function_call_output","output":"tests passed"}}
{"type":"response_item","timestamp":"2026-05-14T10:00:05Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	messages, err := TranscriptMessages("s1", source.SessionMeta{Path: path})
	if err != nil {
		t.Fatalf("TranscriptMessages() error = %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(messages))
	}
	for i, want := range []struct {
		id   string
		role string
		text string
	}{
		{id: "2", role: "user", text: "build this"},
		{id: "3", role: "tool_call", text: "shell"},
		{id: "4", role: "tool_result", text: "tests passed"},
		{id: "5", role: "assistant", text: "done"},
	} {
		if messages[i].ID != want.id || messages[i].Role != want.role || !strings.Contains(messages[i].Preview, want.text) {
			t.Fatalf("messages[%d] = %#v, want id %q role %q preview containing %q", i, messages[i], want.id, want.role, want.text)
		}
	}

	detail, err := TranscriptMessage("s1", source.SessionMeta{Path: path}, "3")
	if err != nil {
		t.Fatalf("TranscriptMessage() error = %v", err)
	}
	if detail.ID != "3" || detail.Role != "tool_call" {
		t.Fatalf("detail = %#v, want id 3 role tool_call", detail)
	}
	if !strings.Contains(detail.Text, "go test ./...") {
		t.Fatalf("detail.Text = %q, want command arguments", detail.Text)
	}
	if !strings.Contains(detail.RawText, `"type": "response_item"`) || !strings.Contains(detail.RawText, "go test ./...") {
		t.Fatalf("detail.RawText = %q, want raw response item", detail.RawText)
	}
}

func TestTranscriptMessageCapsLargeToolOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	huge := strings.Repeat("x", source.MaxTranscriptMessageTextRunes+1)
	data := `{"type":"response_item","timestamp":"2026-05-14T10:00:04Z","payload":{"type":"function_call_output","output":"` + huge + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	detail, err := TranscriptMessage("s1", source.SessionMeta{Path: path}, "1")
	if err != nil {
		t.Fatalf("TranscriptMessage() error = %v", err)
	}
	if !detail.Truncated {
		t.Fatalf("detail.Truncated = false, want true")
	}
	if len([]rune(detail.Text)) != source.MaxTranscriptMessageTextRunes {
		t.Fatalf("detail text length = %d, want %d", len([]rune(detail.Text)), source.MaxTranscriptMessageTextRunes)
	}
}
