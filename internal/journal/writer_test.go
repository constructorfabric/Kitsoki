package journal_test

import (
	"encoding/json"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
)

func TestMemWriter_Append_AssignsDocVersion(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)

	sid := app.SessionID("sess-1")
	e1 := journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    1,
		Seq:     0,
		Kind:    journal.KindWorldPatch,
		Doc:     "world",
		Body:    json.RawMessage(`{"ops":[]}`),
	}
	e2 := journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    1,
		Seq:     1,
		Kind:    journal.KindWorldPatch,
		Doc:     "world",
		Body:    json.RawMessage(`{"ops":[]}`),
	}

	if err := w.Append(e1); err != nil {
		t.Fatalf("Append e1: %v", err)
	}
	if err := w.Append(e2); err != nil {
		t.Fatalf("Append e2: %v", err)
	}

	r := journal.NewMemReader(store)
	_, ver, err := r.LoadDocument(sid, "world")
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if ver != 2 {
		t.Errorf("version = %d, want 2", ver)
	}
}

func TestMemWriter_AppendCheckpoint_Kind(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	sid := app.SessionID("sess-2")
	full := json.RawMessage(`{"vars":{"x":1}}`)
	if err := w.AppendCheckpoint(sid, 20, 0, "world", full); err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}

	cp, ok, err := r.LatestCheckpoint(sid, "world")
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("LatestCheckpoint returned false")
	}
	if cp.Kind != journal.KindWorldCheckpoint {
		t.Errorf("Kind = %q, want %q", cp.Kind, journal.KindWorldCheckpoint)
	}
	if cp.DocVersion != 1 {
		t.Errorf("DocVersion = %d, want 1", cp.DocVersion)
	}
}

func TestMemWriter_TypedEntry_NotAssignedVersion(t *testing.T) {
	t.Parallel()

	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)

	sid := app.SessionID("sess-3")
	e := journal.Entry{
		Session: sid,
		Turn:    1,
		Seq:     0,
		Kind:    journal.KindHostInvoked,
		// Doc intentionally empty for typed entries.
	}
	if err := w.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	r := journal.NewMemReader(store)
	var typed []journal.Entry
	seq, errFn := r.ReplayTyped(sid)
	for te := range seq {
		typed = append(typed, te)
	}
	if err := errFn(); err != nil {
		t.Fatalf("ReplayTyped: %v", err)
	}
	if len(typed) != 1 {
		t.Fatalf("ReplayTyped len = %d, want 1", len(typed))
	}
	if typed[0].DocVersion != 0 {
		t.Errorf("DocVersion = %d, want 0 for typed entry", typed[0].DocVersion)
	}
}

func TestMemWriter_Flush_NoError(t *testing.T) {
	t.Parallel()
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	if err := w.Flush(); err != nil {
		t.Errorf("Flush: %v", err)
	}
}
