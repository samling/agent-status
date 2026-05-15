package codex

import (
	"encoding/json"
	"os"
	"strings"

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

func TranscriptMessages(sessionID string, meta source.SessionMeta) ([]source.TranscriptMessageSummary, error) {
	_ = sessionID
	if meta.Path == "" {
		return nil, nil
	}
	details, err := parseMessages(meta.Path)
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
	_ = sessionID
	if meta.Path == "" {
		return source.TranscriptMessageDetail{}, source.ErrTranscriptMessageNotFound
	}
	details, err := parseMessages(meta.Path)
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
				if text := source.ExtractTextContent(payload.Content); text != "" {
					source.AppendConversationMessage(&info, source.ConversationMessage{
						Role:      "assistant",
						Text:      source.OneLinePreview(text, 120),
						Timestamp: line.Timestamp,
					})
				}
			case "user":
				if prompt := source.ExtractUserPrompt(payload.Content); prompt != "" {
					info.LastUserPrompt = prompt
					source.AppendConversationMessage(&info, source.ConversationMessage{
						Role:      "user",
						Text:      source.OneLinePreview(prompt, 120),
						Timestamp: line.Timestamp,
					})
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
		if err := json.Unmarshal(buf, &line); err != nil || line.Type != "response_item" {
			return true
		}
		var payload responseItem
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			return true
		}
		if detail, ok := responseItemMessage(lineNumber, line.Timestamp, payload, string(buf)); ok {
			out = append(out, detail)
		}
		return true
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func responseItemMessage(lineNumber int, timestamp string, payload responseItem, raw string) (source.TranscriptMessageDetail, bool) {
	switch payload.Type {
	case "message":
		role := source.MessageContentRole(payload.Role, payload.Content)
		return source.NewTranscriptMessageWithRaw(lineNumber, role, timestamp, source.ExtractMessageContent(payload.Content), raw)
	case "function_call":
		parts := make([]string, 0, 2)
		if payload.Name != "" {
			parts = append(parts, "Tool call: "+payload.Name)
		}
		if payload.Arguments != "" {
			parts = append(parts, payload.Arguments)
		}
		return source.NewTranscriptMessageWithRaw(lineNumber, "tool_call", timestamp, strings.Join(parts, "\n"), raw)
	case "function_call_output":
		return source.NewTranscriptMessageWithRaw(lineNumber, "tool_result", timestamp, rawResponseText(payload.Output), raw)
	default:
		return source.TranscriptMessageDetail{}, false
	}
}

func rawResponseText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

type turnContext struct {
	Model string `json:"model"`
}

type responseItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Output    json.RawMessage `json:"output"`
	Content   json.RawMessage `json:"content"`
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
