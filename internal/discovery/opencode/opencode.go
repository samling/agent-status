// Package opencode is the Opencode discovery backend: it reads Opencode's
// SQLite session database and translates unarchived rows into LiveSession.
package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type dbSession struct {
	ID          string
	ParentID    string
	Directory   string
	Title       string
	Version     string
	TimeCreated int64
	TimeUpdated int64
	Model       string
	Runtime     sessionRuntime
}

type sessionRuntime struct {
	EngineStatus string
	CurrentTool  string
	WaitingFor   string
}

type messageData struct {
	Role   string `json:"role"`
	Finish string `json:"finish"`
	Time   struct {
		Completed int64 `json:"completed"`
	} `json:"time"`
}

type partData struct {
	Type  string `json:"type"`
	Tool  string `json:"tool"`
	State struct {
		Status string `json:"status"`
	} `json:"state"`
}

type liveProcess struct {
	PID       int
	Cwd       string
	SessionID string
	StartedAt time.Time
}

var scanLiveProcesses = liveProcesses

const activeDBUpdateWindow = 10 * time.Second

// Scan returns unarchived Opencode sessions from the local SQLite database.
func Scan() ([]source.LiveSession, int, error) {
	path, err := dbPath()
	if err != nil {
		return nil, 0, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	} else if err != nil {
		return nil, 0, err
	}

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()

	rows, err := db.Query(`select id, parent_id, directory, title, version, time_created, time_updated, model from session where time_archived is null`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	rowsOut := []dbSession{}
	for rows.Next() {
		var (
			row      dbSession
			parentID sql.NullString
			model    sql.NullString
		)
		if err := rows.Scan(&row.ID, &parentID, &row.Directory, &row.Title, &row.Version, &row.TimeCreated, &row.TimeUpdated, &model); err != nil {
			return nil, len(rowsOut), err
		}
		row.ParentID = parentID.String
		row.Model = model.String
		rowsOut = append(rowsOut, row)
	}
	if err := rows.Err(); err != nil {
		return nil, len(rowsOut), err
	}
	applyRuntimeState(db, rowsOut)
	return selectLiveSessions(rowsOut, scanLiveProcesses(), time.Now(), path), len(rowsOut), nil
}

func applyRuntimeState(db *sql.DB, rows []dbSession) {
	for i := range rows {
		rows[i].Runtime = runtimeState(db, rows[i].ID)
	}
}

func runtimeState(db *sql.DB, sessionID string) sessionRuntime {
	if sessionID == "" {
		return sessionRuntime{}
	}
	var msgID, raw string
	err := db.QueryRow(`select id, data from message where session_id = ? order by time_created desc limit 1`, sessionID).Scan(&msgID, &raw)
	if err != nil {
		return sessionRuntime{}
	}
	var msg messageData
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return sessionRuntime{}
	}
	switch msg.Role {
	case "user":
		return sessionRuntime{EngineStatus: "busy"}
	case "assistant":
		tool, hasIncompleteTool, waitingFor := latestToolState(db, msgID)
		if msg.Finish == "tool-calls" || hasIncompleteTool {
			return sessionRuntime{EngineStatus: "busy", CurrentTool: tool, WaitingFor: waitingFor}
		}
		if msg.Finish == "" && msg.Time.Completed == 0 {
			return sessionRuntime{EngineStatus: "busy", CurrentTool: tool, WaitingFor: waitingFor}
		}
		return sessionRuntime{EngineStatus: "idle", CurrentTool: tool, WaitingFor: waitingFor}
	default:
		return sessionRuntime{}
	}
}

func latestToolState(db *sql.DB, messageID string) (string, bool, string) {
	rows, err := db.Query(`select data from part where message_id = ? order by time_created asc`, messageID)
	if err != nil {
		return "", false, ""
	}
	defer rows.Close()
	var tool string
	incomplete := false
	waitingFor := ""
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var part partData
		if err := json.Unmarshal([]byte(raw), &part); err != nil || part.Type != "tool" {
			continue
		}
		if part.Tool != "" {
			tool = part.Tool
		}
		if part.State.Status != "completed" && part.State.Status != "error" {
			incomplete = true
			if part.Tool == "question" {
				waitingFor = "answer question"
			}
		}
	}
	return tool, incomplete, waitingFor
}

func selectLiveSessions(rows []dbSession, procs []liveProcess, now time.Time, dbPath string) []source.LiveSession {
	byID := make(map[string]dbSession, len(rows))
	childrenByParent := map[string][]dbSession{}
	for _, row := range rows {
		if row.ID == "" {
			continue
		}
		byID[row.ID] = row
		if row.ParentID != "" {
			childrenByParent[row.ParentID] = append(childrenByParent[row.ParentID], row)
		}
	}

	liveParents := map[string]int{}
	matchedPIDs := map[int]bool{}
	for _, proc := range procs {
		if proc.SessionID != "" {
			row, ok := byID[proc.SessionID]
			if ok && row.ParentID == "" {
				liveParents[proc.SessionID] = proc.PID
				matchedPIDs[proc.PID] = true
			}
			continue
		}
		if proc.Cwd == "" {
			continue
		}
		if row, ok := newestParentForProcess(rows, proc); ok {
			liveParents[row.ID] = proc.PID
			matchedPIDs[proc.PID] = true
		}
	}

	childrenIncluded := map[string][]dbSession{}
	for parentID := range liveParents {
		childrenIncluded[parentID] = childrenByParent[parentID]
	}

	out := make([]source.LiveSession, 0, len(liveParents))
	for _, row := range rows {
		pid, ok := liveParents[row.ID]
		if !ok || row.ParentID != "" {
			continue
		}
		children := childrenIncluded[row.ID]
		out = append(out, liveSessionFromRow(row, pid, "", len(children), 0, "", now, dbPath))
		for _, child := range children {
			out = append(out, liveSessionFromRow(child, pid, row.ID, 0, 0, "closed", now, dbPath))
		}
	}
	for _, proc := range procs {
		if proc.PID <= 0 || matchedPIDs[proc.PID] {
			continue
		}
		out = append(out, syntheticLiveSession(proc, now))
	}
	return out
}

func syntheticLiveSession(proc liveProcess, now time.Time) source.LiveSession {
	startedAt := proc.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	return source.LiveSession{
		Agent:        state.AgentOpencode,
		SessionID:    "opencode:pid:" + strconv.Itoa(proc.PID),
		StartedAt:    startedAt,
		Event:        state.EventSessionStart,
		EventAt:      startedAt,
		EngineStatus: "idle",
		Meta: source.SessionMeta{
			PID:       proc.PID,
			Name:      "opencode",
			Cwd:       proc.Cwd,
			UpdatedAt: startedAt,
		},
	}
}

func newestParentForProcess(rows []dbSession, proc liveProcess) (dbSession, bool) {
	var best dbSession
	found := false
	for _, row := range rows {
		if row.ParentID != "" || row.Directory != proc.Cwd {
			continue
		}
		if !proc.StartedAt.IsZero() && unixMilli(row.TimeUpdated).Before(proc.StartedAt.Add(-time.Second)) {
			continue
		}
		if !found || row.TimeUpdated > best.TimeUpdated {
			best = row
			found = true
		}
	}
	return best, found
}

func liveSessionFromRow(row dbSession, pid int, parentID string, childCount, openChildCount int, childStatus string, now time.Time, dbPath string) source.LiveSession {
	updatedAt := unixMilli(row.TimeUpdated)
	engineStatus := "idle"
	if row.Runtime.EngineStatus != "" {
		engineStatus = row.Runtime.EngineStatus
	} else if !updatedAt.IsZero() && !now.IsZero() && now.Sub(updatedAt) <= activeDBUpdateWindow {
		engineStatus = "busy"
	}
	return source.LiveSession{
		Agent:        state.AgentOpencode,
		SessionID:    row.ID,
		StartedAt:    unixMilli(row.TimeCreated),
		Event:        state.EventDiscovered,
		EventAt:      updatedAt,
		EngineStatus: engineStatus,
		Meta: source.SessionMeta{
			PID:             pid,
			Name:            row.Title,
			ParentSessionID: parentID,
			ChildCount:      childCount,
			OpenChildCount:  openChildCount,
			ChildStatus:     childStatus,
			Cwd:             row.Directory,
			Version:         row.Version,
			Model:           modelName(row.Model),
			Path:            dbPath,
			UpdatedAt:       updatedAt,
			WaitingFor:      row.Runtime.WaitingFor,
		},
	}
}

func liveProcesses() []liveProcess {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []liveProcess
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		args := splitCmdline(cmdline)
		if len(args) == 0 || filepath.Base(args[0]) != "opencode" {
			continue
		}
		cwd, _ := os.Readlink(filepath.Join("/proc", entry.Name(), "cwd"))
		out = append(out, liveProcess{PID: pid, Cwd: cwd, SessionID: sessionFlag(args), StartedAt: processStartTime(entry.Name())})
	}
	return out
}

func processStartTime(pid string) time.Time {
	stat, err := os.Stat(filepath.Join("/proc", pid))
	if err != nil {
		return time.Time{}
	}
	return stat.ModTime()
}

func splitCmdline(raw []byte) []string {
	parts := bytes.Split(bytes.TrimRight(raw, "\x00"), []byte{0})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			out = append(out, string(part))
		}
	}
	return out
}

func sessionFlag(args []string) string {
	for i, arg := range args {
		if arg == "-s" || arg == "--session" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if value, ok := strings.CutPrefix(arg, "-s="); ok {
			return value
		}
		if value, ok := strings.CutPrefix(arg, "--session="); ok {
			return value
		}
	}
	return ""
}

// Apply upserts a scanned Opencode session into the store.
func Apply(ctx context.Context, s *state.Store, sess source.LiveSession) bool {
	createdAt := sess.StartedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)
	eventAt := sess.EventAt
	if eventAt.IsZero() {
		eventAt = createdAt
	}
	eventTS := eventAt.UTC().Format(time.RFC3339Nano)
	inserted, err := s.InsertSession(ctx, state.Session{
		SessionID:    sess.SessionID,
		Agent:        state.AgentOpencode,
		PID:          sess.Meta.PID,
		FirstSeenAt:  ts,
		LastEvent:    state.EventDiscovered,
		LastEventAt:  eventTS,
		EngineStatus: sess.EngineStatus,
		WaitingFor:   sess.Meta.WaitingFor,
		StatusAt:     eventTS,
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: opencode insert failed",
			"session", state.ShortID(sess.SessionID), "err", err)
		return false
	}
	if inserted {
		slog.InfoContext(ctx, "discovery: opencode session discovered",
			"session", state.ShortID(sess.SessionID), "event", state.EventDiscovered)
		return true
	}

	var priorAgent string
	changed, err := s.UpdateSession(ctx, sess.SessionID, func(stored *state.Session) bool {
		var changed bool
		prevStatus := state.DeriveStatus(*stored)
		if stored.Agent == "" || stored.Agent == state.AgentUnidentified {
			priorAgent = stored.Agent
			stored.Agent = state.AgentOpencode
			changed = true
		}
		if sess.Meta.PID > 0 && stored.PID != sess.Meta.PID {
			stored.PID = sess.Meta.PID
			changed = true
		}
		if stored.EngineStatus != sess.EngineStatus {
			stored.EngineStatus = sess.EngineStatus
			changed = true
		}
		if sess.Meta.WaitingFor != "" && stored.WaitingFor != sess.Meta.WaitingFor {
			stored.WaitingFor = sess.Meta.WaitingFor
			changed = true
		} else if sess.Meta.WaitingFor == "" && stored.WaitingFor != "" && stored.LastEvent != state.EventPermissionRequest {
			stored.WaitingFor = ""
			changed = true
		}
		if state.DeriveStatus(*stored) != prevStatus {
			stored.StatusAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		return changed
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: opencode refine failed",
			"session", state.ShortID(sess.SessionID), "err", err)
		return false
	}
	if changed && priorAgent != "" {
		slog.InfoContext(ctx, "discovery: agent identified",
			"session", state.ShortID(sess.SessionID),
			"from", priorAgent, "to", state.AgentOpencode)
	}
	return changed
}

func dataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode"), nil
}

func dbPath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "opencode.db"), nil
}

func sqliteReadOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(1000)")
	u.RawQuery = q.Encode()
	return u.String()
}

func modelName(raw string) string {
	if raw == "" {
		return ""
	}
	var decoded struct {
		ID      string `json:"id"`
		ModelID string `json:"modelID"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		if decoded.ID != "" {
			return decoded.ID
		}
		if decoded.ModelID != "" {
			return decoded.ModelID
		}
	}
	return raw
}

func unixMilli(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
