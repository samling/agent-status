package source

import (
	"strings"
	"testing"
)

func TestOneLinePreviewCollapsesWhitespaceAndTruncates(t *testing.T) {
	got := OneLinePreview("hello\n\nthere   friend", 14)
	if got != "hello there..." {
		t.Fatalf("OneLinePreview() = %q, want %q", got, "hello there...")
	}
}

func TestAppendConversationMessageCapsRecentMessages(t *testing.T) {
	var info TranscriptInfo
	for i := 0; i < MaxConversationMessages+2; i++ {
		AppendConversationMessage(&info, ConversationMessage{
			Role:      "user",
			Text:      OneLinePreview("message", 80),
			Timestamp: string(rune('a' + i)),
		})
	}

	if len(info.RecentMessages) != MaxConversationMessages {
		t.Fatalf("len(RecentMessages) = %d, want %d", len(info.RecentMessages), MaxConversationMessages)
	}
	if info.RecentMessages[0].Timestamp != "c" {
		t.Fatalf("oldest retained timestamp = %q, want c", info.RecentMessages[0].Timestamp)
	}
	if info.UserMessages != MaxConversationMessages+2 {
		t.Fatalf("UserMessages = %d, want %d", info.UserMessages, MaxConversationMessages+2)
	}
}

func TestAppendConversationMessageCountsRoles(t *testing.T) {
	var info TranscriptInfo
	AppendConversationMessage(&info, ConversationMessage{Role: "user", Text: "question"})
	AppendConversationMessage(&info, ConversationMessage{Role: "assistant", Text: "answer"})

	if info.UserMessages != 1 {
		t.Fatalf("UserMessages = %d, want 1", info.UserMessages)
	}
	if info.AgentMessages != 1 {
		t.Fatalf("AgentMessages = %d, want 1", info.AgentMessages)
	}
}

func TestNewTranscriptMessagePrettyPrintsJSONObject(t *testing.T) {
	detail, ok := NewTranscriptMessage(1, "tool_result", "", `{"z":2,"nested":{"ok":true}}`)
	if !ok {
		t.Fatal("NewTranscriptMessage() ok = false, want true")
	}
	for _, want := range []string{"{\n", "  \"nested\": {", "    \"ok\": true", "  \"z\": 2"} {
		if !strings.Contains(detail.Text, want) {
			t.Fatalf("detail.Text missing %q:\n%s", want, detail.Text)
		}
	}
}

func TestNewTranscriptMessageLeavesJSONScalarsCompact(t *testing.T) {
	detail, ok := NewTranscriptMessage(1, "tool_result", "", `"plain string"`)
	if !ok {
		t.Fatal("NewTranscriptMessage() ok = false, want true")
	}
	if detail.Text != `"plain string"` {
		t.Fatalf("detail.Text = %q, want compact JSON string", detail.Text)
	}
}

func TestNewTranscriptMessagePrettyPrintsJSONLinesInMixedText(t *testing.T) {
	detail, ok := NewTranscriptMessage(1, "tool_call", "", `Tool call: exec_command
{"cmd":"go test ./...","yield_time_ms":1000}`)
	if !ok {
		t.Fatal("NewTranscriptMessage() ok = false, want true")
	}
	for _, want := range []string{"Tool call: exec_command", "{\n", "  \"cmd\": \"go test ./...\"", "  \"yield_time_ms\": 1000"} {
		if !strings.Contains(detail.Text, want) {
			t.Fatalf("detail.Text missing %q:\n%s", want, detail.Text)
		}
	}
}

func TestNewTranscriptMessageWithRawPrettyPrintsRawJSON(t *testing.T) {
	detail, ok := NewTranscriptMessageWithRaw(1, "user", "", "filtered", `{"payload":{"content":"raw body"}}`)
	if !ok {
		t.Fatal("NewTranscriptMessageWithRaw() ok = false, want true")
	}
	if detail.Text != "filtered" {
		t.Fatalf("detail.Text = %q, want filtered", detail.Text)
	}
	for _, want := range []string{"{\n", "  \"payload\": {", "    \"content\": \"raw body\""} {
		if !strings.Contains(detail.RawText, want) {
			t.Fatalf("detail.RawText missing %q:\n%s", want, detail.RawText)
		}
	}
}

func TestExtractTextContentSupportsStringAndBlocks(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "string", raw: `"hello"`, want: "hello"},
		{name: "blocks", raw: `[{"type":"text","text":"hello"},{"type":"output_text","text":"world"}]`, want: "hello\nworld"},
		{name: "ignores non text", raw: `[{"type":"tool_result","text":"skip"},{"type":"input_text","text":"keep"}]`, want: "keep"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractTextContent([]byte(tc.raw)); got != tc.want {
				t.Fatalf("ExtractTextContent() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractUserPromptIgnoresOutputText(t *testing.T) {
	raw := `[
		{"type":"text","text":"typed"},
		{"type":"output_text","text":"assistant output"},
		{"type":"input_text","text":"more typed"},
		{"type":"tool_result","text":"tool output"}
	]`

	got := ExtractUserPrompt([]byte(raw))
	want := "typed\nmore typed"
	if got != want {
		t.Fatalf("ExtractUserPrompt() = %q, want %q", got, want)
	}
}

func TestExtractUserPromptStripsIDEContextWrapper(t *testing.T) {
	raw := `[
		{"type":"input_text","text":"# Context from my IDE setup:\n\n## Active file: agent-status/internal/discovery/source/source.go\n\n## Open tabs:\n- source.go: agent-status/internal/discovery/source/source.go\n- waiting.go: agent-status/internal/discovery/codex/waiting.go\n\n## My request for Codex:\nCan we see the actual message?\n"}
	]`

	got := ExtractUserPrompt([]byte(raw))
	want := "Can we see the actual message?"
	if got != want {
		t.Fatalf("ExtractUserPrompt() = %q, want %q", got, want)
	}
}
