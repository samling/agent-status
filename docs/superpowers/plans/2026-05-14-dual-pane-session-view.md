# Dual-Pane Session View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a tmux-friendly dual-pane `agent-status ui` with compact session cards, right-pane metadata, and newest-first one-line conversation previews.

**Architecture:** Add transcript conversation previews, then introduce `internal/sessionview` as a server-side presentation layer. The daemon exposes view endpoints consumed by `internal/client` and the TUI, while existing `/state`, `/meta`, `/transcript`, and focus paths remain intact.

**Tech Stack:** Go 1.26, Bubble Tea, Lipgloss, `net/http`, `log/slog`, existing `state`, `discovery`, `client`, and `server` packages.

---

## File Structure

- Modify `internal/discovery/source/transcript.go`: add shared conversation preview types and helpers.
- Create `internal/discovery/source/transcript_test.go`: test preview collapse and capped append behavior.
- Modify `internal/discovery/claudecode/transcript.go`: collect Claude user and assistant message previews.
- Create `internal/discovery/claudecode/transcript_test.go`: cover Claude conversation preview parsing.
- Modify `internal/discovery/codex/codex.go`: include rollout line timestamps in the shared package-local `transcriptLine`.
- Modify `internal/discovery/codex/transcript.go`: collect Codex user and assistant message previews.
- Modify `internal/discovery/codex/transcript_test.go`: extend existing parser test for conversation previews.
- Modify `internal/state/state.go`: add a shared not-found error for view endpoints.
- Create `internal/sessionview/view.go`: build `SessionCard` and `SessionDetail` view models.
- Create `internal/sessionview/view_test.go`: cover card construction, detail metadata, transcript failure, notes, and newest-first conversation.
- Modify `internal/server/server.go`: add optional view endpoints.
- Modify `internal/server/server_test.go`: cover view endpoint responses and existing endpoint compatibility.
- Modify `internal/client/client.go`: add view client methods.
- Modify `internal/client/client_test.go`: cover decoding of view payloads.
- Modify `internal/cli/server.go`: wire the daemon to `sessionview.Provider`.
- Modify `internal/cli/ui/ui.go`: change model state from raw sessions/meta/detail to session cards/detail.
- Modify `internal/cli/ui/commands.go`: load cards and selected detail through the client.
- Modify `internal/cli/ui/actions.go`: update selection and note helpers to use cards.
- Modify `internal/cli/ui/sort.go`: add card sorting while preserving the current sort cycle.
- Modify `internal/cli/ui/view.go`: replace the table plus bottom block with two-pane rendering.
- Modify `internal/cli/ui/view_test.go`: assert agent kind in cards, top metadata, and newest-first conversation.

Before implementing, note that the working tree may contain unrelated local edits in `internal/client/client.go`, `internal/cli/ui/view.go`, `internal/client/client_test.go`, and `internal/cli/ui/view_test.go`. Do not revert or overwrite user changes. Read the files before editing and integrate with what is present.

---

## Task 1: Shared Transcript Conversation Helpers

**Files:**
- Modify: `internal/discovery/source/transcript.go`
- Create: `internal/discovery/source/transcript_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/discovery/source/transcript_test.go`:

```go
package source

import "testing"

func TestOneLinePreviewCollapsesWhitespaceAndTruncates(t *testing.T) {
	got := OneLinePreview("hello\n\nthere   friend", 14)
	if got != "hello there..." {
		t.Fatalf("OneLinePreview() = %q, want %q", got, "hello there...")
	}
}

func TestAppendConversationMessageCapsRecentMessages(t *testing.T) {
	var info TranscriptInfo
	for i := 0; i < MaxConversationMessages+2; i++ {
		AppendConversationMessage(&info, ConversationMessage{
			Role:      "user",
			Text:      OneLinePreview("message", 80),
			Timestamp: string(rune('a' + i)),
		})
	}

	if len(info.RecentMessages) != MaxConversationMessages {
		t.Fatalf("len(RecentMessages) = %d, want %d", len(info.RecentMessages), MaxConversationMessages)
	}
	if info.RecentMessages[0].Timestamp != "c" {
		t.Fatalf("oldest retained timestamp = %q, want c", info.RecentMessages[0].Timestamp)
	}
}

func TestExtractTextContentSupportsStringAndBlocks(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "string", raw: `"hello"`, want: "hello"},
		{name: "blocks", raw: `[{"type":"text","text":"hello"},{"type":"output_text","text":"world"}]`, want: "hello\nworld"},
		{name: "ignores non text", raw: `[{"type":"tool_result","text":"skip"},{"type":"input_text","text":"keep"}]`, want: "keep"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractTextContent([]byte(tc.raw)); got != tc.want {
				t.Fatalf("ExtractTextContent() = %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/discovery/source`

Expected: FAIL with undefined identifiers `OneLinePreview`, `AppendConversationMessage`, `ConversationMessage`, `MaxConversationMessages`, and `ExtractTextContent`.

- [ ] **Step 3: Implement shared helpers**

Modify `internal/discovery/source/transcript.go`:

```go
package source

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const MaxConversationMessages = 12

// ConversationMessage is a single transcript message preview.
type ConversationMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
}

// TranscriptInfo summarizes one agent transcript.
type TranscriptInfo struct {
	Model               string
	GitBranch           string
	Version             string
	PermissionMode      string
	TurnCount           int
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	LastUserPrompt      string
	RecentMessages      []ConversationMessage
}
```

Add these functions below `LoadTranscriptPath`:

```go
func AppendConversationMessage(info *TranscriptInfo, msg ConversationMessage) {
	if info == nil || msg.Role == "" || msg.Text == "" {
		return
	}
	info.RecentMessages = append(info.RecentMessages, msg)
	if len(info.RecentMessages) > MaxConversationMessages {
		info.RecentMessages = info.RecentMessages[len(info.RecentMessages)-MaxConversationMessages:]
	}
}

func OneLinePreview(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func ExtractTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if (b.Type == "text" || b.Type == "input_text" || b.Type == "output_text") && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
```

Change `ExtractUserPrompt` to use the new helper:

```go
func ExtractUserPrompt(content json.RawMessage) string {
	return ExtractTextContent(content)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/discovery/source`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/source/transcript.go internal/discovery/source/transcript_test.go
git commit -m "feat: add transcript conversation previews"
```

---

## Task 2: Claude and Codex Conversation Parsing

**Files:**
- Modify: `internal/discovery/claudecode/transcript.go`
- Create: `internal/discovery/claudecode/transcript_test.go`
- Modify: `internal/discovery/codex/codex.go`
- Modify: `internal/discovery/codex/transcript.go`
- Modify: `internal/discovery/codex/transcript_test.go`

- [ ] **Step 1: Write the failing Claude parser test**

Create `internal/discovery/claudecode/transcript_test.go`:

```go
package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTranscriptCapturesConversationPreviews(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := `{"type":"user","timestamp":"2026-05-14T10:00:00Z","message":{"content":[{"type":"text","text":"hello\nfrom user"}]}}
{"type":"assistant","timestamp":"2026-05-14T10:00:10Z","message":{"model":"claude-sonnet","content":[{"type":"text","text":"hello from assistant"}],"usage":{"input_tokens":10,"output_tokens":5}}}
{"type":"user","timestamp":"2026-05-14T10:01:00Z","message":{"content":"second user message"}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := parseTranscript(path)
	if err != nil {
		t.Fatalf("parseTranscript() error = %v", err)
	}
	if len(info.RecentMessages) != 3 {
		t.Fatalf("len(RecentMessages) = %d, want 3", len(info.RecentMessages))
	}
	if info.RecentMessages[0].Role != "user" || info.RecentMessages[0].Text != "hello from user" {
		t.Fatalf("first message = %#v", info.RecentMessages[0])
	}
	if info.RecentMessages[1].Role != "assistant" || info.RecentMessages[1].Text != "hello from assistant" {
		t.Fatalf("second message = %#v", info.RecentMessages[1])
	}
	if info.LastUserPrompt != "second user message" {
		t.Fatalf("LastUserPrompt = %q, want second user message", info.LastUserPrompt)
	}
}
```

- [ ] **Step 2: Extend the Codex parser test**

Modify `internal/discovery/codex/transcript_test.go` by adding user and assistant conversation assertions to `TestParseTranscript`:

```go
	if len(info.RecentMessages) != 2 {
		t.Fatalf("len(RecentMessages) = %d, want 2", len(info.RecentMessages))
	}
	if info.RecentMessages[0].Role != "user" || info.RecentMessages[0].Text != "build this" {
		t.Fatalf("first message = %#v", info.RecentMessages[0])
	}
	if info.RecentMessages[1].Role != "assistant" || info.RecentMessages[1].Text != "ok" {
		t.Fatalf("second message = %#v", info.RecentMessages[1])
	}
```

Also update the test data lines to include timestamps:

```go
data := `{"type":"session_meta","timestamp":"2026-05-14T10:00:00Z","payload":{"cli_version":"0.128.0","git":{"branch":"feature"}}}
{"type":"turn_context","timestamp":"2026-05-14T10:00:01Z","payload":{"model":"gpt-5.5"}}
{"type":"response_item","timestamp":"2026-05-14T10:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build this"}]}}
{"type":"response_item","timestamp":"2026-05-14T10:00:03Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
{"type":"event_msg","timestamp":"2026-05-14T10:00:04Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":400,"output_tokens":250}}}}
`
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/discovery/claudecode ./internal/discovery/codex`

Expected: FAIL because `RecentMessages` is empty.

- [ ] **Step 4: Implement Claude conversation parsing**

Modify `internal/discovery/claudecode/transcript.go`:

```go
type transcriptLine struct {
	Type           string `json:"type"`
	Timestamp      string `json:"timestamp,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	GitBranch      string `json:"gitBranch,omitempty"`
	Version        string `json:"version,omitempty"`
	IsMeta         bool   `json:"isMeta,omitempty"`
	Message        struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}
```

In the `"assistant"` case, after usage/version updates:

```go
			if text := source.ExtractTextContent(line.Message.Content); text != "" {
				source.AppendConversationMessage(&info, source.ConversationMessage{
					Role:      "assistant",
					Text:      source.OneLinePreview(text, 120),
					Timestamp: line.Timestamp,
				})
			}
```

In the `"user"` case, after setting `LastUserPrompt`:

```go
			if prompt := source.ExtractUserPrompt(line.Message.Content); prompt != "" {
				info.LastUserPrompt = prompt
				source.AppendConversationMessage(&info, source.ConversationMessage{
					Role:      "user",
					Text:      source.OneLinePreview(prompt, 120),
					Timestamp: line.Timestamp,
				})
			}
```

- [ ] **Step 5: Implement Codex conversation parsing**

Modify the `transcriptLine` type in `internal/discovery/codex/codex.go`:

```go
type transcriptLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}
```

Modify `internal/discovery/codex/transcript.go`.

In the `"assistant"` case:

```go
			case "assistant":
				info.TurnCount++
				if text := source.ExtractTextContent(payload.Content); text != "" {
					source.AppendConversationMessage(&info, source.ConversationMessage{
						Role:      "assistant",
						Text:      source.OneLinePreview(text, 120),
						Timestamp: line.Timestamp,
					})
				}
```

In the `"user"` case:

```go
			case "user":
				if prompt := source.ExtractUserPrompt(payload.Content); prompt != "" {
					info.LastUserPrompt = prompt
					source.AppendConversationMessage(&info, source.ConversationMessage{
						Role:      "user",
						Text:      source.OneLinePreview(prompt, 120),
						Timestamp: line.Timestamp,
					})
				}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/discovery/claudecode ./internal/discovery/codex`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/discovery/claudecode/transcript.go internal/discovery/claudecode/transcript_test.go internal/discovery/codex/codex.go internal/discovery/codex/transcript.go internal/discovery/codex/transcript_test.go
git commit -m "feat: parse recent transcript messages"
```

---

## Task 3: Session View Builder

**Files:**
- Modify: `internal/state/state.go`
- Create: `internal/sessionview/view.go`
- Create: `internal/sessionview/view_test.go`

- [ ] **Step 1: Write failing builder tests**

Create `internal/sessionview/view_test.go`:

```go
package sessionview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

type fakeMeta struct {
	meta       map[string]source.SessionMeta
	transcript source.TranscriptInfo
	err        error
}

func (f fakeMeta) LatestMeta() map[string]source.SessionMeta { return f.meta }
func (f fakeMeta) Transcript(string, string, source.SessionMeta) (source.TranscriptInfo, error) {
	return f.transcript, f.err
}

func seedStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.InsertSession(context.Background(), state.Session{
		SessionID:   "session-1",
		Agent:       state.AgentCodex,
		PID:         1234,
		FirstSeenAt: "2026-05-14T10:00:00Z",
		LastEvent:   state.EventUserPromptSubmit,
		LastEventAt: "2026-05-14T10:01:00Z",
		StatusAt:    "2026-05-14T10:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCardsIncludeAgentStatusTitleAndHint(t *testing.T) {
	store := seedStore(t)
	p := Provider{
		Store: store,
		Meta: fakeMeta{meta: map[string]source.SessionMeta{
			"session-1": {Cwd: "/home/test/github/agent-status", WaitingFor: "approve shell"},
		}},
	}

	cards, err := p.Cards(context.Background())
	if err != nil {
		t.Fatalf("Cards() error = %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("len(Cards()) = %d, want 1", len(cards))
	}
	card := cards[0]
	if card.Agent != state.AgentCodex || card.Status != "active" {
		t.Fatalf("card identity = %#v", card)
	}
	if card.Title != "agent-status" {
		t.Fatalf("Title = %q, want agent-status", card.Title)
	}
	if card.Subtitle != "approve shell" {
		t.Fatalf("Subtitle = %q, want approve shell", card.Subtitle)
	}
}

func TestDetailReturnsMetadataNotesAndNewestFirstConversation(t *testing.T) {
	store := seedStore(t)
	notesPath := filepath.Join(t.TempDir(), "notes.json")
	if err := os.WriteFile(notesPath, []byte(`{"session-1":"follow up"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Provider{
		Store:     store,
		NotesPath: notesPath,
		Meta: fakeMeta{
			meta: map[string]source.SessionMeta{
				"session-1": {Cwd: "/home/test/github/agent-status", Model: "gpt-5.5", Version: "0.128.0", PID: 1234},
			},
			transcript: source.TranscriptInfo{
				GitBranch: "feature/ui",
				RecentMessages: []source.ConversationMessage{
					{Role: "user", Text: "older", Timestamp: "2026-05-14T10:00:00Z"},
					{Role: "assistant", Text: "newer", Timestamp: "2026-05-14T10:01:00Z"},
				},
			},
		},
	}

	detail, err := p.Detail(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if got := fieldValue(detail.Metadata, "note"); got != "follow up" {
		t.Fatalf("note field = %q, want follow up", got)
	}
	if got := fieldValue(detail.Metadata, "branch"); got != "feature/ui" {
		t.Fatalf("branch field = %q, want feature/ui", got)
	}
	if len(detail.Conversation) != 2 || detail.Conversation[0].Text != "newer" || detail.Conversation[1].Text != "older" {
		t.Fatalf("conversation order = %#v", detail.Conversation)
	}
}

func TestDetailKeepsMetadataWhenTranscriptFails(t *testing.T) {
	store := seedStore(t)
	p := Provider{
		Store: store,
		Meta: fakeMeta{
			meta: map[string]source.SessionMeta{"session-1": {Cwd: "/tmp/project"}},
			err:  errors.New("parse failed"),
		},
	}

	detail, err := p.Detail(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if detail.TranscriptError == "" {
		t.Fatalf("TranscriptError was empty")
	}
	if got := fieldValue(detail.Metadata, "cwd"); got != "/tmp/project" {
		t.Fatalf("cwd field = %q, want /tmp/project", got)
	}
}

func fieldValue(fields []Field, label string) string {
	for _, f := range fields {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sessionview`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add shared not-found error**

Add this to `internal/state/state.go` near other package-level declarations:

```go
var ErrSessionNotFound = errors.New("session not found")
```

- [ ] **Step 4: Implement the session view builder**

Create `internal/sessionview/view.go`:

```go
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
	SessionID    string `json:"session_id"`
	Agent        string `json:"agent"`
	Status       string `json:"status"`
	Title        string `json:"title"`
	Subtitle     string `json:"subtitle"`
	ActivityTime string `json:"activity_time"`
	FirstSeenAt  string `json:"first_seen_at"`
	StatusAt     string `json:"status_at"`
	Note         string `json:"note,omitempty"`
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
	out := make([]SessionCard, 0, len(sessions))
	for _, sess := range sessions {
		m := meta[sess.SessionID]
		out = append(out, SessionCard{
			SessionID:    sess.SessionID,
			Agent:        sess.Agent,
			Status:       state.DeriveStatus(sess),
			Title:        titleFor(m.Cwd),
			Subtitle:     subtitleFor(sess, m),
			ActivityTime: relTime(sess.StatusTime),
			FirstSeenAt:  sess.FirstSeenAt,
			StatusAt:     sess.StatusAt,
			Note:         notes[sess.SessionID],
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
		Title:     titleFor(m.Cwd),
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
```

Add the helper functions below the methods:

```go
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

func titleFor(cwd string) string {
	if cwd == "" {
		return "-"
	}
	base := filepath.Base(cwd)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return cwd
	}
	return base
}

func subtitleFor(sess state.Session, meta source.SessionMeta) string {
	if meta.WaitingFor != "" {
		return meta.WaitingFor
	}
	if sess.LastEvent != "" {
		return sess.LastEvent
	}
	return "-"
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
	return []Field{
		{Label: "agent", Value: valueOrDash(sess.Agent)},
		{Label: "model", Value: valueOrDash(model)},
		{Label: "branch", Value: valueOrDash(info.GitBranch)},
		{Label: "version", Value: valueOrDash(version)},
		{Label: "pid", Value: pid},
		{Label: "cwd", Value: valueOrDash(meta.Cwd)},
		{Label: "waiting", Value: valueOrDash(meta.WaitingFor)},
		{Label: "note", Value: valueOrDash(note)},
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

```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/sessionview`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sessionview/view.go internal/sessionview/view_test.go internal/state/state.go
git commit -m "feat: add session view builder"
```

---

## Task 4: Server and Client View Endpoints

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/client/client.go`
- Modify: `internal/client/client_test.go`
- Modify: `internal/cli/server.go`

- [ ] **Step 1: Write failing server endpoint test**

Add to `internal/server/server_test.go`:

```go
type fakeViews struct {
	cards  []sessionview.SessionCard
	detail sessionview.SessionDetail
	err    error
}

func (f fakeViews) Cards(context.Context) ([]sessionview.SessionCard, error) {
	return f.cards, f.err
}

func (f fakeViews) Detail(context.Context, string) (sessionview.SessionDetail, error) {
	return f.detail, f.err
}

func TestViewEndpointsReturnSessionViews(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	views := fakeViews{
		cards: []sessionview.SessionCard{{
			SessionID: "session-1",
			Agent:     state.AgentCodex,
			Status:    "active",
			Title:     "agent-status",
		}},
		detail: sessionview.SessionDetail{
			SessionID: "session-1",
			Agent:     state.AgentCodex,
			Status:    "active",
			Title:     "agent-status",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/views/sessions", nil)
	rr := httptest.NewRecorder()
	HandlerWithViews(store, nil, views).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", rr.Code, http.StatusOK)
	}
	var cards []sessionview.SessionCard
	if err := json.Unmarshal(rr.Body.Bytes(), &cards); err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Agent != state.AgentCodex {
		t.Fatalf("cards = %#v", cards)
	}

	req = httptest.NewRequest(http.MethodGet, "/views/sessions/session-1", nil)
	rr = httptest.NewRecorder()
	HandlerWithViews(store, nil, views).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d", rr.Code, http.StatusOK)
	}
	var detail sessionview.SessionDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.SessionID != "session-1" {
		t.Fatalf("detail = %#v", detail)
	}
}
```

Add imports:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

- [ ] **Step 2: Write failing client tests**

Add to `internal/client/client_test.go`:

```go
func TestSessionCardsDecodesViewEndpoint(t *testing.T) {
	c := &Client{
		endpoint: "collector.test",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/views/sessions" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`[{"session_id":"s1","agent":"codex","status":"active","title":"agent-status"}]`)),
			}, nil
		})},
	}

	cards, err := c.SessionCards(context.Background())
	if err != nil {
		t.Fatalf("SessionCards() error = %v", err)
	}
	if len(cards) != 1 || cards[0].Title != "agent-status" {
		t.Fatalf("cards = %#v", cards)
	}
}

func TestSessionDetailDecodesViewEndpoint(t *testing.T) {
	c := &Client{
		endpoint: "collector.test",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/views/sessions/s1" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"session_id":"s1","agent":"codex","status":"active","title":"agent-status"}`)),
			}, nil
		})},
	}

	detail, err := c.SessionDetail(context.Background(), "s1")
	if err != nil {
		t.Fatalf("SessionDetail() error = %v", err)
	}
	if detail.SessionID != "s1" {
		t.Fatalf("detail = %#v", detail)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/server ./internal/client`

Expected: FAIL because `HandlerWithViews`, `SessionCards`, and `SessionDetail` are undefined.

- [ ] **Step 4: Implement server endpoints**

Modify `internal/server/server.go`.

Add import:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

Add interfaces and default implementation below `nopMeta`:

```go
type ViewProvider interface {
	Cards(context.Context) ([]sessionview.SessionCard, error)
	Detail(context.Context, string) (sessionview.SessionDetail, error)
}

type nopViews struct{}

func (nopViews) Cards(context.Context) ([]sessionview.SessionCard, error) {
	return []sessionview.SessionCard{}, nil
}

func (nopViews) Detail(context.Context, string) (sessionview.SessionDetail, error) {
	return sessionview.SessionDetail{}, state.ErrSessionNotFound
}
```

Keep `Handler` and add `HandlerWithViews`:

```go
func Handler(s *state.Store, mp MetaProvider) http.Handler {
	return HandlerWithViews(s, mp, nopViews{})
}

func HandlerWithViews(s *state.Store, mp MetaProvider, vp ViewProvider) http.Handler {
	if mp == nil {
		mp = nopMeta{}
	}
	if vp == nil {
		vp = nopViews{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", makeHookHandler(s))
	mux.HandleFunc("GET /state", makeStateListHandler(s))
	mux.HandleFunc("GET /state/{session_id}", makeStateOneHandler(s))
	mux.HandleFunc("GET /state/{session_id}/transcript", makeTranscriptHandler(s, mp))
	mux.HandleFunc("GET /meta", makeMetaHandler(mp))
	mux.HandleFunc("GET /views/sessions", makeSessionCardsHandler(vp))
	mux.HandleFunc("GET /views/sessions/{session_id}", makeSessionDetailHandler(vp))
	mux.HandleFunc("GET /healthz", makeHealthHandler())
	mux.HandleFunc("GET /version", makeVersionHandler())
	return logMiddleware(mux)
}
```

Add handlers:

```go
func makeSessionCardsHandler(vp ViewProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cards, err := vp.Cards(r.Context())
		if err != nil {
			http.Error(w, "session views failed", http.StatusInternalServerError)
			return
		}
		writeJSON(r.Context(), w, http.StatusOK, cards)
	}
}

func makeSessionDetailHandler(vp ViewProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("session_id")
		if id == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		detail, err := vp.Detail(r.Context(), id)
		if err != nil {
			if errors.Is(err, state.ErrSessionNotFound) {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
			http.Error(w, "session detail failed", http.StatusInternalServerError)
			return
		}
		writeJSON(r.Context(), w, http.StatusOK, detail)
	}
}
```

- [ ] **Step 5: Implement client methods**

Modify `internal/client/client.go`.

Add import:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

Add methods:

```go
func (c *Client) SessionCards(ctx context.Context) ([]sessionview.SessionCard, error) {
	var out []sessionview.SessionCard
	if err := c.getJSON(ctx, "/views/sessions", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) SessionDetail(ctx context.Context, id string) (sessionview.SessionDetail, error) {
	var out sessionview.SessionDetail
	if err := c.getJSON(ctx, "/views/sessions/"+id, &out); err != nil {
		return sessionview.SessionDetail{}, err
	}
	return out, nil
}
```

- [ ] **Step 6: Wire the daemon to sessionview**

Modify `internal/cli/server.go`.

Add import:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

In `runServer`, after `s, err := state.Open(statePath)`:

```go
	views := sessionview.Provider{
		Store:     s,
		Meta:      discoveryMeta{},
		NotesPath: state.NotesPath(statePath),
	}
```

Change the HTTP server handler:

```go
		Handler:           server.HandlerWithViews(s, discoveryMeta{}, views),
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/server ./internal/client ./internal/cli`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go internal/client/client.go internal/client/client_test.go internal/cli/server.go
git commit -m "feat: expose session view endpoints"
```

---

## Task 5: TUI Loads Session Views

**Files:**
- Modify: `internal/cli/ui/ui.go`
- Modify: `internal/cli/ui/commands.go`
- Modify: `internal/cli/ui/actions.go`
- Modify: `internal/cli/ui/sort.go`
- Modify: `internal/cli/ui/view_test.go`

- [ ] **Step 1: Write failing load/render tests**

Add to `internal/cli/ui/view_test.go`:

```go
func TestViewShowsSessionCardsAndRightPaneDetail(t *testing.T) {
	m := uiModel{
		width:  100,
		height: 28,
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Subtitle:  "UserPromptSubmit",
			StatusAt:  "2026-05-14T10:00:00Z",
		}},
		selectedID: "s1",
		detailFor:  "s1",
		detail: sessionview.SessionDetail{
			SessionID: "s1",
			Agent:     "codex",
			Status:    "active",
			Title:     "agent-status",
			Metadata: []sessionview.Field{
				{Label: "model", Value: "gpt-5.5"},
				{Label: "branch", Value: "feature/ui"},
			},
			Conversation: []sessionview.ConversationMessage{
				{Role: "user", Text: "newest"},
				{Role: "assistant", Text: "older"},
			},
		},
	}

	out := m.View()
	for _, want := range []string{"CODEX", "agent-status", "Metadata", "model", "gpt-5.5", "Conversation", "User", "newest"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q; output:\n%s", want, out)
		}
	}
	if strings.Index(out, "newest") > strings.Index(out, "older") {
		t.Fatalf("conversation not newest first; output:\n%s", out)
	}
}
```

Add import:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ui`

Expected: FAIL because `uiModel` has no `cards` or `sessionview` detail fields.

- [ ] **Step 3: Change UI model fields**

Modify `internal/cli/ui/ui.go`.

Add import:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

Replace raw session fields:

```go
	sessions       []state.Session
	meta           map[string]source.SessionMeta
```

with:

```go
	cards          []sessionview.SessionCard
```

Replace:

```go
	detail         source.TranscriptInfo
```

with:

```go
	detail         sessionview.SessionDetail
```

Keep `notes map[string]string` for note input editing until note writes are moved behind the daemon.

- [ ] **Step 4: Load cards and selected detail**

Modify `internal/cli/ui/commands.go`.

Use this message shape:

```go
type snapshotMsg struct {
	cards     []sessionview.SessionCard
	detail    sessionview.SessionDetail
	detailFor string
	sortedBy  sortMode
	serverUp  bool
}
```

Replace `loadSnapshot` with:

```go
func loadSnapshot(serverAddr, selectedID string, mode sortMode) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c := client.New(serverAddr)
		cards, err := c.SessionCards(ctx)
		serverUp := err == nil
		if err != nil {
			cards = nil
		}
		sortCards(cards, mode)
		focus := selectedID
		if focus == "" && len(cards) > 0 {
			focus = cards[0].SessionID
		}
		var detail sessionview.SessionDetail
		if focus != "" {
			detail, _ = c.SessionDetail(ctx, focus)
		}
		return snapshotMsg{
			cards:     cards,
			detail:    detail,
			detailFor: focus,
			sortedBy:  mode,
			serverUp:  serverUp,
		}
	}
}
```

Remove unused `source` and `state` imports if they are no longer needed in `commands.go`.

- [ ] **Step 5: Update selection helpers**

Modify `internal/cli/ui/actions.go`.

Replace session-based helpers with card-based helpers:

```go
func (m *uiModel) moveSelection(delta int) {
	if len(m.cards) == 0 {
		m.selectedID = ""
		return
	}
	cur := 0
	for i, c := range m.cards {
		if c.SessionID == m.selectedID {
			cur = i
			break
		}
	}
	next := cur + delta
	if next < 0 {
		next = 0
	} else if next >= len(m.cards) {
		next = len(m.cards) - 1
	}
	m.selectedID = m.cards[next].SessionID
}

func (m uiModel) activeSelectionID() string {
	if m.selectedID != "" {
		return m.selectedID
	}
	if len(m.cards) > 0 {
		return m.cards[0].SessionID
	}
	return ""
}

func cardsContain(cards []sessionview.SessionCard, id string) bool {
	for _, c := range cards {
		if c.SessionID == id {
			return true
		}
	}
	return false
}
```

Update imports to include `sessionview` and remove `state` if unused.

- [ ] **Step 6: Add card sorting**

Modify `internal/cli/ui/sort.go`.

Add import:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

Add:

```go
func sortCards(cards []sessionview.SessionCard, mode sortMode) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		switch mode {
		case sortCreated:
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt > b.FirstSeenAt
			}
		case sortStatus:
			ra, rb := statusRank(a.Status), statusRank(b.Status)
			if ra != rb {
				return ra < rb
			}
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt < b.FirstSeenAt
			}
		default:
			if a.StatusAt != b.StatusAt {
				return a.StatusAt > b.StatusAt
			}
		}
		return a.SessionID < b.SessionID
	})
}
```

Keep `sortSessions` until all callers are removed, then delete it only if `go test ./...` confirms it is unused.

- [ ] **Step 7: Update model snapshot handling**

Modify the `snapshotMsg` case in `internal/cli/ui/ui.go`:

```go
	case snapshotMsg:
		m.cards = msg.cards
		m.detail = msg.detail
		m.detailFor = msg.detailFor
		m.serverUp = msg.serverUp
		if msg.sortedBy != m.sort {
			sortCards(m.cards, m.sort)
		}
		if m.selectedID != "" && !cardsContain(m.cards, m.selectedID) {
			m.selectedID = ""
		}
		m.err = nil
```

- [ ] **Step 8: Run tests to verify they pass through data-loading changes**

Run: `go test ./internal/cli/ui`

Expected: still FAIL only on render expectations from Step 1.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/ui/ui.go internal/cli/ui/commands.go internal/cli/ui/actions.go internal/cli/ui/sort.go internal/cli/ui/view_test.go
git commit -m "feat: load session views in tui"
```

---

## Task 6: Dual-Pane TUI Rendering

**Files:**
- Modify: `internal/cli/ui/view.go`
- Modify: `internal/cli/ui/view_test.go`

- [ ] **Step 1: Write focused render tests for narrow layout**

Add to `internal/cli/ui/view_test.go`:

```go
func TestViewKeepsAgentVisibleInNarrowPane(t *testing.T) {
	m := uiModel{
		width:  70,
		height: 20,
		cards: []sessionview.SessionCard{{
			SessionID: "s1",
			Agent:     "claude-code",
			Status:    "waiting",
			Title:     "a-very-long-project-name-that-will-truncate",
			Subtitle:  "approve Bash",
		}},
		selectedID: "s1",
	}

	out := m.View()
	if !strings.Contains(out, "CLAUDE-CODE") {
		t.Fatalf("View() should keep agent kind visible; output:\n%s", out)
	}
	if !strings.Contains(out, "approve Bash") {
		t.Fatalf("View() should keep waiting hint visible; output:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ui`

Expected: FAIL because the current renderer is still table-based.

- [ ] **Step 3: Add pane layout helpers**

Modify `internal/cli/ui/view.go`. Add constants:

```go
const (
	minLeftPane  = 24
	maxLeftPane  = 46
	paneGap      = 2
	cardPadWidth = 2
)
```

Add:

```go
func (m uiModel) paneWidths() (int, int) {
	inner := m.width - 4
	if inner < 50 {
		left := minLeftPane
		right := inner - left - paneGap
		if right < 20 {
			right = 20
		}
		return left, right
	}
	left := inner * 38 / 100
	if left < minLeftPane {
		left = minLeftPane
	}
	if left > maxLeftPane {
		left = maxLeftPane
	}
	right := inner - left - paneGap
	if right < 20 {
		right = 20
	}
	return left, right
}
```

- [ ] **Step 4: Render session cards**

Add:

```go
func (m uiModel) renderCards(width int, selectedID string) string {
	if len(m.cards) == 0 {
		return dimStyle.Render("(no live sessions)")
	}
	lines := []string{headerStyle.Render("Sessions")}
	for _, card := range m.cards {
		selected := card.SessionID == selectedID
		lines = append(lines, renderCard(card, width, selected, m.detailFor == card.SessionID, m.detail))
	}
	return strings.Join(lines, "\n")
}

func renderCard(card sessionview.SessionCard, width int, selected, hasDetail bool, detail sessionview.SessionDetail) string {
	if width < 10 {
		width = 10
	}
	agent := strings.ToUpper(card.Agent)
	status := card.Status
	title := truncate(card.Title, width-cardPadWidth)
	subtitle := truncate(card.Subtitle, width-cardPadWidth)
	top := fmt.Sprintf("%-*s %s", max(width-len(status)-1, 1), agent, status)
	parts := []string{top, title, subtitle}
	if selected && hasDetail && len(detail.Conversation) > 0 {
		preview := detail.Conversation[0].Role + ": " + detail.Conversation[0].Text
		parts = append(parts, truncate(preview, width-cardPadWidth))
	}
	body := strings.Join(parts, "\n")
	return rowStyle(card.Status, selected).Render(body)
}
```

Add `sessionview` to imports:

```go
	"github.com/samling/agent-status/internal/sessionview"
```

- [ ] **Step 5: Render metadata and conversation detail**

Add:

```go
func renderSessionDetail(detail sessionview.SessionDetail, width int) string {
	if detail.SessionID == "" {
		return dimStyle.Render("select a session")
	}
	lines := []string{
		accentStyle.Render(detail.Title),
		rowStyle(detail.Status, false).Render(detail.Agent + " " + detail.Status),
		"",
		headerStyle.Render("Metadata"),
	}
	lines = append(lines, renderMetadata(detail.Metadata, width)...)
	lines = append(lines, "", headerStyle.Render("Conversation"))
	if detail.TranscriptError != "" {
		lines = append(lines, errorStyle.Render("transcript: "+detail.TranscriptError))
		return strings.Join(lines, "\n")
	}
	if len(detail.Conversation) == 0 {
		lines = append(lines, dimStyle.Render("(no conversation preview)"))
		return strings.Join(lines, "\n")
	}
	for _, msg := range detail.Conversation {
		label := "User"
		if msg.Role == "assistant" {
			label = "AI"
		}
		line := fmt.Sprintf("%-4s %s", label, msg.Text)
		lines = append(lines, truncate(line, width))
	}
	return strings.Join(lines, "\n")
}

func renderMetadata(fields []sessionview.Field, width int) []string {
	if len(fields) == 0 {
		return []string{dimStyle.Render("(no metadata)")}
	}
	lines := make([]string, 0, (len(fields)+1)/2)
	for i := 0; i < len(fields); i += 2 {
		left := labeledField(fields[i].Label, fields[i].Value)
		right := ""
		if i+1 < len(fields) {
			right = labeledField(fields[i+1].Label, fields[i+1].Value)
		}
		line := strings.TrimSpace(left + "   " + right)
		lines = append(lines, truncate(line, width))
	}
	return lines
}
```

- [ ] **Step 6: Replace table rendering in View**

In `View`, replace the table/detail-bottom construction with:

```go
		selectedID := m.selectedID
		if selectedID == "" && len(m.cards) > 0 {
			selectedID = m.cards[0].SessionID
		}

		if m.err != nil {
			head.WriteString(errorStyle.Render("error: " + m.err.Error()))
		} else {
			leftW, rightW := m.paneWidths()
			left := lipgloss.NewStyle().Width(leftW).Render(m.renderCards(leftW, selectedID))
			rightDetail := sessionview.SessionDetail{}
			if m.detailFor == selectedID {
				rightDetail = m.detail
			}
			right := lipgloss.NewStyle().Width(rightW).Render(renderSessionDetail(rightDetail, rightW))
			head.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", paneGap), right))
		}
```

Update the header count:

```go
	head.WriteString(accentStyle.Render(fmt.Sprintf(", %d session(s)", len(m.cards))))
```

Remove the old bottom `renderDetail` call for selected session. Keep `m.renderConfig()` behavior so `?` still replaces the right/bottom content with config if needed.

- [ ] **Step 7: Run UI tests**

Run: `go test ./internal/cli/ui`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/ui/view.go internal/cli/ui/view_test.go
git commit -m "feat: render dual-pane session ui"
```

---

## Task 7: Full Verification

**Files:**
- Verify all changed files.

- [ ] **Step 1: Run all tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Check working tree**

Run: `git status --short`

Expected: only pre-existing unrelated local files remain, or an empty tree if those were included intentionally by their owner.

- [ ] **Step 3: Manual TUI smoke test**

Run with the server already running:

```bash
go run ./cmd/agent-status ui
```

Expected:

- Header shows connectivity and session count.
- Left pane shows cards with visible `CODEX` or `CLAUDE-CODE`.
- Selected card shows one extra preview line when detail has conversation.
- Right pane shows metadata at the top.
- Conversation appears below metadata, newest first.
- `enter` still focuses the selected session.
- `n` still edits the selected session note.

- [ ] **Step 4: Commit verification fix if needed**

If verification exposes a small issue, fix it with a failing test first, then commit:

```bash
git add <changed-files>
git commit -m "fix: stabilize dual-pane session ui"
```

If verification passes without changes, do not create an empty commit.

---

## Self-Review Results

- Spec coverage: transcript previews, server-side session view, view endpoints, client methods, dual-pane TUI, metadata top layout, newest-first conversation, and existing focus behavior are covered.
- Placeholder scan: no incomplete markers remain in this plan.
- Type consistency: `SessionCard`, `SessionDetail`, `Field`, and `ConversationMessage` are defined in `internal/sessionview`; source transcript messages are explicitly converted into view messages.
- Scope check: this plan avoids the larger provider architecture refactor and preserves existing endpoints.
