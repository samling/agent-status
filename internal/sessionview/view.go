package sessionview

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type MetaProvider interface {
	LatestMeta() map[string]source.SessionMeta
	Transcript(sessionID, agent string, meta source.SessionMeta) (source.TranscriptInfo, error)
}

type Provider struct {
	Store     *state.Store
	Meta      MetaProvider
	NotesPath string
}

type SessionCard struct {
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	Agent           string `json:"agent"`
	Status          string `json:"status"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	ActivityTime    string `json:"activity_time"`
	FirstSeenAt     string `json:"first_seen_at"`
	StatusAt        string `json:"status_at"`
	Note            string `json:"note,omitempty"`
	ChildCount      int    `json:"child_count,omitempty"`
	OpenChildCount  int    `json:"open_child_count,omitempty"`
	ChildStatus     string `json:"child_status,omitempty"`
}

type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type ConversationMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
}

type SessionDetail struct {
	SessionID       string                `json:"session_id"`
	Agent           string                `json:"agent"`
	Status          string                `json:"status"`
	Title           string                `json:"title"`
	Metadata        []Field               `json:"metadata"`
	Conversation    []ConversationMessage `json:"conversation"`
	TranscriptError string                `json:"transcript_error,omitempty"`
}

func (p Provider) Cards(ctx context.Context) ([]SessionCard, error) {
	_ = ctx
	meta := p.latestMeta()
	notes := p.loadNotes()
	sessions := p.Store.Sessions()
	sessionIDs := make(map[string]struct{}, len(sessions))
	for _, sess := range sessions {
		sessionIDs[sess.SessionID] = struct{}{}
	}
	out := make([]SessionCard, 0, len(sessions))
	for _, sess := range sessions {
		m := meta[sess.SessionID]
		if m.ParentSessionID != "" {
			if _, ok := sessionIDs[m.ParentSessionID]; !ok {
				continue
			}
		}
		out = append(out, SessionCard{
			SessionID:       sess.SessionID,
			ParentSessionID: m.ParentSessionID,
			Agent:           sess.Agent,
			Status:          state.DeriveStatus(sess),
			Title:           titleFor(m.Name, m.Cwd),
			Subtitle:        "",
			ActivityTime:    relTime(sess.StatusTime),
			FirstSeenAt:     sess.FirstSeenAt,
			StatusAt:        sess.StatusAt,
			Note:            notes[sess.SessionID],
			ChildCount:      m.ChildCount,
			OpenChildCount:  m.OpenChildCount,
			ChildStatus:     m.ChildStatus,
		})
	}
	return out, nil
}

func (p Provider) Detail(ctx context.Context, id string) (SessionDetail, error) {
	_ = ctx
	sess, ok := p.Store.GetSession(id)
	if !ok {
		return SessionDetail{}, state.ErrSessionNotFound
	}
	meta := p.latestMeta()
	m := meta[id]
	notes := p.loadNotes()
	info := source.TranscriptInfo{}
	var transcriptErr error
	if p.Meta != nil {
		info, transcriptErr = p.Meta.Transcript(id, sess.Agent, m)
	}

	detail := SessionDetail{
		SessionID: sess.SessionID,
		Agent:     sess.Agent,
		Status:    state.DeriveStatus(sess),
		Title:     titleFor(m.Name, m.Cwd),
	}
	if transcriptErr != nil {
		detail.TranscriptError = transcriptErr.Error()
	}
	detail.Metadata = metadataFields(sess, m, info, notes[id])
	if transcriptErr == nil {
		detail.Conversation = newestFirst(info.RecentMessages)
	}
	return detail, nil
}

func (p Provider) latestMeta() map[string]source.SessionMeta {
	if p.Meta == nil {
		return map[string]source.SessionMeta{}
	}
	meta := p.Meta.LatestMeta()
	if meta == nil {
		return map[string]source.SessionMeta{}
	}
	return meta
}

func (p Provider) loadNotes() map[string]string {
	notes, err := state.LoadNotes(p.NotesPath)
	if err != nil {
		return map[string]string{}
	}
	return notes
}

func titleFor(name, cwd string) string {
	if name != "" {
		return name
	}
	if cwd == "" {
		return "-"
	}
	base := filepath.Base(cwd)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return cwd
	}
	return base
}

func metadataFields(sess state.Session, meta source.SessionMeta, info source.TranscriptInfo, note string) []Field {
	model := firstNonEmpty(info.Model, meta.Model)
	version := firstNonEmpty(info.Version, meta.Version)
	pid := "-"
	if meta.PID > 0 {
		pid = strconv.Itoa(meta.PID)
	} else if sess.PID > 0 {
		pid = strconv.Itoa(sess.PID)
	}
	fields := []Field{
		{Label: "agent", Value: valueOrDash(sess.Agent)},
		{Label: "version", Value: valueOrDash(version)},
		{Label: "session", Value: valueOrDash(meta.Name)},
		{Label: "session id", Value: valueOrDash(sess.SessionID)},
		{Label: "model", Value: valueOrDash(model)},
		{Label: "branch", Value: valueOrDash(info.GitBranch)},
	}
	if hasTokenStats(info) {
		fields = append(fields,
			Field{Label: "input tokens", Value: formatCompactCount(info.InputTokens)},
			Field{Label: "output tokens", Value: formatCompactCount(info.OutputTokens)},
			Field{Label: "cache create", Value: formatCompactCount(info.CacheCreationTokens)},
			Field{Label: "cache read", Value: formatCompactCount(info.CacheReadTokens)},
		)
	}
	if hasMessageStats(info) {
		fields = append(fields,
			Field{Label: "user msgs", Value: formatMessageCount(info.UserMessages)},
			Field{Label: "agent msgs", Value: formatMessageCount(info.AgentMessages)},
		)
	}
	fields = append(fields,
		Field{Label: "pid", Value: pid},
		Field{Label: "cwd", Value: valueOrDash(meta.Cwd)},
		Field{Label: "parent", Value: valueOrDash(meta.ParentSessionID)},
		Field{Label: "children", Value: childCountValue(meta.ChildCount, meta.OpenChildCount)},
		Field{Label: "last event", Value: valueOrDash(sess.LastEvent)},
		Field{Label: "waiting", Value: valueOrDash(meta.WaitingFor)},
		Field{Label: "note", Value: valueOrDash(note)},
	)
	return fields
}

func hasTokenStats(info source.TranscriptInfo) bool {
	return info.InputTokens > 0 ||
		info.OutputTokens > 0 ||
		info.CacheCreationTokens > 0 ||
		info.CacheReadTokens > 0
}

func hasMessageStats(info source.TranscriptInfo) bool {
	return info.UserMessages > 0 || info.AgentMessages > 0
}

func formatCompactCount(n int64) string {
	if n <= 0 {
		return "-"
	}
	return formatPositiveCompactCount(n)
}

func formatMessageCount(n int) string {
	if n <= 0 {
		return "0"
	}
	return formatPositiveCompactCount(int64(n))
}

func formatPositiveCompactCount(n int64) string {
	units := []struct {
		value  int64
		suffix string
	}{
		{1_000_000_000, "B"},
		{1_000_000, "M"},
		{1_000, "k"},
	}
	for _, unit := range units {
		if n >= unit.value {
			whole := n / unit.value
			decimal := (n % unit.value) * 10 / unit.value
			if whole >= 100 || decimal == 0 {
				return strconv.FormatInt(whole, 10) + unit.suffix
			}
			return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(decimal, 10) + unit.suffix
		}
	}
	return strconv.FormatInt(n, 10)
}

func childCountValue(total, open int) string {
	if total <= 0 {
		return "-"
	}
	if open > 0 {
		return strconv.Itoa(total) + " (" + strconv.Itoa(open) + " open)"
	}
	return strconv.Itoa(total)
}

func newestFirst(in []source.ConversationMessage) []ConversationMessage {
	out := make([]ConversationMessage, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, ConversationMessage{
			Role:      in[i].Role,
			Text:      in[i].Text,
			Timestamp: in[i].Timestamp,
		})
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func valueOrDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Second:
		return "<1s ago"
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s ago"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}
