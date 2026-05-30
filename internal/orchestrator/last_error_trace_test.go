package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_LastErrorIsReplayable proves that a host call failure which
// stamps world["last_error"] also emits a store.EffectApplied event, so a
// pure replay of the persisted event log reconstructs last_error.
//
// Before the fix the orchestrator set w.Vars["last_error"] directly without
// emitting EffectApplied. The in-memory turn output still carried last_error
// (set in the live world), but a fresh replay from the event log — the path
// taken on process restart / snapshot-less resume — would silently drop it.
//
// This test reconstructs the journey via store.BuildJourney over the persisted
// history (not the live world), which is exactly the replay path. It FAILS on
// the unfixed code (last_error absent from the replayed world) and PASSES once
// the EffectApplied event is emitted.
func TestOrchestrator_LastErrorIsReplayable(t *testing.T) {
	def, err := app.Load("testdata/hosterror/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Error: "deliberate failure"}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	// Replay the persisted event log from scratch (no snapshot, no live world)
	// — the path a process restart / resume would take.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)

	js, err := store.BuildJourney(def, orch.InitialState(), orch.InitialWorld(), history)
	require.NoError(t, err)

	require.Equal(t, "deliberate failure", js.World.Vars["last_error"],
		"last_error must be reconstructable from the event log; an EffectApplied "+
			"event must accompany the world mutation so replay rebuilds the var")
}
