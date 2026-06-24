package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_MachineTurnErrorIsTraced proves that when machine.Turn
// itself fails mid-turn — here an effect's `set:` expression compiles but
// eval-fails (`string + int`) — the orchestrator still records the failure
// in the persisted session trace instead of leaving a gap.
//
// Before the fix every machine.Turn error site did
//
//	return nil, fmt.Errorf("orchestrator: ...: machine.Turn: %w", err)
//
// with NO event written, so the session JSONL kept only the last good turn
// and a TUI bounce-to-idle was impossible to diagnose from the trace. The
// fix journals a TurnStarted → MachineError → TurnEnded(outcome:"error")
// sequence at each site.
//
// The assertion reads the persisted history (the on-disk trace), so it FAILS
// on the unfixed code (no MachineError event) and PASSES once the orchestrator
// journals the failed turn.
func TestOrchestrator_MachineTurnErrorIsTraced(t *testing.T) {
	def, err := app.Load("testdata/turnerror/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarness{})

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// The turn must fail (the effect expression eval-errors).
	_, turnErr := orch.SubmitDirect(ctx, sid, "boom", map[string]any{})
	require.Error(t, turnErr, "machine.Turn should error on the eval-failing effect")

	// The failure must be recorded in the persisted event log.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)

	var sawMachineError, sawErrorOutcome bool
	for _, ev := range history {
		switch ev.Kind {
		case store.MachineError:
			sawMachineError = true
			require.Contains(t, string(ev.Payload), "boom",
				"MachineError payload should record the intent")
			require.Contains(t, string(ev.Payload), "invalid operation",
				"MachineError payload should record the underlying eval error")
		case store.TurnEnded:
			require.Contains(t, string(ev.Payload), `"outcome":"error"`,
				"the failed turn's TurnEnded must carry outcome:error")
			sawErrorOutcome = true
		}
	}
	require.True(t, sawMachineError,
		"a store.MachineError event must be written when machine.Turn fails — "+
			"otherwise the failed turn leaves no row in the session trace")
	require.True(t, sawErrorOutcome,
		"the failed turn must close with a TurnEnded outcome:error so trace "+
			"consumers see the turn boundary")

	// Replay must survive the new event kind as a no-op (no state/world change).
	js, err := store.BuildJourney(def, orch.InitialState(), orch.InitialWorld(), history)
	require.NoError(t, err)
	require.Equal(t, "hello", js.World.Vars["scalar"],
		"a turn that aborted in machine.Turn must not mutate replayed world state")
}
