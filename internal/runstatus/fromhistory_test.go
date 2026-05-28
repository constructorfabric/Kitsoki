package runstatus_test

// TestFromHistory_WithOracleJournal verifies that WithOracleJournal causes
// FromHistory to synthesise oracle.<verb>.start and oracle.<verb>.complete
// TraceEvents from KindOracleCall journal entries and merge them in timestamp
// order with the store-derived events.
//
// Runtime budget: <20 ms (in-memory SQLite, no real LLM calls).

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
)

// buildMinimalAppDef returns the simplest AppDef that FromHistory accepts
// (Compile + FlowchartWithMap must not error).
func buildMinimalAppDef() *app.AppDef {
	return &app.AppDef{
		App: app.AppMeta{
			ID:      "test-app",
			Version: "0.0.1",
		},
	}
}

func TestFromHistory_WithOracleJournal(t *testing.T) {
	t.Parallel()

	// ── Build an in-memory store with one session ──────────────────────────
	st, err := store.OpenMemory()
	require.NoError(t, err, "open memory store")
	t.Cleanup(func() { _ = st.Close() })

	def := buildMinimalAppDef()

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, def)
	require.NoError(t, err, "create session")

	// Append a minimal store history: TurnStarted + StateEntered("start") + TurnEnded.
	// Ts fields are ignored by AppendEvents (the store assigns wall-clock time at insert).
	events := []store.Event{
		{
			Ts:   time.Now(),
			Kind: store.TurnStarted,
			Turn: 1,
		},
		{
			Ts:      time.Now(),
			Kind:    store.StateEntered,
			Turn:    1,
			Payload: json.RawMessage(`{"state":"start"}`),
		},
		{
			Ts:   time.Now(),
			Kind: store.TurnEnded,
			Turn: 1,
		},
	}
	require.NoError(t, st.AppendEvents(sid, events), "append store events")

	// ── Load history to get actual stored timestamps ──────────────────────
	// The store assigns wall-clock timestamps at insert time; the Ts field
	// on the Event structs passed to AppendEvents is ignored. We read back
	// the stored events first so we can write the journal entry at a
	// consistent relative time.
	hist, err := st.LoadHistory(sid)
	require.NoError(t, err, "load history (first load)")
	require.Len(t, hist, 3, "expect 3 stored events")

	// lastEventTs is the timestamp of the last store event.
	lastEventTs := hist[len(hist)-1].Ts

	// ── Write a KindOracleCall journal entry ──────────────────────────────
	jw, err := journal.NewSQLiteWriter(st.DB())
	require.NoError(t, err, "create journal writer")

	// The oracle call completes 200ms after the last store event and lasted 150ms.
	// oracle.start is placed 1µs before oracle.complete in the merged stream so
	// it sorts just before it regardless of the original call duration.
	callCompletedAt := lastEventTs.Add(200 * time.Millisecond)
	callDurationMs := int64(150)
	expectedStartTs := callCompletedAt.Add(-time.Microsecond)

	oracleBody := map[string]any{
		"call_id":       "test-call-001",
		"verb":          "decide",
		"agent":         "my-agent",
		"model":         "claude-sonnet-4",
		"duration_ms":   callDurationMs,
		"prompt":        "Should I go left or right?",
		"system_prompt": "You are a decision assistant.",
		"response":      map[string]any{"decision": "left"},
	}
	bodyJSON, err := json.Marshal(oracleBody)
	require.NoError(t, err, "marshal oracle body")

	require.NoError(t, jw.Append(journal.Entry{
		Ts:      callCompletedAt,
		Session: sid,
		Turn:    1,
		Seq:     10,
		Kind:    journal.KindOracleCall,
		Body:    bodyJSON,
	}), "append journal entry")

	snapNoJournal, err := runstatus.FromHistory(hist, def, string(sid))
	require.NoError(t, err, "FromHistory without journal")

	// Without journal option: no oracle events.
	oracleCountNoJournal := 0
	for _, ev := range snapNoJournal.Events {
		if len(ev.Msg) > 7 && ev.Msg[:7] == "oracle." {
			oracleCountNoJournal++
		}
	}
	assert.Equal(t, 0, oracleCountNoJournal, "no oracle events without journal option")

	// ── Call FromHistory with WithOracleJournal ───────────────────────────
	snapWithJournal, err := runstatus.FromHistory(hist, def, string(sid),
		runstatus.WithOracleJournal(st.DB()))
	require.NoError(t, err, "FromHistory with journal")

	// Expect 3 store events + 2 oracle events = 5 total.
	assert.Equal(t, 5, len(snapWithJournal.Events),
		"expect 3 store + 2 oracle events; got %d", len(snapWithJournal.Events))

	// Find the start and complete oracle events.
	var startEv, completeEv *runstatus.TraceEvent
	for i := range snapWithJournal.Events {
		ev := &snapWithJournal.Events[i]
		if ev.Msg == "oracle.decide.start" {
			startEv = ev
		}
		if ev.Msg == "oracle.decide.complete" {
			completeEv = ev
		}
	}
	require.NotNil(t, startEv, "oracle.decide.start must be present")
	require.NotNil(t, completeEv, "oracle.decide.complete must be present")

	// ── Verify oracle.decide.start ────────────────────────────────────────
	assert.True(t, expectedStartTs.Equal(startEv.Time),
		"start timestamp: want %v got %v", expectedStartTs, startEv.Time)
	assert.Equal(t, "INFO", startEv.Level)
	assert.Equal(t, 1, startEv.Turn)
	assert.NotNil(t, startEv.Attrs)
	assert.Equal(t, "decide", startEv.Attrs["verb"])
	assert.Equal(t, "my-agent", startEv.Attrs["agent"])
	assert.Equal(t, "claude-sonnet-4", startEv.Attrs["model"])
	assert.Equal(t, "test-call-001", startEv.Attrs["call_id"])
	assert.Contains(t, startEv.Attrs["prompt_preview"], "left or right")

	// ── Verify oracle.decide.complete ─────────────────────────────────────
	assert.True(t, callCompletedAt.Equal(completeEv.Time),
		"complete timestamp: want %v got %v", callCompletedAt, completeEv.Time)
	assert.Equal(t, "INFO", completeEv.Level)
	assert.Equal(t, 1, completeEv.Turn)
	require.NotNil(t, completeEv.Attrs)
	assert.Equal(t, "decide", completeEv.Attrs["verb"])
	assert.Equal(t, "test-call-001", completeEv.Attrs["call_id"])
	assert.Equal(t, "You are a decision assistant.", completeEv.Attrs["system_prompt"])
	assert.Equal(t, "Should I go left or right?", completeEv.Attrs["prompt"])

	// ── Verify ordering: all events are sorted by time ────────────────────
	for i := 1; i < len(snapWithJournal.Events); i++ {
		prev := snapWithJournal.Events[i-1]
		curr := snapWithJournal.Events[i]
		assert.False(t, curr.Time.Before(prev.Time),
			"events must be sorted by time: events[%d]=%v > events[%d]=%v",
			i-1, prev.Time, i, curr.Time)
	}

	// ── Verify state_path propagation ─────────────────────────────────────
	// The oracle start event is after the StateEntered("start") store event,
	// so nearestStatePath should propagate "start" to both synthesised events.
	assert.Equal(t, "start", startEv.StatePath,
		"start event state_path should inherit nearest-preceding store state")
	assert.Equal(t, "start", completeEv.StatePath,
		"complete event state_path should inherit nearest-preceding store state")
}

// TestFromHistory_WithOracleJournal_NilDB confirms that passing a nil DB via
// WithOracleJournal is a safe no-op.
func TestFromHistory_WithOracleJournal_NilDB(t *testing.T) {
	t.Parallel()

	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	def := buildMinimalAppDef()
	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	require.NoError(t, st.AppendEvents(sid, []store.Event{
		{Kind: store.TurnStarted, Turn: 1},
	}))

	hist, err := st.LoadHistory(sid)
	require.NoError(t, err)

	// Must not panic or error with a nil DB.
	snap, err := runstatus.FromHistory(hist, def, string(sid), runstatus.WithOracleJournal(nil))
	require.NoError(t, err, "nil DB must be a safe no-op")
	assert.Equal(t, 1, len(snap.Events), "store events only")
}
