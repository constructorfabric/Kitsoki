package app_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
)

// TestPhases_LoadAndExpand verifies that phase templates expand into the
// expected state names and transition shape.
func TestPhases_LoadAndExpand(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "three-phase.yaml"))
	require.NoError(t, err)

	wantStates := []string{
		"phase_a_executing", "phase_a_awaiting_reply", "phase_a_error",
		"phase_b_executing", "phase_b_awaiting_reply", "phase_b_error",
		"phase_c_executing", "phase_c_awaiting_reply", "phase_c_error",
		"terminated",
	}
	for _, name := range wantStates {
		assert.Contains(t, def.States, name, "expanded state %q must exist", name)
	}
	assert.Len(t, def.States, len(wantStates))
}

// TestPhases_TplSubstitution verifies that {{ tpl.X }} is substituted into
// state body strings.
func TestPhases_TplSubstitution(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "three-phase.yaml"))
	require.NoError(t, err)

	exec := def.States["phase_b_executing"]
	require.NotNil(t, exec)
	assert.Equal(t, "Executing Phase B", exec.Description)
	assert.Equal(t, "Phase Phase B running", exec.View.SourceString())
}

// TestPhases_NextRewrite verifies {{ phase.next.continue }} rewrites to
// <phase>_executing.
func TestPhases_NextRewrite(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "three-phase.yaml"))
	require.NoError(t, err)

	// phase_a → phase_b.
	doneA := def.States["phase_a_executing"].On["done"]
	require.NotEmpty(t, doneA)
	// First entry has the checkpoint when-guard ("tpl.checkpoint == true"),
	// which is "false" for phase_a — but the loader still emits it. The
	// default fall-through must point at phase_b_executing.
	var fellThrough bool
	for _, tr := range doneA {
		if tr.Default {
			assert.Equal(t, "phase_b_executing", tr.Target)
			fellThrough = true
		}
	}
	require.True(t, fellThrough, "phase_a must have a default fall-through to phase_b_executing")

	// phase_c.continue → terminated (no rewriting because it's a literal).
	doneC := def.States["phase_c_executing"].On["done"]
	for _, tr := range doneC {
		if tr.Default {
			assert.Equal(t, "terminated", tr.Target)
		}
	}
}

// TestPhases_CycleBudgetsSynthesis verifies that:
//   - the arc transitions get an Increment effect
//   - the arc's `when:` is tightened with the cycle bound
//   - a default fall-through to <phase>_error is appended
func TestPhases_CycleBudgetsSynthesis(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "three-phase.yaml"))
	require.NoError(t, err)

	exec := def.States["phase_c_executing"]
	require.NotNil(t, exec)

	arc := exec.On["on_failure"]
	require.NotEmpty(t, arc, "phase_c.on_failure must be synthesized from cycle_budgets")

	// First entry: the synthesized increment + guard.
	first := arc[0]
	require.False(t, first.Default)
	assert.Contains(t, first.When, "world.cycle__phase_c__on_failure",
		"guard must be rooted under world. so expr-lang can resolve it at eval time")
	assert.Contains(t, first.When, " < 2")
	require.NotEmpty(t, first.Effects)
	assert.Equal(t, 1, first.Effects[0].Increment["cycle__phase_c__on_failure"])

	// Last entry: the fall-through to phase_c_error.
	last := arc[len(arc)-1]
	assert.True(t, last.Default, "trailing transition must be the default fall-through")
	assert.Equal(t, "phase_c_error", last.Target)
	assert.NotEmpty(t, last.GuardHint)
}

// TestPhases_CheckpointIntentsMerged verifies that checkpoint_intents
// are merged into every {id}_awaiting_reply state's intents.
func TestPhases_CheckpointIntentsMerged(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "three-phase.yaml"))
	require.NoError(t, err)

	for _, name := range []string{"phase_a_awaiting_reply", "phase_b_awaiting_reply", "phase_c_awaiting_reply"} {
		s := def.States[name]
		require.NotNil(t, s, name)
		require.Contains(t, s.Intents, "continue", name)
		require.Contains(t, s.Intents, "refine", name)
	}
	// _executing states must NOT receive checkpoint_intents.
	exec := def.States["phase_a_executing"]
	require.NotNil(t, exec)
	_, hasContinue := exec.Intents["continue"]
	require.False(t, hasContinue, "_executing must not receive checkpoint_intents")
}

// TestPhases_OnEnterOverride verifies that a per-phase `on_enter:` list on
// the graph entry REPLACES the template's on_enter on the {id}_executing
// state, with `{{ tpl.X }}` and `{{ phase.next.X }}` substitution applied
// to the override body. The phase that does NOT declare an override must
// still inherit the template's default on_enter unchanged.
//
// This is the substrate for script-driven phases (host.run) coexisting in
// a room whose template defaults to host.oracle.ask_with_mcp — see
// stories/bugfix/app.yaml's phase_0 / phase_0_5 / phase_9_5 / phase_9_6 /
// phase_12 entries.
func TestPhases_OnEnterOverride(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "on-enter-override.yaml"))
	require.NoError(t, err)

	// LLM phase: no override; the template's default on_enter applies.
	llm := def.States["phase_llm_executing"]
	require.NotNil(t, llm)
	require.Len(t, llm.OnEnter, 1, "phase_llm must inherit the template's single default on_enter effect")
	assert.Equal(t, "host.oracle.ask_with_mcp", llm.OnEnter[0].Invoke,
		"phase_llm must call the template's host.oracle.ask_with_mcp")
	// `{{ tpl.id }}_artifact` substitution must run in the inherited body.
	assert.Equal(t, "submitted", llm.OnEnter[0].Bind["phase_llm_artifact"],
		"template substitution must apply to the inherited bind keys")

	// Script phase: graph entry declared its own on_enter; that REPLACES
	// the template default rather than appending to it.
	scr := def.States["phase_script_executing"]
	require.NotNil(t, scr)
	require.Len(t, scr.OnEnter, 1,
		"phase_script must have exactly the override's effect — replacement, not merge")
	assert.Equal(t, "host.run", scr.OnEnter[0].Invoke,
		"phase_script must call host.run from the override; the template's host.oracle.ask_with_mcp must NOT bleed through")

	// `{{ tpl.id }}` and `{{ world.X }}` substitutions must apply to the
	// override body (the latter passes through unchanged at expansion
	// time — it's evaluated at runtime).
	cmd, ok := scr.OnEnter[0].With["cmd"].(string)
	require.True(t, ok, "host.run override must declare cmd")
	assert.Contains(t, cmd, "phase_script",
		"`{{ tpl.id }}` must expand to the phase id in the override")
	assert.Contains(t, cmd, "{{ world.ticket }}",
		"`{{ world.X }}` references must pass through expansion unchanged for runtime eval")
	assert.Equal(t, "stdout", scr.OnEnter[0].Bind["phase_script_artifact"],
		"override bind keys must have `{{ tpl.id }}` substituted")
	assert.Equal(t, "phase_script_error", scr.OnEnter[0].OnError,
		"override on_error must have `{{ tpl.id }}` substituted")
}

// TestPhases_HandWrittenOverride verifies that a top-level hand-written
// state whose name collides with a template-expanded state name silently
// wins: the expander skips the templated assignment for that one name,
// keeps the hand-written body in def.States, and continues expanding the
// rest of the phase's states unchanged.
//
// This is the substrate for phases that want to keep the template's
// `_executing` / `_error` states but route a checkpoint intent through a
// hand-written `_awaiting_reply` to a literal target the template can't
// model (e.g. stories/bugfix/app.yaml's phase_13).
func TestPhases_HandWrittenOverride(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "hand-written-override.yaml"))
	require.NoError(t, err, "load must succeed; the collision must NOT error")

	// phase_a_awaiting_reply must be the hand-written version — recognisable
	// by its description and by carrying the `custom` arc that the template
	// body does not declare.
	aAwait := def.States["phase_a_awaiting_reply"]
	require.NotNil(t, aAwait)
	assert.Equal(t, "Hand-written awaiting reply for Phase A", aAwait.Description,
		"hand-written body must win over the template body")
	require.Contains(t, aAwait.On, "custom",
		"hand-written `custom` arc must be present (template doesn't declare it)")
	assert.Equal(t, "terminated", aAwait.On["custom"][0].Target,
		"hand-written arc must route to the literal `terminated` target")
	// Conversely the template's `continue` arc must NOT have been merged in.
	_, hasContinue := aAwait.On["continue"]
	assert.False(t, hasContinue, "template's `continue` arc must NOT bleed into the hand-written state")

	// The phase's other template-generated states must still be present and
	// unmodified — only the one colliding name is skipped.
	aExec := def.States["phase_a_executing"]
	require.NotNil(t, aExec, "phase_a_executing must still be templated")
	assert.Equal(t, "Executing Phase A", aExec.Description)
	aErr := def.States["phase_a_error"]
	require.NotNil(t, aErr, "phase_a_error must still be templated")
	assert.Equal(t, "Error in Phase A", aErr.Description)

	// A sibling phase with no override must template all three states
	// normally — the override must not be sticky across phases.
	bAwait := def.States["phase_b_awaiting_reply"]
	require.NotNil(t, bAwait)
	assert.Equal(t, "Templated awaiting reply for Phase B", bAwait.Description,
		"phase_b has no override; it must get the templated body")
}

// TestPhases_HandWritten_ExecutingOverride_AppliesCycleBudgets verifies
// that the expander applies `cycle_budgets:` synthesis to a phase's
// hand-written `_executing` state.  The override wins for the state's
// body (description, on_enter, explicit arcs in `on:`) but the cycle-
// budget retry arcs are grafted in regardless — they protect the state
// machine from runaway loops independently of whatever the hand-written
// body looks like.
//
// Why this matters: the audit (.context/test-audit.md §"Surface 4")
// pinned the prior silent-drop behaviour.  Silently dropping budgets
// when a phase author hand-writes `_executing` would let a runaway loop
// escape the guard the author thought they had.  `applyCycleBudgets`
// merges into existing arcs idempotently — arcs the hand-written state
// declares explicitly stay (decorated with the guard); arcs only the
// budget declares get fresh retry/error transitions.
//
// A sibling phase (phase_b) with cycle_budgets but no override is
// used as a positive control — it must still get the synthesis, which
// proves the merge path didn't break the standard one.
func TestPhases_HandWritten_ExecutingOverride_AppliesCycleBudgets(t *testing.T) {
	def, err := app.Load(filepath.Join(
		"testdata", "phases", "hand-written-executing-with-cycle-budgets.yaml",
	))
	require.NoError(t, err)

	// ── phase_a: hand-written `_executing` wins for the body, AND
	// cycle-budget synthesis is grafted on top.
	aExec := def.States["phase_a_executing"]
	require.NotNil(t, aExec)
	require.Equal(t, "Hand-written executing for Phase A", aExec.Description,
		"hand-written body must win over the template body")
	require.Contains(t, aExec.On, "custom",
		"hand-written `custom` arc must be preserved verbatim")

	// `on_failure` is declared by cycle_budgets but NOT by the hand-
	// written body — applyCycleBudgets must synthesise it via the
	// missing-arc → fall-through-error path.
	aOnFailure := aExec.On["on_failure"]
	require.NotEmpty(t, aOnFailure,
		"hand-written _executing must receive the synthesised on_failure arc")
	aFirst := aOnFailure[0]
	assert.Contains(t, aFirst.When, "world.cycle__phase_a__on_failure",
		"synthesised guard must reference the world counter")
	assert.Contains(t, aFirst.When, " < 2")
	require.NotEmpty(t, aFirst.Effects)
	assert.Equal(t, 1, aFirst.Effects[0].Increment["cycle__phase_a__on_failure"])
	aLast := aOnFailure[len(aOnFailure)-1]
	assert.True(t, aLast.Default,
		"trailing transition must be the synthesised fall-through")
	assert.Equal(t, "phase_a_error", aLast.Target)

	// ── phase_b: NO override.  Positive control — proves the merge
	// path didn't break standard template-only expansion.
	bExec := def.States["phase_b_executing"]
	require.NotNil(t, bExec)
	assert.Equal(t, "Templated executing for Phase B", bExec.Description,
		"phase_b has no override; it must get the templated body")

	bOnFailure := bExec.On["on_failure"]
	require.NotEmpty(t, bOnFailure,
		"phase_b.on_failure must be synthesised by applyCycleBudgets")
	bFirst := bOnFailure[0]
	assert.Contains(t, bFirst.When, "world.cycle__phase_b__on_failure")
	assert.Contains(t, bFirst.When, " < 2")
	require.NotEmpty(t, bFirst.Effects)
	assert.Equal(t, 1, bFirst.Effects[0].Increment["cycle__phase_b__on_failure"])
	bLast := bOnFailure[len(bOnFailure)-1]
	assert.True(t, bLast.Default)
	assert.Equal(t, "phase_b_error", bLast.Target)
}

// TestPhases_CycleBudgetSynthesisMissingArcCreatesErrorRoute verifies that
// declaring cycle_budgets for an arc not present in the template synthesizes
// a fall-through-to-error transition (rather than silently dropping the
// budget).
func TestPhases_CycleBudgetSynthesisMissingArcCreatesErrorRoute(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "three-phase.yaml"))
	require.NoError(t, err)

	// `on_failure` is not declared in the template's `on:` block — only
	// `done` is. The cycle_budgets entry should synthesize a transition.
	exec := def.States["phase_c_executing"]
	arc := exec.On["on_failure"]
	require.NotEmpty(t, arc)
}

// TestPhases_OnEnterOverride_BackgroundOnCompleteTarget verifies that a
// per-phase on_enter: override carrying `background: true`, an
// `on_complete:` chain, and a `target:` inside that chain has ALL three
// fields preserved through substEffect. Without the substEffect copy-
// completeness fix these fields are silently stripped:
//
//   - Background drops → the override fires synchronously and the runtime
//     emits HostInvoked{Background: false} instead of dispatching a job.
//   - OnComplete drops → the post-job continuation never runs.
//   - Target drops → the synthetic transition out of the background turn
//     never fires, and the session is stuck in `_executing`.
//
// Template substitution must still apply: the `target:` references
// `{{ tpl.id }}_awaiting_reply` which expands to `phase_script_awaiting_reply`.
func TestPhases_OnEnterOverride_BackgroundOnCompleteTarget(t *testing.T) {
	def, err := app.Load(filepath.Join("testdata", "phases", "on-enter-override-background.yaml"))
	require.NoError(t, err)

	scr := def.States["phase_script_executing"]
	require.NotNil(t, scr)
	require.Len(t, scr.OnEnter, 1, "override must REPLACE the template's on_enter with exactly one effect")

	eff := scr.OnEnter[0]
	assert.Equal(t, "host.run", eff.Invoke,
		"override invoke must survive substEffect")
	assert.True(t, eff.Background,
		"Background: true MUST be copied through substEffect — otherwise the override dispatches synchronously")

	// OnComplete: ordered list — set/bind first, target last.
	require.Len(t, eff.OnComplete, 2,
		"on_complete: must be copied through substEffect, with list order preserved")

	mutate := eff.OnComplete[0]
	assert.Equal(t, "result.status", mutate.Set["scr_status"],
		"on_complete[0].set must be carried through")
	assert.Equal(t, "stdout", mutate.Bind["scr_artifact"],
		"on_complete[0].bind must be carried through")
	assert.Empty(t, mutate.Target, "on_complete[0] has no target")

	transition := eff.OnComplete[1]
	assert.Equal(t, "phase_script_awaiting_reply", transition.Target,
		"on_complete[1].target must be substituted: `{{ tpl.id }}_awaiting_reply` -> `phase_script_awaiting_reply`")
	// Mutation/transition split must be preserved verbatim.
	assert.Empty(t, transition.Set)
	assert.Empty(t, transition.Bind)
	assert.Empty(t, transition.Invoke)

	// The outer effect's own Target must remain empty — Target only lives
	// on on_complete entries. substEffect must not invent one.
	assert.Empty(t, eff.Target, "outer effect must not have Target populated")
}
