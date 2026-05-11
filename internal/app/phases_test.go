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
	assert.Equal(t, "Phase Phase B running", exec.View)
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
