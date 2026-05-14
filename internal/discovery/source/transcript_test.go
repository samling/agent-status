package source

import "testing"

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
