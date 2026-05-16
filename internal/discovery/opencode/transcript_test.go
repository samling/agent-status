package opencode

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/samling/agent-status/internal/discovery/source"
)

func TestTranscriptBuildsSummaryFromMessages(t *testing.T) {
	path := createTranscriptDB(t)
	insertTranscriptSession(t, path, "ses_1", `{"id":"gpt-5.5-fast"}`, "1.15.0")
	insertTranscriptMessage(t, path, "msg_user", "ses_1", `{"role":"user"}`)
	insertTranscriptPart(t, path, "part_user", "msg_user", 1767225600000, `{"text":"build this"}`)
	insertTranscriptMessage(t, path, "msg_assistant", "ses_1", `{"role":"assistant","modelID":"gpt-5.5-fast","tokens":{"input":12,"output":5,"cacheRead":3,"cacheWrite":2}}`)
	insertTranscriptPart(t, path, "part_assistant", "msg_assistant", 1767225601000, `{"text":"done"}`)

	info, err := Transcript("ses_1", source.SessionMeta{Path: path, Version: "1.15.0", Model: "fallback-model"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "gpt-5.5-fast" {
		t.Fatalf("Model = %q, want gpt-5.5-fast", info.Model)
	}
	if info.Version != "1.15.0" {
		t.Fatalf("Version = %q, want 1.15.0", info.Version)
	}
	if info.LastUserPrompt != "build this" {
		t.Fatalf("LastUserPrompt = %q, want build this", info.LastUserPrompt)
	}
	if info.UserMessages != 1 {
		t.Fatalf("UserMessages = %d, want 1", info.UserMessages)
	}
	if info.AgentMessages != 1 {
		t.Fatalf("AgentMessages = %d, want 1", info.AgentMessages)
	}
	if info.TurnCount != 1 {
		t.Fatalf("TurnCount = %d, want 1", info.TurnCount)
	}
	if len(info.RecentMessages) != 2 {
		t.Fatalf("len(RecentMessages) = %d, want 2", len(info.RecentMessages))
	}
}

func TestTranscriptMessagesIncludesToolPartsAndStableIDs(t *testing.T) {
	path := createTranscriptDB(t)
	insertTranscriptSession(t, path, "ses_1", `{"id":"gpt-5.5-fast"}`, "1.15.0")
	insertTranscriptMessage(t, path, "msg_user", "ses_1", `{"role":"user"}`)
	insertTranscriptPart(t, path, "part_user", "msg_user", 1767225600000, `{"text":"run tests"}`)
	insertTranscriptPart(t, path, "part_tool", "msg_user", 1767225601000, `{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./..."},"output":"ok"}}`)

	messages, err := TranscriptMessages("ses_1", source.SessionMeta{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[0].ID != "part_user" {
		t.Fatalf("messages[0].ID = %q, want part_user", messages[0].ID)
	}
	if messages[0].Role != "user" {
		t.Fatalf("messages[0].Role = %q, want user", messages[0].Role)
	}
	if !strings.Contains(messages[0].Preview, "run tests") {
		t.Fatalf("messages[0].Preview = %q, want run tests", messages[0].Preview)
	}
	if messages[1].ID != "part_tool" {
		t.Fatalf("messages[1].ID = %q, want part_tool", messages[1].ID)
	}
	if messages[1].Role != "tool_call" {
		t.Fatalf("messages[1].Role = %q, want tool_call", messages[1].Role)
	}
	if !strings.Contains(messages[1].Preview, "bash") {
		t.Fatalf("messages[1].Preview = %q, want bash", messages[1].Preview)
	}

	detail, err := TranscriptMessage("ses_1", source.SessionMeta{Path: path}, "part_tool")
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != "part_tool" {
		t.Fatalf("detail.ID = %q, want part_tool", detail.ID)
	}
	if detail.Role != "tool_call" {
		t.Fatalf("detail.Role = %q, want tool_call", detail.Role)
	}
	if !strings.Contains(detail.Text, "go test ./...") {
		t.Fatalf("detail.Text = %q, want go test ./...", detail.Text)
	}
}

func TestTranscriptMessageTruncatesLargeTextAndRaw(t *testing.T) {
	path := createTranscriptDB(t)
	largeText := strings.Repeat("x", source.MaxTranscriptMessageTextRunes+100)
	insertTranscriptSession(t, path, "ses_1", `{"id":"gpt-5.5-fast"}`, "1.15.0")
	insertTranscriptMessage(t, path, "msg_assistant", "ses_1", `{"role":"assistant"}`)
	insertTranscriptPart(t, path, "part_large", "msg_assistant", 1767225600000, fmt.Sprintf(`{"text":%q}`, largeText))

	detail, err := TranscriptMessage("ses_1", source.SessionMeta{Path: path}, "part_large")
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != "part_large" {
		t.Fatalf("detail.ID = %q, want part_large", detail.ID)
	}
	if detail.Index != 1 {
		t.Fatalf("detail.Index = %d, want 1", detail.Index)
	}
	if !detail.Truncated {
		t.Fatal("detail.Truncated = false, want true")
	}
	if got := len([]rune(detail.Text)); got != source.MaxTranscriptMessageTextRunes {
		t.Fatalf("len(detail.Text) = %d, want %d", got, source.MaxTranscriptMessageTextRunes)
	}
	if !detail.RawTruncated {
		t.Fatal("detail.RawTruncated = false, want true")
	}
	if got := len([]rune(detail.RawText)); got != source.MaxTranscriptMessageTextRunes {
		t.Fatalf("len(detail.RawText) = %d, want %d", got, source.MaxTranscriptMessageTextRunes)
	}
}

func TestTranscriptSumsMessageTokensWhenSessionTotalsAreZero(t *testing.T) {
	path := createTranscriptDB(t)
	insertTranscriptSessionWithTokens(t, path, "ses_1", `{"id":"gpt-5.5-fast"}`, "1.15.0", 0, 0, 0, 0)
	insertTranscriptMessage(t, path, "msg_assistant_1", "ses_1", `{"role":"assistant","tokens":{"input":3,"output":4,"cacheRead":1,"cacheWrite":2}}`)
	insertTranscriptPart(t, path, "part_assistant_1", "msg_assistant_1", 1767225600000, `{"text":"first"}`)
	insertTranscriptMessage(t, path, "msg_assistant_2", "ses_1", `{"role":"assistant","tokens":{"input":5,"output":6,"cacheRead":7,"cacheWrite":8}}`)
	insertTranscriptPart(t, path, "part_assistant_2", "msg_assistant_2", 1767225601000, `{"text":"second"}`)

	info, err := Transcript("ses_1", source.SessionMeta{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if info.InputTokens != 8 {
		t.Fatalf("InputTokens = %d, want 8", info.InputTokens)
	}
	if info.OutputTokens != 10 {
		t.Fatalf("OutputTokens = %d, want 10", info.OutputTokens)
	}
	if info.CacheReadTokens != 8 {
		t.Fatalf("CacheReadTokens = %d, want 8", info.CacheReadTokens)
	}
	if info.CacheCreationTokens != 10 {
		t.Fatalf("CacheCreationTokens = %d, want 10", info.CacheCreationTokens)
	}
}

func createTranscriptDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	execTranscriptSQL(t, db, `create table session (
		id text primary key, version text not null, model text,
		tokens_input integer not null default 0,
		tokens_output integer not null default 0,
		tokens_cache_read integer not null default 0,
		tokens_cache_write integer not null default 0
	)`)
	execTranscriptSQL(t, db, `create table message (id text primary key, session_id text not null, data text not null)`)
	execTranscriptSQL(t, db, `create table part (id text primary key, message_id text not null, time_created integer not null, data text not null)`)
	return path
}

func insertTranscriptSession(t *testing.T, path, id, model, version string) {
	t.Helper()
	insertTranscriptSessionWithTokens(t, path, id, model, version, 20, 10, 4, 3)
}

func insertTranscriptSessionWithTokens(t *testing.T, path, id, model, version string, input, output, cacheRead, cacheWrite int64) {
	t.Helper()
	db := openTranscriptDB(t, path)
	defer db.Close()
	execTranscriptSQL(t, db, `insert into session (id, version, model, tokens_input, tokens_output, tokens_cache_read, tokens_cache_write) values (?, ?, ?, ?, ?, ?, ?)`, id, version, model, input, output, cacheRead, cacheWrite)
}

func insertTranscriptMessage(t *testing.T, path, id, sessionID, data string) {
	t.Helper()
	db := openTranscriptDB(t, path)
	defer db.Close()
	execTranscriptSQL(t, db, `insert into message (id, session_id, data) values (?, ?, ?)`, id, sessionID, data)
}

func insertTranscriptPart(t *testing.T, path, id, messageID string, timeCreated int64, data string) {
	t.Helper()
	db := openTranscriptDB(t, path)
	defer db.Close()
	execTranscriptSQL(t, db, `insert into part (id, message_id, time_created, data) values (?, ?, ?, ?)`, id, messageID, timeCreated, data)
}

func openTranscriptDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func execTranscriptSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
