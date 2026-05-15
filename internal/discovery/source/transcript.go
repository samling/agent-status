package source

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxConversationMessages       = 200
	MaxTranscriptMessageTextRunes = 16000
)

var ErrTranscriptMessageNotFound = errors.New("transcript message not found")

// ConversationMessage is a single transcript message preview.
type ConversationMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
}

type TranscriptMessageSummary struct {
	ID        string `json:"id"`
	Index     int    `json:"index"`
	Role      string `json:"role"`
	Preview   string `json:"preview"`
	Timestamp string `json:"timestamp,omitempty"`
}

type TranscriptMessageDetail struct {
	ID           string `json:"id"`
	Index        int    `json:"index"`
	Role         string `json:"role"`
	Preview      string `json:"preview"`
	Text         string `json:"text"`
	RawText      string `json:"raw_text,omitempty"`
	Timestamp    string `json:"timestamp,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
	RawTruncated bool   `json:"raw_truncated,omitempty"`
}

// TranscriptInfo summarizes one agent transcript.
type TranscriptInfo struct {
	Model               string
	GitBranch           string
	Version             string
	PermissionMode      string
	TurnCount           int
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	UserMessages        int
	AgentMessages       int
	LastUserPrompt      string // raw text of the most recent user-typed prompt
	RecentMessages      []ConversationMessage
}

// LoadTranscriptPath stat-caches a transcript file and runs parse on cache miss.
// Per-agent loaders pass their own parse function; this layer is agent-agnostic.
func LoadTranscriptPath(path string, parse func(string) (TranscriptInfo, error)) (TranscriptInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return TranscriptInfo{}, nil
		}
		return TranscriptInfo{}, err
	}

	transcriptMu.Lock()
	cached, ok := transcriptCache[path]
	transcriptMu.Unlock()
	if ok && cached.path == path && cached.mtime.Equal(fi.ModTime()) && cached.size == fi.Size() {
		return cached.info, nil
	}

	info, err := parse(path)
	if err != nil {
		return TranscriptInfo{}, err
	}

	transcriptMu.Lock()
	transcriptCache[path] = cachedTranscript{path: path, mtime: fi.ModTime(), size: fi.Size(), info: info}
	transcriptMu.Unlock()

	return info, nil
}

func AppendConversationMessage(info *TranscriptInfo, msg ConversationMessage) {
	if info == nil || msg.Role == "" || msg.Text == "" {
		return
	}
	switch msg.Role {
	case "user":
		info.UserMessages++
	case "assistant":
		info.AgentMessages++
	}
	info.RecentMessages = append(info.RecentMessages, msg)
	if len(info.RecentMessages) > MaxConversationMessages {
		info.RecentMessages = info.RecentMessages[len(info.RecentMessages)-MaxConversationMessages:]
	}
}

func NewTranscriptMessage(lineNumber int, role, timestamp, text string) (TranscriptMessageDetail, bool) {
	return NewTranscriptMessageWithRaw(lineNumber, role, timestamp, text, "")
}

func NewTranscriptMessageWithRaw(lineNumber int, role, timestamp, text, raw string) (TranscriptMessageDetail, bool) {
	text = strings.TrimSpace(text)
	if lineNumber <= 0 || role == "" || text == "" {
		return TranscriptMessageDetail{}, false
	}
	text = prettyJSONObjectsOrArrays(text)
	capped, truncated := truncateRunes(text, MaxTranscriptMessageTextRunes)
	detail := TranscriptMessageDetail{
		ID:        strconv.Itoa(lineNumber),
		Index:     lineNumber,
		Role:      role,
		Preview:   OneLinePreview(text, 120),
		Text:      capped,
		Timestamp: timestamp,
		Truncated: truncated,
	}
	raw = strings.TrimSpace(raw)
	if raw != "" {
		raw = prettyJSONObjectOrArray(raw)
		detail.RawText, detail.RawTruncated = truncateRunes(raw, MaxTranscriptMessageTextRunes)
	}
	return detail, true
}

func prettyJSONObjectOrArray(s string) string {
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return s
	}
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(s), "", "  "); err != nil {
		return s
	}
	return b.String()
}

func prettyJSONObjectsOrArrays(s string) string {
	if pretty := prettyJSONObjectOrArray(s); pretty != s {
		return pretty
	}
	lines := strings.Split(s, "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		pretty := prettyJSONObjectOrArray(trimmed)
		if pretty == trimmed {
			continue
		}
		lines[i] = prefixLines(leadingWhitespace(line), pretty)
		changed = true
	}
	if !changed {
		return s
	}
	return strings.Join(lines, "\n")
}

func leadingWhitespace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

func prefixLines(prefix, s string) string {
	if prefix == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func TranscriptMessageSummaryFromDetail(detail TranscriptMessageDetail) TranscriptMessageSummary {
	return TranscriptMessageSummary{
		ID:        detail.ID,
		Index:     detail.Index,
		Role:      detail.Role,
		Preview:   detail.Preview,
		Timestamp: detail.Timestamp,
	}
}

func OneLinePreview(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func ExtractTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if (b.Type == "text" || b.Type == "input_text" || b.Type == "output_text") && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func ExtractMessageContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			switch b.Type {
			case "text", "input_text", "output_text":
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			case "tool_use":
				if b.Name != "" {
					parts = append(parts, "Tool use: "+b.Name)
				}
				if text := rawJSONText(b.Input); text != "" {
					parts = append(parts, "Input: "+text)
				}
			case "tool_result":
				if text := rawJSONText(b.Content); text != "" {
					parts = append(parts, "Tool result: "+text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return rawJSONText(content)
}

func MessageContentRole(base string, content json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil || len(blocks) == 0 {
		return base
	}
	allToolUse := true
	allToolResult := true
	for _, b := range blocks {
		allToolUse = allToolUse && b.Type == "tool_use"
		allToolResult = allToolResult && b.Type == "tool_result"
	}
	switch {
	case allToolUse:
		return "tool_call"
	case allToolResult:
		return "tool_result"
	default:
		return base
	}
}

func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	if text := ExtractTextContent(raw); text != "" {
		return text
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

func truncateRunes(s string, max int) (string, bool) {
	if max <= 0 {
		return "", s != ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}

type cachedTranscript struct {
	path  string
	mtime time.Time
	size  int64
	info  TranscriptInfo
}

var (
	transcriptMu    sync.Mutex
	transcriptCache = map[string]cachedTranscript{}
)

// ScanJSONL reads newline-delimited records from r and invokes fn on each
// line (with the trailing newline stripped). fn returns false to stop early.
// Unlike bufio.Scanner, there is no per-line size cap, so a single oversized
// pasted prompt can't silently abort transcript parsing.
func ScanJSONL(r io.Reader, fn func(line []byte) bool) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if !fn(line) {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// ExtractUserPrompt returns typed text from a transcript user message,
// ignoring tool results and other non-typed content.
func ExtractUserPrompt(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return cleanUserPrompt(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if (b.Type == "text" || b.Type == "input_text") && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return cleanUserPrompt(strings.Join(parts, "\n"))
	}
	return ""
}

func cleanUserPrompt(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "# Context from my IDE setup:") {
		return s
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "## My request for ") {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return s
}
