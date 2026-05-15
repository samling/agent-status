// Package claudecode is the Claude Code discovery backend: it reads the
// session JSON files Claude Code writes under ~/.claude/sessions/ and
// translates them into the shared source.LiveSession shape.
package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type sessionFile struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	StartedAt  int64  `json:"startedAt"` // Unix milliseconds
	Entrypoint string `json:"entrypoint"`
	Cwd        string `json:"cwd"`
	Name       string `json:"name,omitempty"`
	Title      string `json:"title,omitempty"`
	Status     string `json:"status"`               // "idle"|"busy"; absent for non-cli entrypoints
	Version    string `json:"version"`              // Claude Code version string, e.g. "2.1.128"
	WaitingFor string `json:"waitingFor,omitempty"` // populated while a permission prompt is open, e.g. "approve Bash"
}

func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "sessions"), nil
}

// Scan returns the currently-live Claude sessions (PID-alive sessions whose
// JSON file is present under ~/.claude/sessions/).
func Scan() ([]source.LiveSession, int, error) {
	files, scanned, err := walkAlive()
	if err != nil {
		return nil, scanned, err
	}
	out := make([]source.LiveSession, 0, len(files))
	for _, sf := range files {
		var startedAt time.Time
		if sf.StartedAt > 0 {
			startedAt = time.UnixMilli(sf.StartedAt)
		}
		children := loadSubagentSessions(sf, startedAt)
		out = append(out, source.LiveSession{
			Agent:        state.AgentClaudeCode,
			SessionID:    sf.SessionID,
			StartedAt:    startedAt,
			Event:        state.EventDiscovered,
			EngineStatus: sf.Status,
			Meta: source.SessionMeta{
				PID:        sf.PID,
				Name:       sessionName(sf),
				ChildCount: len(children),
				Entrypoint: sf.Entrypoint,
				Cwd:        sf.Cwd,
				Version:    sf.Version,
				WaitingFor: sf.WaitingFor,
			},
		})
		out = append(out, children...)
	}
	return out, scanned, nil
}

func walkAlive() (alive []sessionFile, scanned int, err error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		seen[path] = struct{}{}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		sf, ok := loadSessionFile(path, fi.ModTime(), fi.Size())
		if !ok {
			continue
		}
		if sf.SessionID == "" || sf.PID <= 0 {
			continue
		}
		scanned++
		if !source.PIDAlive(sf.PID) {
			continue
		}
		alive = append(alive, sf)
	}
	pruneWalkCache(seen)
	return alive, scanned, nil
}

func sessionName(sf sessionFile) string {
	if name := firstNonEmpty(sf.Name, sf.Title); name != "" {
		return name
	}
	if sf.SessionID == "" || sf.Cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".claude", "projects", encodePath(sf.Cwd), sf.SessionID+".jsonl")
	return loadTranscriptSessionName(path, sf.SessionID)
}

type titleLine struct {
	Type             string `json:"type"`
	SessionID        string `json:"sessionId"`
	CustomTitle      string `json:"customTitle"`
	CustomTitleSnake string `json:"custom_title"`
	Title            string `json:"title"`
}

type cachedTranscriptName struct {
	mtime time.Time
	size  int64
	name  string
}

var (
	transcriptNameMu    sync.Mutex
	transcriptNameCache = map[string]cachedTranscriptName{}
)

func loadTranscriptSessionName(path, sessionID string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	transcriptNameMu.Lock()
	cached, ok := transcriptNameCache[path]
	transcriptNameMu.Unlock()
	if ok && cached.mtime.Equal(fi.ModTime()) && cached.size == fi.Size() {
		return cached.name
	}

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var name string
	_ = source.ScanJSONL(f, func(line []byte) bool {
		var rec titleLine
		if err := json.Unmarshal(line, &rec); err != nil {
			return true
		}
		if rec.Type != "custom-title" && rec.Type != "custom_title" {
			return true
		}
		if rec.SessionID != "" && rec.SessionID != sessionID {
			return true
		}
		if next := firstNonEmpty(rec.CustomTitle, rec.CustomTitleSnake, rec.Title); next != "" {
			name = next
		}
		return true
	})

	transcriptNameMu.Lock()
	transcriptNameCache[path] = cachedTranscriptName{mtime: fi.ModTime(), size: fi.Size(), name: name}
	transcriptNameMu.Unlock()
	return name
}

func loadSubagentSessions(sf sessionFile, parentStartedAt time.Time) []source.LiveSession {
	if sf.SessionID == "" || sf.Cwd == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".claude", "projects", encodePath(sf.Cwd), sf.SessionID, "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []source.LiveSession
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, ok := loadSubagentInfo(path)
		if !ok {
			continue
		}
		if info.AgentID == "" {
			info.AgentID = strings.TrimPrefix(strings.TrimSuffix(e.Name(), ".jsonl"), "agent-")
		}
		if info.AgentID == "" {
			continue
		}
		if info.StartedAt.IsZero() {
			info.StartedAt = parentStartedAt
		}
		if info.UpdatedAt.IsZero() {
			if fi, err := e.Info(); err == nil {
				info.UpdatedAt = fi.ModTime()
			} else {
				info.UpdatedAt = info.StartedAt
			}
		}
		name := firstNonEmpty(info.Name, "subagent "+info.AgentID)
		out = append(out, source.LiveSession{
			Agent:        state.AgentClaudeCode,
			SessionID:    sf.SessionID + ":" + info.AgentID,
			StartedAt:    info.StartedAt,
			Event:        state.EventDiscovered,
			EventAt:      info.UpdatedAt,
			EngineStatus: "idle",
			Meta: source.SessionMeta{
				PID:             sf.PID,
				Name:            name,
				ParentSessionID: sf.SessionID,
				Entrypoint:      sf.Entrypoint,
				Cwd:             firstNonEmpty(info.Cwd, sf.Cwd),
				Version:         firstNonEmpty(info.Version, sf.Version),
				Model:           info.Model,
				Path:            path,
				UpdatedAt:       info.UpdatedAt,
			},
		})
	}
	return out
}

type subagentInfo struct {
	AgentID   string
	Name      string
	Cwd       string
	Version   string
	Model     string
	StartedAt time.Time
	UpdatedAt time.Time
}

func loadSubagentInfo(path string) (subagentInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return subagentInfo{}, false
	}
	defer f.Close()

	var info subagentInfo
	_ = source.ScanJSONL(f, func(line []byte) bool {
		var rec struct {
			Timestamp        string `json:"timestamp"`
			AgentID          string `json:"agentId"`
			AttributionAgent string `json:"attributionAgent"`
			Cwd              string `json:"cwd"`
			Version          string `json:"version"`
			Message          struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return true
		}
		if rec.AgentID != "" {
			info.AgentID = rec.AgentID
		}
		if rec.AttributionAgent != "" {
			info.Name = rec.AttributionAgent
		}
		if rec.Cwd != "" {
			info.Cwd = rec.Cwd
		}
		if rec.Version != "" {
			info.Version = rec.Version
		}
		if rec.Message.Model != "" {
			info.Model = rec.Message.Model
		}
		if ts := parseRFC3339(rec.Timestamp); !ts.IsZero() {
			if info.StartedAt.IsZero() {
				info.StartedAt = ts
			}
			info.UpdatedAt = ts
		}
		return true
	})
	return info, true
}

func parseRFC3339(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Apply adapts a scanned source.LiveSession into the claude-specific upsert
// path.
func Apply(ctx context.Context, s *state.Store, sess source.LiveSession) bool {
	return applySessionFile(ctx, s, sessionFile{
		PID:        sess.Meta.PID,
		SessionID:  sess.SessionID,
		Entrypoint: sess.Meta.Entrypoint,
		Cwd:        sess.Meta.Cwd,
		Status:     sess.EngineStatus,
		Version:    sess.Meta.Version,
		StartedAt:  unixMilli(sess.StartedAt),
	})
}

// applySessionFile upserts a parsed Claude session JSON into the store.
//
// On first sight: insert a row stamped "Discovered" with the engine status
// (idle/busy) read from the file.
//
// On already-known rows: refresh Agent and EngineStatus only. If the refresh
// causes the derived status to flip, bump StatusAt to mark the transition.
// Hook-driven LastEvent / LastEventAt / TurnID are deliberately untouched so
// they aren't clobbered by a later poll.
func applySessionFile(ctx context.Context, s *state.Store, sf sessionFile) bool {
	createdAt := time.Now()
	if sf.StartedAt > 0 {
		createdAt = time.UnixMilli(sf.StartedAt)
	}
	ts := createdAt.UTC().Format(time.RFC3339Nano)

	inserted, err := s.InsertSession(ctx, state.Session{
		SessionID:    sf.SessionID,
		Agent:        state.AgentClaudeCode,
		PID:          sf.PID,
		FirstSeenAt:  ts,
		LastEvent:    state.EventDiscovered,
		LastEventAt:  ts,
		StatusAt:     ts,
		EngineStatus: sf.Status,
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: claude-code insert failed",
			"session", state.ShortID(sf.SessionID), "err", err)
		return false
	}
	if inserted {
		slog.InfoContext(ctx, "discovery: new claude-code session",
			"session", state.ShortID(sf.SessionID),
			"pid", sf.PID, "entrypoint", sf.Entrypoint, "version", sf.Version,
			"engine_status", sf.Status)
		return true
	}

	var (
		transitioned bool
		identified   bool
		priorAgent   string
	)
	changed, err := s.UpdateSession(ctx, sf.SessionID, func(stored *state.Session) bool {
		var changed bool
		// Identify (or re-identify) Agent if a hook stamped it before us with
		// the placeholder. We never downgrade a concrete label.
		if stored.Agent == "" || stored.Agent == state.AgentUnidentified {
			priorAgent = stored.Agent
			stored.Agent = state.AgentClaudeCode
			identified = true
			changed = true
		}
		// Refresh PID when the freshly-scanned value differs and is non-zero;
		// never overwrite a known PID with zero.
		if sf.PID > 0 && stored.PID != sf.PID {
			stored.PID = sf.PID
			changed = true
		}
		if stored.EngineStatus != sf.Status {
			prevStatus := state.DeriveStatus(*stored)
			stored.EngineStatus = sf.Status
			if state.DeriveStatus(*stored) != prevStatus {
				stored.StatusAt = time.Now().UTC().Format(time.RFC3339Nano)
				transitioned = true
			}
			changed = true
		}
		return changed
	})
	if err != nil {
		slog.WarnContext(ctx, "discovery: claude-code refine failed",
			"session", state.ShortID(sf.SessionID), "err", err)
		return false
	}
	if identified {
		slog.InfoContext(ctx, "discovery: agent identified",
			"session", state.ShortID(sf.SessionID),
			"from", priorAgent, "to", state.AgentClaudeCode)
	}
	switch {
	case transitioned:
		slog.InfoContext(ctx, "discovery: claude-code status transitioned",
			"session", state.ShortID(sf.SessionID), "engine_status", sf.Status)
	case changed:
		slog.DebugContext(ctx, "discovery: claude-code engine_status recorded",
			"session", state.ShortID(sf.SessionID), "engine_status", sf.Status)
	}
	return changed
}

type cachedSessionFile struct {
	mtime time.Time
	size  int64
	sf    sessionFile
}

var (
	walkCacheMu sync.Mutex
	walkCache   = map[string]cachedSessionFile{}
)

func loadSessionFile(path string, mtime time.Time, size int64) (sessionFile, bool) {
	walkCacheMu.Lock()
	cached, ok := walkCache[path]
	walkCacheMu.Unlock()
	if ok && cached.mtime.Equal(mtime) && cached.size == size {
		return cached.sf, true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, false
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return sessionFile{}, false
	}
	walkCacheMu.Lock()
	walkCache[path] = cachedSessionFile{mtime: mtime, size: size, sf: sf}
	walkCacheMu.Unlock()
	return sf, true
}

func pruneWalkCache(seen map[string]struct{}) {
	walkCacheMu.Lock()
	defer walkCacheMu.Unlock()
	for path := range walkCache {
		if _, ok := seen[path]; !ok {
			delete(walkCache, path)
		}
	}
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
