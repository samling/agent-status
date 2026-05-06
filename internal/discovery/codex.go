package discovery

import (
	"database/sql"
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

func scanCodexLive() ([]liveAgentSession, int, error) {
	dir, err := codexDir()
	if err != nil {
		return nil, 0, err
	}
	statePath, ok, err := newestSQLite(dir, "state_*.sqlite")
	if err != nil || !ok {
		return nil, 0, err
	}
	threads, err := loadCodexThreads(statePath)
	if err != nil {
		return nil, 0, err
	}

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
			continue
		}
		proc := processes[th.ID]
		if proc.PID <= 0 || !pidAlive(proc.PID) {
			continue
		}
		updatedAt := th.UpdatedAt
		if proc.LatestAt.After(updatedAt) {
			updatedAt = proc.LatestAt
		}
		out = append(out, liveAgentSession{
			Agent:     state.AgentCodex,
			SessionID: th.ID,
			StartedAt: th.CreatedAt,
			Event:     "Discovered",
			EventAt:   updatedAt,
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
		select l.thread_id, l.process_uuid, l.ts, l.ts_nanos
		from logs l
		join (
			select thread_id, max(ts * 1000000000 + ts_nanos) as latest
			from logs
			where thread_id is not null and process_uuid like 'pid:%'
			group by thread_id
		) latest
		  on latest.thread_id = l.thread_id
		 and latest.latest = (l.ts * 1000000000 + l.ts_nanos)
		where l.process_uuid like 'pid:%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]codexProcess{}
	for rows.Next() {
		var threadID, processUUID string
		var ts, tsNanos int64
		if err := rows.Scan(&threadID, &processUUID, &ts, &tsNanos); err != nil {
			return nil, err
		}
		pid := parseCodexPID(processUUID)
		if pid <= 0 {
			continue
		}
		out[threadID] = codexProcess{PID: pid, LatestAt: time.Unix(ts, tsNanos)}
	}
	return out, rows.Err()
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
