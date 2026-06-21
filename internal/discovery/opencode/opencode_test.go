package opencode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

func TestScanMissingDBReturnsEmpty(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 0 {
		t.Fatalf("scanned = %d, want 0", scanned)
	}
	if len(sessions) != 0 {
		t.Fatalf("len(sessions) = %d, want 0", len(sessions))
	}
}

func TestScanMapsUnarchivedSessions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	stubLiveProcesses(t, []liveProcess{{PID: 1234, Cwd: "/work/live"}})
	db := filepath.Join(dir, "opencode", "opencode.db")
	createOpencodeDB(t, db)

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 1 {
		t.Fatalf("scanned = %d, want 1", scanned)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}

	got := sessions[0]
	if got.Agent != state.AgentOpencode {
		t.Fatalf("Agent = %q, want %q", got.Agent, state.AgentOpencode)
	}
	if got.SessionID != "ses_live" {
		t.Fatalf("SessionID = %q, want ses_live", got.SessionID)
	}
	if got.Event != state.EventDiscovered {
		t.Fatalf("Event = %q, want %q", got.Event, state.EventDiscovered)
	}
	if want := time.UnixMilli(1767225600123).UTC(); !got.StartedAt.Equal(want) {
		t.Fatalf("StartedAt = %s, want %s", got.StartedAt, want)
	}
	if want := time.UnixMilli(1767225600456).UTC(); !got.EventAt.Equal(want) {
		t.Fatalf("EventAt = %s, want %s", got.EventAt, want)
	}
	if got.Meta.Name != "Live Session" {
		t.Fatalf("Meta.Name = %q, want Live Session", got.Meta.Name)
	}
	if got.Meta.Cwd != "/work/live" {
		t.Fatalf("Meta.Cwd = %q, want /work/live", got.Meta.Cwd)
	}
	if got.Meta.Version != "1.2.3" {
		t.Fatalf("Meta.Version = %q, want 1.2.3", got.Meta.Version)
	}
	if got.Meta.Model != "gpt-5.5-fast" {
		t.Fatalf("Meta.Model = %q, want gpt-5.5-fast", got.Meta.Model)
	}
	if got.Meta.Path != db {
		t.Fatalf("Meta.Path = %q, want %q", got.Meta.Path, db)
	}
	if want := time.UnixMilli(1767225600456).UTC(); !got.Meta.UpdatedAt.Equal(want) {
		t.Fatalf("Meta.UpdatedAt = %s, want %s", got.Meta.UpdatedAt, want)
	}
}

func TestSelectLiveSessionsGroupsChildrenUnderLiveParents(t *testing.T) {
	now := time.UnixMilli(1767225602000).UTC()
	rows := []dbSession{
		{
			ID:          "parent-live",
			Directory:   "/work/live",
			Title:       "Live Parent",
			Version:     "1.2.3",
			TimeCreated: 1767225600000,
			TimeUpdated: 1767225601000,
		},
		{
			ID:          "child-closed",
			ParentID:    "parent-live",
			Directory:   "/work/live",
			Title:       "Closed Child",
			Version:     "1.2.3",
			TimeCreated: 1767225600100,
			TimeUpdated: 1767225600200,
		},
		{
			ID:          "parent-stale",
			Directory:   "/work/stale",
			Title:       "Stale Parent",
			Version:     "1.2.3",
			TimeCreated: 1767225600000,
			TimeUpdated: 1767225601100,
		},
	}

	sessions := selectLiveSessions(rows, []liveProcess{{PID: 4321, Cwd: "/work/live"}}, now, "db-path")

	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2: %#v", len(sessions), sessions)
	}
	parent := liveSessionByID(sessions, "parent-live")
	if parent.SessionID == "" {
		t.Fatalf("parent-live missing: %#v", sessions)
	}
	if parent.Meta.PID != 4321 {
		t.Fatalf("parent PID = %d, want 4321", parent.Meta.PID)
	}
	if parent.Meta.ChildCount != 1 || parent.Meta.OpenChildCount != 0 {
		t.Fatalf("parent child counts = %d/%d, want 1/0", parent.Meta.ChildCount, parent.Meta.OpenChildCount)
	}
	child := liveSessionByID(sessions, "child-closed")
	if child.SessionID == "" {
		t.Fatalf("child-closed missing: %#v", sessions)
	}
	if child.Meta.ParentSessionID != "parent-live" || child.Meta.ChildStatus != "closed" {
		t.Fatalf("child meta = %#v, want parent-live/closed", child.Meta)
	}
	if stale := liveSessionByID(sessions, "parent-stale"); stale.SessionID != "" {
		t.Fatalf("stale parent should not be live: %#v", stale)
	}
}

func TestSelectLiveSessionsUsesExplicitSessionFlag(t *testing.T) {
	now := time.UnixMilli(1767225602000).UTC()
	rows := []dbSession{
		{
			ID:          "newer-same-cwd",
			Directory:   "/work/live",
			Title:       "Newer Same Cwd",
			Version:     "1.2.3",
			TimeCreated: 1767225600000,
			TimeUpdated: 1767225601000,
		},
		{
			ID:          "explicit-live",
			Directory:   "/work/live",
			Title:       "Explicit Live",
			Version:     "1.2.3",
			TimeCreated: 1767225590000,
			TimeUpdated: 1767225591000,
		},
	}

	sessions := selectLiveSessions(rows, []liveProcess{{PID: 4321, Cwd: "/work/live", SessionID: "explicit-live"}}, now, "db-path")

	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1: %#v", len(sessions), sessions)
	}
	if sessions[0].SessionID != "explicit-live" {
		t.Fatalf("SessionID = %q, want explicit-live", sessions[0].SessionID)
	}
}

func TestSelectLiveSessionsDoesNotReopenOlderCwdSession(t *testing.T) {
	now := time.UnixMilli(1767225604000).UTC()
	rows := []dbSession{
		{
			ID:          "old-session",
			Directory:   "/work/live",
			Title:       "Old Session",
			Version:     "1.2.3",
			TimeCreated: 1767225600000,
			TimeUpdated: 1767225601000,
		},
	}

	sessions := selectLiveSessions(rows, []liveProcess{{PID: 4321, Cwd: "/work/live", StartedAt: unixMilli(1767225603000)}}, now, "db-path")

	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1 synthetic session: %#v", len(sessions), sessions)
	}
	if sessions[0].SessionID != "opencode:pid:4321" {
		t.Fatalf("SessionID = %q, want opencode:pid:4321", sessions[0].SessionID)
	}
	if sessions[0].Meta.Name != "opencode" || sessions[0].Meta.Cwd != "/work/live" {
		t.Fatalf("synthetic meta = %#v", sessions[0].Meta)
	}
	if sessions[0].Event != state.EventSessionStart {
		t.Fatalf("Event = %q, want SessionStart", sessions[0].Event)
	}
}

func TestSelectLiveSessionsOmitsSyntheticWhenRealSessionMatches(t *testing.T) {
	now := time.UnixMilli(1767225604000).UTC()
	rows := []dbSession{
		{
			ID:          "real-session",
			Directory:   "/work/live",
			Title:       "Real Session",
			Version:     "1.2.3",
			TimeCreated: 1767225603100,
			TimeUpdated: 1767225603200,
		},
	}

	sessions := selectLiveSessions(rows, []liveProcess{{PID: 4321, Cwd: "/work/live", StartedAt: unixMilli(1767225603000)}}, now, "db-path")

	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1 real session: %#v", len(sessions), sessions)
	}
	if sessions[0].SessionID != "real-session" {
		t.Fatalf("SessionID = %q, want real-session", sessions[0].SessionID)
	}
}

func TestSelectLiveSessionsMarksRecentDatabaseActivityBusy(t *testing.T) {
	now := time.UnixMilli(1767225610000).UTC()
	rows := []dbSession{
		{
			ID:          "active-session",
			Directory:   "/work/live",
			Title:       "Active Session",
			Version:     "1.2.3",
			TimeCreated: 1767225600000,
			TimeUpdated: 1767225609000,
		},
	}

	sessions := selectLiveSessions(rows, []liveProcess{{PID: 4321, Cwd: "/work/live", StartedAt: unixMilli(1767225600000)}}, now, "db-path")

	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1: %#v", len(sessions), sessions)
	}
	if got := sessions[0].EngineStatus; got != "busy" {
		t.Fatalf("EngineStatus = %q, want busy", got)
	}
}

func TestScanKeepsOpencodeBusyWhenLastUserMessageHasNoAssistantCompletion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	stubLiveProcesses(t, []liveProcess{{PID: 1234, Cwd: "/work/live"}})
	db := filepath.Join(dir, "opencode", "opencode.db")
	createOpencodeDB(t, db)
	insertOpencodeMessage(t, db, "msg_user", "ses_live", 1767225600500, `{"role":"user","time":{"created":1767225600500}}`)

	sessions, _, err := Scan()
	if err != nil {
		t.Fatal(err)
	}

	got := liveSessionByID(sessions, "ses_live")
	if got.SessionID == "" {
		t.Fatalf("ses_live missing: %#v", sessions)
	}
	if got.EngineStatus != "busy" {
		t.Fatalf("EngineStatus = %q, want busy", got.EngineStatus)
	}
}

func TestScanKeepsOpencodeBusyWhenAssistantMessageIsIncomplete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	stubLiveProcesses(t, []liveProcess{{PID: 1234, Cwd: "/work/live"}})
	db := filepath.Join(dir, "opencode", "opencode.db")
	createOpencodeDB(t, db)
	insertOpencodeMessage(t, db, "msg_user", "ses_live", 1767225600500, `{"role":"user","time":{"created":1767225600500}}`)
	insertOpencodeMessage(t, db, "msg_assistant", "ses_live", 1767225600600, `{"role":"assistant","time":{"created":1767225600600}}`)

	sessions, _, err := Scan()
	if err != nil {
		t.Fatal(err)
	}

	got := liveSessionByID(sessions, "ses_live")
	if got.SessionID == "" {
		t.Fatalf("ses_live missing: %#v", sessions)
	}
	if got.EngineStatus != "busy" {
		t.Fatalf("EngineStatus = %q, want busy", got.EngineStatus)
	}
}

func TestScanMarksRunningQuestionToolAsWaiting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	stubLiveProcesses(t, []liveProcess{{PID: 1234, Cwd: "/work/live"}})
	db := filepath.Join(dir, "opencode", "opencode.db")
	createOpencodeDB(t, db)
	insertOpencodeMessage(t, db, "msg_assistant", "ses_live", 1767225600600, `{"role":"assistant","time":{"created":1767225600600}}`)
	insertOpencodePart(t, db, "part_question", "msg_assistant", 1767225600700, `{"type":"tool","tool":"question","state":{"status":"running"}}`)

	sessions, _, err := Scan()
	if err != nil {
		t.Fatal(err)
	}

	got := liveSessionByID(sessions, "ses_live")
	if got.SessionID == "" {
		t.Fatalf("ses_live missing: %#v", sessions)
	}
	if got.EngineStatus != "busy" {
		t.Fatalf("EngineStatus = %q, want busy", got.EngineStatus)
	}
	if got.Meta.WaitingFor != "answer question" {
		t.Fatalf("WaitingFor = %q, want answer question", got.Meta.WaitingFor)
	}
}

func TestScanMarksRunningExternalDirectoryToolAsWaiting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	stubLiveProcesses(t, []liveProcess{{PID: 1234, Cwd: "/work/live"}})
	db := filepath.Join(dir, "opencode", "opencode.db")
	createOpencodeDB(t, db)
	insertOpencodeMessage(t, db, "msg_assistant", "ses_live", 1767225600600, `{"role":"assistant","path":{"cwd":"/work/live","root":"/work/live"},"time":{"created":1767225600600}}`)
	insertOpencodePart(t, db, "part_glob", "msg_assistant", 1767225600700, `{"type":"tool","tool":"glob","state":{"status":"running","input":{"pattern":"*.go","path":"/home/sboynton"}}}`)

	sessions, _, err := Scan()
	if err != nil {
		t.Fatal(err)
	}

	got := liveSessionByID(sessions, "ses_live")
	if got.SessionID == "" {
		t.Fatalf("ses_live missing: %#v", sessions)
	}
	if got.EngineStatus != "busy" {
		t.Fatalf("EngineStatus = %q, want busy", got.EngineStatus)
	}
	if got.Meta.WaitingFor != "access external directory" {
		t.Fatalf("WaitingFor = %q, want access external directory", got.Meta.WaitingFor)
	}
}

func TestApplyStoresDiscoveredWaitingForAsWaitingStatus(t *testing.T) {
	store := mustOpenStore(t)
	if !Apply(context.Background(), store, source.LiveSession{
		Agent:        state.AgentOpencode,
		SessionID:    "ses_live",
		StartedAt:    mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:        state.EventDiscovered,
		EngineStatus: "busy",
		Meta:         source.SessionMeta{PID: 4321, WaitingFor: "answer question"},
	}) {
		t.Fatal("Apply returned false")
	}

	sessions := store.Sessions()
	if got := sessions[0].WaitingFor; got != "answer question" {
		t.Fatalf("WaitingFor = %q, want answer question", got)
	}
	if got := state.DeriveStatus(sessions[0]); got != "waiting" {
		t.Fatalf("DeriveStatus = %q, want waiting", got)
	}
}

func TestApplyRefreshesOpencodeEngineStatus(t *testing.T) {
	store := mustOpenStore(t)
	if !Apply(context.Background(), store, source.LiveSession{
		Agent:        state.AgentOpencode,
		SessionID:    "ses_live",
		StartedAt:    mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:        state.EventDiscovered,
		EngineStatus: "idle",
		Meta:         source.SessionMeta{PID: 4321},
	}) {
		t.Fatal("initial Apply returned false")
	}

	if !Apply(context.Background(), store, source.LiveSession{
		Agent:        state.AgentOpencode,
		SessionID:    "ses_live",
		StartedAt:    mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:        state.EventDiscovered,
		EngineStatus: "busy",
		Meta:         source.SessionMeta{PID: 4321},
	}) {
		t.Fatal("busy Apply returned false")
	}

	sessions := store.Sessions()
	if got := sessions[0].EngineStatus; got != "busy" {
		t.Fatalf("EngineStatus = %q, want busy", got)
	}
	if got := state.DeriveStatus(sessions[0]); got != "active" {
		t.Fatalf("DeriveStatus = %q, want active", got)
	}
}

func TestApplySetsOpencodeEngineStatusOnInsert(t *testing.T) {
	store := mustOpenStore(t)
	if !Apply(context.Background(), store, source.LiveSession{
		Agent:        state.AgentOpencode,
		SessionID:    "ses_live",
		StartedAt:    mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:        state.EventDiscovered,
		EngineStatus: "busy",
		Meta:         source.SessionMeta{PID: 4321},
	}) {
		t.Fatal("Apply returned false")
	}

	sessions := store.Sessions()
	if got := sessions[0].EngineStatus; got != "busy" {
		t.Fatalf("EngineStatus = %q, want busy", got)
	}
	if got := state.DeriveStatus(sessions[0]); got != "active" {
		t.Fatalf("DeriveStatus = %q, want active", got)
	}
}

func TestModelNameParsesModelID(t *testing.T) {
	got := modelName(`{"modelID":"gpt-5.5","providerID":"openai"}`)
	if got != "gpt-5.5" {
		t.Fatalf("modelName = %q, want gpt-5.5", got)
	}
}

func TestApplyInsertAlwaysUsesDiscoveredEvent(t *testing.T) {
	store := mustOpenStore(t)

	if !Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentOpencode,
		SessionID: "ses_live",
		StartedAt: mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:     state.EventSessionStart,
		Meta:      source.SessionMeta{PID: 4321},
	}) {
		t.Fatal("Apply returned false")
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if got := sessions[0].LastEvent; got != state.EventDiscovered {
		t.Fatalf("LastEvent = %q, want %q", got, state.EventDiscovered)
	}
	if got := sessions[0].PID; got != 4321 {
		t.Fatalf("PID = %d, want 4321", got)
	}
}

func TestApplyRefreshesPIDWithoutClobberingHookFields(t *testing.T) {
	store := mustOpenStore(t)
	lastEventAt := "2026-05-06T22:00:05Z"
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentOpencode,
		SessionID:  "ses_live",
		Event:      state.EventUserPromptSubmit,
		TurnID:     "turn-1",
		ReceivedAt: lastEventAt,
	}); err != nil {
		t.Fatal(err)
	}

	changed := Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentOpencode,
		SessionID: "ses_live",
		StartedAt: mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:     state.EventDiscovered,
		Meta:      source.SessionMeta{PID: 4321},
	})
	if !changed {
		t.Fatal("Apply returned false")
	}

	sessions := store.Sessions()
	if got := sessions[0].PID; got != 4321 {
		t.Fatalf("PID = %d, want 4321", got)
	}
	if got := sessions[0].LastEvent; got != state.EventUserPromptSubmit {
		t.Fatalf("LastEvent = %q, want %q", got, state.EventUserPromptSubmit)
	}
	if got := sessions[0].LastEventAt; got != lastEventAt {
		t.Fatalf("LastEventAt = %q, want %q", got, lastEventAt)
	}
	if got := sessions[0].TurnID; got != "turn-1" {
		t.Fatalf("TurnID = %q, want turn-1", got)
	}
}

func TestApplyIdentifiesUnidentifiedSessionWithoutClobberingHookFields(t *testing.T) {
	store := mustOpenStore(t)
	lastEventAt := "2026-05-06T22:00:05Z"
	if _, err := store.RecordEvent(context.Background(), state.HookEvent{
		Agent:      state.AgentUnidentified,
		SessionID:  "ses_live",
		Event:      state.EventUserPromptSubmit,
		TurnID:     "turn-1",
		ReceivedAt: lastEventAt,
	}); err != nil {
		t.Fatal(err)
	}

	changed := Apply(context.Background(), store, source.LiveSession{
		Agent:     state.AgentOpencode,
		SessionID: "ses_live",
		StartedAt: mustParseTime(t, "2026-05-06T22:00:00Z"),
		Event:     state.EventDiscovered,
	})
	if !changed {
		t.Fatal("Apply returned false")
	}

	sessions := store.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	if got := sessions[0].Agent; got != state.AgentOpencode {
		t.Fatalf("Agent = %q, want %q", got, state.AgentOpencode)
	}
	if got := sessions[0].LastEvent; got != state.EventUserPromptSubmit {
		t.Fatalf("LastEvent = %q, want %q", got, state.EventUserPromptSubmit)
	}
	if got := sessions[0].LastEventAt; got != lastEventAt {
		t.Fatalf("LastEventAt = %q, want %q", got, lastEventAt)
	}
	if got := sessions[0].TurnID; got != "turn-1" {
		t.Fatalf("TurnID = %q, want turn-1", got)
	}
}

func TestWatchAppliesScanOnDBChange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	stubLiveProcesses(t, []liveProcess{{PID: 1234, Cwd: "/work/live"}, {PID: 1234, Cwd: "/work/second"}})
	db := filepath.Join(dir, "opencode", "opencode.db")
	createOpencodeDB(t, db)
	store := mustOpenStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, store)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Watch returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Watch did not exit after cancellation")
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := appendOpencodeComment(db); err != nil {
			t.Fatal(err)
		}
		if waitForOpencodeSession(store, "ses_live", 300*time.Millisecond) {
			break
		}
	}
	if !waitForOpencodeSession(store, "ses_live", 0) {
		t.Fatal("watch did not apply scanned opencode session")
	}

	insertOpencodeSession(t, db, "ses_second")
	if !waitForOpencodeSession(store, "ses_second", 3*time.Second) {
		t.Fatal("watch did not apply second scanned opencode session")
	}
}

func appendOpencodeComment(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("pragma user_version = 1")
	return err
}

func waitForOpencodeSession(store *state.Store, id string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		for _, sess := range store.Sessions() {
			if sess.SessionID == id && sess.Agent == state.AgentOpencode {
				return true
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func insertOpencodeSession(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insert := `insert into session (
		id, directory, title, version, time_created, time_updated, time_archived, model
	) values (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := db.Exec(insert, id, "/work/second", "Second Session", "1.2.3", int64(1767225601123), int64(1767225601456), nil, `{"id":"gpt-5.5-fast"}`); err != nil {
		t.Fatal(err)
	}
}

func createOpencodeDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`create table session (
		id text primary key, project_id text, parent_id text, slug text,
		directory text not null, title text not null, version text not null,
		share_url text, summary_additions integer, summary_deletions integer,
		summary_files integer, summary_diffs text, revert text, permission text,
		time_created integer not null, time_updated integer not null,
		time_compacting integer, time_archived integer, workspace_id text, path text,
		agent text, model text, cost real not null default 0,
		tokens_input integer not null default 0,
		tokens_output integer not null default 0,
		tokens_reasoning integer not null default 0,
		tokens_cache_read integer not null default 0,
		tokens_cache_write integer not null default 0
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table message (
		id text primary key, session_id text not null, time_created integer not null,
		time_updated integer not null, data text not null
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table part (
		id text primary key, message_id text not null, time_created integer not null,
		data text not null
	)`); err != nil {
		t.Fatal(err)
	}
	insert := `insert into session (
		id, directory, title, version, time_created, time_updated, time_archived, model
	) values (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := db.Exec(insert, "ses_live", "/work/live", "Live Session", "1.2.3", int64(1767225600123), int64(1767225600456), nil, `{"id":"gpt-5.5-fast","providerID":"openai"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, "ses_archived", "/work/old", "Archived Session", "1.2.3", int64(1767225600000), int64(1767225600000), int64(1767225601000), `{"id":"gpt-old"}`); err != nil {
		t.Fatal(err)
	}
}

func insertOpencodeMessage(t *testing.T, path, id, sessionID string, created int64, data string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`insert into message (id, session_id, time_created, time_updated, data) values (?, ?, ?, ?, ?)`, id, sessionID, created, created, data)
	if err != nil {
		t.Fatal(err)
	}
}

func insertOpencodePart(t *testing.T, path, id, messageID string, created int64, data string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`insert into part (id, message_id, time_created, data) values (?, ?, ?, ?)`, id, messageID, created, data)
	if err != nil {
		t.Fatal(err)
	}
}

func mustOpenStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func liveSessionByID(sessions []source.LiveSession, id string) source.LiveSession {
	for _, sess := range sessions {
		if sess.SessionID == id {
			return sess
		}
	}
	return source.LiveSession{}
}

func stubLiveProcesses(t *testing.T, procs []liveProcess) {
	t.Helper()
	original := scanLiveProcesses
	scanLiveProcesses = func() []liveProcess { return procs }
	t.Cleanup(func() { scanLiveProcesses = original })
}
