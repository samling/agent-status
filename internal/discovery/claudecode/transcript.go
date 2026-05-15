package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/samling/agent-status/internal/discovery/source"
)

// Transcript loads the Claude transcript for the given session.
func Transcript(sessionID string, meta source.SessionMeta) (source.TranscriptInfo, error) {
	if meta.Path != "" {
		info, err := source.LoadTranscriptPath(meta.Path, parseTranscript)
		if info.Model == "" {
			info.Model = meta.Model
		}
		if info.Version == "" {
			info.Version = meta.Version
		}
		return info, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return source.TranscriptInfo{}, err
	}
	path := filepath.Join(home, ".claude", "projects", encodePath(meta.Cwd), sessionID+".jsonl")
	return source.LoadTranscriptPath(path, parseTranscript)
}

// encodePath mirrors Claude Code's cwd-to-project-dir encoding.
func encodePath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func parseTranscript(path string) (source.TranscriptInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return source.TranscriptInfo{}, err
	}
	defer f.Close()

	var info source.TranscriptInfo
	if err := source.ScanJSONL(f, func(buf []byte) bool {
		var line transcriptLine
		if err := json.Unmarshal(buf, &line); err != nil {
			return true
		}
		switch line.Type {
		case "assistant":
			if line.Message.Model != "" {
				info.Model = line.Message.Model
			}
			info.TurnCount++
			info.InputTokens += line.Message.Usage.InputTokens
			info.OutputTokens += line.Message.Usage.OutputTokens
			info.CacheCreationTokens += line.Message.Usage.CacheCreationInputTokens
			info.CacheReadTokens += line.Message.Usage.CacheReadInputTokens
			if line.GitBranch != "" {
				info.GitBranch = line.GitBranch
			}
			if line.Version != "" {
				info.Version = line.Version
			}
			if text := source.ExtractTextContent(line.Message.Content); text != "" {
				source.AppendConversationMessage(&info, source.ConversationMessage{
					Role:      "assistant",
					Text:      source.OneLinePreview(text, 120),
					Timestamp: line.Timestamp,
				})
			}
		case "permission-mode":
			if line.PermissionMode != "" {
				info.PermissionMode = line.PermissionMode
			}
		case "user":
			if line.IsMeta {
				return true
			}
			if prompt := source.ExtractUserPrompt(line.Message.Content); prompt != "" {
				info.LastUserPrompt = prompt
				source.AppendConversationMessage(&info, source.ConversationMessage{
					Role:      "user",
					Text:      source.OneLinePreview(prompt, 120),
					Timestamp: line.Timestamp,
				})
			}
		}
		return true
	}); err != nil {
		return info, err
	}
	return info, nil
}

type transcriptLine struct {
	Type           string `json:"type"`
	Timestamp      string `json:"timestamp,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	GitBranch      string `json:"gitBranch,omitempty"`
	Version        string `json:"version,omitempty"`
	IsMeta         bool   `json:"isMeta,omitempty"`
	Message        struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}
