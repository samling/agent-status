package discovery

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samling/agent-status/internal/state"
)

// TranscriptInfo summarizes the per-session transcript exposed by the
// backing agent. Fields are derived from the most recent assistant turn
// (Model, GitBranch, Version) or summed across the whole transcript
// (token counts, TurnCount).
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

// LoadTranscript reads the transcript for sessionID under cwd. Returns
// the zero value (no error) if the file does not exist yet — useful for
// freshly started sessions that have no assistant turns.
func LoadTranscript(sessionID, cwd string) (TranscriptInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return TranscriptInfo{}, err
	}
	path := filepath.Join(home, ".claude", "projects", encodePath(cwd), sessionID+".jsonl")
	return loadTranscriptPath(path, parseClaudeTranscript)
}

func LoadTranscriptForMeta(sessionID string, meta SessionMeta) (TranscriptInfo, error) {
	if state.NormalizeAgent(meta.Agent) == state.AgentCodex {
		if meta.Path == "" {
			return TranscriptInfo{}, nil
		}
		info, err := loadTranscriptPath(meta.Path, parseCodexTranscript)
		if info.Model == "" {
			info.Model = meta.Model
		}
		if info.Version == "" {
			info.Version = meta.Version
		}
		return info, err
	}
	return LoadTranscript(sessionID, meta.Cwd)
}

func loadTranscriptPath(path string, parse func(string) (TranscriptInfo, error)) (TranscriptInfo, error) {
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

// encodePath mirrors Claude Code's encoding of a cwd into a project
// directory name: every non-alphanumeric byte becomes '-'. So
// "/home/me/.local/x" → "-home-me--local-x".
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

func parseClaudeTranscript(path string) (TranscriptInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return TranscriptInfo{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<24) // up to 16 MiB per line

	var info TranscriptInfo
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
			if prompt := extractUserPrompt(line.Message.Content); prompt != "" {
				info.LastUserPrompt = prompt
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return info, err
	}
	return info, nil
}

func parseCodexTranscript(path string) (TranscriptInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return TranscriptInfo{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)

	var info TranscriptInfo
	for scanner.Scan() {
		var line codexTranscriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		switch line.Type {
		case "session_meta":
			var payload codexSessionMeta
			if err := json.Unmarshal(line.Payload, &payload); err == nil {
				if payload.CLIVersion != "" {
					info.Version = payload.CLIVersion
				}
				if payload.Git.Branch != "" {
					info.GitBranch = payload.Git.Branch
				}
			}
		case "turn_context":
			var payload codexTurnContext
			if err := json.Unmarshal(line.Payload, &payload); err == nil && payload.Model != "" {
				info.Model = payload.Model
			}
		case "response_item":
			var payload codexResponseItem
			if err := json.Unmarshal(line.Payload, &payload); err != nil {
				continue
			}
			if payload.Type != "message" {
				continue
			}
			switch payload.Role {
			case "assistant":
				info.TurnCount++
			case "user":
				if prompt := extractUserPrompt(payload.Content); prompt != "" {
					info.LastUserPrompt = prompt
				}
			}
		case "event_msg":
			var payload codexEventMsg
			if err := json.Unmarshal(line.Payload, &payload); err != nil || payload.Type != "token_count" {
				continue
			}
			info.InputTokens = payload.Info.TotalTokenUsage.InputTokens
			info.OutputTokens = payload.Info.TotalTokenUsage.OutputTokens
			info.CacheReadTokens = payload.Info.TotalTokenUsage.CachedInputTokens
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

type codexTranscriptLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	Cwd        string `json:"cwd"`
	Source     string `json:"source"`
	CLIVersion string `json:"cli_version"`
	Model      string `json:"model"`
	Git        struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type codexTurnContext struct {
	Model string `json:"model"`
}

type codexResponseItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type codexEventMsg struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage struct {
			InputTokens       int64 `json:"input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}

// extractUserPrompt pulls the textual content from a user-line message.
// Returns "" if the content is a tool_result or otherwise non-textual.
func extractUserPrompt(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// Most typed prompts are stored as a plain string.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	// Multimodal prompts come through as a content-block array; take
	// the first text block, ignoring tool_result entries.
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
