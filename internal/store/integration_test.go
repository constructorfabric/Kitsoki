// Package store_test — integration test: Machine + Store round-trip.
//
// This test drives the Cloak of Darkness winning path via machine.Step,
// persists every turn's events via Store.AppendEvents, then closes the store,
// reopens it, replays via BuildJourney, and asserts the reconstructed journey
// matches what the machine produced live.
//
// This is the headline proof that "event-sourced replay reproduces journey
// state deterministically" (Determinism Contract).
package store_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// winningPath enumerates the intent calls for the Cloak winning path, matching
// testdata/apps/cloak/flows/winning.yaml. Turn 3 uses a hard-coded go east
// (same as cloak_test.go does for the oracle-backed turn).
var winningPath = []intent.IntentCall{
	{Intent: "go", Slots: world.Slots{"direction": "west"}},  // foyer → cloakroom
	{Intent: "hang_cloak", Slots: world.Slots{}},             // cloakroom, sets wearing_cloak=false
	{Intent: "go", Slots: world.Slots{"direction": "east"}},  // cloakroom → foyer
	{Intent: "go", Slots: world.Slots{"direction": "south"}}, // foyer → bar.lit (no cloak)
	{Intent: "read_message", Slots: world.Slots{}},           // bar.lit → ended
}

// loadCloakForIntegration loads the Cloak app from the testdata fixtures.
func loadCloakForIntegration(t *testing.T) (*app.AppDef, machine.Machine) {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err, "cloak app must load")
	m, err := machine.New(def)
	require.NoError(t, err, "machine.New must succeed")
	return def, m
}

// normaliseWorld converts all numeric world values to int64 for stable comparison.
func normaliseWorldVars(vars map[string]any) map[string]any {
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		switch x := v.(type) {
		case float64:
			out[k] = int64(x)
		case json.Number:
			n, _ := x.Int64()
			out[k] = n
		default:
			out[k] = v
		}
	}
	return out
}

// TestIntegration_MachineAndStore_WinningPath is the headline integration test.
// It:
//  1. Drives the Cloak winning path via Machine.Turn.
//  2. Appends each turn's events to the Store.
//  3. Marks the session completed.
//  4. Closes and reopens the store (file-backed).
//  5. Loads history via LoadHistory.
//  6. Reconstructs the journey via BuildJourney.
//  7. Asserts the reconstructed final state and world match the live machine output.
func TestIntegration_MachineAndStore_WinningPath(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cloak-integration.db")

	def, m := loadCloakForIntegration(t)

	// Initialise world from schema defaults.
	w := machine.WorldFromSchema(app.WorldSchema(def.World))
	cur := app.StatePath(def.Root.(string))

	// ── Phase 1: live machine run with persistence ────────────────────────────

	st, err := store.Open(dbPath)
	require.NoError(t, err)

	sid, err := st.CreateSession(context.Background(), def)
	require.NoError(t, err)

	var (
		liveFinalState app.StatePath
		liveFinalWorld world.World
	)

	for turnIdx, call := range winningPath {
		turnNum := app.TurnNumber(turnIdx + 1)

		res, err := m.Turn(context.Background(), cur, w, call)
		require.NoError(t, err, "turn %d should not error", turnNum)
		require.Nil(t, res.ValidationError, "turn %d should not have validation error", turnNum)

		// Stamp turn number on each event before persisting.
		evs := stampTurn(res.Events, turnNum)

		require.NoError(t, st.AppendEvents(sid, evs),
			"turn %d: AppendEvents should succeed", turnNum)

		cur = res.NewState
		w = res.World
	}

	liveFinalState = cur
	liveFinalWorld = w

	// Verify the live machine reached the expected final state.
	require.Equal(t, app.StatePath("ended"), liveFinalState)
	require.Equal(t, false, liveFinalWorld.Vars["wearing_cloak"])
	require.Equal(t, false, liveFinalWorld.Vars["message_rumpled"])

	// Mark session completed.
	require.NoError(t, st.MarkCompleted(context.Background(), sid))

	// Verify that no further appends are accepted after completion.
	appendErr := st.AppendEvents(sid, []store.Event{
		{Turn: 99, Kind: store.TurnStarted, Payload: json.RawMessage("{}")},
	})
	require.ErrorIs(t, appendErr, store.ErrSessionClosed,
		"AppendEvents must fail on a completed session")

	// Close the store.
	require.NoError(t, st.Close())

	// ── Phase 2: close, reopen, replay ───────────────────────────────────────

	st2, err := store.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st2.Close() })

	history, err := st2.LoadHistory(sid)
	require.NoError(t, err)
	require.NotEmpty(t, history, "history must be non-empty after reopen")

	// Reconstruct initial state for replay.
	initialWorld := machine.WorldFromSchema(app.WorldSchema(def.World))
	initialState := app.StatePath(def.Root.(string))

	js, err := store.BuildJourney(def, initialState, initialWorld, history)
	require.NoError(t, err)
	require.NotNil(t, js)

	// ── Phase 3: assert replay == live ───────────────────────────────────────

	require.Equal(t, liveFinalState, js.State,
		"replayed state must match live final state")

	// Normalise world values for comparison (JSON replay may produce float64 for int).
	liveVars := normaliseWorldVars(liveFinalWorld.Vars)
	replayVars := normaliseWorldVars(js.World.Vars)

	for k, liveVal := range liveVars {
		replayVal, ok := replayVars[k]
		require.True(t, ok, "replayed world missing key %q", k)
		require.Equal(t, liveVal, replayVal,
			"world[%q]: live=%v replay=%v", k, liveVal, replayVal)
	}
}

// TestIntegration_RerunProducesConsistentResults verifies that running the
// integration test twice in the same temp dir (different sessions) produces
// consistent results (idempotency of schema creation and session isolation).
func TestIntegration_RerunProducesConsistentResults(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "multi-session.db")

	def, m := loadCloakForIntegration(t)

	// Helper: run one complete winning-path session against the given store.
	runSession := func(st store.Store) (app.SessionID, app.StatePath, world.World) {
		w := machine.WorldFromSchema(app.WorldSchema(def.World))
		cur := app.StatePath(def.Root.(string))

		sid, err := st.CreateSession(context.Background(), def)
		require.NoError(t, err)

		for turnIdx, call := range winningPath {
			turnNum := app.TurnNumber(turnIdx + 1)
			res, err := m.Turn(context.Background(), cur, w, call)
			require.NoError(t, err)
			require.Nil(t, res.ValidationError)

			evs := stampTurn(res.Events, turnNum)
			require.NoError(t, st.AppendEvents(sid, evs))

			cur = res.NewState
			w = res.World
		}
		require.NoError(t, st.MarkCompleted(context.Background(), sid))
		return sid, cur, w
	}

	// First run.
	st1, err := store.Open(dbPath)
	require.NoError(t, err)
	sid1, state1, world1 := runSession(st1)
	require.NoError(t, st1.Close())

	// Second run (same file, new session).
	st2, err := store.Open(dbPath)
	require.NoError(t, err)
	sid2, state2, world2 := runSession(st2)
	require.NoError(t, st2.Close())

	require.NotEqual(t, sid1, sid2, "two runs must produce different session IDs")
	require.Equal(t, state1, state2, "both runs should end in the same state")

	// World values must match.
	liveVars1 := normaliseWorldVars(world1.Vars)
	liveVars2 := normaliseWorldVars(world2.Vars)
	for k := range liveVars1 {
		require.Equal(t, liveVars1[k], liveVars2[k], "world[%q] should match across runs", k)
	}

	// Reopen and verify both histories are stored and replays agree.
	st3, err := store.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st3.Close() })

	initialWorld := machine.WorldFromSchema(app.WorldSchema(def.World))
	initialState := app.StatePath(def.Root.(string))

	for _, sid := range []app.SessionID{sid1, sid2} {
		history, err := st3.LoadHistory(sid)
		require.NoError(t, err)
		require.NotEmpty(t, history)

		js, err := store.BuildJourney(def, initialState, initialWorld, history)
		require.NoError(t, err)
		require.Equal(t, app.StatePath("ended"), js.State,
			"session %s: replayed state should be 'ended'", sid)
	}
}

// stampTurn sets the Turn field on each event (the machine emits events with
// Turn=0; the caller assigns the turn number before persisting).
func stampTurn(evs []store.Event, turn app.TurnNumber) []store.Event {
	out := make([]store.Event, len(evs))
	copy(out, evs)
	for i := range out {
		out[i].Turn = turn
	}
	return out
}
