package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
