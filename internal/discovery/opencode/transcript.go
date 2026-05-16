package opencode

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
)

// Transcript loads a summary of an Opencode session transcript from meta.Path.
func Transcript(sessionID string, meta source.SessionMeta) (source.TranscriptInfo, error) {
	if meta.Path == "" {
		return source.TranscriptInfo{}, nil
	}
	details, session, err := loadTranscriptDetails(sessionID, meta.Path)
	if err != nil {
		return source.TranscriptInfo{}, err
	}
	info := source.TranscriptInfo{
		Model:               firstNonEmpty(session.Model, meta.Model),
		Version:             firstNonEmpty(session.Version, meta.Version),
		InputTokens:         session.InputTokens,
		OutputTokens:        session.OutputTokens,
		CacheReadTokens:     session.CacheReadTokens,
		CacheCreationTokens: session.CacheWriteTokens,
	}
	for _, detail := range details {
		switch detail.Role {
		case "user":
			info.LastUserPrompt = detail.Text
			source.AppendConversationMessage(&info, source.ConversationMessage{
				Role:      "user",
				Text:      source.OneLinePreview(detail.Text, 120),
				Timestamp: detail.Timestamp,
			})
		case "assistant":
			info.TurnCount++
			source.AppendConversationMessage(&info, source.ConversationMessage{
				Role:      "assistant",
				Text:      source.OneLinePreview(detail.Text, 120),
				Timestamp: detail.Timestamp,
			})
		}
	}
	return info, nil
}

func TranscriptMessages(sessionID string, meta source.SessionMeta) ([]source.TranscriptMessageSummary, error) {
	if meta.Path == "" {
		return nil, nil
	}
	details, _, err := loadTranscriptDetails(sessionID, meta.Path)
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
	if meta.Path == "" {
		return source.TranscriptMessageDetail{}, source.ErrTranscriptMessageNotFound
	}
	details, _, err := loadTranscriptDetails(sessionID, meta.Path)
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

func loadTranscriptDetails(sessionID, path string) ([]source.TranscriptMessageDetail, opencodeTranscriptSession, error) {
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
	if err != nil {
		return nil, opencodeTranscriptSession{}, err
	}
	defer db.Close()

	session, err := loadTranscriptSession(db, sessionID)
	if err != nil {
		return nil, opencodeTranscriptSession{}, err
	}
	details, err := loadTranscriptParts(db, sessionID, &session)
	if err != nil {
		return nil, opencodeTranscriptSession{}, err
	}
	return details, session, nil
}

func loadTranscriptSession(db *sql.DB, sessionID string) (opencodeTranscriptSession, error) {
	var session opencodeTranscriptSession
	var model sql.NullString
	err := db.QueryRow(`select tokens_input, tokens_output, tokens_cache_read, tokens_cache_write, version, model from session where id = ?`, sessionID).Scan(
		&session.InputTokens,
		&session.OutputTokens,
		&session.CacheReadTokens,
		&session.CacheWriteTokens,
		&session.Version,
		&model,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return session, nil
	}
	if err != nil {
		return session, err
	}
	session.Model = modelName(model.String)
	return session, nil
}

func loadTranscriptParts(db *sql.DB, sessionID string, session *opencodeTranscriptSession) ([]source.TranscriptMessageDetail, error) {
	rows, err := db.Query(`select m.id, m.data, p.id, p.time_created, p.data from message m join part p on p.message_id = m.id where m.session_id = ? order by p.time_created asc, p.id asc`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []source.TranscriptMessageDetail
	index := 0
	useMessageTokenTotals := session != nil && session.InputTokens == 0 && session.OutputTokens == 0 && session.CacheReadTokens == 0 && session.CacheWriteTokens == 0
	seenTokenMessages := map[string]bool{}
	for rows.Next() {
		var messageID, messageData, partID, partData string
		var timeCreated int64
		if err := rows.Scan(&messageID, &messageData, &partID, &timeCreated, &partData); err != nil {
			return nil, err
		}
		message := parseMessageData(messageData)
		if session != nil && session.Model == "" && message.Model != "" {
			session.Model = message.Model
		}
		if useMessageTokenTotals && !seenTokenMessages[messageID] {
			session.InputTokens += message.InputTokens
			session.OutputTokens += message.OutputTokens
			session.CacheReadTokens += message.CacheReadTokens
			session.CacheWriteTokens += message.CacheWriteTokens
			seenTokenMessages[messageID] = true
		}
		part := parsePartData(partData)
		role := message.Role
		if part.Tool {
			role = "tool_call"
		}
		text := part.Text
		if strings.TrimSpace(text) == "" && !json.Valid([]byte(partData)) {
			text = partData
		}
		text = strings.TrimSpace(text)
		if role == "" || text == "" {
			continue
		}
		index++
		if detail, ok := newOpencodeTranscriptMessage(index, partID, role, transcriptTimestamp(timeCreated), text, partData); ok {
			out = append(out, detail)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func newOpencodeTranscriptMessage(index int, id, role, timestamp, text, raw string) (source.TranscriptMessageDetail, bool) {
	detail, ok := source.NewTranscriptMessageWithRaw(index, role, timestamp, text, raw)
	if !ok {
		return source.TranscriptMessageDetail{}, false
	}
	detail.ID = id
	detail.Index = index
	return detail, true
}

func parseMessageData(raw string) opencodeMessageData {
	var msg struct {
		Role    string `json:"role"`
		ModelID string `json:"modelID"`
		Model   struct {
			ID      string `json:"id"`
			ModelID string `json:"modelID"`
		} `json:"model"`
		Tokens struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			CacheRead  int64 `json:"cacheRead"`
			CacheWrite int64 `json:"cacheWrite"`
		} `json:"tokens"`
		Usage struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return opencodeMessageData{}
	}
	return opencodeMessageData{
		Role:             msg.Role,
		Model:            firstNonEmpty(msg.ModelID, msg.Model.ModelID, msg.Model.ID),
		InputTokens:      firstInt64(msg.Tokens.Input, msg.Usage.InputTokens),
		OutputTokens:     firstInt64(msg.Tokens.Output, msg.Usage.OutputTokens),
		CacheReadTokens:  firstInt64(msg.Tokens.CacheRead, msg.Usage.CacheReadInputTokens),
		CacheWriteTokens: firstInt64(msg.Tokens.CacheWrite, msg.Usage.CacheCreationInputTokens),
	}
}

func parsePartData(raw string) opencodePartData {
	var part struct {
		Type    string          `json:"type"`
		Tool    string          `json:"tool"`
		Name    string          `json:"name"`
		Text    json.RawMessage `json:"text"`
		Content json.RawMessage `json:"content"`
		Output  json.RawMessage `json:"output"`
		State   struct {
			Output json.RawMessage `json:"output"`
			Input  struct {
				Command string `json:"command"`
			} `json:"input"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(raw), &part); err != nil {
		return opencodePartData{Text: raw}
	}
	toolName := firstNonEmpty(part.Tool, part.Name)
	texts := []string{
		rawJSONText(part.Text),
		rawJSONText(part.Content),
		rawJSONText(part.Output),
		rawJSONText(part.State.Output),
	}
	if part.State.Input.Command != "" {
		texts = append(texts, part.State.Input.Command)
	}
	if part.Type == "tool" || toolName != "" {
		lines := make([]string, 0, len(texts)+1)
		if toolName != "" {
			lines = append(lines, "Tool call: "+toolName)
		}
		for _, text := range texts {
			if strings.TrimSpace(text) != "" {
				lines = append(lines, text)
			}
		}
		return opencodePartData{Tool: true, Text: strings.Join(lines, "\n")}
	}
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			return opencodePartData{Text: text}
		}
	}
	return opencodePartData{}
}

func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return fmt.Sprint(v)
}

func transcriptTimestamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

type opencodeTranscriptSession struct {
	Model            string
	Version          string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

type opencodeMessageData struct {
	Role             string
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

type opencodePartData struct {
	Tool bool
	Text string
}
