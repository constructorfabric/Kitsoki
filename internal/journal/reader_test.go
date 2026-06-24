package journal_test

import (
	"encoding/json"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
)

func appendPatch(t *testing.T, w journal.Writer, sid app.SessionID, turn app.TurnNumber, seq int, doc journal.DocID, kind string) {
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

func appendTyped(t *testing.T, w journal.Writer, sid app.SessionID, turn app.TurnNumber, seq int, kind string) {
	t.Helper()
	e := journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     seq,
		Kind:    kind,
	}
	if err := w.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// TestReader_OrderedByTurnSeq checks that ReplayFrom returns entries sorted
// by (Turn, Seq) regardless of insertion order.
func TestReader_OrderedByTurnSeq(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	sid := app.SessionID("order-test")
	// Insert out of order.
	appendPatch(t, w, sid, 3, 1, "world", journal.KindWorldPatch)
	appendPatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, sid, 2, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, sid, 1, 1, "world", journal.KindWorldPatch)

	var entries []journal.Entry
	seq, errFn := r.ReplayFrom(sid, "world", 1)
	for e := range seq {
		entries = append(entries, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayFrom: %v", err)
	}

	if len(entries) != 4 {
		t.Fatalf("len = %d, want 4", len(entries))
	}
	wantOrder := [][2]int64{{1, 0}, {1, 1}, {2, 0}, {3, 1}}
	for i, e := range entries {
		if int64(e.Turn) != wantOrder[i][0] || int64(e.Seq) != wantOrder[i][1] {
			t.Errorf("entries[%d] = (turn=%d, seq=%d), want (%d, %d)",
				i, e.Turn, e.Seq, wantOrder[i][0], wantOrder[i][1])
		}
	}
}

// TestReader_ReplayFrom_FiltersVersion checks that entries with DocVersion <
// from are excluded.
func TestReader_ReplayFrom_FiltersVersion(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	sid := app.SessionID("filter-test")
	// Append 5 patch entries; they get DocVersions 1..5.
	for i := range 5 {
		appendPatch(t, w, sid, app.TurnNumber(i+1), 0, "world", journal.KindWorldPatch)
	}

	var entries []journal.Entry
	seq, errFn := r.ReplayFrom(sid, "world", 3)
	for e := range seq {
		entries = append(entries, e)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayFrom: %v", err)
	}
	// Versions 3, 4, 5 should be included.
	if len(entries) != 3 {
		t.Fatalf("len = %d, want 3 (versions >= 3)", len(entries))
	}
	for _, e := range entries {
		if e.DocVersion < 3 {
			t.Errorf("DocVersion %d < 3 included in ReplayFrom(3)", e.DocVersion)
		}
	}
}

// TestReader_CheckpointPrecedence verifies that LoadDocument drops patches at
// or below the checkpoint version.
func TestReader_CheckpointPrecedence(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	sid := app.SessionID("checkpoint-test")
	// Three patches (DocVersions 1, 2, 3) then a checkpoint (DocVersion 4),
	// then two more patches (5, 6).
	appendPatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, sid, 2, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, sid, 3, 0, "world", journal.KindWorldPatch)

	if err := w.AppendCheckpoint(sid, 4, 0, "world", json.RawMessage(`{"vars":{}}`)); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}

	appendPatch(t, w, sid, 5, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, sid, 6, 0, "world", journal.KindWorldPatch)

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

	// ReplayFrom at checkpoint+1 should return only the 2 post-checkpoint patches.
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

	_, ver, err := r.LoadDocument(sid, "world")
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if ver != 6 {
		t.Errorf("highest version = %d, want 6", ver)
	}
}

// TestReader_ReplayTyped returns only typed (non-patch, non-checkpoint) entries.
func TestReader_ReplayTyped(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	sid := app.SessionID("typed-test")

	appendPatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendTyped(t, w, sid, 1, 1, journal.KindHostInvoked)
	appendTyped(t, w, sid, 2, 0, journal.KindClarifyRequested)
	appendPatch(t, w, sid, 2, 1, "world", journal.KindWorldPatch)
	if err := w.AppendCheckpoint(sid, 3, 0, "world", json.RawMessage(`{"vars":{}}`)); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}
	appendTyped(t, w, sid, 3, 1, journal.KindClarifyAnswered)

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

// TestReader_ListLiveDocs covers the doc enumeration.
func TestReader_ListLiveDocs(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	sid := app.SessionID("docs-test")
	appendPatch(t, w, sid, 1, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, sid, 1, 1, "state", journal.KindStateTransition)
	appendPatch(t, w, sid, 1, 2, "chats/c1", journal.KindChatsAppend)
	// A typed entry with no doc should not appear.
	appendTyped(t, w, sid, 1, 3, journal.KindHostInvoked)

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

// TestReader_ErrAccessorsAlwaysNil confirms the in-memory reader honors the
// same (seq, err) / (Entry, bool, error) contract as the SQLite reader: it
// never fails, so its accessors report nil. This keeps the two implementations
// aligned against the Reader interface.
func TestReader_ErrAccessorsAlwaysNil(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	r := journal.NewMemReader(store)

	sid := app.SessionID("nil-err")

	fromSeq, fromErr := r.ReplayFrom(sid, "world", 1)
	for range fromSeq {
	}
	if err := fromErr(); err != nil {
		t.Errorf("ReplayFrom err() = %v, want nil", err)
	}

	typedSeq, typedErr := r.ReplayTyped(sid)
	for range typedSeq {
	}
	if err := typedErr(); err != nil {
		t.Errorf("ReplayTyped err() = %v, want nil", err)
	}

	if _, _, err := r.LatestCheckpoint(sid, "world"); err != nil {
		t.Errorf("LatestCheckpoint err = %v, want nil", err)
	}
}

// TestReader_MultiSession checks isolation between sessions.
func TestReader_MultiSession(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	s1 := app.SessionID("session-A")
	s2 := app.SessionID("session-B")

	appendPatch(t, w, s1, 1, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, s2, 1, 0, "world", journal.KindWorldPatch)
	appendPatch(t, w, s2, 2, 0, "world", journal.KindWorldPatch)

	var s1Entries []journal.Entry
	s1Seq, s1Err := r.ReplayFrom(s1, "world", 1)
	for e := range s1Seq {
		s1Entries = append(s1Entries, e)
	}
	if err := s1Err(); err != nil {
		t.Fatalf("ReplayFrom(s1): %v", err)
	}
	if len(s1Entries) != 1 {
		t.Errorf("session A entries = %d, want 1", len(s1Entries))
	}

	var s2Entries []journal.Entry
	s2Seq, s2Err := r.ReplayFrom(s2, "world", 1)
	for e := range s2Seq {
		s2Entries = append(s2Entries, e)
	}
	if err := s2Err(); err != nil {
		t.Fatalf("ReplayFrom(s2): %v", err)
	}
	if len(s2Entries) != 2 {
		t.Errorf("session B entries = %d, want 2", len(s2Entries))
	}
}
