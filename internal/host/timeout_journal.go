// Package host — timeout_journal.go: SQLite-backed timeout persistence.
//
// TimeoutStore is the persistence seam for the timeout dispatcher.  The
// concrete implementation (SQLiteTimeoutStore) owns the timeouts table in the
// same SQLite database used by the session store, so no second file handle is
// required.  The orchestrator depends only on the TimeoutStore interface; the
// database/sql import stays inside internal/host/.
//
// Table layout:
//
//	CREATE TABLE IF NOT EXISTS timeouts (
//	    session_id  TEXT    NOT NULL,
//	    state_path  TEXT    NOT NULL,
//	    target      TEXT    NOT NULL,
//	    fire_at     INTEGER NOT NULL,  -- Unix milli
//	    payload     TEXT    NOT NULL,  -- JSON (reserved for future use)
//	    fired       INTEGER NOT NULL DEFAULT 0,
//	    PRIMARY KEY (session_id, state_path)
//	);
//
// The primary key is (session_id, state_path) because each session can have at
// most one pending timeout per state; a new arm() replaces the previous row.
// fired=1 rows are kept until the next arm() or an explicit Fire() so that
// RearmPersisted can skip them without a delete-race.
package host

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // ensure driver is linked when host is imported
)

// TimeoutEntry is the persisted record for one pending timeout.
type TimeoutEntry struct {
	SessionID string
	StatePath string
	Target    string
	FireAt    time.Time
	// Payload is reserved for future versioning; currently always "{}".
	Payload json.RawMessage
}

// TimeoutStore is the persistence seam for the timeout dispatcher.
// All methods must be safe for concurrent use.
type TimeoutStore interface {
	// Schedule upserts a timeout entry.  If (session_id, state_path) already
	// exists the row is replaced.
	Schedule(e TimeoutEntry) error

	// Cancel removes the row for (sessionID, statePath).  Idempotent — no
	// error if the row does not exist.
	Cancel(sessionID, statePath string) error

	// Pending returns every row where fired=0.  Used by RearmPersisted on
	// orchestrator startup to restore in-memory timers.
	Pending() ([]TimeoutEntry, error)

	// Fire marks (sessionID, statePath) as fired so a second restart does not
	// replay the same timeout.  Idempotent — no error if the row does not exist.
	Fire(sessionID, statePath string) error
}

// ── NoopTimeoutStore ─────────────────────────────────────────────────────────

// noopTimeoutStore is the default when no *sql.DB is available (in-memory
// store tests, CLI paths that don't wire a journal).  All operations succeed
// without storing anything.
type noopTimeoutStore struct{}

// NewNoopTimeoutStore returns a TimeoutStore that silently discards all calls.
// Use this when SQLite persistence is not needed (e.g. in-memory test rigs).
func NewNoopTimeoutStore() TimeoutStore { return &noopTimeoutStore{} }

func (*noopTimeoutStore) Schedule(_ TimeoutEntry) error    { return nil }
func (*noopTimeoutStore) Cancel(_, _ string) error         { return nil }
func (*noopTimeoutStore) Pending() ([]TimeoutEntry, error) { return nil, nil }
func (*noopTimeoutStore) Fire(_, _ string) error           { return nil }

// ── SQLiteTimeoutStore ───────────────────────────────────────────────────────

const timeoutsTableDDL = `
CREATE TABLE IF NOT EXISTS timeouts (
    session_id  TEXT    NOT NULL,
    state_path  TEXT    NOT NULL,
    target      TEXT    NOT NULL,
    fire_at     INTEGER NOT NULL,
    payload     TEXT    NOT NULL DEFAULT '{}',
    fired       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, state_path)
);`

// SQLiteTimeoutStore persists pending timeout entries in the timeouts table.
type SQLiteTimeoutStore struct {
	db *sql.DB
}

// NewSQLiteTimeoutStore opens (or creates) the timeouts table in db and
// returns a ready-to-use TimeoutStore.  db must already be open; this call
// only adds the table if it does not yet exist. If an old schema exists,
// it is migrated to the current schema.
func NewSQLiteTimeoutStore(db *sql.DB) (*SQLiteTimeoutStore, error) {
	if db == nil {
		return nil, fmt.Errorf("host.NewSQLiteTimeoutStore: db must not be nil")
	}
	if _, err := db.Exec(timeoutsTableDDL); err != nil {
		return nil, fmt.Errorf("host.NewSQLiteTimeoutStore: create table: %w", err)
	}

	// Check if table exists and has the correct schema. If an old table exists
	// without fire_at column, drop and recreate it.
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('timeouts') WHERE name = 'fire_at'`).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("host.NewSQLiteTimeoutStore: schema check: %w", err)
	}
	if count == 0 {
		// Column doesn't exist; drop and recreate the table with the correct schema
		if _, err := db.Exec(`DROP TABLE IF EXISTS timeouts`); err != nil {
			return nil, fmt.Errorf("host.NewSQLiteTimeoutStore: drop old table: %w", err)
		}
		if _, err := db.Exec(timeoutsTableDDL); err != nil {
			return nil, fmt.Errorf("host.NewSQLiteTimeoutStore: recreate table: %w", err)
		}
	}

	return &SQLiteTimeoutStore{db: db}, nil
}

// Schedule upserts a timeout entry.
func (s *SQLiteTimeoutStore) Schedule(e TimeoutEntry) error {
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	_, err := s.db.Exec(
		`INSERT INTO timeouts (session_id, state_path, target, fire_at, payload, fired)
         VALUES (?, ?, ?, ?, ?, 0)
         ON CONFLICT(session_id, state_path) DO UPDATE SET
             target   = excluded.target,
             fire_at  = excluded.fire_at,
             payload  = excluded.payload,
             fired    = 0`,
		e.SessionID, e.StatePath, e.Target, e.FireAt.UnixMilli(), string(payload),
	)
	if err != nil {
		return fmt.Errorf("host.SQLiteTimeoutStore.Schedule: %w", err)
	}
	return nil
}

// Cancel removes the row for (sessionID, statePath).
func (s *SQLiteTimeoutStore) Cancel(sessionID, statePath string) error {
	_, err := s.db.Exec(
		`DELETE FROM timeouts WHERE session_id = ? AND state_path = ?`,
		sessionID, statePath,
	)
	if err != nil {
		return fmt.Errorf("host.SQLiteTimeoutStore.Cancel: %w", err)
	}
	return nil
}

// Pending returns all rows where fired=0.
func (s *SQLiteTimeoutStore) Pending() ([]TimeoutEntry, error) {
	rows, err := s.db.Query(
		`SELECT session_id, state_path, target, fire_at, payload
         FROM timeouts WHERE fired = 0`,
	)
	if err != nil {
		return nil, fmt.Errorf("host.SQLiteTimeoutStore.Pending: %w", err)
	}
	defer rows.Close()

	var out []TimeoutEntry
	for rows.Next() {
		var (
			sessionID string
			statePath string
			target    string
			fireAtMS  int64
			payload   string
		)
		if err := rows.Scan(&sessionID, &statePath, &target, &fireAtMS, &payload); err != nil {
			return nil, fmt.Errorf("host.SQLiteTimeoutStore.Pending: scan: %w", err)
		}
		out = append(out, TimeoutEntry{
			SessionID: sessionID,
			StatePath: statePath,
			Target:    target,
			FireAt:    time.UnixMilli(fireAtMS).UTC(),
			Payload:   json.RawMessage(payload),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("host.SQLiteTimeoutStore.Pending: rows: %w", err)
	}
	return out, nil
}

// Fire marks (sessionID, statePath) as fired.
func (s *SQLiteTimeoutStore) Fire(sessionID, statePath string) error {
	_, err := s.db.Exec(
		`UPDATE timeouts SET fired = 1 WHERE session_id = ? AND state_path = ?`,
		sessionID, statePath,
	)
	if err != nil {
		return fmt.Errorf("host.SQLiteTimeoutStore.Fire: %w", err)
	}
	return nil
}
