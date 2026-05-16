package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSessionsParsesTimes(t *testing.T) {
	c := &Client{
		endpoint: "collector.test",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/state" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`[{
			"session_id":"s1",
			"agent":"codex",
			"first_seen_at":"2026-05-08T00:00:00Z",
			"last_event":"Discovered",
			"last_event_at":"2026-05-08T00:01:00Z",
			"status_at":"2026-05-08T00:02:00Z",
			"engine_status":""
		}]`)),
			}, nil
		})},
	}

	sessions, err := c.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Sessions() length = %d, want 1", len(sessions))
	}
	if sessions[0].FirstSeenTime.IsZero() {
		t.Fatalf("FirstSeenTime was not parsed")
	}
	if sessions[0].StatusTime.IsZero() {
		t.Fatalf("StatusTime was not parsed")
	}
}

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

func TestSessionMessagesDecodesViewEndpoint(t *testing.T) {
	c := &Client{
		endpoint: "collector.test",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/views/sessions/s1/messages" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"session_id":"s1","messages":[{"id":"7","role":"tool_result","preview":"tests passed"}]}`)),
			}, nil
		})},
	}

	messages, err := c.SessionMessages(context.Background(), "s1")
	if err != nil {
		t.Fatalf("SessionMessages() error = %v", err)
	}
	if len(messages.Messages) != 1 || messages.Messages[0].ID != "7" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestSessionMessageDecodesViewEndpoint(t *testing.T) {
	c := &Client{
		endpoint: "collector.test",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/views/sessions/s1/messages/7" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"id":"7","role":"tool_result","text":"tests passed in detail"}`)),
			}, nil
		})},
	}

	detail, err := c.SessionMessage(context.Background(), "s1", "7")
	if err != nil {
		t.Fatalf("SessionMessage() error = %v", err)
	}
	if detail.ID != "7" || detail.Text != "tests passed in detail" {
		t.Fatalf("detail = %#v", detail)
	}
}
