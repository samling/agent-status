package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Session struct {
	SessionID   string          `json:"session_id"`
	Status      string          `json:"status"`
	LastEvent   string          `json:"last_event"`
	LastEventAt string          `json:"last_event_at"`
	FirstSeenAt string          `json:"first_seen_at"`
	LastPayload json.RawMessage `json:"last_payload"`
}

type Event struct {
	ID            int64           `json:"id"`
	ReceivedAt    string          `json:"received_at"`
	SessionID     string          `json:"session_id"`
	HookEventName string          `json:"hook_event_name"`
	Payload       json.RawMessage `json:"payload"`
}

func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			received_at TEXT NOT NULL,
			session_id TEXT,
			hook_event_name TEXT,
			payload TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id);
		CREATE INDEX IF NOT EXISTS idx_events_received ON events(received_at);
	`)
	return err
}

func InsertEvent(ctx context.Context, db *sql.DB, receivedAt, sessionID, hookEventName, payload string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO events (received_at, session_id, hook_event_name, payload) VALUES (?, ?, ?, ?)`,
		receivedAt, sessionID, hookEventName, payload,
	)
	return err
}

// ReapAbsent inserts a synthetic "Reaped" event for every session whose
// latest event is not already SessionEnd or Reaped, and whose session_id
// is NOT in the provided alive set. Used to detect sessions that exited
// without firing SessionEnd (e.g. Ctrl-C). Returns count inserted.
func ReapAbsent(ctx context.Context, db *sql.DB, alive map[string]bool) (int, error) {
	rows, err := db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT session_id, hook_event_name,
			       ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY id DESC) AS rn
			FROM events
			WHERE session_id IS NOT NULL AND session_id != ''
		)
		SELECT session_id FROM ranked
		WHERE rn = 1 AND hook_event_name NOT IN ('SessionEnd', 'Reaped')
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var candidates []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		if !alive[id] {
			candidates = append(candidates, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	inserted := 0
	for _, id := range candidates {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO events (received_at, session_id, hook_event_name, payload) VALUES (?, ?, 'Reaped', '')`,
			receivedAt, id,
		); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}

// DiscoverSession records a synthetic "Discovered" event for a session_id
// only if no events already exist for that session. Returns true if a row
// was inserted, false if the session was already known. Used at collector
// startup to surface sessions that began before the collector was running.
// createdAt should be the session's actual start time when known (e.g. from
// ~/.claude/sessions/<pid>.json startedAt); pass time.Time{} to use now.
func DiscoverSession(ctx context.Context, db *sql.DB, sessionID string, createdAt time.Time) (bool, error) {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	receivedAt := createdAt.UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `
		INSERT INTO events (received_at, session_id, hook_event_name, payload)
		SELECT ?, ?, 'Discovered', ''
		WHERE NOT EXISTS (SELECT 1 FROM events WHERE session_id = ?)
	`, receivedAt, sessionID, sessionID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func QuerySessions(ctx context.Context, db *sql.DB) ([]Session, error) {
	rows, err := db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT session_id, hook_event_name, received_at, payload,
			       ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY id DESC) AS rn
			FROM events
			WHERE session_id IS NOT NULL AND session_id != ''
		),
		firsts AS (
			SELECT session_id, MIN(received_at) AS first_at
			FROM events
			WHERE session_id IS NOT NULL AND session_id != ''
			GROUP BY session_id
		)
		SELECT r.session_id, r.hook_event_name, r.received_at, r.payload, f.first_at
		FROM ranked r
		JOIN firsts f USING (session_id)
		WHERE r.rn = 1
		ORDER BY r.received_at DESC;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Session{}
	for rows.Next() {
		var s Session
		var payload string
		if err := rows.Scan(&s.SessionID, &s.LastEvent, &s.LastEventAt, &payload, &s.FirstSeenAt); err != nil {
			return nil, err
		}
		if payload != "" {
			s.LastPayload = json.RawMessage(payload)
		}
		switch s.LastEvent {
		case "SessionEnd", "Reaped":
			s.Status = "ended"
		case "SessionStart", "Stop", "StopFailure", "Discovered":
			s.Status = "idle"
		case "Notification", "PermissionRequest":
			s.Status = "waiting"
		default:
			s.Status = "active"
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func QueryEventsAfter(ctx context.Context, db *sql.DB, afterID int64) ([]Event, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, received_at, COALESCE(session_id, ''), COALESCE(hook_event_name, ''), payload
		FROM events
		WHERE id > ?
		ORDER BY id ASC
	`, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var e Event
		var payload string
		if err := rows.Scan(&e.ID, &e.ReceivedAt, &e.SessionID, &e.HookEventName, &payload); err != nil {
			return nil, err
		}
		if payload != "" {
			e.Payload = json.RawMessage(payload)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func MaxEventID(ctx context.Context, db *sql.DB) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&id)
	return id, err
}
