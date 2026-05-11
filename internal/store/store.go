package store

import (
	"context"
	"database/sql"
	"time"

	"kitsoki/internal/app"

	// Blank import to ensure modernc.org/sqlite stays in go.mod after tidy.
	_ "modernc.org/sqlite"
)

// Store is event-sourced session persistence, backed by modernc.org/sqlite (§12.1).
// This is one of the five core interfaces from §12.1.
//
// Thread-safety: all methods are safe for concurrent use. The underlying
// sqliteStore sets MaxOpenConns(1) to serialize writes at the connection level.
type Store interface {
	// CreateSession creates a new session for the given app and returns its ID.
	CreateSession(ctx context.Context, def *app.AppDef) (app.SessionID, error)

	// AppendEvents atomically appends events for one turn inside a BEGIN IMMEDIATE transaction.
	// seq is overwritten: events within a turn receive monotonic seq starting at 0.
	// Returns ErrSessionClosed if the session has been completed or abandoned.
	AppendEvents(session app.SessionID, events []Event) error

	// LoadHistory returns the ordered events since the latest snapshot for a session.
	LoadHistory(session app.SessionID) (History, error)

	// Snapshot materializes the current state every N turns (default N=20).
	Snapshot(session app.SessionID, at app.TurnNumber, snap Snapshot) error

	// LatestSnapshot loads the most recent snapshot for the resume path.
	// Returns (snapshot, false, nil) if no snapshot exists yet.
	LatestSnapshot(session app.SessionID) (Snapshot, bool, error)

	// MarkCompleted marks a session as completed. After this, AppendEvents returns ErrSessionClosed.
	MarkCompleted(ctx context.Context, session app.SessionID) error

	// MarkAbandoned marks a session as abandoned. After this, AppendEvents returns ErrSessionClosed.
	MarkAbandoned(ctx context.Context, session app.SessionID) error

	// DeleteSession removes a session and all of its associated rows
	// (events, snapshots, external_keys, session_locks) inside a single
	// transaction.  Returns ErrSessionNotFound if no session exists with
	// the given id.  Intended for testing and operator-driven cleanup of
	// abandoned sessions; production code should prefer MarkAbandoned to
	// preserve the audit trail.
	DeleteSession(ctx context.Context, session app.SessionID) error

	// ListSessions returns up to limit sessions for the given app ID, newest first.
	// Pass limit=0 for no limit.
	ListSessions(ctx context.Context, appID string, limit int) ([]SessionSummary, error)

	// BindExternalKey associates a (transport, thread) key with the given
	// session. A session may carry multiple keys; the (transport, thread)
	// pair is unique across all sessions. Returns ErrExternalKeyTaken if
	// the pair is already bound to a different session.
	BindExternalKey(ctx context.Context, session app.SessionID, transport, thread string) error

	// LookupByKey returns the session bound to (transport, thread), or
	// (ErrSessionNotFound) if none.
	LookupByKey(ctx context.Context, transport, thread string) (app.SessionID, error)

	// ListExternalKeys returns the (transport, thread) keys bound to a session.
	ListExternalKeys(ctx context.Context, session app.SessionID) ([]ExternalKey, error)

	// ListSessionsByTransport returns up to `limit` sessions that have at
	// least one external key with the given transport, newest-key-first.
	// Pass limit=0 for no limit.
	ListSessionsByTransport(ctx context.Context, transport string, limit int) ([]SessionSummary, error)

	// WithWriterLock acquires a session-scoped writer lock, runs fn, and
	// releases the lock. Returns ErrSessionBusy if another live process
	// holds the lock. Stale locks (owner pid no longer alive) are reaped.
	WithWriterLock(ctx context.Context, session app.SessionID, fn func() error) error

	// DB returns the underlying *sql.DB so that auxiliary packages
	// (e.g. jobs.NewJobStore) can share the same connection without
	// opening a second file handle.  The caller must not close the
	// returned *sql.DB directly; use Store.Close instead.
	DB() *sql.DB

	// Close flushes WAL and closes the underlying *sql.DB.
	Close() error
}

// ExternalKey is one (transport, thread) binding to a session.
type ExternalKey struct {
	Transport string
	Thread    string
	CreatedAt time.Time
}

func (k ExternalKey) String() string { return k.Transport + ":" + k.Thread }
