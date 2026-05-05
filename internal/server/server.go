package server

import (
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"agent-status/internal/store"
)

func Handler(db *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", makeHookHandler(db))
	mux.HandleFunc("/state", makeStateHandler(db))
	return mux
}

type envelope struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
}

func makeHookHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			log.Printf("hook: %s %s rejected (method not allowed)", r.Method, r.URL.Path)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("hook: read body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			log.Printf("hook: unmarshal: %v (%d bytes)", err, len(body))
		}

		receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.InsertEvent(r.Context(), db, receivedAt, env.SessionID, env.HookEventName, ""); err != nil {
			log.Printf("hook: insert error session=%s event=%s: %v", shortID(env.SessionID), env.HookEventName, err)
			http.Error(w, "insert failed", http.StatusInternalServerError)
			return
		}
		log.Printf("hook: event=%s session=%s", env.HookEventName, shortID(env.SessionID))
		w.WriteHeader(http.StatusNoContent)
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "?"
	}
	return id
}

func makeStateHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions, err := store.QuerySessions(r.Context(), db)
		if err != nil {
			log.Printf("state: query error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("state: %d session(s)", len(sessions))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	}
}
