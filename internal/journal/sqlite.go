package journal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"iter"
	"time"

	"kitsoki/internal/app"
)

// ---- SQLite Writer ----------------------------------------------------------

// sqliteWriter implements Writer backed by a *sql.DB (modernc.org/sqlite).
// It shares the same *sql.DB as the session store so journal writes can be
// included in the caller's transaction via AppendJournalTx (§4.9 Rule 1).
type sqliteWriter struct {
	db *sql.DB
}

// NewSQLiteWriter returns a Writer backed by db.
// db must already be open and have the journal table created (via schema.sql).
func NewSQLiteWriter(db *sql.DB) (Writer, error) {
	if db == nil {
		return nil, fmt.Errorf("journal.NewSQLiteWriter: db must not be nil")
	}
	return &sqliteWriter{db: db}, nil
}

// Append writes a single entry to the journal inside its own transaction.
// For doc-targeted patch entries the DocVersion is assigned by reading the
// current MAX from the table and incrementing; this is consistent as long as
// callers serialise writes through the session writer lock (§4.9 Rule 5).
func (w *sqliteWriter) Append(e Entry) error {
	tx, err := w.db.Begin()
	if err != nil {
		return fmt.Errorf("journal.sqliteWriter.Append: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := AppendJournalTx(tx, e.Session, []Entry{e}); err != nil {
		return err
	}
	return tx.Commit()
}

// AppendCheckpoint writes a full-document checkpoint entry in its own
// transaction. The DocVersion is assigned as MAX(doc_version)+1 for (sid, doc).
func (w *sqliteWriter) AppendCheckpoint(sid app.SessionID, turn app.TurnNumber, seq int, doc DocID, full json.RawMessage) error {
	tx, err := w.db.Begin()
	if err != nil {
		return fmt.Errorf("journal.sqliteWriter.AppendCheckpoint: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ver, err := nextVersionTx(tx, sid, doc)
	if err != nil {
		return fmt.Errorf("journal.sqliteWriter.AppendCheckpoint: next version: %w", err)
	}

	body, err := json.Marshal(struct {
		Full json.RawMessage `json:"full"`
	}{Full: full})
	if err != nil {
		return fmt.Errorf("journal.sqliteWriter.AppendCheckpoint: marshal body: %w", err)
	}

	e := Entry{
		Ts:         time.Now(),
		Session:    sid,
		Turn:       turn,
		Seq:        seq,
		Kind:       checkpointKindFor(doc),
		Doc:        doc,
		DocVersion: ver,
		Body:       body,
	}
	if err := insertEntryTx(tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

// Flush runs a WAL checkpoint to ensure journal writes are in the main DB
// file. This is a best-effort operation; errors are returned but not fatal.
func (w *sqliteWriter) Flush() error {
	_, err := w.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return err
}

// ---- Package-level helpers used by both Writer and the store layer ----------

// AppendJournalTx inserts a batch of entries into the journal table within
// the provided transaction. Callers that also write events rows (§4.9 Rule 1)
// should pass their existing transaction here so both writes share atomicity.
//
// For patch entries that target a doc, DocVersion is assigned as
// MAX(doc_version)+1 per (session_id, doc). For typed-only entries (Doc=="")
// DocVersion is left as 0 / NULL.
//
// The function is exported so the store layer can call it from within its
// own AppendEvents transaction (next-wave wiring).
func AppendJournalTx(tx *sql.Tx, sid app.SessionID, entries []Entry) error {
	for i := range entries {
		e := entries[i]
		// Assign version for doc-targeting entries (patches and checkpoints).
		if e.Doc != "" && (IsPatchKind(e.Kind) || IsCheckpointKind(e.Kind)) {
			ver, err := nextVersionTx(tx, sid, e.Doc)
			if err != nil {
				return fmt.Errorf("journal.AppendJournalTx: next version for %q: %w", e.Doc, err)
			}
			e.DocVersion = ver
		}
		// Out-of-turn writes (chat appends, drive lifecycle, inbox events
		// emitted post-commit from non-orchestrator paths) carry Turn=0,
		// Seq=0 by convention. The (session_id, turn, seq) PK collides
		// across multiple such entries — and the SQLite-Writer's swallow-
		// errors path would silently drop all but the first. Auto-assign
		// Seq from MAX+1 for the (session, turn=0) row group so multiple
		// out-of-turn entries coexist.
		//
		// Orchestrator-driven writes set Seq explicitly (matching the
		// paired events row) and use Turn>=1, so this branch leaves them
		// untouched.
		if e.Turn == 0 && e.Seq == 0 {
			nextSeq, err := nextSeqTx(tx, sid, e.Turn)
			if err != nil {
				return fmt.Errorf("journal.AppendJournalTx: next seq for out-of-turn entry: %w", err)
			}
			e.Seq = nextSeq
		}
		if err := insertEntryTx(tx, e); err != nil {
			return err
		}
	}
	return nil
}

// nextSeqTx returns MAX(seq)+1 for (sid, turn) within tx. If no rows exist
// for that (session, turn) pair it returns 0 (so the entry becomes seq=0,
// the first row in its turn). Used to safely assign seqs for out-of-turn
// entries (chat appends, drive lifecycle, etc.) without colliding with
// other post-commit writes.
func nextSeqTx(tx *sql.Tx, sid app.SessionID, turn app.TurnNumber) (int, error) {
	var maxSeq sql.NullInt64
	err := tx.QueryRow(
		`SELECT MAX(seq) FROM journal WHERE session_id = ? AND turn = ?`,
		string(sid), int64(turn),
	).Scan(&maxSeq)
	if err != nil {
		return 0, fmt.Errorf("nextSeqTx: %w", err)
	}
	if maxSeq.Valid {
		return int(maxSeq.Int64 + 1), nil
	}
	return 0, nil
}

// nextVersionTx returns MAX(doc_version)+1 for (sid, doc) within tx.
// If no rows exist yet it returns 1.
func nextVersionTx(tx *sql.Tx, sid app.SessionID, doc DocID) (Version, error) {
	var maxVer sql.NullInt64
	err := tx.QueryRow(
		`SELECT MAX(doc_version) FROM journal WHERE session_id = ? AND doc = ?`,
		string(sid), string(doc),
	).Scan(&maxVer)
	if err != nil {
		return 0, fmt.Errorf("nextVersionTx: %w", err)
	}
	if maxVer.Valid {
		return Version(maxVer.Int64 + 1), nil
	}
	return 1, nil
}

// insertEntryTx writes a single Entry row inside tx.
func insertEntryTx(tx *sql.Tx, e Entry) error {
	tsMicro := e.Ts.UnixMicro()
	if tsMicro == 0 {
		tsMicro = time.Now().UnixMicro()
	}

	body := e.Body
	if body == nil {
		body = json.RawMessage("{}")
	}

	var doc sql.NullString
	if e.Doc != "" {
		doc = sql.NullString{String: string(e.Doc), Valid: true}
	}

	var docVer sql.NullInt64
	if e.DocVersion != 0 {
		docVer = sql.NullInt64{Int64: int64(e.DocVersion), Valid: true}
	}

	_, err := tx.Exec(
		`INSERT INTO journal (session_id, turn, seq, ts, kind, doc, doc_version, body_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(e.Session),
		int64(e.Turn),
		e.Seq,
		tsMicro,
		e.Kind,
		doc,
		docVer,
		string(body),
	)
	if err != nil {
		return fmt.Errorf("journal.insertEntryTx (kind=%s turn=%d seq=%d): %w",
			e.Kind, e.Turn, e.Seq, err)
	}
	return nil
}

// ---- SQLite Reader ----------------------------------------------------------

// sqliteReader implements Reader backed by a *sql.DB.
type sqliteReader struct {
	db *sql.DB
}

// NewSQLiteReader returns a Reader backed by db.
func NewSQLiteReader(db *sql.DB) (Reader, error) {
	if db == nil {
		return nil, fmt.Errorf("journal.NewSQLiteReader: db must not be nil")
	}
	return &sqliteReader{db: db}, nil
}

// LoadDocument finds the latest checkpoint for (sid, doc), then returns its
// body.full as current and its doc_version as version. If no checkpoint
// exists both current and version are zero values with nil error.
func (r *sqliteReader) LoadDocument(sid app.SessionID, doc DocID) (json.RawMessage, Version, error) {
	// Find latest checkpoint for this doc.
	var (
		cpKind string
		cpVer  int64
		cpBody string
	)
	err := r.db.QueryRow(
		`SELECT kind, doc_version, body_json
		 FROM journal
		 WHERE session_id = ? AND doc = ?
		   AND kind LIKE '%.checkpoint'
		 ORDER BY doc_version DESC
		 LIMIT 1`,
		string(sid), string(doc),
	).Scan(&cpKind, &cpVer, &cpBody)

	var checkpointVer Version
	var checkpointBody json.RawMessage

	if err == nil {
		// Checkpoint found.
		checkpointVer = Version(cpVer)
		var payload struct {
			Full json.RawMessage `json:"full"`
		}
		if jsonErr := json.Unmarshal([]byte(cpBody), &payload); jsonErr != nil {
			return nil, 0, fmt.Errorf("journal.LoadDocument: unmarshal checkpoint body: %w", jsonErr)
		}
		checkpointBody = payload.Full
	} else if err != sql.ErrNoRows {
		return nil, 0, fmt.Errorf("journal.LoadDocument: query checkpoint: %w", err)
	}
	// err == sql.ErrNoRows => no checkpoint, checkpointVer=0, checkpointBody=nil

	// Find the highest patch version after the checkpoint.
	var maxPatchVer sql.NullInt64
	err = r.db.QueryRow(
		`SELECT MAX(doc_version)
		 FROM journal
		 WHERE session_id = ? AND doc = ?
		   AND doc_version > ?
		   AND kind NOT LIKE '%.checkpoint'`,
		string(sid), string(doc), int64(checkpointVer),
	).Scan(&maxPatchVer)
	if err != nil && err != sql.ErrNoRows {
		return nil, 0, fmt.Errorf("journal.LoadDocument: query max patch version: %w", err)
	}

	highestVer := checkpointVer
	if maxPatchVer.Valid && Version(maxPatchVer.Int64) > highestVer {
		highestVer = Version(maxPatchVer.Int64)
	}

	return checkpointBody, highestVer, nil
}

// ReplayFrom returns an iterator over patch entries for (sid, doc) where
// DocVersion >= from, ordered by (turn, seq). The query streams rows lazily.
func (r *sqliteReader) ReplayFrom(sid app.SessionID, doc DocID, from Version) iter.Seq[Entry] {
	return func(yield func(Entry) bool) {
		rows, err := r.db.Query(
			`SELECT turn, seq, ts, kind, doc, doc_version, body_json
			 FROM journal
			 WHERE session_id = ? AND doc = ? AND doc_version >= ?
			   AND kind NOT LIKE '%.checkpoint'
			 ORDER BY turn ASC, seq ASC`,
			string(sid), string(doc), int64(from),
		)
		if err != nil {
			// Iterators can't return errors; callers should check for anomalies.
			return
		}
		defer rows.Close()

		for rows.Next() {
			var (
				turnN   int64
				seq     int
				tsMicro int64
				kind    string
				docStr  sql.NullString
				docVer  sql.NullInt64
				body    string
			)
			if err := rows.Scan(&turnN, &seq, &tsMicro, &kind, &docStr, &docVer, &body); err != nil {
				return
			}
			e := Entry{
				Ts:      time.UnixMicro(tsMicro),
				Session: sid,
				Turn:    app.TurnNumber(turnN),
				Seq:     seq,
				Kind:    kind,
				Body:    json.RawMessage(body),
			}
			if docStr.Valid {
				e.Doc = DocID(docStr.String)
			}
			if docVer.Valid {
				e.DocVersion = Version(docVer.Int64)
			}
			if !yield(e) {
				return
			}
		}
	}
}

// replayTypedSQL is the query for ReplayTyped: excludes the four patch kinds
// and all checkpoint kinds (which end in ".checkpoint").
// Patch kinds are enumerated explicitly to avoid false positives from LIKE.
const replayTypedSQL = `SELECT turn, seq, ts, kind, doc, doc_version, body_json
FROM journal
WHERE session_id = ?
  AND kind NOT IN (
      'world.patch',
      'state.transition',
      'chats.append',
      'jobs.update'
  )
  AND kind NOT LIKE '%.checkpoint'
ORDER BY turn ASC, seq ASC`

// ReplayTyped returns an iterator over all typed (non-patch, non-checkpoint)
// entries for sid, ordered by (turn, seq). Rows are streamed lazily.
func (r *sqliteReader) ReplayTyped(sid app.SessionID) iter.Seq[Entry] {
	return func(yield func(Entry) bool) {
		rows, err := r.db.Query(replayTypedSQL, string(sid))
		if err != nil {
			return
		}
		defer rows.Close()

		for rows.Next() {
			var (
				turnN   int64
				seq     int
				tsMicro int64
				kind    string
				docStr  sql.NullString
				docVer  sql.NullInt64
				body    string
			)
			if err := rows.Scan(&turnN, &seq, &tsMicro, &kind, &docStr, &docVer, &body); err != nil {
				return
			}
			e := Entry{
				Ts:      time.UnixMicro(tsMicro),
				Session: sid,
				Turn:    app.TurnNumber(turnN),
				Seq:     seq,
				Kind:    kind,
				Body:    json.RawMessage(body),
			}
			if docStr.Valid {
				e.Doc = DocID(docStr.String)
			}
			if docVer.Valid {
				e.DocVersion = Version(docVer.Int64)
			}
			if !yield(e) {
				return
			}
		}
	}
}

// LatestCheckpoint returns the most recent checkpoint entry for (sid, doc).
// Returns a zero Entry and false if no checkpoint exists.
func (r *sqliteReader) LatestCheckpoint(sid app.SessionID, doc DocID) (Entry, bool) {
	var (
		turnN   int64
		seq     int
		tsMicro int64
		kind    string
		docVer  int64
		body    string
	)
	err := r.db.QueryRow(
		`SELECT turn, seq, ts, kind, doc_version, body_json
		 FROM journal
		 WHERE session_id = ? AND doc = ?
		   AND kind LIKE '%.checkpoint'
		 ORDER BY doc_version DESC
		 LIMIT 1`,
		string(sid), string(doc),
	).Scan(&turnN, &seq, &tsMicro, &kind, &docVer, &body)
	if err == sql.ErrNoRows {
		return Entry{}, false
	}
	if err != nil {
		return Entry{}, false
	}
	return Entry{
		Ts:         time.UnixMicro(tsMicro),
		Session:    sid,
		Turn:       app.TurnNumber(turnN),
		Seq:        seq,
		Kind:       kind,
		Doc:        doc,
		DocVersion: Version(docVer),
		Body:       json.RawMessage(body),
	}, true
}

// LoadOracleCallEntries returns all KindOracleCall journal entries for sid as
// a slice of Entry values (including timestamps). Entries are ordered by
// (turn, seq). This is used by FromHistory to synthesise oracle trace events
// from journal data when WithOracleJournal is supplied.
func LoadOracleCallEntries(db *sql.DB, sid app.SessionID) ([]Entry, error) {
	rows, err := db.Query(
		`SELECT ts, turn, seq, body_json FROM journal
		 WHERE session_id = ? AND kind = 'oracle.call'
		 ORDER BY turn ASC, seq ASC`,
		string(sid),
	)
	if err != nil {
		return nil, fmt.Errorf("journal.LoadOracleCallEntries: query: %w", err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var (
			tsMicro int64
			turnN   int64
			seq     int
			bodyStr string
		)
		if err := rows.Scan(&tsMicro, &turnN, &seq, &bodyStr); err != nil {
			continue
		}
		out = append(out, Entry{
			Ts:      time.UnixMicro(tsMicro),
			Session: sid,
			Turn:    app.TurnNumber(turnN),
			Seq:     seq,
			Kind:    KindOracleCall,
			Body:    json.RawMessage(bodyStr),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal.LoadOracleCallEntries: scan: %w", err)
	}
	return out, nil
}

// LoadOracleCalls returns all KindOracleCall journal entries for sid, keyed by
// the call_id field in the body. Entries with no parseable call_id are skipped.
// This is used by export-status to merge full prompt/response payloads into the
// lean slog oracle.<verb>.complete records.
func LoadOracleCalls(db *sql.DB, sid app.SessionID) (map[string]json.RawMessage, error) {
	rows, err := db.Query(
		`SELECT body_json FROM journal
		 WHERE session_id = ? AND kind = 'oracle.call'
		 ORDER BY turn ASC, seq ASC`,
		string(sid),
	)
	if err != nil {
		return nil, fmt.Errorf("journal.LoadOracleCalls: query: %w", err)
	}
	defer rows.Close()

	out := make(map[string]json.RawMessage)
	for rows.Next() {
		var bodyStr string
		if err := rows.Scan(&bodyStr); err != nil {
			continue
		}
		// Parse just the call_id field to build the index key.
		var partial struct {
			CallID string `json:"call_id"`
		}
		if err := json.Unmarshal([]byte(bodyStr), &partial); err != nil || partial.CallID == "" {
			continue
		}
		out[partial.CallID] = json.RawMessage(bodyStr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal.LoadOracleCalls: scan: %w", err)
	}
	return out, nil
}

// ListLiveDocs returns the distinct DocIDs that have at least one entry for sid.
func (r *sqliteReader) ListLiveDocs(sid app.SessionID) []DocID {
	rows, err := r.db.Query(
		`SELECT DISTINCT doc FROM journal
		 WHERE session_id = ? AND doc IS NOT NULL
		 ORDER BY doc ASC`,
		string(sid),
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var docs []DocID
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			continue
		}
		docs = append(docs, DocID(d))
	}
	return docs
}
