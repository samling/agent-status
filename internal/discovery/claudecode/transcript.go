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
	path, err := transcriptPath(sessionID, meta)
	if err != nil {
		return source.TranscriptInfo{}, err
	}
	info, err := source.LoadTranscriptPath(path, parseTranscript)
	if info.Model == "" {
		info.Model = meta.Model
	}
	if info.Version == "" {
		info.Version = meta.Version
	}
	return info, err
}

func TranscriptMessages(sessionID string, meta source.SessionMeta) ([]source.TranscriptMessageSummary, error) {
	path, err := transcriptPath(sessionID, meta)
	if err != nil {
		return nil, err
	}
	details, err := parseMessages(path)
	if err != nil {
		return nil, err
	}
	out := make([]source.TranscriptMessageSummary, 0, len(details))
	for _, detail := range details {
		out = append(out, source.TranscriptMessageSummaryFromDetail(detail))
	}
	return out, nil
}

func TranscriptMessage(sessionID string, meta source.SessionMeta, id string) (source.TranscriptMessageDetail, error) {
	path, err := transcriptPath(sessionID, meta)
	if err != nil {
		return source.TranscriptMessageDetail{}, err
	}
	details, err := parseMessages(path)
	if err != nil {
		return source.TranscriptMessageDetail{}, err
	}
	for _, detail := range details {
		if detail.ID == id {
			return detail, nil
		}
	}
	return source.TranscriptMessageDetail{}, source.ErrTranscriptMessageNotFound
}

func transcriptPath(sessionID string, meta source.SessionMeta) (string, error) {
	if meta.Path != "" {
		return meta.Path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects", encodePath(meta.Cwd), sessionID+".jsonl"), nil
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

func parseMessages(path string) ([]source.TranscriptMessageDetail, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []source.TranscriptMessageDetail
	lineNumber := 0
	if err := source.ScanJSONL(f, func(buf []byte) bool {
		lineNumber++
		var line transcriptLine
		if err := json.Unmarshal(buf, &line); err != nil {
			return true
		}
		switch line.Type {
		case "assistant":
			if detail, ok := claudeLineMessage(lineNumber, line, string(buf)); ok {
				out = append(out, detail)
			}
		case "user":
			if !line.IsMeta {
				if detail, ok := claudeLineMessage(lineNumber, line, string(buf)); ok {
					out = append(out, detail)
				}
			}
		}
		return true
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func claudeLineMessage(lineNumber int, line transcriptLine, raw string) (source.TranscriptMessageDetail, bool) {
	role := source.MessageContentRole(line.Type, line.Message.Content)
	text := source.ExtractMessageContent(line.Message.Content)
	return source.NewTranscriptMessageWithRaw(lineNumber, role, line.Timestamp, text, raw)
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
