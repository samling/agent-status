package codex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samling/agent-status/internal/state"
)

func TestScan(t *testing.T) {
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
	// Live process wins over old persisted activity.
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
	createLogsTable(t, logsDB)
	execTestSQL(t, logsDB,
		`insert into logs (thread_id, process_uuid, ts, ts_nanos, feedback_log_body) values (?, ?, ?, ?, ?)`,
		"thread-1",
		fmt.Sprintf("pid:%d:test", os.Getpid()),
		now.Unix(),
		int64(now.Nanosecond()),
		"",
	)
	logsDB.Close()

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
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

func TestScanIncludesRecentThreadBeforeProcessLink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	createThreadsTable(t, stateDB)
	now := time.Now()
	insertThread(t, stateDB, "thread-1", filepath.Join(dir, "rollout.jsonl"), now, now, "/tmp/project")
	stateDB.Close()

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if scanned != 1 {
		t.Fatalf("scanned = %d, want 1", scanned)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	got := sessions[0]
	if got.SessionID != "thread-1" {
		t.Fatalf("SessionID = %q, want thread-1", got.SessionID)
	}
	if got.Meta.PID != 0 {
		t.Fatalf("Meta.PID = %d, want 0", got.Meta.PID)
	}
}

func TestScanUsesCodexSessionIndexName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	createThreadsTable(t, stateDB)
	now := time.Now()
	insertThread(t, stateDB, "thread-1", filepath.Join(dir, "rollout.jsonl"), now, now, "/tmp/project")
	stateDB.Close()
	if err := os.WriteFile(
		filepath.Join(dir, "session_index.jsonl"),
		[]byte(`{"id":"thread-1","thread_name":"Compare lazyagent to agent-status"}`+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	sessions, _, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].Meta.Name != "Compare lazyagent to agent-status" {
		t.Fatalf("Meta.Name = %q, want Compare lazyagent to agent-status", sessions[0].Meta.Name)
	}
}

func TestScanUsesCodexSpawnEdges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	createThreadsTable(t, stateDB)
	createThreadSpawnEdgesTable(t, stateDB)
	now := time.Now()
	insertThread(t, stateDB, "parent", filepath.Join(dir, "parent.jsonl"), now, now, "/tmp/project")
	insertThread(t, stateDB, "child-open", filepath.Join(dir, "child-open.jsonl"), now, now, "/tmp/project")
	insertThread(t, stateDB, "child-closed", filepath.Join(dir, "child-closed.jsonl"), now, now, "/tmp/project")
	execTestSQL(t, stateDB,
		`insert into thread_spawn_edges (parent_thread_id, child_thread_id, status) values (?, ?, ?)`,
		"parent",
		"child-open",
		"open",
	)
	execTestSQL(t, stateDB,
		`insert into thread_spawn_edges (parent_thread_id, child_thread_id, status) values (?, ?, ?)`,
		"parent",
		"child-closed",
		"closed",
	)
	stateDB.Close()

	sessions, _, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	byID := map[string]string{}
	childCounts := map[string]int{}
	openCounts := map[string]int{}
	childStatus := map[string]string{}
	for _, sess := range sessions {
		byID[sess.SessionID] = sess.Meta.ParentSessionID
		childCounts[sess.SessionID] = sess.Meta.ChildCount
		openCounts[sess.SessionID] = sess.Meta.OpenChildCount
		childStatus[sess.SessionID] = sess.Meta.ChildStatus
	}
	if byID["child-open"] != "parent" {
		t.Fatalf("child-open parent = %q, want parent", byID["child-open"])
	}
	if childStatus["child-closed"] != "closed" {
		t.Fatalf("child-closed status = %q, want closed", childStatus["child-closed"])
	}
	if childCounts["parent"] != 2 {
		t.Fatalf("parent child count = %d, want 2", childCounts["parent"])
	}
	if openCounts["parent"] != 1 {
		t.Fatalf("parent open child count = %d, want 1", openCounts["parent"])
	}
}

func TestScanLabelsFreshThreadAsSessionStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	createThreadsTable(t, stateDB)
	now := time.Now()
	insertThread(t, stateDB, "thread-fresh", filepath.Join(dir, "fresh.jsonl"), now, now, "/tmp/project")
	insertThread(t, stateDB, "thread-stale", filepath.Join(dir, "stale.jsonl"), now.Add(-2*freshSessionWindow), now, "/tmp/project")
	stateDB.Close()

	sessions, _, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	got := map[string]string{}
	for _, sess := range sessions {
		got[sess.SessionID] = sess.Event
	}
	if got["thread-fresh"] != state.EventSessionStart {
		t.Fatalf("thread-fresh Event = %q, want SessionStart", got["thread-fresh"])
	}
	if got["thread-stale"] != state.EventDiscovered {
		t.Fatalf("thread-stale Event = %q, want Discovered", got["thread-stale"])
	}
}

func TestScanIncludesRecentRolloutBeforeSQLiteState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	now := time.Now()
	rolloutPath := writeRollout(t, dir, "thread-1", now, "/tmp/project")

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if scanned != 1 {
		t.Fatalf("scanned = %d, want 1", scanned)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	got := sessions[0]
	if got.SessionID != "thread-1" {
		t.Fatalf("SessionID = %q, want thread-1", got.SessionID)
	}
	if got.Meta.Path != rolloutPath {
		t.Fatalf("Meta.Path = %q, want %q", got.Meta.Path, rolloutPath)
	}
	if got.Meta.Cwd != "/tmp/project" {
		t.Fatalf("Meta.Cwd = %q, want /tmp/project", got.Meta.Cwd)
	}
	if got.Meta.Version != "0.128.0" {
		t.Fatalf("Meta.Version = %q, want 0.128.0", got.Meta.Version)
	}
}

func TestScanMarksTurnAbortAsStop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	createdAt := mustParseTime(t, "2026-05-06T22:00:00Z")
	abortedAt := createdAt.Add(10 * time.Second)
	rolloutPath := writeRollout(t, dir, "thread-1", createdAt, "/tmp/project")
	appendRolloutLine(t, rolloutPath, fmt.Sprintf(
		`{"timestamp":%q,"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1","reason":"interrupted","completed_at":0,"duration_ms":10000}}`+"\n",
		abortedAt.UTC().Format(time.RFC3339Nano),
	))

	sessions, _, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].Event != state.EventStop {
		t.Fatalf("Event = %q, want Stop", sessions[0].Event)
	}
	if sessions[0].TurnID != "turn-1" {
		t.Fatalf("TurnID = %q, want turn-1", sessions[0].TurnID)
	}
	if !sessions[0].EventAt.Equal(abortedAt) {
		t.Fatalf("EventAt = %v, want %v", sessions[0].EventAt, abortedAt)
	}
}

func TestScanSkipsOldThreadWithoutProcessLink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	createThreadsTable(t, stateDB)
	old := time.Now().Add(-2 * unlinkedThreadGrace)
	insertThread(t, stateDB, "thread-1", filepath.Join(dir, "rollout.jsonl"), old, old, "/tmp/project")
	stateDB.Close()

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if scanned != 1 {
		t.Fatalf("scanned = %d, want 1", scanned)
	}
	if len(sessions) != 0 {
		t.Fatalf("len(sessions) = %d, want 0", len(sessions))
	}
}

func TestScanSkipsOldRolloutWithoutProcessLink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	old := time.Now().Add(-2 * unlinkedThreadGrace)
	rolloutPath := writeRollout(t, dir, "thread-1", old, "/tmp/project")
	if err := os.Chtimes(rolloutPath, old, old); err != nil {
		t.Fatal(err)
	}

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if scanned != 0 {
		t.Fatalf("scanned = %d, want 0", scanned)
	}
	if len(sessions) != 0 {
		t.Fatalf("len(sessions) = %d, want 0", len(sessions))
	}
}

func TestScanSkipsDeadLinkedProcessEvenWhenRecent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	createThreadsTable(t, stateDB)
	now := time.Now()
	insertThread(t, stateDB, "thread-1", filepath.Join(dir, "rollout.jsonl"), now, now, "/tmp/project")
	stateDB.Close()

	logsDB := openTestDB(t, filepath.Join(dir, "logs_2.sqlite"))
	createLogsTable(t, logsDB)
	execTestSQL(t, logsDB,
		`insert into logs (thread_id, process_uuid, ts, ts_nanos, feedback_log_body) values (?, ?, ?, ?, ?)`,
		"thread-1",
		"pid:999999999:test",
		now.Unix(),
		int64(now.Nanosecond()),
		"",
	)
	logsDB.Close()

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if scanned != 1 {
		t.Fatalf("scanned = %d, want 1", scanned)
	}
	if len(sessions) != 0 {
		t.Fatalf("len(sessions) = %d, want 0", len(sessions))
	}
}

func TestScanLinksProcessFromConversationIDLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	stateDB := openTestDB(t, filepath.Join(dir, "state_5.sqlite"))
	createThreadsTable(t, stateDB)
	now := time.Now()
	insertThread(t, stateDB, "019dffaa-0c6f-7bc0-bda9-fc9a81537894", filepath.Join(dir, "rollout.jsonl"), now, now, "/tmp/project")
	stateDB.Close()

	logsDB := openTestDB(t, filepath.Join(dir, "logs_2.sqlite"))
	createLogsTable(t, logsDB)
	execTestSQL(t, logsDB,
		`insert into logs (thread_id, process_uuid, ts, ts_nanos, feedback_log_body) values (?, ?, ?, ?, ?)`,
		nil,
		fmt.Sprintf("pid:%d:test", os.Getpid()),
		now.Unix(),
		int64(now.Nanosecond()),
		`event.name="codex.sse_event" conversation.id=019dffaa-0c6f-7bc0-bda9-fc9a81537894 app.version=0.128.0`,
	)
	logsDB.Close()

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if scanned != 1 {
		t.Fatalf("scanned = %d, want 1", scanned)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].Meta.PID != os.Getpid() {
		t.Fatalf("Meta.PID = %d, want %d", sessions[0].Meta.PID, os.Getpid())
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

func createThreadsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	execTestSQL(t, db, `
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
}

func createLogsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	execTestSQL(t, db, `
		create table logs (
			thread_id text,
			process_uuid text,
			ts integer not null,
			ts_nanos integer not null,
			feedback_log_body text
		)`)
}

func createThreadSpawnEdgesTable(t *testing.T, db *sql.DB) {
	t.Helper()
	execTestSQL(t, db, `
		create table thread_spawn_edges (
			parent_thread_id text not null,
			child_thread_id text not null primary key,
			status text not null
		)`)
}

func insertThread(t *testing.T, db *sql.DB, id, rolloutPath string, createdAt, updatedAt time.Time, cwd string) {
	t.Helper()
	execTestSQL(t, db, `
		insert into threads (
			id, rollout_path, created_at, updated_at, created_at_ms,
			updated_at_ms, source, cwd, archived, cli_version, model, git_branch
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		rolloutPath,
		createdAt.Unix(),
		updatedAt.Unix(),
		createdAt.UnixMilli(),
		updatedAt.UnixMilli(),
		"cli",
		cwd,
		0,
		"0.128.0",
		"gpt-5.5",
		"main",
	)
}

func writeRollout(t *testing.T, dir, id string, createdAt time.Time, cwd string) string {
	t.Helper()
	path := filepath.Join(dir, "sessions", "2026", "05", "06", "rollout-2026-05-06T16-40-27-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(
		`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"timestamp":%q,"cwd":%q,"source":"cli","cli_version":"0.128.0","git":{"branch":"main"}}}`+"\n",
		createdAt.UTC().Format(time.RFC3339Nano),
		id,
		createdAt.UTC().Format(time.RFC3339Nano),
		cwd,
	)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendRolloutLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func execTestSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
