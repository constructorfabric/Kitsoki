package chats

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/journal"
	"kitsoki/internal/ulid"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var chatsSchemaDDL string

// expectedSchemaVersion is the PRAGMA user_version this codebase expects.
// SQLite's user_version is a per-database integer slot. The schema.sql sets
// it to this value on first apply. Because every CREATE TABLE in schema.sql
// uses IF NOT EXISTS, a future column addition would silently no-op on an
// existing DB; bumping this constant alongside the DDL makes that case fail
// loudly at NewStore time. Any bump must be paired with an explicit
// migration step before reaching the version assertion.
const expectedSchemaVersion = 3

// migratableFromVersions lists the prior user_version values from which the
// migration steps in NewStore can bring a DB up to expectedSchemaVersion.
// Each entry has a corresponding code path that handles the *additive*
// changes (new tables, new columns); fresh DBs (gotVersion == 0) take the
// same path because every step is idempotent (CREATE … IF NOT EXISTS for
// tables, "ALTER TABLE … ADD COLUMN" guarded by a column-presence probe).
//
// When a future bump adds a destructive change (column drop, type change,
// renamed table), the migration step must serialize a copy-then-rename
// pattern instead of relying on IF NOT EXISTS.
var migratableFromVersions = map[int]bool{
	1: true, // v1 → v2: adds chat_pty_sessions and chat_input_queue.
	2: true, // v2 → v3: adds on_complete_json / origin_session_id / origin_state on chat_input_queue.
}

// ErrChatNotFound is returned when a requested chat does not exist.
var ErrChatNotFound = errors.New("chats: chat not found")

// Store provides SQLite-backed persistence for chats and their transcripts.
// It operates on an existing *sql.DB (opened by the parent store package).
type Store struct {
	db            *sql.DB
	clock         clock.Clock
	journalWriter journal.Writer
}

// Option is a functional option for constructing a Store.
type Option func(*Store)

// WithClock injects a clock.Clock into the Store. Defaults to clock.Real()
// when not supplied. Use clock.NewFake in tests to drive time deterministically.
func WithClock(c clock.Clock) Option {
	return func(s *Store) {
		if c != nil {
			s.clock = c
		}
	}
}

// WithJournalWriter injects a journal.Writer into the Store. When non-nil,
// chat mutations emit typed journal entries alongside the SQLite writes
// (the continue-mode dual-write). When nil (the default), no journal
// entries are written — this preserves backward compatibility for callers
// that do not participate in the journal (e.g. flow tests, chathost adapter
// in non-continue-mode builds).
func WithJournalWriter(jw journal.Writer) Option {
	return func(s *Store) {
		s.journalWriter = jw
	}
}

// NewStore creates a Store and applies the chats schema migration idempotently.
func NewStore(db *sql.DB, opts ...Option) (*Store, error) {
	s := &Store{
		db:    db,
		clock: clock.Real(),
	}
	for _, o := range opts {
		o(s)
	}
	// Inspect the on-disk schema version BEFORE applying DDL. A fresh DB
	// reports 0 (the SQLite default); an existing one reports whatever the
	// last writer stamped. We reject any non-zero value that doesn't match
	// expectedSchemaVersion — that means the DB was written by a different
	// kitsoki build and we can't safely re-run our IF NOT EXISTS DDL on it
	// without corrupting state.
	var gotVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&gotVersion); err != nil {
		return nil, fmt.Errorf("chats.NewStore: read schema version: %w", err)
	}
	// A fresh DB reports 0 and is always migratable. A DB stamped at the
	// current version is a no-op re-application. Any other value must be in
	// migratableFromVersions, or we're being asked to open a DB written by
	// an incompatible kitsoki build.
	if gotVersion != 0 && gotVersion != expectedSchemaVersion && !migratableFromVersions[gotVersion] {
		return nil, fmt.Errorf(
			"chats.NewStore: unexpected schema version %d (want %d) — DB written by a different kitsoki build; refusing to open",
			gotVersion, expectedSchemaVersion,
		)
	}

	// Pre-DDL migrations. The embedded schema.sql adds new tables and
	// columns via IF NOT EXISTS, but SQLite's ALTER TABLE … ADD COLUMN
	// has no IF NOT EXISTS form, so a v2 → v3 upgrade needs an explicit
	// column-presence probe before each ALTER. The probe is cheap (a
	// PRAGMA table_info read of a small table) and pre-DDL so a partial
	// migration on a previous run is recoverable.
	if err := migrateChatInputQueueColumns(db); err != nil {
		return nil, fmt.Errorf("chats.NewStore: alter chat_input_queue: %w", err)
	}

	if _, err := db.Exec(chatsSchemaDDL); err != nil {
		return nil, fmt.Errorf("chats.NewStore: schema migration: %w", err)
	}
	// Sanity-check post-migration: the DDL ends with `PRAGMA user_version = N`,
	// so a successful Exec must have stamped expectedSchemaVersion.
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&gotVersion); err != nil {
		return nil, fmt.Errorf("chats.NewStore: read schema version after migration: %w", err)
	}
	if gotVersion != expectedSchemaVersion {
		return nil, fmt.Errorf(
			"chats.NewStore: post-migration schema version %d (want %d) — schema.sql out of sync with expectedSchemaVersion",
			gotVersion, expectedSchemaVersion,
		)
	}
	return s, nil
}

// migrateChatInputQueueColumns adds the v3 columns (on_complete_json,
// origin_session_id, origin_state) to chat_input_queue when they are
// missing. The function is a no-op when:
//   - the table doesn't exist yet (fresh DB; the embedded DDL will
//     create it with all columns), or
//   - the columns are already present (already-migrated DB, or v3 from
//     scratch).
//
// Idempotent so a partially-applied migration on a prior run can be
// resumed without manual intervention.
func migrateChatInputQueueColumns(db *sql.DB) error {
	// PRAGMA table_info returns one row per column. Empty result means
	// the table doesn't exist — defer to the IF NOT EXISTS DDL below.
	rows, err := db.Query(`PRAGMA table_info(chat_input_queue)`)
	if err != nil {
		return fmt.Errorf("read table_info: %w", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan table_info: %w", err)
		}
		cols[name] = true
	}
	rows.Close()
	if len(cols) == 0 {
		// Table doesn't exist yet — fresh DB; let the embedded DDL run.
		return nil
	}

	type addCol struct {
		name string
		ddl  string
	}
	want := []addCol{
		{"on_complete_json", `ALTER TABLE chat_input_queue ADD COLUMN on_complete_json TEXT NOT NULL DEFAULT ''`},
		{"origin_session_id", `ALTER TABLE chat_input_queue ADD COLUMN origin_session_id TEXT NOT NULL DEFAULT ''`},
		{"origin_state", `ALTER TABLE chat_input_queue ADD COLUMN origin_state TEXT NOT NULL DEFAULT ''`},
	}
	for _, c := range want {
		if cols[c.name] {
			continue
		}
		if _, err := db.Exec(c.ddl); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	return nil
}

// Create inserts a new chat row and returns it.
func (s *Store) Create(ctx context.Context, appID, room, scopeKey, title string) (*Chat, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, fmt.Errorf("chats.Create: empty app")
	}
	if strings.TrimSpace(room) == "" {
		return nil, fmt.Errorf("chats.Create: empty room")
	}
	now := s.clock.Now().UnixMicro()
	id := ulid.New()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats
		  (id, app_id, room, scope_key, title, status, claude_session_id, parent_chat_id, session_id,
		   created_at, updated_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, '', NULL, NULL, ?, ?, ?)`,
		id, appID, room, scopeKey, title, string(ChatActive),
		now, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("chats.Create: %w", err)
	}
	return s.Get(ctx, id)
}

// GetOrEnsure returns the chat with the given ID. If the chat does not exist,
// it inserts a minimal placeholder row (empty app_id/room/scope_key, title
// "untitled chat", status "active") via INSERT OR IGNORE and then returns the
// row. This is used by host.agent.converse under --harness replay, where the
// preceding host.chat.resolve effect is served from a cassette without ever
// touching the real ChatStore, so no row was inserted before the converse
// handler runs.
func (s *Store) GetOrEnsure(ctx context.Context, chatID string) (*Chat, error) {
	c, err := s.Get(ctx, chatID)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, ErrChatNotFound) {
		return nil, fmt.Errorf("chats.GetOrEnsure: %w", err)
	}
	// Row not found — insert a placeholder, ignoring conflicts so a concurrent
	// GetOrEnsure on the same chatID is safe.
	now := s.clock.Now().UnixMicro()
	if _, execErr := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO chats
		  (id, app_id, room, scope_key, title, status, claude_session_id,
		   parent_chat_id, session_id, created_at, updated_at, last_active_at)
		VALUES (?, '', '', '', 'untitled chat', 'active', '', NULL, NULL, ?, ?, ?)`,
		chatID, now, now, now,
	); execErr != nil {
		return nil, fmt.Errorf("chats.GetOrEnsure: insert placeholder: %w", execErr)
	}
	return s.Get(ctx, chatID)
}

// Get returns the chat with the given ID, or ErrChatNotFound if it does not exist.
func (s *Store) Get(ctx context.Context, chatID string) (*Chat, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, app_id, room, scope_key, title, status,
		       COALESCE(claude_session_id, ''),
		       COALESCE(parent_chat_id, ''),
		       COALESCE(session_id, ''),
		       created_at, updated_at, last_active_at
		FROM chats WHERE id = ?`, chatID)
	c, err := scanChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrChatNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chats.Get: %w", err)
	}
	return c, nil
}

// List returns chats matching the given filters, ordered by last_active_at DESC.
// Pass empty strings to skip a filter. All non-empty filters are ANDed.
func (s *Store) List(ctx context.Context, appID, room, scopeKey string) ([]Chat, error) {
	q := `SELECT id, app_id, room, scope_key, title, status,
		       COALESCE(claude_session_id, ''),
		       COALESCE(parent_chat_id, ''),
		       COALESCE(session_id, ''),
		       created_at, updated_at, last_active_at
		FROM chats WHERE 1=1`
	var args []any
	if appID != "" {
		q += " AND app_id = ?"
		args = append(args, appID)
	}
	if room != "" {
		q += " AND room = ?"
		args = append(args, room)
	}
	if scopeKey != "" {
		q += " AND scope_key = ?"
		args = append(args, scopeKey)
	}
	q += " ORDER BY last_active_at DESC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("chats.List: %w", err)
	}
	defer rows.Close()

	var out []Chat
	for rows.Next() {
		c, err := scanChatRow(rows)
		if err != nil {
			return nil, fmt.Errorf("chats.List: scan: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Resolve finds the newest non-fork chat matching (appID, room, scopeKey).
// If none exists, it creates one with the supplied title and returns it.
// "Non-fork" means parent_chat_id IS NULL.
//
// The returned bool reports whether the chat was newly created (true) or
// already existed (false). The query and the optional INSERT run inside a
// single transaction so concurrent callers can't both see "not found" and
// then both create — only one wins.
func (s *Store) Resolve(ctx context.Context, appID, room, scopeKey, title string) (*Chat, bool, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, false, fmt.Errorf("chats.Resolve: empty app")
	}
	if strings.TrimSpace(room) == "" {
		return nil, false, fmt.Errorf("chats.Resolve: empty room")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("chats.Resolve: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Archived rows are soft-deleted — Resolve treats them as not present
	// so a fresh row gets created. /meta new in the TUI relies on this.
	var id string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM chats
		WHERE app_id = ? AND room = ? AND scope_key = ?
		  AND parent_chat_id IS NULL
		  AND status != 'archived'
		ORDER BY last_active_at DESC
		LIMIT 1`,
		appID, room, scopeKey,
	).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("chats.Resolve: query: %w", err)
	}

	if err == nil {
		// Found existing — read it inside the same tx and return it.
		c, scanErr := scanChat(tx.QueryRowContext(ctx, `
			SELECT id, app_id, room, scope_key, title, status,
			       COALESCE(claude_session_id, ''),
			       COALESCE(parent_chat_id, ''),
			       COALESCE(session_id, ''),
			       created_at, updated_at, last_active_at
			FROM chats WHERE id = ?`, id))
		if scanErr != nil {
			return nil, false, fmt.Errorf("chats.Resolve: read existing: %w", scanErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, false, fmt.Errorf("chats.Resolve: commit: %w", commitErr)
		}
		return c, false, nil
	}

	// None found — create a new one in the same tx.
	now := s.clock.Now().UnixMicro()
	newID := ulid.New()
	if _, execErr := tx.ExecContext(ctx, `
		INSERT INTO chats
		  (id, app_id, room, scope_key, title, status, claude_session_id, parent_chat_id, session_id,
		   created_at, updated_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, '', NULL, NULL, ?, ?, ?)`,
		newID, appID, room, scopeKey, title, string(ChatActive),
		now, now, now,
	); execErr != nil {
		return nil, false, fmt.Errorf("chats.Resolve: insert: %w", execErr)
	}
	c, scanErr := scanChat(tx.QueryRowContext(ctx, `
		SELECT id, app_id, room, scope_key, title, status,
		       COALESCE(claude_session_id, ''),
		       COALESCE(parent_chat_id, ''),
		       COALESCE(session_id, ''),
		       created_at, updated_at, last_active_at
		FROM chats WHERE id = ?`, newID))
	if scanErr != nil {
		return nil, false, fmt.Errorf("chats.Resolve: read new: %w", scanErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return nil, false, fmt.Errorf("chats.Resolve: commit: %w", commitErr)
	}
	return c, true, nil
}

// SetClaudeSessionID updates the claude_session_id field for a chat.
func (s *Store) SetClaudeSessionID(ctx context.Context, chatID, claudeSessionID string) error {
	now := s.clock.Now().UnixMicro()
	res, err := s.db.ExecContext(ctx,
		`UPDATE chats SET claude_session_id = ?, updated_at = ? WHERE id = ?`,
		claudeSessionID, now, chatID)
	if err != nil {
		return fmt.Errorf("chats.SetClaudeSessionID: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrChatNotFound
	}
	// Site 22: emit chats.append for the claude_session_id update.
	if s.journalWriter != nil {
		sid := chatSessionID(ctx, s.db, chatID)
		body := mustJSON([]map[string]any{
			{"op": "replace", "path": "/meta/claude_session_id", "value": claudeSessionID},
		})
		appendJournalEntry(s.journalWriter, journal.Entry{
			Ts:      s.clock.Now(),
			Session: sid,
			Kind:    journal.KindChatsAppend,
			Doc:     journal.DocID("chats/" + chatID),
			Body:    body,
		})
	}
	return nil
}

// Rename updates the title of an existing chat. Returns ErrChatNotFound
// if no chat row matches.
func (s *Store) Rename(ctx context.Context, chatID, title string) error {
	if chatID == "" {
		return fmt.Errorf("chats.Rename: empty chat ID")
	}
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("chats.Rename: empty title")
	}
	now := s.clock.Now().UnixMicro()
	res, err := s.db.ExecContext(ctx,
		`UPDATE chats SET title = ?, updated_at = ? WHERE id = ?`,
		title, now, chatID)
	if err != nil {
		return fmt.Errorf("chats.Rename: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrChatNotFound
	}
	// Site 21: emit chats.append for the title rename.
	if s.journalWriter != nil {
		sid := chatSessionID(ctx, s.db, chatID)
		body := mustJSON([]map[string]any{
			{"op": "replace", "path": "/meta/title", "value": title},
		})
		appendJournalEntry(s.journalWriter, journal.Entry{
			Ts:      s.clock.Now(),
			Session: sid,
			Kind:    journal.KindChatsAppend,
			Doc:     journal.DocID("chats/" + chatID),
			Body:    body,
		})
	}
	return nil
}

// Archive sets the chat status to "archived".
func (s *Store) Archive(ctx context.Context, chatID string) error {
	now := s.clock.Now().UnixMicro()
	res, err := s.db.ExecContext(ctx,
		`UPDATE chats SET status = ?, updated_at = ? WHERE id = ?`,
		string(ChatArchived), now, chatID)
	if err != nil {
		return fmt.Errorf("chats.Archive: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrChatNotFound
	}
	// Site 20: emit chats.append for the archive status change.
	if s.journalWriter != nil {
		sid := chatSessionID(ctx, s.db, chatID)
		body := mustJSON([]map[string]any{
			{"op": "replace", "path": "/meta/status", "value": string(ChatArchived)},
		})
		appendJournalEntry(s.journalWriter, journal.Entry{
			Ts:      s.clock.Now(),
			Session: sid,
			Kind:    journal.KindChatsAppend,
			Doc:     journal.DocID("chats/" + chatID),
			Body:    body,
		})
	}
	return nil
}

// AppendMessage appends a new message to the chat transcript and returns it.
// The seq number is auto-incremented (LatestSeq + 1).
func (s *Store) AppendMessage(ctx context.Context, chatID, role, content string, metadata map[string]any) (Message, error) {
	now := s.clock.Now().UnixMicro()

	var metaJSON []byte
	if metadata != nil {
		var err error
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return Message{}, fmt.Errorf("chats.AppendMessage: marshal metadata: %w", err)
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Message{}, fmt.Errorf("chats.AppendMessage: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Determine next seq atomically within the transaction.
	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM chat_messages WHERE chat_id = ?`, chatID,
	).Scan(&maxSeq); err != nil {
		return Message{}, fmt.Errorf("chats.AppendMessage: get max seq: %w", err)
	}
	seq := 0
	if maxSeq.Valid {
		seq = int(maxSeq.Int64) + 1
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO chat_messages (chat_id, seq, role, content, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		chatID, seq, role, content, nullableBytes(metaJSON), now,
	); err != nil {
		return Message{}, fmt.Errorf("chats.AppendMessage: insert: %w", err)
	}

	// Update last_active_at and updated_at on the chat.
	if _, err := tx.ExecContext(ctx,
		`UPDATE chats SET last_active_at = ?, updated_at = ? WHERE id = ?`,
		now, now, chatID,
	); err != nil {
		return Message{}, fmt.Errorf("chats.AppendMessage: update chat: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("chats.AppendMessage: commit: %w", err)
	}

	var m map[string]any
	if metaJSON != nil {
		_ = json.Unmarshal(metaJSON, &m)
	}
	msg := Message{
		ChatID:    chatID,
		Seq:       seq,
		Role:      role,
		Content:   content,
		Metadata:  m,
		CreatedAt: time.UnixMicro(now),
	}
	// Emit chats.append for the new message. Post-commit write — acceptable
	// because chat appends serialise behind the per-chat lock.
	if s.journalWriter != nil {
		sid := chatSessionID(ctx, s.db, chatID)
		msgValue := map[string]any{
			"seq":        seq,
			"role":       role,
			"content":    content,
			"created_at": time.UnixMicro(now).Format(time.RFC3339Nano),
		}
		if m != nil {
			msgValue["metadata"] = m
		}
		body := mustJSON([]map[string]any{
			{"op": "add", "path": "/messages/-", "value": msgValue},
		})
		appendJournalEntry(s.journalWriter, journal.Entry{
			Ts:      s.clock.Now(),
			Session: sid,
			Kind:    journal.KindChatsAppend,
			Doc:     journal.DocID("chats/" + chatID),
			Body:    body,
		})
	}
	return msg, nil
}

// Transcript returns messages for a chat ordered by seq ASC, optionally
// starting from a minimum seq (sinceSeq). Pass sinceSeq=0 for all messages.
func (s *Store) Transcript(ctx context.Context, chatID string, sinceSeq int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT chat_id, seq, role, content, COALESCE(metadata, ''), created_at
		FROM chat_messages
		WHERE chat_id = ? AND seq >= ?
		ORDER BY seq ASC`,
		chatID, sinceSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("chats.Transcript: %w", err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var (
			m         Message
			metaStr   string
			createdAt int64
		)
		if err := rows.Scan(&m.ChatID, &m.Seq, &m.Role, &m.Content, &metaStr, &createdAt); err != nil {
			return nil, fmt.Errorf("chats.Transcript: scan: %w", err)
		}
		m.CreatedAt = time.UnixMicro(createdAt)
		if metaStr != "" {
			_ = json.Unmarshal([]byte(metaStr), &m.Metadata)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LatestSeq returns the highest seq in the chat transcript, or -1 if empty.
func (s *Store) LatestSeq(ctx context.Context, chatID string) (int, error) {
	var maxSeq sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM chat_messages WHERE chat_id = ?`, chatID,
	).Scan(&maxSeq); err != nil {
		return -1, fmt.Errorf("chats.LatestSeq: %w", err)
	}
	if !maxSeq.Valid {
		return -1, nil
	}
	return int(maxSeq.Int64), nil
}

// Fork creates a new chat with parent_chat_id = parentChatID, copies all messages
// from the parent atomically, and returns the new chat. The new chat has an empty
// claude_session_id. If newTitle is empty, the parent's title + " (fork)" is used.
func (s *Store) Fork(ctx context.Context, parentChatID, newTitle string) (*Chat, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("chats.Fork: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Read parent.
	var parent Chat
	var createdAt, updatedAt, lastActiveAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT id, app_id, room, scope_key, title, status,
		       COALESCE(claude_session_id, ''),
		       COALESCE(parent_chat_id, ''),
		       COALESCE(session_id, ''),
		       created_at, updated_at, last_active_at
		FROM chats WHERE id = ?`, parentChatID,
	).Scan(
		&parent.ID, &parent.AppID, &parent.Room, &parent.ScopeKey,
		&parent.Title, &parent.Status, &parent.ClaudeSessionID,
		&parent.ParentChatID, &parent.SessionID,
		&createdAt, &updatedAt, &lastActiveAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrChatNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chats.Fork: read parent: %w", err)
	}

	if newTitle == "" {
		newTitle = parent.Title + " (fork)"
	}

	now := s.clock.Now().UnixMicro()
	newID := ulid.New()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chats
		  (id, app_id, room, scope_key, title, status, claude_session_id, parent_chat_id, session_id,
		   created_at, updated_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, NULL, ?, ?, ?)`,
		newID, parent.AppID, parent.Room, parent.ScopeKey, newTitle,
		string(ChatActive), parentChatID, now, now, now,
	); err != nil {
		return nil, fmt.Errorf("chats.Fork: insert: %w", err)
	}

	// Copy messages atomically.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_messages (chat_id, seq, role, content, metadata, created_at)
		SELECT ?, seq, role, content, metadata, created_at
		FROM chat_messages WHERE chat_id = ? ORDER BY seq`,
		newID, parentChatID,
	); err != nil {
		return nil, fmt.Errorf("chats.Fork: copy messages: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chats.Fork: commit: %w", err)
	}

	result, err := s.Get(ctx, newID)
	if err != nil {
		return nil, err
	}
	// Site 23: emit journal entries for the fork.
	// One entry on the new chat (full snapshot), one on the source chat (forked_to marker).
	if s.journalWriter != nil {
		// Fetch copied messages for the new chat snapshot.
		newMsgs, _ := s.Transcript(ctx, newID, 0)
		msgsVal := make([]map[string]any, 0, len(newMsgs))
		for _, nm := range newMsgs {
			mv := map[string]any{
				"seq":        nm.Seq,
				"role":       nm.Role,
				"content":    nm.Content,
				"created_at": nm.CreatedAt.Format(time.RFC3339Nano),
			}
			if nm.Metadata != nil {
				mv["metadata"] = nm.Metadata
			}
			msgsVal = append(msgsVal, mv)
		}
		newChatBody := mustJSON([]map[string]any{
			{"op": "add", "path": "/messages", "value": msgsVal},
		})
		// Use parent's session_id for both entries (fork shares the session).
		parentSID := chatSessionID(ctx, s.db, parentChatID)
		appendJournalEntry(s.journalWriter, journal.Entry{
			Ts:      s.clock.Now(),
			Session: parentSID,
			Kind:    journal.KindChatsAppend,
			Doc:     journal.DocID("chats/" + newID),
			Body:    newChatBody,
		})
		// Source chat gets a forked_to marker.
		forkMarkerBody := mustJSON([]map[string]any{
			{"op": "replace", "path": "/meta/forked_to", "value": newID},
		})
		appendJournalEntry(s.journalWriter, journal.Entry{
			Ts:      s.clock.Now(),
			Session: parentSID,
			Kind:    journal.KindChatsAppend,
			Doc:     journal.DocID("chats/" + parentChatID),
			Body:    forkMarkerBody,
		})
	}
	return result, nil
}

// ─── scan helpers ─────────────────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...any) error
}

func scanChat(row scanner) (*Chat, error) {
	var (
		c                          Chat
		createdAt, updatedAt, last int64
	)
	err := row.Scan(
		&c.ID, &c.AppID, &c.Room, &c.ScopeKey, &c.Title, &c.Status,
		&c.ClaudeSessionID, &c.ParentChatID, &c.SessionID,
		&createdAt, &updatedAt, &last,
	)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = time.UnixMicro(createdAt)
	c.UpdatedAt = time.UnixMicro(updatedAt)
	c.LastActiveAt = time.UnixMicro(last)
	return &c, nil
}

func scanChatRow(rows *sql.Rows) (*Chat, error) {
	var (
		c                          Chat
		createdAt, updatedAt, last int64
	)
	err := rows.Scan(
		&c.ID, &c.AppID, &c.Room, &c.ScopeKey, &c.Title, &c.Status,
		&c.ClaudeSessionID, &c.ParentChatID, &c.SessionID,
		&createdAt, &updatedAt, &last,
	)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = time.UnixMicro(createdAt)
	c.UpdatedAt = time.UnixMicro(updatedAt)
	c.LastActiveAt = time.UnixMicro(last)
	return &c, nil
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// ─── journal helpers ───────────────────────────────────────────────────────────

// chatSessionID fetches the kitsoki session_id stored on the chat row.
// Returns an empty string if the row has no session_id (pre-continue builds).
// This is a best-effort lookup — a miss never blocks the main operation.
func chatSessionID(ctx context.Context, db *sql.DB, chatID string) app.SessionID {
	var sid string
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(session_id,'') FROM chats WHERE id = ?`, chatID).Scan(&sid)
	return app.SessionID(sid)
}

// mustJSON marshals v to JSON, returning an empty object on error so a bad
// value never silently drops a journal entry. Only used for journal bodies
// where a marshalling failure is vanishingly unlikely.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// appendJournalEntry calls jw.Append when jw is non-nil; silently no-ops
// otherwise. Errors are swallowed — journal writes must not block or fail
// the main operation.
func appendJournalEntry(jw journal.Writer, e journal.Entry) {
	if jw == nil {
		return
	}
	_ = jw.Append(e)
}
