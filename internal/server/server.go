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
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var env envelope
		_ = json.Unmarshal(body, &env)

		receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.InsertEvent(r.Context(), db, receivedAt, env.SessionID, env.HookEventName, string(body)); err != nil {
			log.Printf("insert error: %v", err)
			http.Error(w, "insert failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func makeStateHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions, err := store.QuerySessions(r.Context(), db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	}
}
