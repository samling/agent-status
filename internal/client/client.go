// Package client is the local-process API every standalone process uses to
// interact with the agent-status system: HTTP reads against the running
// collector (Sessions, Session) plus locally-orchestrated actions that
// compose those reads with side effects (Focus). The daemon does not import
// this package; the CLI focus subcommand is the boundary so the daemon's
// dependency graph stays free of internal/focus.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/focus"
	"github.com/samling/agent-status/internal/sessionview"
	"github.com/samling/agent-status/internal/state"
)

// ErrSessionNotFound signals a 404 response from /state/{session_id}.
var ErrSessionNotFound = errors.New("session not found")

// Client talks to the collector at endpoint (host:port). The collector binds
// 127.0.0.1 by default, so callers must run as the same user on the same
// host as the daemon.
type Client struct {
	endpoint string
	http     *http.Client
}

// New returns a Client with a 5s default timeout.
func New(endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		http:     &http.Client{Timeout: 5 * time.Second},
	}
}

// Sessions returns the live session list from GET /state.
func (c *Client) Sessions(ctx context.Context) ([]state.Session, error) {
	var out []state.Session
	if err := c.getJSON(ctx, "/state", &out); err != nil {
		return nil, err
	}
	for i := range out {
		parseSessionTimes(&out[i])
	}
	return out, nil
}

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

// Health probes GET /healthz; returns nil when the collector is reachable
// and responding 200.
func (c *Client) Health(ctx context.Context) error {
	var out map[string]string
	return c.getJSON(ctx, "/healthz", &out)
}

// Version fetches the collector's reported version from GET /version.
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, "/version", &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// Meta returns the daemon's most recent SessionMeta snapshot, keyed by
// session id, from GET /meta.
func (c *Client) Meta(ctx context.Context) (map[string]source.SessionMeta, error) {
	var out map[string]source.SessionMeta
	if err := c.getJSON(ctx, "/meta", &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]source.SessionMeta{}
	}
	return out, nil
}

// Transcript returns the parsed transcript for sessionID via GET
// /state/{session_id}/transcript. Returns ErrSessionNotFound if the daemon
// doesn't know the session.
func (c *Client) Transcript(ctx context.Context, sessionID string) (source.TranscriptInfo, error) {
	var out source.TranscriptInfo
	if err := c.getJSON(ctx, "/state/"+sessionID+"/transcript", &out); err != nil {
		return source.TranscriptInfo{}, err
	}
	return out, nil
}

// Session returns one session by id from GET /state/{session_id}.
// Returns ErrSessionNotFound when the server responds 404.
func (c *Client) Session(ctx context.Context, id string) (state.Session, error) {
	var out state.Session
	if err := c.getJSON(ctx, "/state/"+id, &out); err != nil {
		return state.Session{}, err
	}
	return out, nil
}

// Focus resolves id via Session and brings the session's window and tmux
// pane to the foreground. Returns the human-readable result message from
// the focus backend (e.g. "Focused via niri") on success, or an error.
//
// This is the single focus pathway used by every in-process caller (focus
// subcommand, TUI, future tooling). Out-of-process callers (notification
// activation in the daemon) exec the focus subcommand instead, which is a
// thin wrapper around this method.
func (c *Client) Focus(ctx context.Context, id string) (string, error) {
	sess, err := c.Session(ctx, id)
	if err != nil {
		return "", err
	}
	if sess.PID <= 0 {
		return "", fmt.Errorf("focus: session %s has no live PID", id)
	}
	return focus.PID(sess.PID)
}

func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	url := "http://" + c.endpoint + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("collector unreachable at %s: %w (is `agent-status server` running?)", c.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func parseSessionTimes(sess *state.Session) {
	sess.FirstSeenTime, _ = time.Parse(time.RFC3339Nano, sess.FirstSeenAt)
	sess.StatusTime, _ = time.Parse(time.RFC3339Nano, sess.StatusAt)
}
