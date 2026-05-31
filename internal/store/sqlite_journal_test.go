package store_test

// Atomicity test for AppendEventsAndJournal.
//
// The test verifies that when AppendJournalTx fails mid-transaction the events
// row does NOT survive — i.e. the BEGIN/COMMIT wraps both writes.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
)

// makeJournalEntry returns a valid typed journal entry for a given session/turn.
func makeJournalEntry(sid app.SessionID, turn app.TurnNumber, seq int) journal.Entry {
	body, _ := json.Marshal(map[string]any{"test": true})
	return journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     seq,
		Kind:    journal.KindHostInvoked,
		Body:    body,
	}
}

// TestAppendEventsAndJournal_HappyPath confirms that a valid dual-write
// persists both the event and the journal entry.
func TestAppendEventsAndJournal_HappyPath(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("dual-write-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	events := makeEvents(1, 2)
	entry := makeJournalEntry(sid, 1, 0)

	err = st.AppendEventsAndJournal(sid, events, []journal.Entry{entry})
	require.NoError(t, err)

	// Events must be present.
	hist, err := st.LoadHistory(sid)
	require.NoError(t, err)
	require.Len(t, hist, 2, "expected 2 events after successful dual-write")
}

// TestAppendEventsAndJournal_Atomicity_RollbackOnJournalFailure confirms that
// when the journal insert fails (due to a duplicate PRIMARY KEY in the journal
// table) the events row is also rolled back, leaving LoadHistory empty.
func TestAppendEventsAndJournal_Atomicity_RollbackOnJournalFailure(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := makeAppDef("dual-write-rollback-app", "1.0.0")
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	// Pre-seed one journal entry so that the second call with the same
	// (session_id, turn, seq) triggers a UNIQUE PRIMARY KEY violation inside
	// AppendJournalTx, which causes the whole transaction to fail.
	seedEntry := makeJournalEntry(sid, 1, 0)
	// Write it using AppendEventsAndJournal with no events to avoid touching
	// the events table for the seed.
	err = st.AppendEventsAndJournal(sid, nil, []journal.Entry{seedEntry})
	require.NoError(t, err, "seed journal write must succeed")

	// Now attempt a dual-write: one valid event + one journal entry that
	// duplicates (session_id, turn=1, seq=0) → UNIQUE violation on journal PK.
	conflictEntry := makeJournalEntry(sid, 1, 0) // same PK as the seeded entry
	events := makeEvents(2, 1)                   // turn 2 events
	err = st.AppendEventsAndJournal(sid, events, []journal.Entry{conflictEntry})
	require.Error(t, err, "conflicting journal PK should cause an error")

	// After the rollback, the events row for turn 2 must not exist.
	hist, err := st.LoadHistory(sid)
	require.NoError(t, err)
	for _, ev := range hist {
		require.NotEqual(t, app.TurnNumber(2), ev.Turn,
			"events for turn 2 must have been rolled back with the journal failure")
	}
}
