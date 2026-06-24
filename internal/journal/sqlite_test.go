package journal_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"

	_ "modernc.org/sqlite"
)

// openTestDB opens an isolated SQLite database in dir with WAL mode and the
// journal table DDL applied. It mirrors the setup in internal/store/sqlite.go.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "journal_test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("pragma %q: %v", p, err)
		}
	}

	ddl := `
CREATE TABLE IF NOT EXISTS journal (
    session_id   TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    ts           INTEGER NOT NULL,
    kind         TEXT    NOT NULL,
    doc          TEXT,
    doc_version  INTEGER,
    body_json    TEXT    NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;
CREATE INDEX IF NOT EXISTS journal_doc_idx ON journal (session_id, doc, doc_version);
`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("DDL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// makeWriter/makeReader are helpers to reduce boilerplate.
func makeWriter(t *testing.T, db *sql.DB) journal.Writer {
	t.Helper()
	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}
	return w
}

func makeReader(t *testing.T, db *sql.DB) journal.Reader {
	t.Helper()
	r, err := journal.NewSQLiteReader(db)
	if err != nil {
		t.Fatalf("NewSQLiteReader: %v", err)
	}
	return r
}

// appendSQLitePatch is a test helper that appends a patch entry.
func appendSQLitePatch(t *testing.T, w journal.Writer, sid app.SessionID, turn app.TurnNumber, seq int, doc journal.DocID, kind string) {
	t.Helper()
	e := journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     seq,
		Kind:    kind,
		Doc:     doc,
		Body:    json.RawMessage(`{"ops":[]}`),
	}
	if err := w.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// appendSQLiteTyped appends a typed-only entry (no doc).
func appendSQLiteTyped(t *testing.T, w journal.Writer, sid app.SessionID, turn app.TurnNumber, seq int, kind string) {
	t.Helper()
	e := journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     seq,
		Kind:    kind,
		Body:    json.RawMessage(`{}`),
	}
	if err := w.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// ----- Round-trip tests -------------------------------------------------------

// TestSQLite_RoundTrip_PatchEntries writes several patch entries and reads
// them back via ReplayFrom, asserting correct ordering and count.
func TestSQLite_RoundTrip_PatchEntries(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("rt-1")
	// Insert in non-monotonic order.
	appendSQLitePatch(t, w, sid, 3, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 1, 1, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 2, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)

	var got []journal.Entry
	seq, errFn := r.ReplayFrom(sid, "world", 1)
	for e := range seq {
		got = append(got, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayFrom: %v", err)
	}

	if len(got) != 4 {
		t.Fatalf("ReplayFrom len = %d, want 4", len(got))
	}
	// Should come back sorted by (turn, seq).
	wantOrder := [][2]int64{{1, 0}, {1, 1}, {2, 0}, {3, 0}}
	for i, e := range got {
		if int64(e.Turn) != wantOrder[i][0] || int64(e.Seq) != wantOrder[i][1] {
			t.Errorf("got[%d] = (turn=%d,seq=%d), want (%d,%d)",
				i, e.Turn, e.Seq, wantOrder[i][0], wantOrder[i][1])
		}
	}
}

// TestSQLite_ReplayFrom_FiltersVersion ensures entries with DocVersion < from
// are excluded.
func TestSQLite_ReplayFrom_FiltersVersion(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("rv-1")
	// Five patches get DocVersions 1..5.
	for i := range 5 {
		appendSQLitePatch(t, w, sid, app.TurnNumber(i+1), 0, "world", journal.KindWorldPatch)
	}

	var got []journal.Entry
	seq, errFn := r.ReplayFrom(sid, "world", 3)
	for e := range seq {
		got = append(got, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayFrom: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ReplayFrom(3) len = %d, want 3", len(got))
	}
	for _, e := range got {
		if e.DocVersion < 3 {
			t.Errorf("DocVersion %d < 3 included", e.DocVersion)
		}
	}
}

// TestSQLite_CheckpointPrecedence verifies LoadDocument / ReplayFrom drop
// entries at or below the checkpoint version.
func TestSQLite_CheckpointPrecedence(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("cp-1")
	// Three patches (versions 1,2,3), then a checkpoint (version 4),
	// then two more patches (versions 5,6).
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 2, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 3, 0, "world", journal.KindWorldPatch)

	if err := w.AppendCheckpoint(sid, 4, 0, "world", json.RawMessage(`{"vars":{}}`)); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}

	appendSQLitePatch(t, w, sid, 5, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 6, 0, "world", journal.KindWorldPatch)

	cp, ok, err := r.LatestCheckpoint(sid, "world")
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("LatestCheckpoint: not found")
	}
	if cp.DocVersion != 4 {
		t.Errorf("checkpoint DocVersion = %d, want 4", cp.DocVersion)
	}

	// Patches after checkpoint.
	var afterCp []journal.Entry
	seq, errFn := r.ReplayFrom(sid, "world", cp.DocVersion+1)
	for e := range seq {
		afterCp = append(afterCp, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayFrom: %v", err)
	}
	if len(afterCp) != 2 {
		t.Errorf("patches after checkpoint = %d, want 2", len(afterCp))
	}

	// LoadDocument: highest version is 6.
	_, ver, err := r.LoadDocument(sid, "world")
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if ver != 6 {
		t.Errorf("highest version = %d, want 6", ver)
	}
}

// TestSQLite_LoadDocument_NoCheckpoint ensures LoadDocument returns (nil, 0, nil)
// when no checkpoint exists.
func TestSQLite_LoadDocument_NoCheckpoint(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("no-cp")
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)

	cur, ver, err := r.LoadDocument(sid, "world")
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if cur != nil {
		t.Errorf("current = %s, want nil", cur)
	}
	// Highest version is 1 (the patch).
	if ver != 1 {
		t.Errorf("ver = %d, want 1", ver)
	}
}

// TestSQLite_LoadDocument_CheckpointReturnsFullBody verifies that LoadDocument
// extracts the body.full field from a checkpoint entry.
func TestSQLite_LoadDocument_CheckpointReturnsFullBody(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("cp-full")
	full := json.RawMessage(`{"vars":{"x":42}}`)
	if err := w.AppendCheckpoint(sid, 1, 0, "world", full); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}

	cur, ver, err := r.LoadDocument(sid, "world")
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if string(cur) != string(full) {
		t.Errorf("LoadDocument body = %s, want %s", cur, full)
	}
	if ver != 1 {
		t.Errorf("LoadDocument version = %d, want 1", ver)
	}
}

// ----- DocVersion counter tests -----------------------------------------------

// TestSQLite_DocVersion_Increments verifies DocVersion increments correctly
// across multiple Append calls within the same writer instance.
func TestSQLite_DocVersion_Increments(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("dv-1")
	for i := range 4 {
		appendSQLitePatch(t, w, sid, app.TurnNumber(i+1), 0, "world", journal.KindWorldPatch)
	}

	_, ver, err := r.LoadDocument(sid, "world")
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if ver != 4 {
		t.Errorf("DocVersion after 4 appends = %d, want 4", ver)
	}
}

// TestSQLite_DocVersion_PerDocIsolation verifies that versions are tracked
// independently per (session, doc).
func TestSQLite_DocVersion_PerDocIsolation(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("dv-iso")
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 1, 1, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 1, 2, "state", journal.KindStateTransition)

	_, worldVer, err := r.LoadDocument(sid, "world")
	if err != nil {
		t.Fatalf("LoadDocument world: %v", err)
	}
	_, stateVer, err := r.LoadDocument(sid, "state")
	if err != nil {
		t.Fatalf("LoadDocument state: %v", err)
	}
	if worldVer != 2 {
		t.Errorf("world version = %d, want 2", worldVer)
	}
	if stateVer != 1 {
		t.Errorf("state version = %d, want 1", stateVer)
	}
}

// ----- appendJournalTx tests --------------------------------------------------

// TestSQLite_AppendJournalTx_CommitSucceeds verifies that appendJournalTx
// (called inside a caller-supplied transaction) inserts rows on commit.
func TestSQLite_AppendJournalTx_CommitSucceeds(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	sid := app.SessionID("tx-commit")
	entries := []journal.Entry{
		{
			Ts:      time.Now(),
			Session: sid,
			Turn:    1,
			Seq:     0,
			Kind:    journal.KindWorldPatch,
			Doc:     "world",
			Body:    json.RawMessage(`{"ops":[]}`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := journal.AppendJournalTx(tx, sid, entries); err != nil {
		_ = tx.Rollback()
		t.Fatalf("appendJournalTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	r := makeReader(t, db)
	var got []journal.Entry
	seq, errFn := r.ReplayFrom(sid, "world", 1)
	for e := range seq {
		got = append(got, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayFrom: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("after commit: entries = %d, want 1", len(got))
	}
}

// TestSQLite_AppendJournalTx_RollbackYieldsNoRows verifies that rolling back
// a transaction that called appendJournalTx leaves no rows in the table.
func TestSQLite_AppendJournalTx_RollbackYieldsNoRows(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	sid := app.SessionID("tx-rollback")
	entries := []journal.Entry{
		{
			Ts:      time.Now(),
			Session: sid,
			Turn:    1,
			Seq:     0,
			Kind:    journal.KindHostInvoked,
			Body:    json.RawMessage(`{}`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := journal.AppendJournalTx(tx, sid, entries); err != nil {
		_ = tx.Rollback()
		t.Fatalf("appendJournalTx: %v", err)
	}
	// Deliberately rollback.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// No rows should be visible.
	r := makeReader(t, db)
	var got []journal.Entry
	seq, errFn := r.ReplayTyped(sid)
	for e := range seq {
		got = append(got, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayTyped: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("after rollback: typed entries = %d, want 0", len(got))
	}
}

// ----- Typed entry tests ------------------------------------------------------

// TestSQLite_ReplayTyped_FiltersCorrectly checks that ReplayTyped returns only
// non-patch, non-checkpoint entries in (turn, seq) order.
func TestSQLite_ReplayTyped_FiltersCorrectly(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("typed-sql")
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendSQLiteTyped(t, w, sid, 1, 1, journal.KindHostInvoked)
	appendSQLiteTyped(t, w, sid, 2, 0, journal.KindClarifyRequested)
	appendSQLitePatch(t, w, sid, 2, 1, "world", journal.KindWorldPatch)
	if err := w.AppendCheckpoint(sid, 3, 0, "world", json.RawMessage(`{"vars":{}}`)); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}
	appendSQLiteTyped(t, w, sid, 3, 1, journal.KindClarifyAnswered)

	var typed []journal.Entry
	seq, errFn := r.ReplayTyped(sid)
	for e := range seq {
		typed = append(typed, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayTyped: %v", err)
	}
	if len(typed) != 3 {
		t.Fatalf("ReplayTyped len = %d, want 3", len(typed))
	}
	wantKinds := []string{
		journal.KindHostInvoked,
		journal.KindClarifyRequested,
		journal.KindClarifyAnswered,
	}
	for i, e := range typed {
		if e.Kind != wantKinds[i] {
			t.Errorf("typed[%d].Kind = %q, want %q", i, e.Kind, wantKinds[i])
		}
	}
}

// TestSQLite_ListLiveDocs returns the distinct docs for a session.
func TestSQLite_ListLiveDocs(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("docs-sql")
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, sid, 1, 1, "state", journal.KindStateTransition)
	appendSQLitePatch(t, w, sid, 1, 2, "chats/c1", journal.KindChatsAppend)
	appendSQLiteTyped(t, w, sid, 1, 3, journal.KindHostInvoked) // no doc

	docs := r.ListLiveDocs(sid)
	want := map[string]struct{}{"world": {}, "state": {}, "chats/c1": {}}
	if len(docs) != len(want) {
		t.Fatalf("ListLiveDocs len = %d, want %d", len(docs), len(want))
	}
	for _, d := range docs {
		if _, ok := want[string(d)]; !ok {
			t.Errorf("unexpected doc %q", d)
		}
	}
}

// TestSQLite_MultiSession verifies session isolation.
func TestSQLite_MultiSession(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	s1 := app.SessionID("sql-sess-A")
	s2 := app.SessionID("sql-sess-B")

	appendSQLitePatch(t, w, s1, 1, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, s2, 1, 0, "world", journal.KindWorldPatch)
	appendSQLitePatch(t, w, s2, 2, 0, "world", journal.KindWorldPatch)

	var s1E []journal.Entry
	s1Seq, s1Err := r.ReplayFrom(s1, "world", 1)
	for e := range s1Seq {
		s1E = append(s1E, e)
	}
	if err := s1Err(); err != nil {
		t.Fatalf("ReplayFrom(s1): %v", err)
	}
	if len(s1E) != 1 {
		t.Errorf("session A entries = %d, want 1", len(s1E))
	}

	var s2E []journal.Entry
	s2Seq, s2Err := r.ReplayFrom(s2, "world", 1)
	for e := range s2Seq {
		s2E = append(s2E, e)
	}
	if err := s2Err(); err != nil {
		t.Fatalf("ReplayFrom(s2): %v", err)
	}
	if len(s2E) != 2 {
		t.Errorf("session B entries = %d, want 2", len(s2E))
	}
}

// TestSQLite_Flush_NoError verifies Flush doesn't error on a healthy DB.
func TestSQLite_Flush_NoError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	if err := w.Flush(); err != nil {
		t.Errorf("Flush: %v", err)
	}
}

// TestSQLite_AppendCheckpoint_VersionContinuesFromPatches checks that the
// checkpoint version follows on from previous patch versions (not reset to 1).
func TestSQLite_AppendCheckpoint_VersionContinuesFromPatches(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("cp-ver")
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch) // v1
	appendSQLitePatch(t, w, sid, 2, 0, "world", journal.KindWorldPatch) // v2

	if err := w.AppendCheckpoint(sid, 3, 0, "world", json.RawMessage(`{"vars":{}}`)); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}

	cp, ok, err := r.LatestCheckpoint(sid, "world")
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("no checkpoint found")
	}
	// Checkpoint should be version 3 (MAX of {1,2}+1).
	if cp.DocVersion != 3 {
		t.Errorf("checkpoint DocVersion = %d, want 3", cp.DocVersion)
	}
}

// TestSQLite_NilConstructors_Errors verifies that nil db returns an error.
func TestSQLite_NilConstructors_Errors(t *testing.T) {
	t.Parallel()

	if _, err := journal.NewSQLiteWriter(nil); err == nil {
		t.Error("NewSQLiteWriter(nil) expected error, got nil")
	}
	if _, err := journal.NewSQLiteReader(nil); err == nil {
		t.Error("NewSQLiteReader(nil) expected error, got nil")
	}
}

// TestSQLite_AppendJournalTx_TypedEntryNoDocVersion verifies that typed-only
// entries (Doc=="") receive NULL doc_version (DocVersion stays 0 in Go).
func TestSQLite_AppendJournalTx_TypedEntryNoDocVersion(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	sid := app.SessionID("typed-dv")
	entries := []journal.Entry{
		{
			Ts:      time.Now(),
			Session: sid,
			Turn:    1,
			Seq:     0,
			Kind:    journal.KindClarifyRequested,
			// Doc intentionally empty.
			Body: json.RawMessage(`{}`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := journal.AppendJournalTx(tx, sid, entries); err != nil {
		_ = tx.Rollback()
		t.Fatalf("appendJournalTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	r := makeReader(t, db)
	var got []journal.Entry
	seq, errFn := r.ReplayTyped(sid)
	for e := range seq {
		got = append(got, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayTyped: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("typed entries = %d, want 1", len(got))
	}
	if got[0].DocVersion != 0 {
		t.Errorf("DocVersion = %d, want 0 for typed entry", got[0].DocVersion)
	}
}

// ----- Error-surfacing tests --------------------------------------------------
//
// These cover the contract that scan/query failures are no longer silently
// swallowed: the iterators expose them via their err() accessor and
// LatestCheckpoint returns a non-nil error kept distinct from "not found".
// Each asserts a condition the pre-fix code (which returned silently / collapsed
// errors to false) could not satisfy.

// TestSQLite_ReplayFrom_SurfacesQueryError drops the journal table out from
// under an open reader so the underlying Query fails, and asserts the err()
// accessor reports it rather than the stream ending clean.
func TestSQLite_ReplayFrom_SurfacesQueryError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("query-err")
	appendSQLitePatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)

	if _, err := db.Exec("DROP TABLE journal"); err != nil {
		t.Fatalf("DROP TABLE: %v", err)
	}

	seq, errFn := r.ReplayFrom(sid, "world", 1)
	var n int
	for range seq {
		n++
	}
	if n != 0 {
		t.Fatalf("yielded %d entries after table drop, want 0", n)
	}
	if errFn() == nil {
		t.Fatal("ReplayFrom err() = nil after query failure, want non-nil")
	}
}

// TestSQLite_ReplayTyped_SurfacesQueryError mirrors the above for ReplayTyped.
func TestSQLite_ReplayTyped_SurfacesQueryError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	w := makeWriter(t, db)
	r := makeReader(t, db)

	sid := app.SessionID("query-err-typed")
	appendSQLiteTyped(t, w, sid, 1, 0, journal.KindHostInvoked)

	if _, err := db.Exec("DROP TABLE journal"); err != nil {
		t.Fatalf("DROP TABLE: %v", err)
	}

	seq, errFn := r.ReplayTyped(sid)
	var n int
	for range seq {
		n++
	}
	if n != 0 {
		t.Fatalf("yielded %d entries after table drop, want 0", n)
	}
	if errFn() == nil {
		t.Fatal("ReplayTyped err() = nil after query failure, want non-nil")
	}
}

// TestSQLite_ReplayTyped_SurfacesScanError replaces the journal table with one
// whose turn column holds non-numeric text, forcing rows.Scan to fail
// mid-stream. This is the subtler case: the query succeeds, but a row can't be
// decoded, and the pre-fix code returned silently on it.
func TestSQLite_ReplayTyped_SurfacesScanError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	r := makeReader(t, db)

	sid := app.SessionID("scan-err")

	// Swap in a non-STRICT table so we can store text in the turn column, then
	// insert a row that passes ReplayTyped's WHERE filter but cannot scan into
	// the int64 turn field.
	if _, err := db.Exec("DROP TABLE journal"); err != nil {
		t.Fatalf("DROP TABLE: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE journal (
		session_id  TEXT,
		turn        TEXT,
		seq         INTEGER,
		ts          INTEGER,
		kind        TEXT,
		doc         TEXT,
		doc_version INTEGER,
		body_json   TEXT
	)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO journal (session_id, turn, seq, ts, kind, doc, doc_version, body_json)
		 VALUES (?, 'not-a-number', 0, 0, ?, NULL, NULL, '{}')`,
		string(sid), journal.KindHostInvoked,
	); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	seq, errFn := r.ReplayTyped(sid)
	for range seq { //nolint:revive // draining the (truncated) sequence
	}
	if errFn() == nil {
		t.Fatal("ReplayTyped err() = nil after scan failure, want non-nil")
	}
}

// TestSQLite_LatestCheckpoint_ErrorVsNotFound asserts the third return value
// distinguishes a query failure (non-nil error) from a clean "no checkpoint"
// (false, nil error) — the distinction the old (Entry, bool) signature lost.
func TestSQLite_LatestCheckpoint_ErrorVsNotFound(t *testing.T) {
	t.Parallel()

	// Not-found: intact table, no checkpoint rows.
	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		r := makeReader(t, db)
		_, ok, err := r.LatestCheckpoint(app.SessionID("absent"), "world")
		if err != nil {
			t.Fatalf("err = %v, want nil for not-found", err)
		}
		if ok {
			t.Fatal("ok = true, want false for not-found")
		}
	})

	// Error: table dropped so the query fails.
	t.Run("query_error", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		r := makeReader(t, db)
		if _, err := db.Exec("DROP TABLE journal"); err != nil {
			t.Fatalf("DROP TABLE: %v", err)
		}
		_, ok, err := r.LatestCheckpoint(app.SessionID("any"), "world")
		if err == nil {
			t.Fatal("err = nil after query failure, want non-nil")
		}
		if ok {
			t.Fatal("ok = true on error, want false")
		}
	})
}
