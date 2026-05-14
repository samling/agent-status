package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/samling/agent-status/internal/sessionview"
)

func TestLoadSnapshotSelectsFirstCardWhenSelectionIsStale(t *testing.T) {
	var detailPath string
	var unexpectedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{
				{SessionID: "fresh", Status: "active", FirstSeenAt: "2026-05-14T10:00:00Z"},
			})
		case "/views/sessions/fresh":
			detailPath = r.URL.Path
			writeJSON(w, sessionview.SessionDetail{SessionID: "fresh"})
		default:
			unexpectedPath = r.URL.Path
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := loadSnapshot(serverAddr(server), "stale", sortStatus)().(snapshotMsg)
	if unexpectedPath != "" {
		t.Fatalf("unexpected request path %q", unexpectedPath)
	}
	if msg.detailFor != "fresh" {
		t.Fatalf("detailFor = %q, want fresh", msg.detailFor)
	}
	if msg.detail.SessionID != "fresh" {
		t.Fatalf("detail.SessionID = %q, want fresh", msg.detail.SessionID)
	}
	if detailPath != "/views/sessions/fresh" {
		t.Fatalf("detail path = %q, want /views/sessions/fresh", detailPath)
	}
}

func TestLoadSnapshotDoesNotFetchStaleDetailWhenCardsFail(t *testing.T) {
	var detailPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			detailPath = r.URL.Path
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := loadSnapshot(serverAddr(server), "stale", sortStatus)().(snapshotMsg)
	if msg.serverUp {
		t.Fatal("serverUp = true, want false")
	}
	if msg.detailFor != "" {
		t.Fatalf("detailFor = %q, want empty", msg.detailFor)
	}
	if msg.detail.SessionID != "" {
		t.Fatalf("detail.SessionID = %q, want empty", msg.detail.SessionID)
	}
	if detailPath != "" {
		t.Fatalf("detail endpoint was requested at %q", detailPath)
	}
}

func TestLoadSnapshotCarriesDetailError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions":
			writeJSON(w, []sessionview.SessionCard{
				{SessionID: "s1", Status: "active", FirstSeenAt: "2026-05-14T10:00:00Z"},
			})
		case "/views/sessions/s1":
			http.Error(w, "detail exploded", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := loadSnapshot(serverAddr(server), "s1", sortStatus)().(snapshotMsg)
	if msg.detailFor != "s1" {
		t.Fatalf("detailFor = %q, want s1", msg.detailFor)
	}
	if msg.detailErr == nil {
		t.Fatal("detailErr = nil, want error")
	}
	if !strings.Contains(msg.detailErr.Error(), "detail exploded") {
		t.Fatalf("detailErr = %v, want detail exploded", msg.detailErr)
	}
}

func TestUpdateDownReturnsImmediateDetailLoad(t *testing.T) {
	var detailPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/views/sessions/s2":
			detailPath = r.URL.Path
			writeJSON(w, sessionview.SessionDetail{SessionID: "s2", Title: "second"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := uiModel{
		serverAddr: serverAddr(server),
		selectedID: "s1",
		cards: []sessionview.SessionCard{
			{SessionID: "s1"},
			{SessionID: "s2"},
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	gotModel := updated.(uiModel)
	if gotModel.selectedID != "s2" {
		t.Fatalf("selectedID = %q, want s2", gotModel.selectedID)
	}
	if cmd == nil {
		t.Fatal("Update returned nil command, want immediate detail load")
	}
	msg := cmd()
	got, ok := msg.(detailMsg)
	if !ok {
		t.Fatalf("command returned %T, want detailMsg", msg)
	}
	if detailPath != "/views/sessions/s2" {
		t.Fatalf("detail path = %q, want /views/sessions/s2", detailPath)
	}
	if got.detailFor != "s2" {
		t.Fatalf("detailFor = %q, want s2", got.detailFor)
	}
	if got.detail.SessionID != "s2" {
		t.Fatalf("detail.SessionID = %q, want s2", got.detail.SessionID)
	}
}

func TestUpdateIgnoresStaleDetailMessage(t *testing.T) {
	m := uiModel{
		selectedID: "s2",
		detailFor:  "s2",
		detail:     sessionview.SessionDetail{SessionID: "s2", Title: "current"},
	}

	updated, _ := m.Update(detailMsg{
		detailFor: "s1",
		detail:    sessionview.SessionDetail{SessionID: "s1", Title: "stale"},
	})
	got := updated.(uiModel)

	if got.detailFor != "s2" {
		t.Fatalf("detailFor = %q, want s2", got.detailFor)
	}
	if got.detail.SessionID != "s2" {
		t.Fatalf("detail.SessionID = %q, want s2", got.detail.SessionID)
	}
	if got.detail.Title != "current" {
		t.Fatalf("detail.Title = %q, want current", got.detail.Title)
	}
}

func TestUpdateIgnoresStaleSnapshotDetail(t *testing.T) {
	m := uiModel{
		selectedID: "s2",
		detailFor:  "s2",
		detail:     sessionview.SessionDetail{SessionID: "s2", Title: "current"},
	}

	updated, _ := m.Update(snapshotMsg{
		cards: []sessionview.SessionCard{
			{SessionID: "s1"},
			{SessionID: "s2"},
		},
		detailFor: "s1",
		detail:    sessionview.SessionDetail{SessionID: "s1", Title: "stale"},
		serverUp:  true,
	})
	got := updated.(uiModel)

	if got.detailFor != "s2" {
		t.Fatalf("detailFor = %q, want s2", got.detailFor)
	}
	if got.detail.SessionID != "s2" {
		t.Fatalf("detail.SessionID = %q, want s2", got.detail.SessionID)
	}
	if got.detail.Title != "current" {
		t.Fatalf("detail.Title = %q, want current", got.detail.Title)
	}
}

func TestViewShowsSessionsHeadingForEmptyCards(t *testing.T) {
	m := uiModel{width: 80, height: 20}

	out := m.View()
	for _, want := range []string{"Sessions", "(no live sessions)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q; output:\n%s", want, out)
		}
	}
}

func serverAddr(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
