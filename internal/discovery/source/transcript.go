package source

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const MaxConversationMessages = 12

// ConversationMessage is a single transcript message preview.
type ConversationMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
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
	info.RecentMessages = append(info.RecentMessages, msg)
	if len(info.RecentMessages) > MaxConversationMessages {
		info.RecentMessages = info.RecentMessages[len(info.RecentMessages)-MaxConversationMessages:]
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
		return s
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
		return strings.Join(parts, "\n")
	}
	return ""
}
