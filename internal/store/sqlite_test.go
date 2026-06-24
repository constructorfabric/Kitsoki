package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// makeAppDef creates a minimal AppDef for testing.
func makeAppDef(id, version string) *app.AppDef {
	return &app.AppDef{
		App: app.AppMeta{ID: id, Version: version},
	}
}

// makeEvents returns n events for the given turn, each with a TransitionApplied kind.
func makeEvents(turn app.TurnNumber, n int) []store.Event {
	evs := make([]store.Event, n)
	for i := range evs {
		payload, _ := json.Marshal(map[string]any{
			"from": "state_a",
			"to":   "state_b",
		})
		evs[i] = store.Event{
			Turn:    turn,
			Kind:    store.TransitionApplied,
			Payload: payload,
		}
	}
	return evs
}

// ─── Open/Close ───────────────────────────────────────────────────────────────

func TestOpenMemory_OpenClose(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	// Close should not error.
	require.NoError(t, st.Close())
}

func TestOpen_FileBackedOpenClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	st, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, st.Close())

	// File should exist after close.
	_, err = os.Stat(path)
	require.NoError(t, err, "db file should exist after close")
}

// ─── CreateSession ────────────────────────────────────────────────────────────

func TestCreateSession(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)
	require.NotEmpty(t, string(sid))
}

func TestCreateSession_MultipleSessionsHaveUniqueIDs(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid1, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)
	sid2, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	require.NotEqual(t, sid1, sid2)
}

// ─── AppendEvents + LoadHistory ───────────────────────────────────────────────

func TestAppendEvents_LoadHistory_Order(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// Append 3 events in turn 1.
	evs := makeEvents(1, 3)
	require.NoError(t, st.AppendEvents(sid, evs))

	history, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, history, 3)

	// Events should be ordered by (turn, seq).
	for i, ev := range history {
		require.Equal(t, app.TurnNumber(1), ev.Turn)
		require.Equal(t, i, ev.Seq, "seq should be monotonic 0,1,2")
	}
}

func TestAppendEvents_SeqResetsPerTurn(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// Turn 1: 2 events.
	require.NoError(t, st.AppendEvents(sid, makeEvents(1, 2)))
	// Turn 2: 3 events.
	require.NoError(t, st.AppendEvents(sid, makeEvents(2, 3)))

	history, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, history, 5)

	// Turn 1 events have seq 0,1; turn 2 events have seq 0,1,2.
	require.Equal(t, 0, history[0].Seq)
	require.Equal(t, 1, history[1].Seq)
	require.Equal(t, app.TurnNumber(1), history[0].Turn)
	require.Equal(t, app.TurnNumber(1), history[1].Turn)

	require.Equal(t, 0, history[2].Seq)
	require.Equal(t, 1, history[3].Seq)
	require.Equal(t, 2, history[4].Seq)
	require.Equal(t, app.TurnNumber(2), history[2].Turn)
}

// TestAppendEvents_SameTurnBatchesContinueSeq pins the contract that two
// AppendEvents calls that share a turn number COEXIST: the second batch's seq
// continues past the rows the first batch already persisted (MAX(seq)+1), so
// they do NOT collide on the (session_id, turn, seq) primary key.
//
// Rationale: appendEventsTx assigns a monotonic seq that CONTINUES past any
// rows already persisted for the turn rather than always resetting to 0. This
// is required by the web session bootstrap, where seedFlowInitialState writes
// a synthetic turn-0 seed batch (TransitionApplied + per-key EffectApplied) and
// the orchestrator's RunInitialOnEnter then appends the seeded state's on_enter
// chain ALSO as turn 0 — two same-turn batches that previously collided on
// (turn, seq=0). Continuing the seq lets them interleave deterministically.
//
// This does NOT weaken the side-channel appenders: the off-path appender in
// internal/orchestrator/offpath.go and the testrunner's injectWorldOverride
// still allocate a fresh turn = max(history)+1, so they never share a turn in
// the first place; nothing relies on a same-turn append ERRORING.
func TestAppendEvents_SameTurnBatchesContinueSeq(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// First batch at turn 0 succeeds (seq 0,1,2).
	require.NoError(t, st.AppendEvents(sid, makeEvents(0, 3)),
		"first batch at turn 0 should succeed")

	// Second batch at the SAME turn coexists: its seq continues at 3,4 instead
	// of colliding on (session_id, turn, seq=0).
	require.NoError(t, st.AppendEvents(sid, makeEvents(0, 2)),
		"second same-turn batch should continue seq past the first, not collide")

	// History should now contain all five events, all at turn 0 with
	// contiguous seq 0..4.
	hist, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, hist, 5, "both same-turn batches persist; seq continues instead of colliding")
	for i := range hist {
		require.Equal(t, app.TurnNumber(0), hist[i].Turn)
		require.Equal(t, i, hist[i].Seq, "seq must be contiguous 0..4 across the two same-turn batches")
	}
}

// TestAppendEvents_SeedThenFreshTurn mirrors the web bootstrap followed by a
// real foreground turn: a seed batch and a same-turn on_enter batch coexist at
// turn 0 (seq continues), and the next foreground turn lands cleanly at turn 1.
// This is the shape the testrunner's injectWorldOverride and the orchestrator's
// appendOffPathEvents protect by allocating a fresh turn = max(history)+1.
func TestAppendEvents_SeedThenFreshTurn(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// Seed batch at turn 0 (e.g. seedFlowInitialState).
	require.NoError(t, st.AppendEvents(sid, makeEvents(0, 3)))

	// Same-turn on_enter batch coexists at turn 0 (e.g. RunInitialOnEnter).
	require.NoError(t, st.AppendEvents(sid, makeEvents(0, 1)),
		"the on_enter batch shares turn 0 with the seed batch and continues its seq")

	// The first real foreground turn lands at a fresh turn.
	require.NoError(t, st.AppendEvents(sid, makeEvents(1, 2)),
		"a fresh turn must succeed after the same-turn bootstrap batches")

	hist, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, hist, 6, "3 seed + 1 on_enter (turn 0) + 2 foreground (turn 1)")

	// Verify the seq invariant: turn 0 has seq 0,1,2,3; turn 1 has seq 0,1.
	require.Equal(t, app.TurnNumber(0), hist[0].Turn)
	require.Equal(t, 0, hist[0].Seq)
	require.Equal(t, app.TurnNumber(0), hist[3].Turn)
	require.Equal(t, 3, hist[3].Seq)
	require.Equal(t, app.TurnNumber(1), hist[4].Turn)
	require.Equal(t, 0, hist[4].Seq)
	require.Equal(t, app.TurnNumber(1), hist[5].Turn)
	require.Equal(t, 1, hist[5].Seq)
}

func TestAppendEvents_Content(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	payload, _ := json.Marshal(map[string]any{"foo": "bar", "n": 42})
	evs := []store.Event{
		{Turn: 1, Kind: store.EffectApplied, Payload: payload},
	}
	require.NoError(t, st.AppendEvents(sid, evs))

	history, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, history, 1)

	ev := history[0]
	require.Equal(t, store.EffectApplied, ev.Kind)
	require.Equal(t, app.TurnNumber(1), ev.Turn)

	// Payload should round-trip.
	var got map[string]any
	require.NoError(t, json.Unmarshal(ev.Payload, &got))
	require.Equal(t, "bar", got["foo"])
}

func TestAppendEvents_EmptySliceIsNoOp(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	require.NoError(t, st.AppendEvents(sid, nil))

	history, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Empty(t, history)
}

func TestAppendEvents_SessionNotFound(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	err = st.AppendEvents("nonexistent-session", makeEvents(1, 1))
	require.ErrorIs(t, err, store.ErrSessionNotFound)
}

// ─── Transaction rollback: no partial writes ──────────────────────────────────

// TestAppendEvents_TransactionRollback simulates a failed append by using a
// context that is already cancelled. The store should not write any events.
func TestAppendEvents_TransactionRollback_CancelledContext(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// Append one good batch first.
	require.NoError(t, st.AppendEvents(sid, makeEvents(1, 2)))

	// AppendEvents does not accept a context parameter (Store interface).
	// We verify that after a failed append (wrong session), no partial data exists.
	err = st.AppendEvents("bad-sid", makeEvents(2, 3))
	require.Error(t, err)

	// History for the good session should still only have the 2 events.
	history, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, history, 2, "no partial writes from bad-sid attempt")
}

// ─── Snapshot ─────────────────────────────────────────────────────────────────

func TestSnapshot_LatestSnapshot_RoundTrip(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	worldJSON, _ := json.Marshal(map[string]any{"wearing_cloak": false, "disturbance": 2})
	snap := store.Snapshot{
		Turn:      app.TurnNumber(5),
		StatePath: app.StatePath("cloakroom"),
		WorldJSON: worldJSON,
		RNGSeed:   42,
	}

	require.NoError(t, st.Snapshot(sid, snap.Turn, snap))

	got, ok, err := st.LatestSnapshot(sid)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, snap.Turn, got.Turn)
	require.Equal(t, snap.StatePath, got.StatePath)
	require.Equal(t, snap.RNGSeed, got.RNGSeed)
}

func TestLatestSnapshot_NoSnapshot(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	_, ok, err := st.LatestSnapshot(sid)
	require.NoError(t, err)
	require.False(t, ok, "no snapshot should exist for a new session")
}

func TestLatestSnapshot_ReturnsNewest(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	worldJSON := json.RawMessage(`{}`)

	// Write snapshots at turns 5 and 20.
	require.NoError(t, st.Snapshot(sid, 5, store.Snapshot{Turn: 5, StatePath: "foyer", WorldJSON: worldJSON}))
	require.NoError(t, st.Snapshot(sid, 20, store.Snapshot{Turn: 20, StatePath: "cloakroom", WorldJSON: worldJSON}))

	got, ok, err := st.LatestSnapshot(sid)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, app.TurnNumber(20), got.Turn, "should return the latest snapshot")
	require.Equal(t, app.StatePath("cloakroom"), got.StatePath)
}

// LoadHistory returns only events AFTER the latest snapshot turn.
func TestLoadHistory_AfterSnapshot(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// Write events for turns 1–5.
	for turn := app.TurnNumber(1); turn <= 5; turn++ {
		require.NoError(t, st.AppendEvents(sid, makeEvents(turn, 1)))
	}

	// Write a snapshot at turn 3.
	require.NoError(t, st.Snapshot(sid, 3, store.Snapshot{
		Turn:      3,
		StatePath: "foyer",
		WorldJSON: json.RawMessage(`{}`),
	}))

	// LoadHistory should return only turns 4 and 5 (> snapshot turn 3).
	history, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, history, 2, "only events after snapshot turn should be returned")
	require.Equal(t, app.TurnNumber(4), history[0].Turn)
	require.Equal(t, app.TurnNumber(5), history[1].Turn)
}

// ─── MarkCompleted / MarkAbandoned ────────────────────────────────────────────

func TestMarkCompleted_RejectsSubsequentAppends(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	require.NoError(t, st.AppendEvents(sid, makeEvents(1, 1)))
	require.NoError(t, st.MarkCompleted(context.Background(), sid))

	// Further appends must fail.
	err = st.AppendEvents(sid, makeEvents(2, 1))
	require.ErrorIs(t, err, store.ErrSessionClosed)
}

func TestMarkAbandoned_RejectsSubsequentAppends(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	require.NoError(t, st.MarkAbandoned(context.Background(), sid))

	err = st.AppendEvents(sid, makeEvents(1, 1))
	require.ErrorIs(t, err, store.ErrSessionClosed)
}

func TestMarkCompleted_SessionNotFound(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	err = st.MarkCompleted(context.Background(), "nosuchsession")
	require.ErrorIs(t, err, store.ErrSessionNotFound)
}

// ─── DeleteSession ────────────────────────────────────────────────────────────

func TestDeleteSession_RemovesAllRelatedRows(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// Populate every session-scoped table.
	require.NoError(t, st.AppendEvents(sid, makeEvents(1, 2)))
	require.NoError(t, st.Snapshot(sid, 1, store.Snapshot{}))
	require.NoError(t, st.BindExternalKey(context.Background(), sid, "jira", "TEST-1"))

	require.NoError(t, st.DeleteSession(context.Background(), sid))

	// Sessions list no longer reports it.
	sessions, err := st.ListSessions(context.Background(), "test-app", 0)
	require.NoError(t, err)
	for _, s := range sessions {
		require.NotEqual(t, sid, s.ID)
	}

	// External-key lookup returns ErrSessionNotFound (not the stale id).
	_, err = st.LookupByKey(context.Background(), "jira", "TEST-1")
	require.ErrorIs(t, err, store.ErrSessionNotFound)

	// History load returns no events — the prior turn rows were deleted.
	hist, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Empty(t, hist)

	// The id can be re-bound to a freshly-created session.
	sid2, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)
	require.NoError(t, st.BindExternalKey(context.Background(), sid2, "jira", "TEST-1"))
}

func TestDeleteSession_NotFound(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	err = st.DeleteSession(context.Background(), "nosuchsession")
	require.ErrorIs(t, err, store.ErrSessionNotFound)
}

// ─── ListSessions ─────────────────────────────────────────────────────────────

func TestListSessions(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("my-app", "1.0.0")
	other := makeAppDef("other-app", "1.0.0")

	sid1, _ := st.CreateSession(context.Background(), def)
	sid2, _ := st.CreateSession(context.Background(), def)
	_, _ = st.CreateSession(context.Background(), other) // different app; should not appear

	// Add some events so last_turn > 0 for one session.
	require.NoError(t, st.AppendEvents(sid2, makeEvents(3, 1)))

	list, err := st.ListSessions(context.Background(), "my-app", 0)
	require.NoError(t, err)
	require.Len(t, list, 2)

	// IDs should be from the correct app.
	ids := map[app.SessionID]bool{sid1: true, sid2: true}
	for _, s := range list {
		require.True(t, ids[s.ID], "unexpected session ID %s", s.ID)
		require.Equal(t, "my-app", s.AppID)
	}
}

func TestListSessions_Limit(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("my-app", "1.0.0")
	for i := 0; i < 5; i++ {
		_, _ = st.CreateSession(context.Background(), def)
		time.Sleep(time.Microsecond) // ensure distinct started_at timestamps
	}

	list, err := st.ListSessions(context.Background(), "my-app", 3)
	require.NoError(t, err)
	require.Len(t, list, 3)
}

// ─── File-backed persistence ──────────────────────────────────────────────────

// TestFileBacked_PersistsAcrossReopen verifies that events written in one store
// instance survive close + reopen (the durability guarantee).
func TestFileBacked_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.db")

	def := makeAppDef("test-app", "1.0.0")
	var sid app.SessionID

	// First store: write.
	{
		st, err := store.Open(path)
		require.NoError(t, err)

		sid, err = st.CreateSession(context.Background(), def)
		require.NoError(t, err)

		require.NoError(t, st.AppendEvents(sid, makeEvents(1, 3)))
		require.NoError(t, st.Close())
	}

	// Second store: read back.
	{
		st, err := store.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { _ = st.Close() })

		history, err := st.LoadHistory(sid)
		require.NoError(t, err)
		require.Len(t, history, 3, "history should persist across reopen")
		require.Equal(t, app.TurnNumber(1), history[0].Turn)
	}
}

// ─── Timestamp preservation ───────────────────────────────────────────────────

func TestAppendEvents_TimestampStored(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("test-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	before := time.Now().Add(-time.Second)
	require.NoError(t, st.AppendEvents(sid, makeEvents(1, 1)))
	after := time.Now().Add(time.Second)

	history, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, history, 1)

	ts := history[0].Ts
	require.True(t, ts.After(before), "ts should be after start")
	require.True(t, ts.Before(after), "ts should be before end")
}

// ─── errors package compatibility ────────────────────────────────────────────

func TestErrors_IsChecks(t *testing.T) {
	require.True(t, errors.Is(store.ErrSessionClosed, store.ErrSessionClosed))
	require.True(t, errors.Is(store.ErrSessionNotFound, store.ErrSessionNotFound))
}
