package codex

import (
	"os"
	"path/filepath"
	"testing"
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
	if info.InputTokens != 1000 || info.CacheReadTokens != 400 || info.OutputTokens != 250 {
		t.Fatalf("tokens = in:%d cache:%d out:%d, want 1000/400/250", info.InputTokens, info.CacheReadTokens, info.OutputTokens)
	}
}
