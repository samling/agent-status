// Package client is the small HTTP client every standalone process uses to
// read live state from the running collector. It exists so the daemon's
// /state surface is the single source of truth for clients (focus
// subcommand, statusline, TUI) and they don't each grow their own
// state.json file readers.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

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
