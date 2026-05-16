package sessionview

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type MetaProvider interface {
	LatestMeta() map[string]source.SessionMeta
	Transcript(sessionID, agent string, meta source.SessionMeta) (source.TranscriptInfo, error)
	TranscriptMessages(sessionID, agent string, meta source.SessionMeta) ([]source.TranscriptMessageSummary, error)
	TranscriptMessage(sessionID, agent string, meta source.SessionMeta, id string) (source.TranscriptMessageDetail, error)
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
	Age             string `json:"age,omitempty"`
	ActivityTime    string `json:"activity_time"`
	FirstSeenAt     string `json:"first_seen_at"`
	StatusAt        string `json:"status_at"`
	Note            string `json:"note,omitempty"`
	ChildCount      int    `json:"child_count,omitempty"`
	OpenChildCount  int    `json:"open_child_count,omitempty"`
	ChildStatus     string `json:"child_status,omitempty"`
}

type DetailMetadata struct {
	Agent               string `json:"agent"`
	Entrypoint          string `json:"entrypoint"`
	Version             string `json:"version"`
	Model               string `json:"model"`
	Session             string `json:"session"`
	SessionID           string `json:"session_id"`
	Created             string `json:"created"`
	Updated             string `json:"updated"`
	Cwd                 string `json:"cwd"`
	Branch              string `json:"branch"`
	InputTokens         int64  `json:"input_tokens,omitempty"`
	OutputTokens        int64  `json:"output_tokens,omitempty"`
	CacheCreationTokens int64  `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int64  `json:"cache_read_tokens,omitempty"`
	UserMessages        int    `json:"user_messages,omitempty"`
	AgentMessages       int    `json:"agent_messages,omitempty"`
	PID                 int    `json:"pid,omitempty"`
	ParentSessionID     string `json:"parent_session_id,omitempty"`
	ChildCount          int    `json:"child_count,omitempty"`
	OpenChildCount      int    `json:"open_child_count,omitempty"`
	LastEvent           string `json:"last_event"`
	Waiting             string `json:"waiting"`
	Note                string `json:"note"`
}

type ConversationMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
}

type MessageSummary struct {
	ID        string `json:"id"`
	Index     int    `json:"index"`
	Role      string `json:"role"`
	Preview   string `json:"preview"`
	Timestamp string `json:"timestamp,omitempty"`
}

type MessageList struct {
	SessionID string           `json:"session_id"`
	Agent     string           `json:"agent"`
	Status    string           `json:"status"`
	Title     string           `json:"title"`
	Messages  []MessageSummary `json:"messages"`
}

type MessageDetail struct {
	ID           string `json:"id"`
	Index        int    `json:"index"`
	Role         string `json:"role"`
	Preview      string `json:"preview"`
	Text         string `json:"text"`
	RawText      string `json:"raw_text,omitempty"`
	Timestamp    string `json:"timestamp,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
	RawTruncated bool   `json:"raw_truncated,omitempty"`
}

type SessionDetail struct {
	SessionID       string                `json:"session_id"`
	Agent           string                `json:"agent"`
	Status          string                `json:"status"`
	Title           string                `json:"title"`
	Metadata        DetailMetadata        `json:"metadata"`
	Conversation    []ConversationMessage `json:"conversation"`
	TranscriptError string                `json:"transcript_error,omitempty"`
}

func (p Provider) Cards(ctx context.Context) ([]SessionCard, error) {
	_ = ctx
	meta := p.latestMeta()
	notes := p.loadNotes()
	sessions := p.Store.Sessions()
	sessions = suppressDuplicateParentSessions(sessions, meta)
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
			Age:             ageTime(sess.FirstSeenTime),
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

func suppressDuplicateParentSessions(sessions []state.Session, meta map[string]source.SessionMeta) []state.Session {
	keep := make([]bool, len(sessions))
	bestByPID := map[string]int{}
	for i, sess := range sessions {
		keep[i] = true
		m := meta[sess.SessionID]
		if m.ParentSessionID != "" {
			continue
		}
		pid := sess.PID
		if pid <= 0 {
			pid = m.PID
		}
		if pid <= 0 {
			continue
		}
		key := sess.Agent + ":" + strconv.Itoa(pid)
		best, ok := bestByPID[key]
		if !ok {
			bestByPID[key] = i
			continue
		}
		if preferDuplicateSession(sess, sessions[best]) {
			keep[best] = false
			bestByPID[key] = i
			continue
		}
		keep[i] = false
	}
	out := make([]state.Session, 0, len(sessions))
	for i, sess := range sessions {
		if keep[i] {
			out = append(out, sess)
		}
	}
	return out
}

func preferDuplicateSession(a, b state.Session) bool {
	if ar, br := duplicateStatusRank(a), duplicateStatusRank(b); ar != br {
		return ar < br
	}
	if at, bt := eventTime(a), eventTime(b); !at.Equal(bt) {
		return at.After(bt)
	}
	if !a.StatusTime.Equal(b.StatusTime) {
		return a.StatusTime.After(b.StatusTime)
	}
	if !a.FirstSeenTime.Equal(b.FirstSeenTime) {
		return a.FirstSeenTime.After(b.FirstSeenTime)
	}
	return a.SessionID > b.SessionID
}

func duplicateStatusRank(sess state.Session) int {
	switch state.DeriveStatus(sess) {
	case "waiting":
		return 0
	case "active":
		return 1
	case "idle":
		return 2
	default:
		return 3
	}
}

func eventTime(sess state.Session) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, sess.LastEventAt); err == nil {
		return t
	}
	if !sess.StatusTime.IsZero() {
		return sess.StatusTime
	}
	return sess.FirstSeenTime
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
	detail.Metadata = detailMetadata(sess, m, info, notes[id])
	if transcriptErr == nil {
		detail.Conversation = newestFirst(info.RecentMessages)
	}
	return detail, nil
}

func (p Provider) Messages(ctx context.Context, id string) (MessageList, error) {
	_ = ctx
	sess, ok := p.Store.GetSession(id)
	if !ok {
		return MessageList{}, state.ErrSessionNotFound
	}
	meta := p.latestMeta()
	m := meta[id]
	list := MessageList{
		SessionID: sess.SessionID,
		Agent:     sess.Agent,
		Status:    state.DeriveStatus(sess),
		Title:     titleFor(m.Name, m.Cwd),
	}
	if p.Meta == nil {
		return list, nil
	}
	messages, err := p.Meta.TranscriptMessages(id, sess.Agent, m)
	if err != nil {
		if os.IsNotExist(err) {
			return list, nil
		}
		return MessageList{}, err
	}
	list.Messages = newestFirstMessages(messages)
	return list, nil
}

func (p Provider) Message(ctx context.Context, id, messageID string) (MessageDetail, error) {
	_ = ctx
	sess, ok := p.Store.GetSession(id)
	if !ok {
		return MessageDetail{}, state.ErrSessionNotFound
	}
	if p.Meta == nil {
		return MessageDetail{}, source.ErrTranscriptMessageNotFound
	}
	meta := p.latestMeta()
	detail, err := p.Meta.TranscriptMessage(id, sess.Agent, meta[id], messageID)
	if err != nil {
		return MessageDetail{}, err
	}
	return MessageDetail{
		ID:           detail.ID,
		Index:        detail.Index,
		Role:         detail.Role,
		Preview:      detail.Preview,
		Text:         detail.Text,
		RawText:      detail.RawText,
		Timestamp:    detail.Timestamp,
		Truncated:    detail.Truncated,
		RawTruncated: detail.RawTruncated,
	}, nil
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

func detailMetadata(sess state.Session, meta source.SessionMeta, info source.TranscriptInfo, note string) DetailMetadata {
	model := firstNonEmpty(info.Model, meta.Model)
	version := firstNonEmpty(info.Version, meta.Version)
	pid := 0
	if meta.PID > 0 {
		pid = meta.PID
	} else if sess.PID > 0 {
		pid = sess.PID
	}
	return DetailMetadata{
		Agent:               sess.Agent,
		Entrypoint:          meta.Entrypoint,
		Version:             version,
		Model:               model,
		Session:             meta.Name,
		SessionID:           sess.SessionID,
		Created:             displayTimestamp(sess.FirstSeenTime),
		Updated:             displayTimestamp(sess.StatusTime),
		Cwd:                 meta.Cwd,
		Branch:              info.GitBranch,
		InputTokens:         info.InputTokens,
		OutputTokens:        info.OutputTokens,
		CacheCreationTokens: info.CacheCreationTokens,
		CacheReadTokens:     info.CacheReadTokens,
		UserMessages:        info.UserMessages,
		AgentMessages:       info.AgentMessages,
		PID:                 pid,
		ParentSessionID:     meta.ParentSessionID,
		ChildCount:          meta.ChildCount,
		OpenChildCount:      meta.OpenChildCount,
		LastEvent:           sess.LastEvent,
		Waiting:             firstNonEmpty(meta.WaitingFor, sess.WaitingFor),
		Note:                note,
	}
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

func newestFirstMessages(in []source.TranscriptMessageSummary) []MessageSummary {
	out := make([]MessageSummary, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, MessageSummary{
			ID:        in[i].ID,
			Index:     in[i].Index,
			Role:      in[i].Role,
			Preview:   in[i].Preview,
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

func ageTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Second:
		return "<1s"
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}

func displayTimestamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
