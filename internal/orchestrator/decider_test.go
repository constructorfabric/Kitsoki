package orchestrator_test

// Tests for the engine-driven LLM decider.
// A one-shot run rests at `choose` — a decision gate with two operator-only
// forward intents and NO firing default emit. The engine invokes the
// configured judge (stubbed here as host.oracle.decide) and either fires the
// chosen intent or bails to human.

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

const deciderYAML = `
app:
  id: decider-gate
  version: 0.1.0
hosts:
  - host.oracle.decide
intents:
  start: {}
  path_a:
    description: "Take path A."
  path_b:
    description: "Take path B."
root: ready
states:
  ready:
    on:
      start:
        - target: choose
  choose:
    description: "Pick a path."
    on:
      path_a:
        - target: done_a
      path_b:
        - target: done_b
  done_a:
    terminal: true
  done_b:
    terminal: true
`

func newDeciderOrchestrator(t *testing.T, verdict map[string]any, opts ...orchestrator.Option) *orchestrator.Orchestrator {
	t.Helper()
	def, err := app.LoadBytes([]byte(deciderYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	// Stub the judge: return whatever verdict the test wants as `submitted`.
	reg.Register("host.oracle.decide", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"submitted": verdict}}, nil
	})

	allOpts := append([]orchestrator.Option{
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithExecutionMode(orchestrator.ExecOneShot),
	}, opts...)
	return orchestrator.New(def, m, s, noopOrchestratorHarness{}, allOpts...)
}

// TestEngineDecider_FiresChosenIntent: a confident verdict naming a valid
// candidate is auto-fired, advancing past the gate.
func TestEngineDecider_FiresChosenIntent(t *testing.T) {
	orch := newDeciderOrchestrator(t,
		map[string]any{"intent": "path_b", "confidence": 0.95, "reason": "b is better"},
		orchestrator.WithDecider(orchestrator.DeciderConfig{Agent: "judge", Schema: "schema.json", Threshold: 0.8}),
	)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("done_b"), out.NewState,
		"the engine decider must fire the chosen intent and advance past the gate")

	var gate map[string]bool = map[string]bool{}
	for _, ev := range out.Events {
		if ev.Kind == store.GateDecided {
			gate["seen"] = true
			require.Contains(t, string(ev.Payload), `"decider":"llm"`)
			require.Contains(t, string(ev.Payload), `"chosen_intent":"path_b"`)
			require.Contains(t, string(ev.Payload), `"bailed_to_human":false`)
		}
	}
	require.True(t, gate["seen"], "a GateDecided event must record the llm decision")
}

// TestEngineDecider_LowConfidenceBailsToHuman: an uncertain / low-confidence
// verdict leaves the turn rested at the gate for a human.
func TestEngineDecider_LowConfidenceBailsToHuman(t *testing.T) {
	orch := newDeciderOrchestrator(t,
		map[string]any{"intent": "path_a", "confidence": 0.3, "reason": "unsure"},
		orchestrator.WithDecider(orchestrator.DeciderConfig{Agent: "judge", Schema: "schema.json", Threshold: 0.8}),
	)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("choose"), out.NewState,
		"a low-confidence verdict must bail to human (rest at the gate)")

	var bailed bool
	for _, ev := range out.Events {
		if ev.Kind == store.GateDecided {
			bailed = true
			require.Contains(t, string(ev.Payload), `"bailed_to_human":true`)
		}
	}
	require.True(t, bailed, "the bail must be recorded as a GateDecided event")
}

// TestEngineDecider_NotConfigured_RestsAtGate: without a decider, a one-shot
// gate with no firing default simply rests (historical behaviour).
func TestEngineDecider_NotConfigured_RestsAtGate(t *testing.T) {
	orch := newDeciderOrchestrator(t,
		map[string]any{"intent": "path_a", "confidence": 0.95},
		// no WithDecider
	)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("choose"), out.NewState,
		"with no decider configured the gate just rests")
	for _, ev := range out.Events {
		require.NotEqual(t, store.GateDecided, ev.Kind)
	}
}
