package discovery

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samling/agent-status/internal/state"
)

func TestScanCodexLive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	execTestSQL(t, stateDB, `
		create table threads (
			id text primary key,
			rollout_path text not null,
			created_at integer not null,
			updated_at integer not null,
			created_at_ms integer,
			updated_at_ms integer,
			source text not null,
			cwd text not null,
			archived integer not null,
			cli_version text,
			model text,
			git_branch text
		)`)
	// A live Codex process should be discovered even when its latest
	// persisted activity is old; hooks provide the active/idle signal.
	now := time.Now().Add(-1 * time.Hour)
	execTestSQL(t, stateDB, `
		insert into threads (
			id, rollout_path, created_at, updated_at, created_at_ms,
			updated_at_ms, source, cwd, archived, cli_version, model, git_branch
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"thread-1",
		filepath.Join(dir, "rollout.jsonl"),
		now.Unix(),
		now.Unix(),
		now.UnixMilli(),
		now.UnixMilli(),
		"cli",
		"/tmp/project",
		0,
		"0.128.0",
		"gpt-5.5",
		"main",
	)
	stateDB.Close()

	logsDB := openTestDB(t, filepath.Join(dir, "logs_2.sqlite"))
	execTestSQL(t, logsDB, `
		create table logs (
			thread_id text,
			process_uuid text,
			ts integer not null,
			ts_nanos integer not null
		)`)
	execTestSQL(t, logsDB,
		`insert into logs (thread_id, process_uuid, ts, ts_nanos) values (?, ?, ?, ?)`,
		"thread-1",
		fmt.Sprintf("pid:%d:test", os.Getpid()),
		now.Unix(),
		int64(now.Nanosecond()),
	)
	logsDB.Close()

	sessions, scanned, err := scanCodexLive()
	if err != nil {
		t.Fatalf("scanCodexLive() error = %v", err)
	}
	if scanned != 1 {
		t.Fatalf("scanned = %d, want 1", scanned)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	got := sessions[0]
	if got.Agent != state.AgentCodex {
		t.Fatalf("Agent = %q, want %q", got.Agent, state.AgentCodex)
	}
	if got.SessionID != "thread-1" {
		t.Fatalf("SessionID = %q, want thread-1", got.SessionID)
	}
	if got.EngineStatus != "" {
		t.Fatalf("EngineStatus = %q, want empty", got.EngineStatus)
	}
	if got.Meta.PID != os.Getpid() {
		t.Fatalf("Meta.PID = %d, want %d", got.Meta.PID, os.Getpid())
	}
	if got.Meta.Cwd != "/tmp/project" {
		t.Fatalf("Meta.Cwd = %q, want /tmp/project", got.Meta.Cwd)
	}
	if got.Meta.Model != "gpt-5.5" {
		t.Fatalf("Meta.Model = %q, want gpt-5.5", got.Meta.Model)
	}
}

func TestParseCodexTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"type":"session_meta","payload":{"cli_version":"0.128.0","git":{"branch":"feature"}}}
{"type":"turn_context","payload":{"model":"gpt-5.5"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build this"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":400,"output_tokens":250}}}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := parseCodexTranscript(path)
	if err != nil {
		t.Fatalf("parseCodexTranscript() error = %v", err)
	}
	if info.Version != "0.128.0" {
		t.Fatalf("Version = %q, want 0.128.0", info.Version)
	}
	if info.GitBranch != "feature" {
		t.Fatalf("GitBranch = %q, want feature", info.GitBranch)
	}
	if info.Model != "gpt-5.5" {
		t.Fatalf("Model = %q, want gpt-5.5", info.Model)
	}
	if info.LastUserPrompt != "build this" {
		t.Fatalf("LastUserPrompt = %q, want build this", info.LastUserPrompt)
	}
	if info.TurnCount != 1 {
		t.Fatalf("TurnCount = %d, want 1", info.TurnCount)
	}
	if info.InputTokens != 1000 || info.CacheReadTokens != 400 || info.OutputTokens != 250 {
		t.Fatalf("tokens = in:%d cache:%d out:%d, want 1000/400/250", info.InputTokens, info.CacheReadTokens, info.OutputTokens)
	}
}

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func execTestSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
