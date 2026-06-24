// Unit tests for Orchestrator.Classify — the side-effect-free "classify only"
// seam that runs the no-LLM routing tiers (deterministic display/example,
// semantic synonym/template, optional embedding) against an explicit
// (state, world) and returns a semroute.Verdict WITHOUT executing any effect,
// writing any event, or calling any LLM.
//
// These are the moat's unit guarantees:
//   - the right tier resolves the right input at the right confidence band,
//   - a genuinely-unknown input is a clean no-match (Confidence == 0),
//   - a synonym hit on an intent with a required unfilled slot still returns a
//     verdict (the gate detects the unfilled slot — Classify does NOT abdicate
//     or execute), and
//   - Classify is pure: the passed world map is byte-identical afterward and no
//     session/events are created (Classify takes no sid).
package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/semroute"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// classifyTestApp is a routing-enabled app whose `start` room allows three
// intents:
//
//   - go_north: a display/example/synonym-routable move with no slots.
//   - go_east:  a synonym-routable move with no slots.
//   - shout:    declares a REQUIRED `message` slot and a bare synonym ("yell"),
//     so a synonym hit cannot fill the slot — the required-unfilled-slot case.
const classifyTestApp = `
app:
  id: classify-test
  version: 0.1.0

world: {}

routing:
  enabled: true

intents:
  go_north:
    title: "Go north"
    examples: ["go north"]
    synonyms: ["head north"]
  go_east:
    title: "Go east"
    examples: ["go east"]
    synonyms: ["wander east"]
  shout:
    title: "Shout"
    synonyms: ["yell"]
    slots:
      message: { type: string, required: true }

root: start

states:
  start:
    view: "compass rose"
    on:
      go_north:
        - target: ended
      go_east:
        - target: ended
      shout:
        - target: ended

  ended:
    terminal: true
    view: "done"
`

// newClassifyOrchestrator builds an orchestrator over classifyTestApp with an
// in-memory store and NO harness (a free-text Turn would error — Classify must
// never need one). The matcher compiles from the routing block's synonyms /
// examples. Returns the orchestrator plus the initial (state, world) Classify
// is exercised against.
func newClassifyOrchestrator(t *testing.T) (*orchestrator.Orchestrator, app.StatePath, world.World) {
	t.Helper()

	def, err := app.LoadBytes([]byte(classifyTestApp))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// nil harness: Classify never routes via the LLM, so it must not depend on
	// one being wired.
	orch := orchestrator.New(def, m, s, nil)
	return orch, orch.InitialState(), orch.InitialWorld()
}

// TestClassify_DeterministicDisplay — the display string of a primary menu
// entry routes via the deterministic tier at Confidence == 1.00.
func TestClassify_DeterministicDisplay(t *testing.T) {
	t.Parallel()
	orch, state, w := newClassifyOrchestrator(t)
	ctx := context.Background()

	// "go north" is the example AND, for a no-slot intent, the rendered Display
	// label of the go_north row — both route deterministically.
	verdict, matched, err := orch.Classify(ctx, state, w, "go north")
	require.NoError(t, err)
	require.True(t, matched, "deterministic display/example input must match")
	require.Equal(t, "go_north", verdict.Intent)
	require.Equal(t, float64(semroute.ConfidenceExact), verdict.Confidence,
		"deterministic tier must emit ConfidenceExact (1.00)")
}

// TestClassify_DeterministicExample — an intent-level example routes via the
// deterministic tier at Confidence == 1.00.
func TestClassify_DeterministicExample(t *testing.T) {
	t.Parallel()
	orch, state, w := newClassifyOrchestrator(t)
	ctx := context.Background()

	verdict, matched, err := orch.Classify(ctx, state, w, "go east")
	require.NoError(t, err)
	require.True(t, matched, "deterministic example input must match")
	require.Equal(t, "go_east", verdict.Intent)
	require.Equal(t, float64(semroute.ConfidenceExact), verdict.Confidence)
}

// TestClassify_SynonymMatch — a bare synonym ("head north") routes via the
// semantic deterministic tier at the whole-synonym band (0.90).
func TestClassify_SynonymMatch(t *testing.T) {
	t.Parallel()
	orch, state, w := newClassifyOrchestrator(t)
	ctx := context.Background()

	verdict, matched, err := orch.Classify(ctx, state, w, "head north")
	require.NoError(t, err)
	require.True(t, matched, "a declared synonym must match")
	require.Equal(t, "go_north", verdict.Intent)
	require.GreaterOrEqual(t, verdict.Confidence, float64(semroute.ConfidenceWholeSynonym),
		"a whole-synonym hit must clear the 0.90 band")
}

// TestClassify_UnknownInputNoMatch — a clearly-unknown utterance is a clean
// no-match: matched == false, Confidence == 0, and no error.
func TestClassify_UnknownInputNoMatch(t *testing.T) {
	t.Parallel()
	orch, state, w := newClassifyOrchestrator(t)
	ctx := context.Background()

	verdict, matched, err := orch.Classify(ctx, state, w, "xyzzy nonsense")
	require.NoError(t, err)
	require.False(t, matched, "an unknown input must not match any no-LLM tier")
	require.Equal(t, float64(0), verdict.Confidence, "a no-match verdict carries zero confidence")
	require.Empty(t, verdict.Intent)
}

// TestClassify_RequiredUnfilledSlotSurfaced — a synonym ("yell") matches the
// `shout` intent, which declares a required `message` slot the bare-string
// matcher cannot fill. Classify returns the verdict WITHOUT executing and
// without error; the caller can detect the unfilled slot via
// RequiresUnfilledSlot. Classify deliberately does NOT abdicate this match
// (that conservative gate lives in the executor, not the classifier).
func TestClassify_RequiredUnfilledSlotSurfaced(t *testing.T) {
	t.Parallel()
	orch, state, w := newClassifyOrchestrator(t)
	ctx := context.Background()

	verdict, matched, err := orch.Classify(ctx, state, w, "yell")
	require.NoError(t, err, "Classify must return without error on a slot-bearing match")
	require.True(t, matched, "the synonym must still produce a verdict for the gate to inspect")
	require.Equal(t, "shout", verdict.Intent)

	// The caller can detect the unfilled required slot via RequiresUnfilledSlot
	// — the bare synonym did not fill `message`.
	require.True(t,
		orchestrator.RequiresUnfilledSlot(orch.AppDef(), state, verdict.Intent, verdict.Slots),
		"RequiresUnfilledSlot must flag the unfilled required `message` slot")
}

// TestClassify_ZeroEffectNonMutating pins the moat invariant: Classify is a
// pure read. Snapshot the passed world map as JSON, run Classify across several
// inputs, and assert the map is byte-identical afterward. Classify takes no sid
// and writes no events, so there is nothing to assert about session state — its
// absence from the signature IS the guarantee, re-stated here by reading the
// store back and finding no sessions were created.
func TestClassify_ZeroEffectNonMutating(t *testing.T) {
	t.Parallel()

	def, err := app.LoadBytes([]byte(classifyTestApp))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	orch := orchestrator.New(def, m, s, nil)

	state := orch.InitialState()
	w := orch.InitialWorld()
	// Seed a world value so the snapshot is non-trivial.
	w.Vars["seeded"] = "value"

	before, err := json.Marshal(w.Vars)
	require.NoError(t, err)

	ctx := context.Background()
	for _, in := range []string{"go north", "head north", "yell", "xyzzy nonsense"} {
		_, _, cErr := orch.Classify(ctx, state, w, in)
		require.NoError(t, cErr, "Classify(%q)", in)
	}

	after, err := json.Marshal(w.Vars)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after),
		"Classify must not mutate the passed world map")

	// Classify never takes a sid and never writes events: no session should
	// exist in the store as a result of classifying.
	sessions, err := s.ListSessions(ctx, def.App.ID, 100)
	require.NoError(t, err)
	require.Empty(t, sessions, "Classify must not create any session or events")
}
