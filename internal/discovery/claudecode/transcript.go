package claudecode

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/samling/agent-status/internal/discovery/source"
)

// Transcript loads the Claude transcript for the given session.
func Transcript(sessionID string, meta source.SessionMeta) (source.TranscriptInfo, error) {
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

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<24) // up to 16 MiB per line

	var info source.TranscriptInfo
	for scanner.Scan() {
		var line transcriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
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
		case "permission-mode":
			if line.PermissionMode != "" {
				info.PermissionMode = line.PermissionMode
			}
		case "user":
			if line.IsMeta {
				continue
			}
			if prompt := source.ExtractUserPrompt(line.Message.Content); prompt != "" {
				info.LastUserPrompt = prompt
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return info, err
	}
	return info, nil
}

type transcriptLine struct {
	Type           string `json:"type"`
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
