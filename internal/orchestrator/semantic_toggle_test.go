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

// toggleAppYAML is a tiny app with one synonym-bearing intent and one plain
// intent. "wade" is a bare synonym for go_west (only the semroute stack
// resolves it); "go west" is the canonical example (the zero-cost exact match
// resolves it even with the stack off).
const toggleAppYAML = `
app:
  id: semroute-toggle-test
  version: 0.1.0
world: {}
intents:
  go_west:
    title: "Go west"
    examples: ["go west"]
    synonyms: ["wade"]
  go_south:
    title: "Go south"
    examples: ["go south"]
root: start
states:
  start:
    view: "start"
    on:
      go_west:
        - target: ended
      go_south:
        - target: ended
  ended:
    terminal: true
    view: "done"
`

func newToggleOrch(t *testing.T, opts ...orchestrator.Option) (*orchestrator.Orchestrator, *countingHarness) {
	t.Helper()
	def, err := app.LoadBytes([]byte(toggleAppYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h := &countingHarness{fall: staticHarness{intentName: "go_west"}}
	orch := orchestrator.New(def, m, s, h, opts...)
	return orch, h
}

// TestSemanticRouting_DisabledRoutesSynonymViaLLM verifies that with the
// semantic-routing stack turned off (WithSemanticRouting(false), the LLM-only
// default), a bare synonym that ONLY the semroute tier would match falls through
// to the harness (the isolated main-model routing decision) instead of being
// resolved deterministically.
func TestSemanticRouting_DisabledRoutesSynonymViaLLM(t *testing.T) {
	orch, h := newToggleOrch(t, orchestrator.WithSemanticRouting(false))
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "wade")
	require.NoError(t, err)
	require.EqualValues(t, 1, h.calls.Load(),
		"with the semantic stack off, a synonym-only utterance must reach the harness")
}

// TestSemanticRouting_DisabledKeepsExactMatch verifies the zero-cost exact match
// still resolves canonical example/display text without an LLM hop, even with
// the stack off.
func TestSemanticRouting_DisabledKeepsExactMatch(t *testing.T) {
	orch, h := newToggleOrch(t, orchestrator.WithSemanticRouting(false))
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)
	require.EqualValues(t, 0, h.calls.Load(),
		"an exact example match must resolve deterministically without the harness")
}

// TestSemanticRouting_EnabledResolvesSynonymDeterministically is the control:
// with the stack on, the same synonym resolves via semroute and never reaches
// the harness.
func TestSemanticRouting_EnabledResolvesSynonymDeterministically(t *testing.T) {
	orch, h := newToggleOrch(t, orchestrator.WithSemanticRouting(true))
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "wade")
	require.NoError(t, err)
	require.EqualValues(t, 0, h.calls.Load(),
		"with the semantic stack on, a declared synonym must resolve without the harness")
}
