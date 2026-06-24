package store

// external_keys.go holds the external-key index and the session writer lock.
//
// External keys map (transport, thread) pairs onto a session_id so external
// orchestrators (loop.py, future webhook receivers) can address sessions by
// their inbound surface — e.g. ("jira", "PLTFRM-12345") or
// ("bitbucket", "DBI/repo/pulls/42"). The session/transport model is
// documented in docs/architecture/transports.md ("Sessions keyed by
// transport").
//
// The writer lock serializes concurrent `kitsoki session continue` invocations
// against the same session. The lock is row-keyed by session_id in
// session_locks; stale locks (owner pid not alive) are reaped.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"kitsoki/internal/app"
)

// ErrExternalKeyTaken is returned when a (transport, thread) pair is already
// bound to a different session.
var ErrExternalKeyTaken = errors.New("store: external key already bound to a different session")

// ErrSessionBusy is returned when another live process holds the writer lock
// for a session. Maps to the proposal-defined exit code 75 (EX_TEMPFAIL) at
// the CLI layer.
var ErrSessionBusy = errors.New("store: session busy: another process holds the writer lock")

// BindExternalKey inserts (transport, thread) → session into the index.
// Returns ErrExternalKeyTaken if the pair is bound to a different session.
// Re-binding to the same session is a no-op success.
func (s *sqliteStore) BindExternalKey(ctx context.Context, session app.SessionID, transport, thread string) error {
	if transport == "" || thread == "" {
		return fmt.Errorf("store.BindExternalKey: transport and thread must be non-empty")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("store.BindExternalKey: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Verify the session exists.
	var existsCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE id = ?`, string(session),
	).Scan(&existsCount); err != nil {
		return fmt.Errorf("store.BindExternalKey: check session: %w", err)
	}
	if existsCount == 0 {
		return ErrSessionNotFound
	}

	var existing string
	err = tx.QueryRowContext(ctx,
		`SELECT session_id FROM external_keys WHERE transport = ? AND thread = ?`,
		transport, thread,
	).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Insert.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO external_keys (transport, thread, session_id, created_at)
			 VALUES (?, ?, ?, ?)`,
			transport, thread, string(session), time.Now().UnixMicro(),
		); err != nil {
			return fmt.Errorf("store.BindExternalKey: insert: %w", err)
		}
	case err != nil:
		return fmt.Errorf("store.BindExternalKey: query: %w", err)
	default:
		if existing != string(session) {
			return ErrExternalKeyTaken
		}
		// Already bound to this session — idempotent.
	}

	return tx.Commit()
}

// LookupByKey returns the session ID bound to (transport, thread).
// Returns ErrSessionNotFound if no binding exists.
func (s *sqliteStore) LookupByKey(ctx context.Context, transport, thread string) (app.SessionID, error) {
	var sid string
	err := s.db.QueryRowContext(ctx,
		`SELECT session_id FROM external_keys WHERE transport = ? AND thread = ?`,
		transport, thread,
	).Scan(&sid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store.LookupByKey: %w", err)
	}
	return app.SessionID(sid), nil
}

// ListExternalKeys returns all (transport, thread) bindings for a session,
// ordered by created_at ascending (oldest first).
func (s *sqliteStore) ListExternalKeys(ctx context.Context, session app.SessionID) ([]ExternalKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT transport, thread, created_at
		 FROM external_keys
		 WHERE session_id = ?
		 ORDER BY created_at ASC`,
		string(session),
	)
	if err != nil {
		return nil, fmt.Errorf("store.ListExternalKeys: %w", err)
	}
	defer rows.Close()

	var out []ExternalKey
	for rows.Next() {
		var (
			transport, thread string
			createdAt         int64
		)
		if err := rows.Scan(&transport, &thread, &createdAt); err != nil {
			return nil, fmt.Errorf("store.ListExternalKeys: scan: %w", err)
		}
		out = append(out, ExternalKey{
			Transport: transport,
			Thread:    thread,
			CreatedAt: time.UnixMicro(createdAt),
		})
	}
	return out, rows.Err()
}

// ListSessionsByTransport returns sessions that have at least one external
// key with the given transport, newest-key-first. Pass limit=0 for no limit.
func (s *sqliteStore) ListSessionsByTransport(ctx context.Context, transport string, limit int) ([]SessionSummary, error) {
	q := `SELECT s.id, s.app_id, s.app_version, s.started_at, s.last_turn, s.status
	      FROM sessions s
	      JOIN external_keys ek ON ek.session_id = s.id
	      WHERE ek.transport = ?
	      GROUP BY s.id
	      ORDER BY MAX(ek.created_at) DESC`
	args := []any{transport}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.ListSessionsByTransport: %w", err)
	}
	defer rows.Close()

	var out []SessionSummary
	for rows.Next() {
		var (
			id, aid, aver, status string
			startedAt, lastTurn   int64
		)
		if err := rows.Scan(&id, &aid, &aver, &startedAt, &lastTurn, &status); err != nil {
			return nil, fmt.Errorf("store.ListSessionsByTransport: scan: %w", err)
		}
		out = append(out, SessionSummary{
			ID:         app.SessionID(id),
			AppID:      aid,
			AppVersion: aver,
			StartedAt:  time.UnixMicro(startedAt),
			LastTurn:   app.TurnNumber(lastTurn),
			Status:     status,
		})
	}
	return out, rows.Err()
}

// WithWriterLock acquires a session-scoped writer lock, runs fn, and releases
// the lock. Returns ErrSessionBusy if another live process holds the lock.
//
// Stale locks (owner_pid no longer alive on the recorded host) are reaped:
// we DELETE the existing row and take it over.
func (s *sqliteStore) WithWriterLock(ctx context.Context, session app.SessionID, fn func() error) error {
	if session == "" {
		return fmt.Errorf("store.WithWriterLock: empty session ID")
	}
	if err := s.acquireLock(ctx, session); err != nil {
		return err
	}
	defer func() {
		_ = s.releaseLock(context.Background(), session)
	}()
	return fn()
}

// acquireLock attempts to grab the session_locks row. Returns ErrSessionBusy
// when another live process holds it.
func (s *sqliteStore) acquireLock(ctx context.Context, session app.SessionID) error {
	host, _ := os.Hostname()
	pid := os.Getpid()
	now := time.Now().UnixMicro()

	for attempt := 0; attempt < 2; attempt++ {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO session_locks (session_id, owner_pid, owner_host, acquired_at)
			 VALUES (?, ?, ?, ?)`,
			string(session), pid, host, now,
		)
		if err == nil {
			return nil
		}
		// Constraint violation → row exists. Inspect ownership.
		if !isUniqueViolation(err) {
			return fmt.Errorf("store.acquireLock: insert: %w", err)
		}

		var (
			ownerPID  int
			ownerHost string
		)
		if err := s.db.QueryRowContext(ctx,
			`SELECT owner_pid, owner_host FROM session_locks WHERE session_id = ?`,
			string(session),
		).Scan(&ownerPID, &ownerHost); err != nil {
			return fmt.Errorf("store.acquireLock: read owner: %w", err)
		}

		// Same process re-entering: treat as already-owned (we don't recurse).
		if ownerPID == pid && ownerHost == host {
			return ErrSessionBusy
		}

		// Cross-host: cannot probe liveness; treat as busy.
		if ownerHost != host {
			return ErrSessionBusy
		}

		// Same host, different PID: probe liveness.
		if processAlive(ownerPID) {
			return ErrSessionBusy
		}

		// Stale; reap and retry.
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM session_locks WHERE session_id = ? AND owner_pid = ? AND owner_host = ?`,
			string(session), ownerPID, ownerHost,
		); err != nil {
			return fmt.Errorf("store.acquireLock: reap stale: %w", err)
		}
		// Loop and re-INSERT.
	}
	return ErrSessionBusy
}

// releaseLock removes our row.
func (s *sqliteStore) releaseLock(ctx context.Context, session app.SessionID) error {
	host, _ := os.Hostname()
	pid := os.Getpid()
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM session_locks WHERE session_id = ? AND owner_pid = ? AND owner_host = ?`,
		string(session), pid, host,
	)
	if err != nil {
		return fmt.Errorf("store.releaseLock: %w", err)
	}
	return nil
}

// processAlive reports whether the given pid corresponds to a running process
// on this host. Uses signal 0 which is a no-op kill that just checks
// permission and existence.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return true
	} else if errors.Is(err, os.ErrPermission) {
		// Permission denied means the process exists.
		return true
	}
	return false
}

// isUniqueViolation reports whether err is a SQLite uniqueness constraint
// violation. modernc.org/sqlite returns errors whose message contains
// "UNIQUE constraint failed".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
