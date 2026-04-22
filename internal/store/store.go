package store

import (
	"context"

	"hally/internal/app"

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

	// ListSessions returns up to limit sessions for the given app ID, newest first.
	// Pass limit=0 for no limit.
	ListSessions(ctx context.Context, appID string, limit int) ([]SessionSummary, error)

	// Close flushes WAL and closes the underlying *sql.DB.
	Close() error
}
