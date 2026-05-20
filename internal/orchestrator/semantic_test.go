// Integration tests for the semantic-routing orchestrator wiring
// (semantic-routing proposal §1, Phase 2). The unit tests for the
// matcher itself live in internal/semroute/*_test.go; these tests
// exercise the orchestrator-side glue:
//
//   - With app.routing.enabled = true, an input that maps to a
//     declared synonym resolves WITHOUT calling the harness.
//   - With app.routing.enabled = false, the same input falls through
//     to the harness.
//   - A miss (no synonym matches) falls through to the harness.
//   - A tie (two intents matched the same synonym) surfaces an
//     AMBIGUOUS_INTENT outcome and the harness is NOT called.
//
// The counting harness lets us assert "harness was not called" by
// reading a counter after Turn returns.
package orchestrator_test

import (
	"context"
	"sync/atomic"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// countingHarness records how many times RunTurn was called and
// returns a static intent call from the recorded fallback. Used by
// the semantic-routing tests to assert "the LLM was not called."
type countingHarness struct {
	calls atomic.Int64
	fall  staticHarness
}

func (h *countingHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	h.calls.Add(1)
	return h.fall.RunTurn(ctx, in)
}

func (h *countingHarness) Close() error { return h.fall.Close() }

// newSemanticTestApp builds an in-memory AppDef with three intents
// (north / south / east) and a single state that allows all three.
// Each intent declares one synonym that the matcher should resolve
// without the LLM.
func newSemanticTestApp(t *testing.T, routingEnabled bool) (*orchestrator.Orchestrator, *countingHarness, app.SessionID) {
	t.Helper()
	const appYAML = `
app:
  id: semroute-test
  version: 0.1.0

world: {}

routing:
  enabled: %t

intents:
  go_north:
    title: "Go north"
    examples: ["go north"]
    synonyms: ["head north"]
  go_south:
    title: "Go south"
    examples: ["go south"]
    synonyms: ["head south"]
  go_east:
    title: "Go east"
    examples: ["go east"]
    synonyms: ["wander east"]

root: start

states:
  start:
    view: "compass rose"
    on:
      go_north:
        - target: ended
      go_south:
        - target: ended
      go_east:
        - target: ended

  ended:
    terminal: true
    view: "done"
`
	body := []byte(rendered(appYAML, routingEnabled))
	def, err := app.LoadBytes(body)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Fallback harness routes to go_north on any LLM call so that
	// when the test EXPECTS the harness to be hit we have a sane
	// outcome to assert on. Tests that want a different fallback
	// can rewrite the .fall field directly.
	h := &countingHarness{fall: staticHarness{intentName: "go_north"}}

	orch := orchestrator.New(def, m, s, h)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	return orch, h, sid
}

// rendered is a tiny helper that fills the `%t` in the test YAML
// without dragging text/template into the test binary.
func rendered(tmpl string, b bool) string {
	out := make([]byte, 0, len(tmpl))
	v := "true"
	if !b {
		v = "false"
	}
	for i := 0; i < len(tmpl); i++ {
		if i+1 < len(tmpl) && tmpl[i] == '%' && tmpl[i+1] == 't' {
			out = append(out, v...)
			i++
			continue
		}
		out = append(out, tmpl[i])
	}
	return string(out)
}

// TestSemantic_SynonymResolvesWithoutHarness — the canonical happy
// path. With routing enabled, "head north" routes to go_north and
// the harness is never called.
func TestSemantic_SynonymResolvesWithoutHarness(t *testing.T) {
	t.Parallel()
	orch, h, sid := newSemanticTestApp(t, true)
	ctx := context.Background()

	out, err := orch.Turn(ctx, sid, "head north")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode,
		"want ModeCompleted (go_north transitions to terminal ended), got %v", out.Mode)
	require.Equal(t, app.StatePath("ended"), out.NewState)
	require.EqualValues(t, 0, h.calls.Load(),
		"harness must NOT be called when semantic routing resolves the turn")
}

// TestSemantic_DisabledFallsThroughToHarness — same input, but with
// routing.enabled=false in the AppDef, the matcher is skipped and
// the harness fires.
func TestSemantic_DisabledFallsThroughToHarness(t *testing.T) {
	t.Parallel()
	orch, h, sid := newSemanticTestApp(t, false)
	ctx := context.Background()

	out, err := orch.Turn(ctx, sid, "head north")
	require.NoError(t, err)
	// Fallback harness routes everything to go_north, which is a
	// valid transition, so the outcome is still Completed — but the
	// load-bearing assertion is the call count.
	require.Equal(t, orchestrator.ModeCompleted, out.Mode)
	require.EqualValues(t, 1, h.calls.Load(),
		"harness MUST be called when routing.enabled=false")
}

// TestSemantic_RefineIntents_FeedbackRequired pins the YAML-side
// half of the 2026-05-20 fix: every story that declares a `refine`
// intent must mark its `feedback` slot as required:true. Without
// that flag, the semantic router's high-confidence path short-
// circuits on the lead verb and the operator's trailing free-form
// feedback is dropped (see TestSemantic_RequiredSlotForcesFallThrough
// for the mechanism). A toggle back to required:false in any of
// these stories would silently re-introduce the regression; this
// test catches that toggle at load time.
func TestSemantic_RefineIntents_FeedbackRequired(t *testing.T) {
	t.Parallel()

	stories := []string{
		"../../stories/bugfix/app.yaml",
		"../../stories/cypilot/app.yaml",
		"../../stories/pr-refinement/app.yaml",
	}

	for _, path := range stories {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			def, err := app.Load(path)
			require.NoError(t, err, "load %s", path)

			intent, ok := def.Intents["refine"]
			require.True(t, ok, "%s must declare a refine intent", path)

			slot, ok := intent.Slots["feedback"]
			require.True(t, ok, "%s: refine intent must declare a feedback slot", path)

			require.True(t, slot.Required,
				"%s: refine.slots.feedback MUST be required:true — without it the semantic router auto-dispatches `refine X Y Z` with empty slots and the operator's directive is lost (2026-05-20 dogfood regression)",
				path)
		})
	}
}

// TestSemantic_RequiredSlotForcesFallThrough is the regression for
// the 2026-05-20 dogfood bug where typing
// `refine don't keep priority - there's no consumers ...`
// at a bugfix checkpoint caused the kitsoki bare-string semantic
// router to short-circuit on the verb-word "refine" and dispatch
// `core__bf__refine` with EMPTY slots. The downstream refine arc
// then set `world.refine_feedback` to "" via
// `{{ slots.feedback ?? world.llm_verdict.reason }}`, the next
// oracle's prompt's `{% if args.refine_feedback %}` rendered false,
// and the operator's directive never reached the LLM.
//
// The fix is encoded in stories/{bugfix,cypilot,pr-refinement}/app.yaml:
// refine.slots.feedback is now `required: true`, which makes
// RequiresUnfilledSlot return true for a bare-string match with an
// empty slot bag — semantic.go's high-confidence branch then
// abdicates to the LLM router, which extracts the feedback from
// natural language.
//
// This test pins the orchestrator behaviour: a synonym/example
// match against an intent that declares a required slot the matcher
// cannot fill MUST fall through to the harness, not auto-dispatch
// with empty slots.
func TestSemantic_RequiredSlotForcesFallThrough(t *testing.T) {
	t.Parallel()

	const appYAML = `
app:
  id: semroute-required-slot
  version: 0.1.0

world: {}

routing:
  enabled: true

intents:
  refine:
    title: "Refine"
    examples: ["refine"]
    synonyms: ["rework"]
    slots:
      # The exact shape of the bugfix story's refine intent after the
      # 2026-05-20 fix. Without required:true the semantic matcher
      # short-circuits on the lead verb and the trailing feedback is
      # lost.
      feedback: { type: string, required: true }

root: start

states:
  start:
    view: "checkpoint"
    on:
      refine:
        - target: ended

  ended:
    terminal: true
    view: "done"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Fallback harness routes everything to `refine` with the
	// feedback slot filled — same shape claude would produce when
	// the LLM router parses "refine don't keep priority" out of
	// natural language. The semantic router falling through to the
	// harness is what we're asserting; the harness's response is
	// irrelevant to the test's load-bearing claim.
	h := &countingHarness{fall: staticHarness{
		intentName: "refine",
		slots:      map[string]any{"feedback": "don't keep priority"},
	}}

	orch := orchestrator.New(def, m, s, h)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Bare-string match on the example word "refine" — the rest of
	// the input is free-form feedback the matcher cannot extract.
	out, err := orch.Turn(ctx, sid, "refine don't keep priority - there's no consumers backwards compat is not relevant")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode,
		"the transition must reach ended (via the harness's filled slots)")
	require.EqualValues(t, 1, h.calls.Load(),
		"harness MUST be called: a semantic match on an intent with a required unfilled slot must abdicate to the LLM, not auto-dispatch with empty slots — this is the 2026-05-20 dogfood regression")
}

// TestSemantic_MissFallsThroughToHarness — routing is enabled but
// the input shares no stems with any declared synonym; the harness
// MUST fire.
func TestSemantic_MissFallsThroughToHarness(t *testing.T) {
	t.Parallel()
	orch, h, sid := newSemanticTestApp(t, true)
	ctx := context.Background()

	out, err := orch.Turn(ctx, sid, "abracadabra please thank you")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode)
	require.EqualValues(t, 1, h.calls.Load(),
		"semantic miss must fall through to the harness")
}

// TestSemantic_TieSurfacesAmbiguousIntent — two intents share a
// synonym; the orchestrator returns an AMBIGUOUS_INTENT outcome
// without calling the harness.
func TestSemantic_TieSurfacesAmbiguousIntent(t *testing.T) {
	t.Parallel()

	const appYAML = `
app:
  id: semroute-tie
  version: 0.1.0

world: {}

intents:
  leave_store:
    title: "Leave store"
    examples: ["leave the store"]
    synonyms: ["leave"]
  cancel_purchase:
    title: "Cancel purchase"
    examples: ["cancel"]
    synonyms: ["leave"]

root: start

states:
  start:
    view: "store"
    on:
      leave_store:
        - target: ended
      cancel_purchase:
        - target: ended

  ended:
    terminal: true
    view: "done"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h := &countingHarness{fall: staticHarness{intentName: "leave_store"}}
	orch := orchestrator.New(def, m, s, h)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "leave")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeRejected, out.Mode,
		"tie verdict must surface as Rejected (orchestrator-side AMBIGUOUS_INTENT)")
	require.Equal(t, "AMBIGUOUS_INTENT", string(out.ErrorCode))
	require.EqualValues(t, 0, h.calls.Load(),
		"harness must NOT be called on a tie verdict — disambiguation comes first")
}

// TestSemantic_MatcherAccessor exposes the lazy-compiled *Matcher
// to outside callers (Matcher() / MatcherError()).
func TestSemantic_MatcherAccessor(t *testing.T) {
	t.Parallel()
	orch, _, _ := newSemanticTestApp(t, true)
	m := orch.Matcher()
	require.NotNil(t, m, "Matcher() must return non-nil when routing is enabled and synonyms exist")
	require.NoError(t, orch.MatcherError())
}

// TestSemantic_MatcherAccessorDisabled — routing.enabled=false
// yields a nil Matcher.
func TestSemantic_MatcherAccessorDisabled(t *testing.T) {
	t.Parallel()
	orch, _, _ := newSemanticTestApp(t, false)
	require.Nil(t, orch.Matcher(), "routing.enabled=false must yield nil Matcher")
}
