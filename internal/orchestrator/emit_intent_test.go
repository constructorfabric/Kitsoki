package orchestrator_test

// End-to-end orchestrator test for the emit_intent: effect.
//
// The runtime mechanism is covered by internal/machine/emit_intent_test.go;
// here we drive the orchestrator with SubmitDirect so the synthetic
// transitions land through the full session/replay path (events
// persisted, state advanced, world updated).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_EmitIntent_AutoAdvance — a two-state machine where
// the destination's on_enter emits the next intent. One SubmitDirect
// call lands the session at the final state.
func TestOrchestrator_EmitIntent_AutoAdvance(t *testing.T) {
	const yamlSrc = `
app:
  id: emit-orch
  version: 0.1.0
world:
  arrived: { type: bool, default: false }
intents:
  enter:  {}
  accept: {}
root: start
states:
  start:
    on:
      enter:
        - target: checkpoint
  checkpoint:
    on_enter:
      - emit_intent: accept
    on:
      accept:
        - target: done
          effects:
            - set: { arrived: true }
  done:
    terminal: true
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// No harness needed — we're using SubmitDirect.
	h, _ := harness.NewReplay("")
	orch := orchestrator.New(def, m, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "enter", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("done"), out.NewState,
		"emit_intent: in checkpoint.on_enter should auto-advance the session to done in one Turn")

	// Load the journey to verify world state landed.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("done"), journey.State)
	require.Equal(t, true, journey.World.Vars["arrived"],
		"auto-fired accept's transition effects must have set arrived=true")

	// Both TransitionApplied events (enter, accept) must be in the session log.
	var transitions int
	for _, ev := range out.Events {
		if ev.Kind == store.TransitionApplied {
			transitions++
		}
	}
	require.GreaterOrEqual(t, transitions, 2,
		"expected at least the user 'enter' and the synthetic 'accept' transitions")
}
