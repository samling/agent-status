package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"agent-status/internal/state"
)

func Handler(s *state.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", makeHookHandler(s))
	mux.HandleFunc("/state", makeStateHandler(s))
	return mux
}

type envelope struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
}

func makeHookHandler(s *state.Store) http.HandlerFunc {
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
		if err := s.RecordEvent(env.SessionID, env.HookEventName, receivedAt); err != nil {
			log.Printf("hook: record error session=%s event=%s: %v", state.ShortID(env.SessionID), env.HookEventName, err)
			http.Error(w, "record failed", http.StatusInternalServerError)
			return
		}
		log.Printf("hook: event=%s session=%s", env.HookEventName, state.ShortID(env.SessionID))

		w.WriteHeader(http.StatusNoContent)
	}
}

func makeStateHandler(s *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions := s.Sessions()
		log.Printf("state: %d session(s)", len(sessions))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	}
}
