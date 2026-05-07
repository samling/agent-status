package source

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

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

// ExtractUserPrompt returns typed text from a transcript user message,
// ignoring tool results and other non-typed content.
func ExtractUserPrompt(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// Most typed prompts are stored as a plain string.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		for _, b := range blocks {
			if (b.Type == "text" || b.Type == "input_text") && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}
