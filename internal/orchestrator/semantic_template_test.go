// Integration tests for the Phase-4 semantic-template path through
// the orchestrator (see docs/architecture/semantic-routing.md
// "Synonym templates"). The matcher
// itself is covered by internal/semroute/template_match_test.go;
// these tests pin the orchestrator-side glue:
//
//   - A 0.80 verdict (template + all slots filled) reaches
//     SubmitDirect with the slots forwarded — harness is NOT called.
//   - A 0.65 verdict (template + ≥1 slot unparseable) yields a
//     ModeClarify outcome targeting the unparseable slot — harness
//     is NOT called.
//
// The same countingHarness from semantic_test.go is reused via the
// orchestrator_test package's shared setup.
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

// newTemplateApp builds an AppDef with a single propose_purchase
// intent declaring synonym templates. The state accepts the intent
// with any slot values; the test asserts on the orchestrator outcome,
// not on the eventual world state.
func newTemplateApp(t *testing.T) (*orchestrator.Orchestrator, *countingHarness, app.SessionID) {
	t.Helper()
	const appYAML = `
app:
  id: semroute-template-test
  version: 0.1.0

world: {}

routing:
  enabled: true

intents:
  propose_purchase:
    title: "Draft a purchase"
    examples: ["buy 2 oxen and 200 lbs food"]
    synonyms:
      - "buy {items} for {total_cost}"
      - "purchase {items}"
      - "spend {total_cost} on {items}"
    slots:
      items:
        type: string
        required: true
        description: "Basket contents."
        prompt: "What do you want to buy?"
      total_cost:
        type: int
        required: true
        description: "Total dollars."
        prompt: "What's the total in dollars?"
  leave_store:
    title: "Leave the store"
    examples: ["leave"]

root: store

states:
  store:
    view: "store"
    on:
      propose_purchase:
        - target: ended
      leave_store:
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
	return orch, h, sid
}

// TestSemanticTemplate_AllSlotsResolveWithoutHarness pins the
// synonym-template path: a
// template that fills every slot must reach SubmitDirect without an
// LLM call. The intent fires (ModeCompleted) because the test app's
// state machine accepts the propose_purchase transition into the
// terminal "ended" state.
func TestSemanticTemplate_AllSlotsResolveWithoutHarness(t *testing.T) {
	t.Parallel()
	orch, h, sid := newTemplateApp(t)
	ctx := context.Background()

	out, err := orch.Turn(ctx, sid, "buy 6 oxen and 200 lbs food for 240")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode,
		"template match with all slots filled must reach ModeCompleted; got %v (err=%q)",
		out.Mode, out.ErrorMessage)
	require.Equal(t, app.StatePath("ended"), out.NewState)
	require.EqualValues(t, 0, h.calls.Load(),
		"harness MUST NOT be called when the template tier resolves the turn")
}

// TestSemanticTemplate_UnparseableSlotClarifies pins the 0.65 band:
// a template matches by literal anchors, but a captured slot fails
// to parse (here, the cost capture is text the int parser refuses).
// The orchestrator returns ModeClarify with the unparseable slot's
// metadata.
func TestSemanticTemplate_UnparseableSlotClarifies(t *testing.T) {
	t.Parallel()
	orch, h, sid := newTemplateApp(t)
	ctx := context.Background()

	out, err := orch.Turn(ctx, sid, "buy 6 oxen for fjord")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeClarify, out.Mode,
		"template match with unparseable slot must reach ModeClarify; got %v", out.Mode)
	require.Equal(t, "propose_purchase", string(out.PendingIntent))
	require.EqualValues(t, 0, h.calls.Load(),
		"harness MUST NOT be called when the template tier produced a 0.65 verdict")

	// SlotsNeeded carries the clarification metadata for the
	// unparseable slot. It must reference total_cost (the int that
	// failed to parse) and surface a prompt the TUI can render.
	found := false
	for _, need := range out.SlotsNeeded {
		if need.Name == "total_cost" {
			found = true
			if need.Prompt == "" {
				t.Errorf("SlotsNeeded[total_cost]: empty Prompt; clarification card cannot render")
			}
			break
		}
	}
	if !found {
		t.Errorf("SlotsNeeded: total_cost not in %v", out.SlotsNeeded)
	}
}
