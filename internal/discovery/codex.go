package discovery

import (
	"bufio"
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

	_ "modernc.org/sqlite"

	"github.com/samling/agent-status/internal/state"
)

type codexThread struct {
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

type codexProcess struct {
	PID      int
	LatestAt time.Time
}

const codexUnlinkedThreadGrace = 30 * time.Minute

// codexFreshSessionWindow is the grace period during which a newly
// observed thread is treated as a brand-new session: the discovery
// loop labels it "SessionStart" so the UI can distinguish "just
// started" from "already running, just noticed". Wider than one poll
// interval so a session created right before the watcher boots is
// still labelled correctly on the initial sweep.
const codexFreshSessionWindow = 30 * time.Second

func scanCodexLive() ([]liveAgentSession, int, error) {
	dir, err := codexDir()
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	threads := []codexThread{}
	statePath, ok, err := newestSQLite(dir, "state_*.sqlite")
	if err != nil {
		return nil, 0, err
	}
	if ok {
		threads, err = loadCodexThreads(statePath)
		if err != nil {
			return nil, 0, err
		}
	}

	rolloutThreads, err := loadRecentCodexRolloutThreads(dir, now.Add(-codexUnlinkedThreadGrace))
	if err != nil {
		return nil, len(threads), err
	}
	threads = mergeCodexThreads(threads, rolloutThreads)

	processes := map[string]codexProcess{}
	if logsPath, ok, err := newestSQLite(dir, "logs_*.sqlite"); err != nil {
		return nil, len(threads), err
	} else if ok {
		processes, err = loadCodexProcesses(logsPath)
		if err != nil {
			return nil, len(threads), err
		}
	}

	out := make([]liveAgentSession, 0, len(threads))
	for _, th := range threads {
		if th.Archived {
			slog.Debug("codex scan: skip archived",
				"session", state.ShortID(th.ID))
			continue
		}
		proc, hasProc := processes[th.ID]
		if hasProc && (proc.PID <= 0 || !pidAlive(proc.PID)) {
			slog.Debug("codex scan: skip dead linked process",
				"session", state.ShortID(th.ID),
				"pid", proc.PID)
			continue
		}
		updatedAt := th.UpdatedAt
		if proc.LatestAt.After(updatedAt) {
			updatedAt = proc.LatestAt
		}
		if !hasProc && !recentUnlinkedCodexThread(th, now) {
			slog.Debug("codex scan: skip stale unlinked thread",
				"session", state.ShortID(th.ID),
				"created_at", th.CreatedAt,
				"updated_at", th.UpdatedAt,
				"age", now.Sub(th.UpdatedAt).Round(time.Second))
			continue
		}
		event := "Discovered"
		eventAt := updatedAt
		// Fresh thread → emit SessionStart so first-time insertion in
		// state records the actual lifecycle event instead of a
		// generic "Discovered". The state store's ReconcileDiscovered
		// only honors this on insert, so a re-poll of the same fresh
		// session won't clobber later hook-driven events.
		if !th.CreatedAt.IsZero() && now.Sub(th.CreatedAt) < codexFreshSessionWindow {
			event = "SessionStart"
			eventAt = th.CreatedAt
		}
		out = append(out, liveAgentSession{
			Agent:     state.AgentCodex,
			SessionID: th.ID,
			StartedAt: th.CreatedAt,
			Event:     event,
			EventAt:   eventAt,
			Meta: SessionMeta{
				Agent:      state.AgentCodex,
				PID:        proc.PID,
				Entrypoint: th.Source,
				Cwd:        th.Cwd,
				Version:    th.Version,
				Model:      th.Model,
				Path:       th.RolloutPath,
				UpdatedAt:  updatedAt,
			},
		})
	}
	return out, len(threads), nil
}

func mergeCodexThreads(primary, fallback []codexThread) []codexThread {
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
		primary[i] = mergeCodexThread(primary[i], th)
	}
	return primary
}

func mergeCodexThread(a, b codexThread) codexThread {
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

func recentUnlinkedCodexThread(th codexThread, now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-codexUnlinkedThreadGrace)
	return th.CreatedAt.After(cutoff) || th.UpdatedAt.After(cutoff)
}

func loadRecentCodexRolloutThreads(dir string, cutoff time.Time) ([]codexThread, error) {
	sessionsDir := filepath.Join(dir, "sessions")
	var out []codexThread
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
		th, ok := loadCodexRolloutThread(path, info.ModTime())
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

func loadCodexRolloutThread(path string, modTime time.Time) (codexThread, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexThread{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		var line codexTranscriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil || line.Type != "session_meta" {
			continue
		}
		var payload codexSessionMeta
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			return codexThread{}, false
		}
		id := payload.ID
		if id == "" {
			id = codexThreadIDFromRolloutPath(path)
		}
		if id == "" {
			return codexThread{}, false
		}
		createdAt := parseCodexTimestamp(payload.Timestamp)
		if createdAt.IsZero() {
			createdAt = modTime
		}
		return codexThread{
			ID:          id,
			RolloutPath: path,
			CreatedAt:   createdAt,
			UpdatedAt:   modTime,
			Source:      payload.Source,
			Cwd:         payload.Cwd,
			Version:     payload.CLIVersion,
			Model:       payload.Model,
			GitBranch:   payload.Git.Branch,
		}, true
	}
	return codexThread{}, false
}

func codexThreadIDFromRolloutPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if len(base) < 36 {
		return ""
	}
	id := base[len(base)-36:]
	if strings.Count(id, "-") != 4 {
		return ""
	}
	return id
}

func parseCodexTimestamp(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func codexDir() (string, error) {
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

func loadCodexThreads(path string) ([]codexThread, error) {
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

	var out []codexThread
	for rows.Next() {
		var th codexThread
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
		th.CreatedAt = codexTime(createdSec, createdMS)
		th.UpdatedAt = codexTime(updatedSec, updatedMS)
		th.Version = version.String
		th.Model = model.String
		th.GitBranch = gitBranch.String
		th.Archived = archived != 0
		out = append(out, th)
	}
	return out, rows.Err()
}

func loadCodexProcesses(path string) (map[string]codexProcess, error) {
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

	out := map[string]codexProcess{}
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
			id = parseCodexConversationID(body.String)
		}
		if id == "" {
			continue
		}
		pid := parseCodexPID(processUUID)
		if pid <= 0 {
			continue
		}
		latestAt := time.Unix(ts, tsNanos)
		if existing, ok := out[id]; ok && !latestAt.After(existing.LatestAt) {
			continue
		}
		out[id] = codexProcess{PID: pid, LatestAt: latestAt}
	}
	return out, rows.Err()
}

func parseCodexConversationID(body string) string {
	const key = "conversation.id="
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	id := body[i+len(key):]
	if len(id) < 36 {
		return ""
	}
	id = id[:36]
	if strings.Count(id, "-") != 4 {
		return ""
	}
	return id
}

func codexTime(sec int64, ms sql.NullInt64) time.Time {
	if ms.Valid && ms.Int64 > 0 {
		return time.UnixMilli(ms.Int64)
	}
	if sec > 0 {
		return time.Unix(sec, 0)
	}
	return time.Time{}
}

func parseCodexPID(processUUID string) int {
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
