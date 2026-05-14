// Package codex is the Codex discovery backend: it reads Codex's SQLite
// databases (state_*.sqlite, logs_*.sqlite) under ~/.codex/ plus recent rollout
// JSONLs, and translates them into the shared source.LiveSession shape.
//
// Codex emits no SessionEnd hook, so end-of-life is detected via PID liveness
// on linked processes. The push-side counterpart lives in watch.go: an
// fsnotify watcher on ~/.codex/shell_snapshots/ catches the per-session
// snapshot Codex drops at session open, so freshly-launched CLIs land in
// the store immediately rather than waiting for the next periodic Scan tick.
// (The rollout JSONL doesn't exist until first turn, so it's not a useful
// session-open signal; Scan picks up its metadata once it appears.)
package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type thread struct {
	ID          string
	RolloutPath string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Source      string
	Cwd         string
	Version     string
	Model       string
	GitBranch   string
	Archived    bool
}

type process struct {
	PID      int
	LatestAt time.Time
}

const unlinkedThreadGrace = 30 * time.Minute

// freshSessionWindow labels very new threads as SessionStart.
const freshSessionWindow = 30 * time.Second

// Scan returns the currently-live Codex sessions: threads from
// state_*.sqlite (merged with recent rollout JSONLs as a fallback) whose
// linked process is still alive, or which are recent enough to be treated as
// "starting up" without a process link yet.
func Scan() ([]source.LiveSession, int, error) {
	dir, err := homeDir()
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	threads := []thread{}
	statePath, ok, err := newestSQLite(dir, "state_*.sqlite")
	if err != nil {
		return nil, 0, err
	}
	if ok {
		threads, err = loadThreads(statePath)
		if err != nil {
			return nil, 0, err
		}
	}

	rolloutThreads, err := loadRecentRolloutThreads(dir, now.Add(-unlinkedThreadGrace))
	if err != nil {
		return nil, len(threads), err
	}
	threads = mergeThreads(threads, rolloutThreads)

	// shell_snapshots are the only on-disk evidence of a Codex CLI that's
	// open but hasn't taken its first turn yet (Codex defers state_*.sqlite
	// + rollout writes until then). Including them here keeps such sessions
	// "alive" across Scan ticks so the reaper doesn't kill them.
	snapshotThreads, err := loadRecentShellSnapshotThreads(dir, now.Add(-unlinkedThreadGrace))
	if err != nil {
		return nil, len(threads), err
	}
	threads = mergeThreads(threads, snapshotThreads)

	processes := map[string]process{}
	if logsPath, ok, err := newestSQLite(dir, "logs_*.sqlite"); err != nil {
		return nil, len(threads), err
	} else if ok {
		processes, err = loadProcesses(logsPath)
		if err != nil {
			return nil, len(threads), err
		}
	}

	out := make([]source.LiveSession, 0, len(threads))
	for _, th := range threads {
		if th.Archived {
			slog.Debug("codex scan: skip archived",
				"session", state.ShortID(th.ID))
			continue
		}
		proc, hasProc := processes[th.ID]
		if hasProc && (proc.PID <= 0 || !source.PIDAlive(proc.PID)) {
			slog.Debug("codex scan: skip dead linked process",
				"session", state.ShortID(th.ID),
				"pid", proc.PID)
			continue
		}
		updatedAt := th.UpdatedAt
		if proc.LatestAt.After(updatedAt) {
			updatedAt = proc.LatestAt
		}
		if !hasProc && !recentUnlinkedThread(th, now) {
			slog.Debug("codex scan: skip stale unlinked thread",
				"session", state.ShortID(th.ID),
				"created_at", th.CreatedAt,
				"updated_at", th.UpdatedAt,
				"age", now.Sub(th.UpdatedAt).Round(time.Second))
			continue
		}
		event := state.EventDiscovered
		eventAt := updatedAt
		// ReconcileDiscovered only honors SessionStart on first insert.
		if !th.CreatedAt.IsZero() && now.Sub(th.CreatedAt) < freshSessionWindow {
			event = state.EventSessionStart
			eventAt = th.CreatedAt
		}
		out = append(out, source.LiveSession{
			Agent:     state.AgentCodex,
			SessionID: th.ID,
			StartedAt: th.CreatedAt,
			Event:     event,
			EventAt:   eventAt,
			Meta: source.SessionMeta{
				PID:        proc.PID,
				Entrypoint: th.Source,
				Cwd:        th.Cwd,
				Version:    th.Version,
				Model:      th.Model,
				Path:       th.RolloutPath,
				UpdatedAt:  updatedAt,
				WaitingFor: detectWaitingFor(th.RolloutPath),
			},
		})
	}
	return out, len(threads), nil
}

// Apply upserts a scanned codex session into the store.
//
// Codex emits no SessionEnd hook and only fires SessionStart on the first
// turn (not at CLI launch), so periodic discovery is the only reliable signal
// for "this session exists" before any hooks have fired. Apply expresses that
// policy:
//
//   - On first sight, insert a row stamped with sess.Event ("SessionStart"
//     for fresh threads, "Discovered" otherwise).
//   - On already-known rows, refine durable metadata only: agent identity,
//     an earlier FirstSeenAt if discovery learned a more accurate creation
//     timestamp, and an initial StatusAt if it was never stamped.
//
// Apply deliberately never touches LastEvent / LastEventAt / EngineStatus /
// TurnID on existing rows so hook-driven progress isn't clobbered by a later
// discovery tick.
func Apply(ctx context.Context, s *state.Store, sess source.LiveSession) bool {
	createdAt := sess.StartedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)
	insertEvent := sess.Event
	if insertEvent == "" {
		insertEvent = state.EventDiscovered
	}

	inserted, err := s.InsertSession(ctx, state.Session{
		SessionID:   sess.SessionID,
		Agent:       sess.Agent,
		PID:         sess.Meta.PID,
		FirstSeenAt: ts,
		LastEvent:   insertEvent,
		LastEventAt: ts,
		StatusAt:    ts,
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: codex insert failed",
			"session", state.ShortID(sess.SessionID), "err", err)
		return false
	}
	if inserted {
		slog.InfoContext(ctx, "discovery: codex session discovered",
			"session", state.ShortID(sess.SessionID), "event", insertEvent)
		return true
	}

	var (
		identified bool
		priorAgent string
	)
	changed, err := s.UpdateSession(ctx, sess.SessionID, func(stored *state.Session) bool {
		var changed bool
		// Identify (or re-identify) Agent if a hook stamped it before us with
		// the placeholder. We never downgrade a concrete label.
		if stored.Agent == "" || stored.Agent == state.AgentUnidentified {
			priorAgent = stored.Agent
			stored.Agent = sess.Agent
			identified = true
			changed = true
		}
		// Refresh PID when the freshly-scanned value differs and is non-zero;
		// codex sessions can transition from unlinked (PID=0) to linked once
		// the logs SQLite catches up. Never overwrite a known PID with zero.
		if sess.Meta.PID > 0 && stored.PID != sess.Meta.PID {
			stored.PID = sess.Meta.PID
			changed = true
		}
		if stored.FirstSeenAt == "" || ts < stored.FirstSeenAt {
			stored.FirstSeenAt = ts
			changed = true
		}
		if stored.StatusAt == "" {
			stored.StatusAt = stored.LastEventAt
			if stored.StatusAt == "" {
				stored.StatusAt = ts
			}
			changed = true
		}
		return changed
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: codex refine failed",
			"session", state.ShortID(sess.SessionID), "err", err)
		return false
	}
	if identified {
		slog.InfoContext(ctx, "discovery: agent identified",
			"session", state.ShortID(sess.SessionID),
			"from", priorAgent, "to", sess.Agent)
	}
	if changed {
		slog.DebugContext(ctx, "discovery: codex session refined",
			"session", state.ShortID(sess.SessionID))
	}
	return changed
}

func mergeThreads(primary, fallback []thread) []thread {
	index := make(map[string]int, len(primary))
	for i, th := range primary {
		if th.ID != "" {
			index[th.ID] = i
		}
	}
	for _, th := range fallback {
		if th.ID == "" {
			continue
		}
		i, ok := index[th.ID]
		if !ok {
			index[th.ID] = len(primary)
			primary = append(primary, th)
			continue
		}
		primary[i] = mergeThread(primary[i], th)
	}
	return primary
}

func mergeThread(a, b thread) thread {
	if a.RolloutPath == "" {
		a.RolloutPath = b.RolloutPath
	}
	if a.CreatedAt.IsZero() || (!b.CreatedAt.IsZero() && b.CreatedAt.Before(a.CreatedAt)) {
		a.CreatedAt = b.CreatedAt
	}
	if b.UpdatedAt.After(a.UpdatedAt) {
		a.UpdatedAt = b.UpdatedAt
	}
	if a.Source == "" {
		a.Source = b.Source
	}
	if a.Cwd == "" {
		a.Cwd = b.Cwd
	}
	if a.Version == "" {
		a.Version = b.Version
	}
	if a.Model == "" {
		a.Model = b.Model
	}
	if a.GitBranch == "" {
		a.GitBranch = b.GitBranch
	}
	return a
}

func recentUnlinkedThread(th thread, now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-unlinkedThreadGrace)
	return th.CreatedAt.After(cutoff) || th.UpdatedAt.After(cutoff)
}

// loadRecentShellSnapshotThreads synthesizes thread rows from the
// <UUID>.<nanoTs>.sh files Codex writes at session open, scoped to the same
// unlinkedThreadGrace window as recent rollouts. Returned threads carry
// just ID + timestamps + Source="cli"; richer fields come in via mergeThreads
// once state_*.sqlite or a rollout JSONL appears for the same session.
func loadRecentShellSnapshotThreads(dir string, cutoff time.Time) ([]thread, error) {
	snapshotDir := filepath.Join(dir, "shell_snapshots")
	entries, err := os.ReadDir(snapshotDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []thread
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(snapshotDir, e.Name())
		if !isShellSnapshotPath(path) {
			continue
		}
		id := threadIDFromShellSnapshotPath(path)
		if id == "" {
			continue
		}
		createdAt := shellSnapshotTimestamp(path)
		if createdAt.IsZero() {
			info, statErr := e.Info()
			if statErr != nil {
				continue
			}
			createdAt = info.ModTime()
		}
		if createdAt.Before(cutoff) {
			continue
		}
		out = append(out, thread{
			ID:        id,
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
			Source:    "cli",
		})
	}
	return out, nil
}

func loadRecentRolloutThreads(dir string, cutoff time.Time) ([]thread, error) {
	sessionsDir := filepath.Join(dir, "sessions")
	var out []thread
	err := filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" || !strings.HasPrefix(filepath.Base(path), "rollout-") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		th, ok := loadRolloutThread(path, info.ModTime())
		if ok {
			out = append(out, th)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

func loadRolloutThread(path string, modTime time.Time) (thread, bool) {
	f, err := os.Open(path)
	if err != nil {
		return thread{}, false
	}
	defer f.Close()

	var (
		found    thread
		ok       bool
		parseErr bool
	)
	_ = source.ScanJSONL(f, func(buf []byte) bool {
		var line transcriptLine
		if err := json.Unmarshal(buf, &line); err != nil || line.Type != "session_meta" {
			return true
		}
		var payload sessionMeta
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			parseErr = true
			return false
		}
		id := payload.ID
		if id == "" {
			id = threadIDFromRolloutPath(path)
		}
		if id == "" {
			parseErr = true
			return false
		}
		createdAt := parseTimestamp(payload.Timestamp)
		if createdAt.IsZero() {
			createdAt = modTime
		}
		found = thread{
			ID:          id,
			RolloutPath: path,
			CreatedAt:   createdAt,
			UpdatedAt:   modTime,
			Source:      payload.Source,
			Cwd:         payload.Cwd,
			Version:     payload.CLIVersion,
			Model:       payload.Model,
			GitBranch:   payload.Git.Branch,
		}
		ok = true
		return false
	})
	if parseErr {
		return thread{}, false
	}
	if ok {
		return found, true
	}
	// No session_meta line found. Codex creates the rollout JSONL at session
	// open but only writes session_meta on the first turn, so a no-turn
	// session has an empty file. Synthesize a minimal thread from the
	// filename (which embeds the UUID) and file mtime; later turns will
	// refine via Apply, and the state_*.sqlite path will overlay richer
	// metadata once Codex flushes.
	id := threadIDFromRolloutPath(path)
	if id == "" {
		return thread{}, false
	}
	return thread{
		ID:          id,
		RolloutPath: path,
		CreatedAt:   modTime,
		UpdatedAt:   modTime,
		Source:      "cli",
	}, true
}

func threadIDFromRolloutPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if len(base) < 36 {
		return ""
	}
	id := base[len(base)-36:]
	if _, err := uuid.Parse(id); err != nil {
		return ""
	}
	return id
}

func parseTimestamp(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func homeDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func newestSQLite(dir, pattern string) (string, bool, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return "", false, err
	}
	matches = filterSQLiteFiles(matches)
	if len(matches) == 0 {
		return "", false, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return sqliteVersion(matches[i]) > sqliteVersion(matches[j])
	})
	return matches[0], true, nil
}

func filterSQLiteFiles(paths []string) []string {
	out := paths[:0]
	for _, p := range paths {
		if strings.HasSuffix(p, ".sqlite") {
			out = append(out, p)
		}
	}
	return out
}

func sqliteVersion(path string) int {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".sqlite")
	i := strings.LastIndexByte(base, '_')
	if i < 0 || i == len(base)-1 {
		return 0
	}
	n, _ := strconv.Atoi(base[i+1:])
	return n
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(1000)")
	u.RawQuery = q.Encode()
	return sql.Open("sqlite", u.String())
}

func loadThreads(path string) ([]thread, error) {
	db, err := openSQLiteReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		select
			id,
			rollout_path,
			created_at,
			updated_at,
			created_at_ms,
			updated_at_ms,
			source,
			cwd,
			archived,
			cli_version,
			model,
			git_branch
		from threads
		where archived = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []thread
	for rows.Next() {
		var th thread
		var createdSec, updatedSec int64
		var createdMS, updatedMS sql.NullInt64
		var version, model, gitBranch sql.NullString
		var archived int
		if err := rows.Scan(
			&th.ID,
			&th.RolloutPath,
			&createdSec,
			&updatedSec,
			&createdMS,
			&updatedMS,
			&th.Source,
			&th.Cwd,
			&archived,
			&version,
			&model,
			&gitBranch,
		); err != nil {
			return nil, err
		}
		th.CreatedAt = sqliteTime(createdSec, createdMS)
		th.UpdatedAt = sqliteTime(updatedSec, updatedMS)
		th.Version = version.String
		th.Model = model.String
		th.GitBranch = gitBranch.String
		th.Archived = archived != 0
		out = append(out, th)
	}
	return out, rows.Err()
}

func loadProcesses(path string) (map[string]process, error) {
	db, err := openSQLiteReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		select thread_id, process_uuid, ts, ts_nanos, feedback_log_body
		from logs
		where process_uuid like 'pid:%'
		  and (thread_id is not null or feedback_log_body like '%conversation.id=%')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]process{}
	for rows.Next() {
		var threadID sql.NullString
		var processUUID string
		var ts, tsNanos int64
		var body sql.NullString
		if err := rows.Scan(&threadID, &processUUID, &ts, &tsNanos, &body); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(threadID.String)
		if id == "" {
			id = parseConversationID(body.String)
		}
		if id == "" {
			continue
		}
		pid := parsePID(processUUID)
		if pid <= 0 {
			continue
		}
		latestAt := time.Unix(ts, tsNanos)
		if existing, ok := out[id]; ok && !latestAt.After(existing.LatestAt) {
			continue
		}
		out[id] = process{PID: pid, LatestAt: latestAt}
	}
	return out, rows.Err()
}

func parseConversationID(body string) string {
	const key = "conversation.id="
	_, after, ok := strings.Cut(body, key)
	if !ok || len(after) < 36 {
		return ""
	}
	id := after[:36]
	if _, err := uuid.Parse(id); err != nil {
		return ""
	}
	return id
}

func sqliteTime(sec int64, ms sql.NullInt64) time.Time {
	if ms.Valid && ms.Int64 > 0 {
		return time.UnixMilli(ms.Int64)
	}
	if sec > 0 {
		return time.Unix(sec, 0)
	}
	return time.Time{}
}

func parsePID(processUUID string) int {
	rest, ok := strings.CutPrefix(processUUID, "pid:")
	if !ok {
		return 0
	}
	pidText, _, found := strings.Cut(rest, ":")
	if !found {
		return 0
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return 0
	}
	return pid
}

// Rollout-line shapes used by Scan to extract session metadata. The shared
// transcript parsers in the parent discovery package use their own copies.
type transcriptLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
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
