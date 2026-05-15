// emit_intent_test.go — covers the synthetic-intent dispatch path
// (Effect.EmitIntent / EmitSlots; machine.go applyEffectsTraced +
// dispatchEmittedIntents). The cases here exercise the load-time
// validator (in concert with internal/app's loader; see also
// loader_emit_intent_test.go) and the runtime behaviour:
//
//   - on_enter emit fires and advances state in one Turn
//   - when: guards an emit (gated-out stays at state)
//   - emit slot values pass through to the dispatched transition
//   - chained emits walk multiple levels
//   - cyclic emit hits the depth cap
//
// The test fixtures live entirely in code (no YAML) so a regression
// failing here is unambiguously a runtime bug, not a YAML/loader one.
package machine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// TestEmitIntent_SimpleOnEnterAutoFire — a state's on_enter emits an
// intent that itself has a transition; the Turn settles at the
// destination in a single externally-initiated turn.
func TestEmitIntent_SimpleOnEnterAutoFire(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-simple"},
		Root: "start",
		Intents: map[string]app.Intent{
			"go":   {},
			"auto": {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"go": {{Target: "middle"}},
				},
			},
			"middle": {
				OnEnter: []app.Effect{
					{EmitIntent: "auto"},
				},
				On: map[string][]app.Transition{
					"auto": {{Target: "end"}},
				},
			},
			"end": {Terminal: true, View: app.LegacyView("end")},
		},
	}

	m := mustNew(t, def)
	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{Intent: "go"})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Equal(t, app.StatePath("end"), res.NewState, "auto-fire must advance past middle in one turn")
}

// TestEmitIntent_GatedByWhen — a `when:` on the emit_intent effect
// gates whether it fires. When the guard is false the state holds.
func TestEmitIntent_GatedByWhen(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-gated"},
		Root: "start",
		World: map[string]app.VarDef{
			"autofire": {Type: "bool", Default: false},
		},
		Intents: map[string]app.Intent{
			"enter": {},
			"go":    {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"enter": {{Target: "middle"}},
				},
			},
			"middle": {
				OnEnter: []app.Effect{
					{When: "world.autofire", EmitIntent: "go"},
				},
				On: map[string][]app.Transition{
					"go": {{Target: "end"}},
				},
			},
			"end": {Terminal: true},
		},
	}
	m := mustNew(t, def)

	// Case 1: gate off — emit doesn't fire, state holds at middle.
	w := world.New()
	w.Vars["autofire"] = false
	res, err := m.Turn(context.Background(), "start", w, intent.IntentCall{Intent: "enter"})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("middle"), res.NewState, "gate off keeps state at middle")

	// Case 2: gate on — emit fires, state advances to end.
	w.Vars["autofire"] = true
	res, err = m.Turn(context.Background(), "start", w, intent.IntentCall{Intent: "enter"})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("end"), res.NewState, "gate on auto-fires through to end")
}

// TestEmitIntent_SlotPassThrough — emit slots reach the dispatched
// transition's effects as `slots.<name>`.
func TestEmitIntent_SlotPassThrough(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-slots"},
		Root: "start",
		World: map[string]app.VarDef{
			"captured": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {},
			"go":    {Slots: map[string]app.Slot{"feedback": {Type: "string"}}},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"enter": {{Target: "middle"}},
				},
			},
			"middle": {
				OnEnter: []app.Effect{
					{EmitIntent: "go", EmitSlots: map[string]any{"feedback": "carried-over"}},
				},
				On: map[string][]app.Transition{
					"go": {{Target: "end", Effects: []app.Effect{
						{Set: map[string]any{"captured": "{{ slots.feedback }}"}},
					}}},
				},
			},
			"end": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{Intent: "enter"})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("end"), res.NewState)
	require.Equal(t, "carried-over", res.World.Vars["captured"], "emit slots must reach the dispatched transition's effects")
}

// TestEmitIntent_MultiLevelChain — A.on_enter emits go_b; B.on_enter
// emits go_c; the turn settles at C.
func TestEmitIntent_MultiLevelChain(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-multi"},
		Root: "start",
		Intents: map[string]app.Intent{
			"enter": {},
			"go_b":  {},
			"go_c":  {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"enter": {{Target: "a"}},
				},
			},
			"a": {
				OnEnter: []app.Effect{{EmitIntent: "go_b"}},
				On: map[string][]app.Transition{
					"go_b": {{Target: "b"}},
				},
			},
			"b": {
				OnEnter: []app.Effect{{EmitIntent: "go_c"}},
				On: map[string][]app.Transition{
					"go_c": {{Target: "c"}},
				},
			},
			"c": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{Intent: "enter"})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("c"), res.NewState)

	// Event sequence should include three TransitionApplied entries
	// (one for the user-initiated enter, two synthetic for the emits).
	var transitions int
	for _, ev := range res.Events {
		if ev.Kind == store.TransitionApplied {
			transitions++
		}
	}
	require.Equal(t, 3, transitions, "expected 1 user + 2 synthetic TransitionApplied events")
}

// TestEmitIntent_TemplateValue — emit_intent value is a templated
// expression resolved at fire time against world. Mirrors the bugfix
// story's `emit_intent: "{{ world.llm_verdict.intent }}"` shape.
func TestEmitIntent_TemplateValue(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-template"},
		Root: "start",
		World: map[string]app.VarDef{
			"intent_name": {Type: "string", Default: "accept"},
		},
		Intents: map[string]app.Intent{
			"enter":  {},
			"accept": {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"enter": {{Target: "checkpoint"}},
				},
			},
			"checkpoint": {
				OnEnter: []app.Effect{
					{EmitIntent: "{{ world.intent_name }}"},
				},
				On: map[string][]app.Transition{
					"accept": {{Target: "done"}},
				},
			},
			"done": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))
	res, err := m.Turn(context.Background(), "start", w, intent.IntentCall{Intent: "enter"})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("done"), res.NewState)
}

// TestEmitIntent_EmptyTemplateRendersToNoop — when the template renders
// to an empty string (e.g. the verdict-intent slot is unset), no
// dispatch happens — the state simply holds.
func TestEmitIntent_EmptyTemplateRendersToNoop(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-empty"},
		Root: "start",
		World: map[string]app.VarDef{
			"intent_name": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter":  {},
			"accept": {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"enter": {{Target: "checkpoint"}},
				},
			},
			"checkpoint": {
				OnEnter: []app.Effect{
					{EmitIntent: "{{ world.intent_name }}"},
				},
				On: map[string][]app.Transition{
					"accept": {{Target: "done"}},
				},
			},
			"done": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{Intent: "enter"})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("checkpoint"), res.NewState, "empty-after-render is a no-op")
}

// TestEmitIntent_DepthCap — A.on_enter emits go_b; B.on_enter emits
// go_a; the ping-pong saturates the dispatcher and the surrounding
// Turn fails loud once EmitIntentMaxDepth is reached.
func TestEmitIntent_DepthCap(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-cycle"},
		Root: "start",
		Intents: map[string]app.Intent{
			"go":   {},
			"go_a": {},
			"go_b": {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"go": {{Target: "a"}},
				},
			},
			"a": {
				OnEnter: []app.Effect{{EmitIntent: "go_b"}},
				On: map[string][]app.Transition{
					"go_b": {{Target: "b"}},
					"go_a": {{Target: "a"}},
				},
			},
			"b": {
				OnEnter: []app.Effect{{EmitIntent: "go_a"}},
				On: map[string][]app.Transition{
					"go_a": {{Target: "a"}},
					"go_b": {{Target: "b"}},
				},
			},
		},
	}
	m := mustNew(t, def)
	_, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{Intent: "go"})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "max depth"), "error must mention depth cap: %v", err)
}

// TestEmitIntent_OnTransitionEffect — emit_intent is allowed on a
// transition's effects (not just on_enter); the chain settles on the
// final state of the synthetic dispatch.
func TestEmitIntent_OnTransitionEffect(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-trans"},
		Root: "start",
		Intents: map[string]app.Intent{
			"go":   {},
			"auto": {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"go": {{
						Target: "middle",
						Effects: []app.Effect{
							{EmitIntent: "auto"},
						},
					}},
					"auto": {{Target: "end"}},
				},
			},
			"middle": {
				// no on_enter; the emit was on the transition itself.
				On: map[string][]app.Transition{
					"auto": {{Target: "end"}},
				},
			},
			"end": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{Intent: "go"})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("end"), res.NewState, "transition-effect emit dispatches against the post-transition leaf")
}

// TestEmitIntent_ResolvesThroughSingleImportAlias — the LLM emits a
// bare intent name (`accept`) while the active state sits inside one
// import alias wrapper. The state's `on:` map has the alias-prefixed
// arc (`bf__accept`); the dispatcher must resolve the bare name via
// State.IntentAliases (populated by the rewriter) and dispatch the
// renamed form. Models the dev-story → bugfix bug fixed by Wave 3 /
// Phase 6 §W6.6 #1.
func TestEmitIntent_ResolvesThroughSingleImportAlias(t *testing.T) {
	// Hand-construct the post-fold AppDef so the test exercises the
	// runtime fix in isolation from the loader. The shape mirrors what
	// the rewriter would produce for one fold under alias `bf`.
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-one-alias"},
		Root: "bf",
		Intents: map[string]app.Intent{
			"bf__accept": {},
		},
		States: map[string]*app.State{
			"bf": {
				Type:    "compound",
				Initial: "checkpoint",
				States: map[string]*app.State{
					"checkpoint": {
						On: map[string][]app.Transition{
							"bf__accept": {{Target: "../../end"}},
						},
						// What the rewriter would record after fold.
						IntentAliases: map[string]string{
							"accept": "bf__accept",
						},
					},
				},
			},
			"end": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	newState, _, _, _, _, err := m.RunEffectsAndState(context.Background(), "bf.checkpoint", world.New(), []app.Effect{{EmitIntent: "accept"}})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("end"), newState, "bare `accept` must resolve through IntentAliases to `bf__accept`")
}

// TestEmitIntent_ResolvesThroughNestedImportAliases — two-layer fold
// (e.g. dev-story imports bugfix as `bf`; kitsoki-dev imports
// dev-story as `core`). Active state is inside `core.bf.<leaf>` and
// the rewriter has chained the alias map so `accept` resolves to
// `core__bf__accept`.
func TestEmitIntent_ResolvesThroughNestedImportAliases(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-nested-alias"},
		Root: "core",
		Intents: map[string]app.Intent{
			"core__bf__accept": {},
		},
		States: map[string]*app.State{
			"core": {
				Type:    "compound",
				Initial: "bf",
				States: map[string]*app.State{
					"bf": {
						Type:    "compound",
						Initial: "checkpoint",
						States: map[string]*app.State{
							"checkpoint": {
								On: map[string][]app.Transition{
									"core__bf__accept": {{Target: "../../../end"}},
								},
								// After two folds, IntentAliases holds
								// both intermediate and final spellings.
								IntentAliases: map[string]string{
									"accept":     "core__bf__accept",
									"bf__accept": "core__bf__accept",
								},
							},
						},
					},
				},
			},
			"end": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	newState, _, _, _, _, err := m.RunEffectsAndState(context.Background(), "core.bf.checkpoint", world.New(), []app.Effect{{EmitIntent: "accept"}})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("end"), newState, "bare `accept` must resolve through chained IntentAliases to `core__bf__accept`")

	// And the intermediate name resolves too (operator might emit it).
	newState2, _, _, _, _, err2 := m.RunEffectsAndState(context.Background(), "core.bf.checkpoint", world.New(), []app.Effect{{EmitIntent: "bf__accept"}})
	require.NoError(t, err2)
	require.Equal(t, app.StatePath("end"), newState2, "intermediate `bf__accept` must also resolve to `core__bf__accept`")
}

// TestEmitIntent_StandaloneNoAliasMap — at a state with no
// IntentAliases (standalone story, no imports), the bare emit name is
// dispatched verbatim. Back-compat for every story that doesn't use
// imports.
func TestEmitIntent_StandaloneNoAliasMap(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-standalone"},
		Root: "checkpoint",
		Intents: map[string]app.Intent{
			"accept": {},
		},
		States: map[string]*app.State{
			"checkpoint": {
				On: map[string][]app.Transition{
					"accept": {{Target: "end"}},
				},
			},
			"end": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	newState, _, _, _, _, err := m.RunEffectsAndState(context.Background(), "checkpoint", world.New(), []app.Effect{{EmitIntent: "accept"}})
	require.NoError(t, err)
	require.Equal(t, app.StatePath("end"), newState, "bare `accept` dispatches verbatim at a standalone state")
}

// TestEmitIntent_NonexistentNameInsideAliasMapIsNoArm — when the LLM
// emits a name that exists nowhere (no alias entry, no bare arc), the
// dispatcher surfaces the "no transition arm matched" error — same
// loud-failure behaviour as before the fix.
func TestEmitIntent_NonexistentNameInsideAliasMapIsNoArm(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "emit-missing"},
		Root: "bf",
		Intents: map[string]app.Intent{
			"bf__accept": {},
		},
		States: map[string]*app.State{
			"bf": {
				Type:    "compound",
				Initial: "checkpoint",
				States: map[string]*app.State{
					"checkpoint": {
						On: map[string][]app.Transition{
							"bf__accept": {{Target: "../../end"}},
						},
						IntentAliases: map[string]string{
							"accept": "bf__accept",
						},
					},
				},
			},
			"end": {Terminal: true},
		},
	}
	m := mustNew(t, def)
	_, _, _, _, _, err := m.RunEffectsAndState(context.Background(), "bf.checkpoint", world.New(), []app.Effect{{EmitIntent: "nonexistent_intent"}})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no transition arm matched"), "unknown emit must fail loud: %v", err)
}
