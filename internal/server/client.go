package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/samling/agent-status/internal/state"
)

// LoadState GETs /state and decodes the response into the same shape
// state.Load returns from disk. Re-parses the RFC3339 timestamps into
// FirstSeenTime / StatusTime so callers don't have to (state.Load
// does the same thing inside materialize). Errors include transport
// failures and non-2xx responses, so callers can use a successful
// LoadState as the collector-reachable signal — no separate TCP probe
// needed.
func LoadState(ctx context.Context, addr string) ([]state.Session, error) {
	if addr == "" {
		return nil, fmt.Errorf("state: empty server address")
	}
	endpoint := "http://" + addr + "/state"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var sessions []state.Session
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("decode state response: %w", err)
	}
	// FirstSeenTime / StatusTime are json:"-" so they don't ride the
	// wire; rehydrate them from the string fields the same way
	// state.materialize does for disk reads.
	for i := range sessions {
		sessions[i].FirstSeenTime, _ = time.Parse(time.RFC3339Nano, sessions[i].FirstSeenAt)
		sessions[i].StatusTime, _ = time.Parse(time.RFC3339Nano, sessions[i].StatusAt)
	}
	return sessions, nil
}

// Focus POSTs to the collector's /focus endpoint and decodes the
// response. Empty sessionID means "first waiting session"; a non-empty
// id targets that session specifically. This is the single client
// every in-process caller (notification activation handler, TUI)
// uses so the HTTP wire shape lives in exactly one place — change
// FocusResponse here and every consumer updates with it.
func Focus(ctx context.Context, addr, sessionID string) (FocusResponse, error) {
	if addr == "" {
		return FocusResponse{}, fmt.Errorf("focus: empty server address")
	}
	path := "/focus"
	if sessionID != "" {
		path = "/focus/" + url.PathEscape(sessionID)
	}
	endpoint := "http://" + addr + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return FocusResponse{}, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return FocusResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return FocusResponse{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var fr FocusResponse
	if err := json.Unmarshal(body, &fr); err != nil {
		return FocusResponse{}, fmt.Errorf("decode focus response: %w", err)
	}
	return fr, nil
}
