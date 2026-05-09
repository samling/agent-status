package codex

import (
	"encoding/json"
	"os"

	"github.com/samling/agent-status/internal/discovery/source"
)

// Transcript loads the Codex transcript at meta.Path. Returns zero info if
// no path is recorded.
func Transcript(sessionID string, meta source.SessionMeta) (source.TranscriptInfo, error) {
	if meta.Path == "" {
		return source.TranscriptInfo{}, nil
	}
	info, err := source.LoadTranscriptPath(meta.Path, parseTranscript)
	if info.Model == "" {
		info.Model = meta.Model
	}
	if info.Version == "" {
		info.Version = meta.Version
	}
	return info, err
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
		case "session_meta":
			var payload sessionMeta
			if err := json.Unmarshal(line.Payload, &payload); err == nil {
				if payload.CLIVersion != "" {
					info.Version = payload.CLIVersion
				}
				if payload.Git.Branch != "" {
					info.GitBranch = payload.Git.Branch
				}
			}
		case "turn_context":
			var payload turnContext
			if err := json.Unmarshal(line.Payload, &payload); err == nil && payload.Model != "" {
				info.Model = payload.Model
			}
		case "response_item":
			var payload responseItem
			if err := json.Unmarshal(line.Payload, &payload); err != nil {
				return true
			}
			if payload.Type != "message" {
				return true
			}
			switch payload.Role {
			case "assistant":
				info.TurnCount++
			case "user":
				if prompt := source.ExtractUserPrompt(payload.Content); prompt != "" {
					info.LastUserPrompt = prompt
				}
			}
		case "event_msg":
			var payload eventMsg
			if err := json.Unmarshal(line.Payload, &payload); err != nil || payload.Type != "token_count" {
				return true
			}
			info.InputTokens = payload.Info.TotalTokenUsage.InputTokens
			info.OutputTokens = payload.Info.TotalTokenUsage.OutputTokens
			info.CacheReadTokens = payload.Info.TotalTokenUsage.CachedInputTokens
		}
		return true
	}); err != nil {
		return info, err
	}
	return info, nil
}

type turnContext struct {
	Model string `json:"model"`
}

type responseItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type eventMsg struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage struct {
			InputTokens       int64 `json:"input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}
