package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hally/internal/app"
	"hally/internal/harness"
	"hally/internal/host"
	"hally/internal/machine"
	"hally/internal/orchestrator"
	"hally/internal/store"
)

// TestInspect_PopulatedSession drives a few real turns of the cloak-of-darkness
// app via the orchestrator, then asserts that buildInspectOutput returns a
// snapshot that matches what just happened.
//
// This is the only integration test we need for `hally inspect`: the surface
// is read-only and the failure modes (missing session, render error) are tiny
// enough to cover with the smoke test below.
func TestInspect_PopulatedSession(t *testing.T) {
	appYAML := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")

	def, err := app.Load(appYAML)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay(filepath.Join("..", "..", "testdata", "apps", "cloak", "oracle.yaml"))
	require.NoError(t, err)

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(hostReg),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	for _, in := range []string{"go west", "hang the cloak", "go east"} {
		_, err := orch.Turn(ctx, sid, in)
		require.NoError(t, err, "turn %q", in)
	}

	out, err := buildInspectOutput(ctx, def, s, string(sid), 5)
	require.NoError(t, err)

	assert.Equal(t, string(sid), out.SessionID)
	assert.Equal(t, def.App.ID, out.AppID)
	assert.Equal(t, "active", out.Status)
	assert.EqualValues(t, 3, out.LastTurn)
	assert.Equal(t, "foyer", out.CurrentState, "ended back in foyer after go east")
	assert.NotEmpty(t, out.LastView, "current state should render to a non-empty view")
	assert.Equal(t, len(out.LastView), out.LastViewBytes)
	assert.NotEmpty(t, out.AllowedIntents, "foyer must have at least one allowed intent")

	// last_turns reflects the three turns we drove. The replay-harness path
	// goes through Turn (LLM-routed), so each turn carries an Input.
	require.Len(t, out.LastTurns, 3)
	assert.Equal(t, "go west", out.LastTurns[0].Input)
	assert.Equal(t, "hang the cloak", out.LastTurns[1].Input)
	assert.Equal(t, "go east", out.LastTurns[2].Input)
	for i, ts := range out.LastTurns {
		assert.NotEmpty(t, ts.ToState, "turn %d should record a destination state", i+1)
		assert.Equal(t, "transitioned", ts.Outcome, "turn %d outcome", i+1)
	}

	// World should track wearing_cloak transitions (set false by hang_cloak).
	if v, ok := out.World["wearing_cloak"].(bool); ok {
		assert.False(t, v, "wearing_cloak should be false after hang_cloak")
	}
}

// TestInspect_MissingSession returns a clear error rather than crashing on
// an unknown session ID.
func TestInspect_MissingSession(t *testing.T) {
	def, err := app.Load(filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml"))
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	_, err = buildInspectOutput(context.Background(), def, s, "no-such-session", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestInspect_LastTurnsTail ensures --last-turns N truncates to the most
// recent N turns rather than dropping arbitrary entries.
func TestInspect_LastTurnsTail(t *testing.T) {
	hist := store.History{
		{Turn: 1, Kind: store.TurnStarted, Payload: []byte(`{"input":"a"}`)},
		{Turn: 1, Kind: store.TurnEnded, Payload: []byte(`{"outcome":"transitioned"}`)},
		{Turn: 2, Kind: store.TurnStarted, Payload: []byte(`{"input":"b"}`)},
		{Turn: 2, Kind: store.TurnEnded, Payload: []byte(`{"outcome":"transitioned"}`)},
		{Turn: 3, Kind: store.TurnStarted, Payload: []byte(`{"input":"c"}`)},
		{Turn: 3, Kind: store.TurnEnded, Payload: []byte(`{"outcome":"transitioned"}`)},
	}
	got := summariseTurns(hist, 2)
	require.Len(t, got, 2)
	assert.Equal(t, int64(2), got[0].Turn)
	assert.Equal(t, "b", got[0].Input)
	assert.Equal(t, int64(3), got[1].Turn)
	assert.Equal(t, "c", got[1].Input)
}
