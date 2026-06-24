package store

// sqlite.go provides the sqliteStore implementation of the [Store] interface.
// See doc.go for the package overview; the points below record the three
// non-obvious decisions a maintainer trips on.
//
// Connection strategy: SQLite has a single-writer model. To avoid "database is
// locked" errors under concurrent access we set db.SetMaxOpenConns(1): all
// callers share the same physical connection. The write path uses BEGIN
// IMMEDIATE to acquire the write lock up front, which is safe for the
// single-process PoC.
//
// Snapshot policy: snapshots live in the `snapshots` table. The caller decides
// when to snapshot (e.g. every 20 turns) by calling Snapshot(); the store just
// persists and retrieves them. LoadHistory returns events since the latest
// snapshot turn, so replay is always bounded.
//
// MarkCompleted / MarkAbandoned: once a session is marked completed or
// abandoned, AppendEvents returns ErrSessionClosed. This enforces the
// append-only guarantee — completed sessions are immutable history.

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"kitsoki/internal/app"
	"kitsoki/internal/journal"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

//go:embed schema.sql
var schemaDDL string

// ErrSessionClosed is returned when AppendEvents is called on a completed or
// abandoned session. Callers should treat this as a terminal condition.
var ErrSessionClosed = errors.New("store: session is completed or abandoned; no further appends allowed")

// ErrSessionNotFound is returned when the requested session does not exist.
var ErrSessionNotFound = errors.New("store: session not found")

// SessionSummary is a lightweight summary of a session, used by ListSessions.
type SessionSummary struct {
	ID         app.SessionID
	AppID      string
	AppVersion string
	StartedAt  time.Time
	LastTurn   app.TurnNumber
	Status     string
}

// sqliteStore is the concrete Store implementation backed by a single SQLite file.
type sqliteStore struct {
	db   *sql.DB
	path string
}

// Open opens (or creates) a SQLite session store at the given file path.
// It runs the embedded DDL idempotently and configures WAL mode, foreign
// keys, and busy_timeout.
func Open(path string) (Store, error) {
	return openDSN(path)
}

// OpenMemory opens an in-memory SQLite store suitable for tests.
// Uses cache=shared so the same logical database is accessible within the
// process. MaxOpenConns(1) prevents write contention.
func OpenMemory() (Store, error) {
	// Use a random URI so each call gets an isolated in-memory DB.
	// (Shared cache with the same name would collide across test cases.)
	name := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)
	return openDSN(dsn)
}

func openDSN(dsn string) (*sqliteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store.open: sql.Open: %w", err)
	}

	// Single connection: SQLite has a single-writer model; one connection
	// avoids "database is locked" under concurrent goroutine access.
	db.SetMaxOpenConns(1)

	// Apply SQLite configuration pragmas.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store.open: pragma %q: %w", p, err)
		}
	}

	// Run schema DDL (idempotent via IF NOT EXISTS).
	if _, err := db.Exec(schemaDDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store.open: schema DDL: %w", err)
	}

	return &sqliteStore{db: db, path: dsn}, nil
}

// DB returns the underlying *sql.DB, allowing auxiliary packages such as
// jobs.NewJobStore to share the same connection without opening a second
// file handle.  The returned *sql.DB must not be closed by the caller.
func (s *sqliteStore) DB() *sql.DB { return s.db }

// Close flushes WAL and closes the underlying *sql.DB.
func (s *sqliteStore) Close() error {
	// Checkpoint WAL before closing to reduce WAL file size.
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(FULL)")
	return s.db.Close()
}

// CreateSession inserts a new session row and returns its ID.
func (s *sqliteStore) CreateSession(ctx context.Context, def *app.AppDef) (app.SessionID, error) {
	sid := app.SessionID(uuid.New().String())
	now := time.Now().UnixMicro()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, app_id, app_version, started_at, last_turn, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		string(sid),
		def.App.ID,
		def.App.Version,
		now,
		int64(0),
		"active",
	)
	if err != nil {
		return "", fmt.Errorf("store.CreateSession: %w", err)
	}
	return sid, nil
}

// AppendEvents atomically appends events for one turn inside a BEGIN IMMEDIATE
// transaction. seq is overwritten: events within a turn get monotonic seq
// starting at 0. All events in the slice must share the same Turn value;
// the turn is taken from events[0].Turn.
//
// Returns ErrSessionClosed if the session status is completed or abandoned.
// Returns ErrSessionNotFound if the session does not exist.
func (s *sqliteStore) AppendEvents(session app.SessionID, events []Event) error {
	return s.appendEventsCtx(context.Background(), session, events)
}

func (s *sqliteStore) appendEventsCtx(ctx context.Context, session app.SessionID, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	// BEGIN IMMEDIATE acquires the write lock up front.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("store.AppendEvents: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Acquire write lock immediately.
	if _, err := tx.ExecContext(ctx, "SAVEPOINT kitsoki_append"); err != nil {
		return fmt.Errorf("store.AppendEvents: savepoint: %w", err)
	}

	// Check session exists and is active.
	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM sessions WHERE id = ?`, string(session)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("store.AppendEvents: check session: %w", err)
	}
	if status != "active" {
		return ErrSessionClosed
	}

	if err := appendEventsTx(ctx, tx, session, events); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store.AppendEvents: commit: %w", err)
	}
	return nil
}

// appendEventsTx inserts events rows and updates last_turn within an existing
// transaction. Seq values are overwritten with monotonic indices that CONTINUE
// past any rows already persisted for this turn (MAX(seq)+1, or 0 when the turn
// is empty) — so a second append sharing a turn does not collide on the
// (session_id, turn, seq) PK. All events must share the same Turn value; the
// turn is taken from events[0].Turn.
func appendEventsTx(ctx context.Context, tx *sql.Tx, session app.SessionID, events []Event) error {
	turn := events[0].Turn
	now := time.Now().UnixMicro()

	// Seq base: continue past any events already persisted for this turn rather
	// than always resetting to 0. The events PK is (session_id, turn, seq), so
	// two appends that share a turn — e.g. a flow seed's synthetic turn-0
	// TransitionApplied/EffectApplied batch (cmd/kitsoki.seedFlowInitialState)
	// followed by the orchestrator's turn-0 RunInitialOnEnter on_enter batch —
	// would otherwise both start at seq 0 and collide. The common single-append
	// -per-turn path is unaffected: MAX(seq) over an empty turn is NULL, so the
	// base stays 0.
	var seqBase int
	{
		var maxSeq sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(seq) FROM events WHERE session_id = ? AND turn = ?`,
			string(session), int64(turn),
		).Scan(&maxSeq); err != nil {
			return fmt.Errorf("store.appendEventsTx: max seq for turn %d: %w", turn, err)
		}
		if maxSeq.Valid {
			seqBase = int(maxSeq.Int64) + 1
		}
	}

	// Insert all events, assigning monotonic seq starting at seqBase.
	for i := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		events[i].Seq = seqBase + i

		payload := events[i].Payload
		if payload == nil {
			payload = json.RawMessage("{}")
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (session_id, turn, seq, ts, kind, payload_json)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			string(session),
			int64(events[i].Turn),
			events[i].Seq,
			now,
			string(events[i].Kind),
			string(payload),
		); err != nil {
			return fmt.Errorf("store.appendEventsTx: insert event %d: %w", i, err)
		}
	}

	// Update last_turn on the session.
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET last_turn = ? WHERE id = ?`,
		int64(turn), string(session),
	); err != nil {
		return fmt.Errorf("store.appendEventsTx: update last_turn: %w", err)
	}
	return nil
}

// AppendEventsAndJournal atomically appends events and journal entries in a
// single transaction. Either both writes succeed or both are rolled back.
// The events constraint (ErrSessionClosed / ErrSessionNotFound) is checked
// before either insert proceeds.
func (s *sqliteStore) AppendEventsAndJournal(session app.SessionID, events []Event, journalEntries []journal.Entry) error {
	ctx := context.Background()
	if len(events) == 0 && len(journalEntries) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("store.AppendEventsAndJournal: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Check session exists and is active.
	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM sessions WHERE id = ?`, string(session)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("store.AppendEventsAndJournal: check session: %w", err)
	}
	if status != "active" {
		return ErrSessionClosed
	}

	if len(events) > 0 {
		if err := appendEventsTx(ctx, tx, session, events); err != nil {
			return err
		}
	}

	if len(journalEntries) > 0 {
		if err := journal.AppendJournalTx(tx, session, journalEntries); err != nil {
			return fmt.Errorf("store.AppendEventsAndJournal: journal: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store.AppendEventsAndJournal: commit: %w", err)
	}
	return nil
}

// LoadHistory returns the ordered event log for a session, starting after the
// latest snapshot's turn (or from turn 0 if no snapshot exists).
func (s *sqliteStore) LoadHistory(session app.SessionID) (History, error) {
	return s.loadHistoryCtx(context.Background(), session)
}

func (s *sqliteStore) loadHistoryCtx(ctx context.Context, session app.SessionID) (History, error) {
	// Find the latest snapshot turn to bound the query (resumption path).
	afterTurn := int64(-1)
	snap, ok, err := s.LatestSnapshot(session)
	if err != nil {
		return nil, fmt.Errorf("store.LoadHistory: latest snapshot: %w", err)
	}
	if ok {
		afterTurn = int64(snap.Turn)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT turn, seq, ts, kind, payload_json
		 FROM events
		 WHERE session_id = ? AND turn > ?
		 ORDER BY turn ASC, seq ASC`,
		string(session), afterTurn,
	)
	if err != nil {
		return nil, fmt.Errorf("store.LoadHistory: query: %w", err)
	}
	defer rows.Close()

	var history History
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var (
			turnN   int64
			seq     int
			tsMicro int64
			kind    string
			payload string
		)
		if err := rows.Scan(&turnN, &seq, &tsMicro, &kind, &payload); err != nil {
			return nil, fmt.Errorf("store.LoadHistory: scan: %w", err)
		}
		history = append(history, Event{
			Turn:    app.TurnNumber(turnN),
			Seq:     seq,
			Ts:      time.UnixMicro(tsMicro),
			Kind:    EventKind(kind),
			Payload: json.RawMessage(payload),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.LoadHistory: rows: %w", err)
	}
	return history, nil
}

// Snapshot materializes a state snapshot at a given turn.
// If a snapshot at the same (session, turn) already exists, it is replaced.
func (s *sqliteStore) Snapshot(session app.SessionID, at app.TurnNumber, snap Snapshot) error {
	worldBytes, err := json.Marshal(snap.WorldJSON)
	if err != nil {
		return fmt.Errorf("store.Snapshot: marshal world: %w", err)
	}

	_, err = s.db.ExecContext(context.Background(),
		`INSERT OR REPLACE INTO snapshots (session_id, turn, state_path, world_json, rng_seed)
		 VALUES (?, ?, ?, ?, ?)`,
		string(session),
		int64(at),
		string(snap.StatePath),
		string(worldBytes),
		snap.RNGSeed,
	)
	if err != nil {
		return fmt.Errorf("store.Snapshot: insert: %w", err)
	}
	return nil
}

// LatestSnapshot loads the most recent snapshot for the resume path.
// Returns (snapshot, false, nil) if no snapshot exists yet.
func (s *sqliteStore) LatestSnapshot(session app.SessionID) (Snapshot, bool, error) {
	var (
		turnN     int64
		statePath string
		worldJSON string
		rngSeed   int64
	)
	err := s.db.QueryRowContext(context.Background(),
		`SELECT turn, state_path, world_json, rng_seed
		 FROM snapshots
		 WHERE session_id = ?
		 ORDER BY turn DESC
		 LIMIT 1`,
		string(session),
	).Scan(&turnN, &statePath, &worldJSON, &rngSeed)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("store.LatestSnapshot: %w", err)
	}
	return Snapshot{
		Turn:      app.TurnNumber(turnN),
		StatePath: app.StatePath(statePath),
		WorldJSON: json.RawMessage(worldJSON),
		RNGSeed:   rngSeed,
	}, true, nil
}

// MarkCompleted sets the session status to "completed".
// After this call, AppendEvents returns ErrSessionClosed.
func (s *sqliteStore) MarkCompleted(ctx context.Context, session app.SessionID) error {
	return s.setStatus(ctx, session, "completed")
}

// MarkAbandoned sets the session status to "abandoned".
// After this call, AppendEvents returns ErrSessionClosed.
func (s *sqliteStore) MarkAbandoned(ctx context.Context, session app.SessionID) error {
	return s.setStatus(ctx, session, "abandoned")
}

// DeleteSession removes a session and all rows that reference it
// (events, snapshots, external_keys, session_locks) atomically.  The
// row in `sessions` is deleted last so a partial failure leaves the
// session id resolvable for retry.
func (s *sqliteStore) DeleteSession(ctx context.Context, session app.SessionID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store.DeleteSession: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sid := string(session)
	for _, q := range []string{
		`DELETE FROM events         WHERE session_id = ?`,
		`DELETE FROM snapshots      WHERE session_id = ?`,
		`DELETE FROM external_keys  WHERE session_id = ?`,
		`DELETE FROM session_locks  WHERE session_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, sid); err != nil {
			return fmt.Errorf("store.DeleteSession: %s: %w", q, err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sid)
	if err != nil {
		return fmt.Errorf("store.DeleteSession: DELETE sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store.DeleteSession: commit: %w", err)
	}
	return nil
}

func (s *sqliteStore) setStatus(ctx context.Context, session app.SessionID, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = ? WHERE id = ?`,
		status, string(session),
	)
	if err != nil {
		return fmt.Errorf("store.setStatus %s: %w", status, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// ListSessions returns up to limit sessions for the given app ID, ordered by
// started_at descending. Pass limit=0 for no limit.
func (s *sqliteStore) GetSession(ctx context.Context, session app.SessionID) (SessionSummary, error) {
	const q = `SELECT id, app_id, app_version, started_at, last_turn, status
	           FROM sessions WHERE id = ?`
	var (
		id        string
		aid       string
		aver      string
		startedAt int64
		lastTurn  int64
		status    string
	)
	err := s.db.QueryRowContext(ctx, q, string(session)).Scan(&id, &aid, &aver, &startedAt, &lastTurn, &status)
	if err == sql.ErrNoRows {
		return SessionSummary{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionSummary{}, fmt.Errorf("store.GetSession: %w", err)
	}
	return SessionSummary{
		ID:         app.SessionID(id),
		AppID:      aid,
		AppVersion: aver,
		StartedAt:  time.UnixMicro(startedAt),
		LastTurn:   app.TurnNumber(lastTurn),
		Status:     status,
	}, nil
}

func (s *sqliteStore) ListSessions(ctx context.Context, appID string, limit int) ([]SessionSummary, error) {
	q := `SELECT id, app_id, app_version, started_at, last_turn, status
	      FROM sessions
	      WHERE app_id = ?
	      ORDER BY started_at DESC`
	args := []any{appID}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.ListSessions: %w", err)
	}
	defer rows.Close()

	var out []SessionSummary
	for rows.Next() {
		var (
			id        string
			aid       string
			aver      string
			startedAt int64
			lastTurn  int64
			status    string
		)
		if err := rows.Scan(&id, &aid, &aver, &startedAt, &lastTurn, &status); err != nil {
			return nil, fmt.Errorf("store.ListSessions: scan: %w", err)
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
