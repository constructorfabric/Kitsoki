// Package machine_test contains unit tests for the machine package.
// Each test uses a hand-crafted minimal AppDef to stay small and focused.
package machine_test

import (
	"context"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/world"

	"github.com/stretchr/testify/require"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func mustNew(t *testing.T, def *app.AppDef) machine.Machine {
	t.Helper()
	m, err := machine.New(def)
	require.NoError(t, err)
	return m
}

func ptr[T any](v T) *T { return &v }

// ─── (a) simple linear transition ────────────────────────────────────────────

func TestSimpleLinearTransition(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "start",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"proceed": {Title: "Proceed"},
		},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("You are at the start."),
				On: map[string][]app.Transition{
					"proceed": {
						{Target: "finish"},
					},
				},
			},
			"finish": {
				Terminal: true,
				View:     app.LegacyView("You have finished."),
			},
		},
	}

	m := mustNew(t, def)
	w := world.New()

	res, err := m.Turn(context.Background(), "start", w, intent.IntentCall{
		Intent: "proceed",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Equal(t, app.StatePath("finish"), res.NewState)
	require.Contains(t, res.View, "You have finished")

	// Events should contain TransitionApplied.
	found := false
	for _, ev := range res.Events {
		if ev.Kind == store.TransitionApplied {
			found = true
		}
	}
	require.True(t, found, "TransitionApplied event must be present")
}

// ─── (b) first-guard-wins with multiple when: branches ───────────────────────

func TestFirstGuardWins(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "room",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"pick": {Slots: map[string]app.Slot{
				"choice": {Type: "string", Required: true},
			}},
		},
		States: map[string]*app.State{
			"room": {
				On: map[string][]app.Transition{
					"pick": {
						{
							When:   "slots.choice == 'a'",
							Target: "dest_a",
						},
						{
							When:   "slots.choice == 'b'",
							Target: "dest_b",
						},
						{
							Default: true,
							Target:  "dest_default",
						},
					},
				},
			},
			"dest_a":       {View: app.LegacyView("Destination A")},
			"dest_b":       {View: app.LegacyView("Destination B")},
			"dest_default": {View: app.LegacyView("Default destination")},
		},
	}

	m := mustNew(t, def)
	w := world.New()

	cases := []struct {
		choice   string
		wantDest string
	}{
		{"a", "dest_a"},
		{"b", "dest_b"},
		{"c", "dest_default"},
	}

	for _, tc := range cases {
		t.Run(tc.choice, func(t *testing.T) {
			res, err := m.Turn(context.Background(), "room", w, intent.IntentCall{
				Intent: "pick",
				Slots:  world.Slots{"choice": tc.choice},
			})
			require.NoError(t, err)
			require.Nil(t, res.ValidationError)
			require.Equal(t, app.StatePath(tc.wantDest), res.NewState)
		})
	}
}

// ─── (c) missing-slots error shape ───────────────────────────────────────────

func TestMissingSlots(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "start",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"go": {Slots: map[string]app.Slot{
				"direction": {Type: "enum", Values: []string{"north", "south"}, Required: true},
			}},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"go": {{Target: "start"}},
				},
			},
		},
	}

	m := mustNew(t, def)
	w := world.New()

	// Call without required slot.
	res, err := m.Turn(context.Background(), "start", w, intent.IntentCall{
		Intent: "go",
		Slots:  world.Slots{}, // no direction
	})
	require.NoError(t, err)
	require.NotNil(t, res.ValidationError)
	require.Equal(t, intent.ErrMissingSlots, res.ValidationError.Code)
	require.Contains(t, res.ValidationError.MissingSlots, "direction")
	// State must not change.
	require.Equal(t, app.StatePath("start"), res.NewState)
}

// ─── (c) invalid enum slot value ─────────────────────────────────────────────

func TestInvalidSlotValue(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "start",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"go": {Slots: map[string]app.Slot{
				"direction": {Type: "enum", Values: []string{"north", "south"}, Required: true},
			}},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"go": {{Target: "start"}},
				},
			},
		},
	}

	m := mustNew(t, def)
	w := world.New()

	res, err := m.Turn(context.Background(), "start", w, intent.IntentCall{
		Intent: "go",
		Slots:  world.Slots{"direction": "diagonal"}, // not in enum
	})
	require.NoError(t, err)
	require.NotNil(t, res.ValidationError)
	require.Equal(t, intent.ErrInvalidSlotValue, res.ValidationError.Code)
}

// ─── (d) guard-rejected with guard_hint populated ────────────────────────────

func TestGuardRejectedWithHint(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "room",
		World: map[string]app.VarDef{"flag": {Type: "bool", Default: false}},
		Intents: map[string]app.Intent{
			"do_thing": {},
		},
		States: map[string]*app.State{
			"room": {
				On: map[string][]app.Transition{
					"do_thing": {
						{
							When:      "world.flag == true",
							Target:    "done",
							GuardHint: "You need the flag to be set first.",
						},
						// No default branch.
					},
				},
			},
			"done": {Terminal: true},
		},
	}

	m := mustNew(t, def)
	w := world.New()
	w.Vars["flag"] = false

	res, err := m.Turn(context.Background(), "room", w, intent.IntentCall{
		Intent: "do_thing",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.NotNil(t, res.ValidationError)
	require.Equal(t, intent.ErrGuardFailed, res.ValidationError.Code)
	require.Contains(t, res.ValidationError.GuardHint, "flag to be set")
	// State unchanged.
	require.Equal(t, app.StatePath("room"), res.NewState)
}

// ─── (e) compound-state entry resolves to initial: ───────────────────────────

func TestCompoundStateInitialResolution(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "lobby",
		World: map[string]app.VarDef{"lit": {Type: "bool", Default: true}},
		Intents: map[string]app.Intent{
			"enter": {},
		},
		States: map[string]*app.State{
			"lobby": {
				On: map[string][]app.Transition{
					"enter": {{Target: "compound"}},
				},
			},
			"compound": {
				Type:    "compound",
				Initial: "{% if world.lit %}bright{% else %}dim{% endif %}",
				States: map[string]*app.State{
					"bright": {View: app.LegacyView("It's bright here.")},
					"dim":    {View: app.LegacyView("It's dim here.")},
				},
			},
		},
	}

	m := mustNew(t, def)

	// world.lit = true → should resolve to compound.bright
	w := world.World{Vars: map[string]any{"lit": true}}
	res, err := m.Turn(context.Background(), "lobby", w, intent.IntentCall{
		Intent: "enter",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Equal(t, app.StatePath("compound.bright"), res.NewState)
	require.Contains(t, res.View, "bright")

	// world.lit = false → should resolve to compound.dim
	w.Vars["lit"] = false
	res, err = m.Turn(context.Background(), "lobby", w, intent.IntentCall{
		Intent: "enter",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Equal(t, app.StatePath("compound.dim"), res.NewState)
}

// ─── (f) unallowed intent produces correct error code ────────────────────────

func TestIntentNotAllowedInState(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "room_a",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"act_a": {},
			"act_b": {},
		},
		States: map[string]*app.State{
			"room_a": {
				On: map[string][]app.Transition{
					"act_a": {{Target: "room_a"}},
				},
			},
			"room_b": {
				On: map[string][]app.Transition{
					"act_b": {{Target: "room_b"}},
				},
			},
		},
	}

	m := mustNew(t, def)
	w := world.New()

	// act_b is not allowed in room_a.
	res, err := m.Turn(context.Background(), "room_a", w, intent.IntentCall{
		Intent: "act_b",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.NotNil(t, res.ValidationError)
	require.Equal(t, intent.ErrIntentNotAllowed, res.ValidationError.Code)
	require.Contains(t, res.ValidationError.AllowedIntents, "act_a")
	require.NotContains(t, res.ValidationError.AllowedIntents, "act_b")
	// State unchanged.
	require.Equal(t, app.StatePath("room_a"), res.NewState)

	// ValidationFailed event must be in result.
	found := false
	for _, ev := range res.Events {
		if ev.Kind == store.ValidationFailed {
			found = true
		}
	}
	require.True(t, found, "ValidationFailed event must be emitted")
}

// ─── world effects test ───────────────────────────────────────────────────────

func TestEffectsApplied(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "test"},
		Root: "start",
		World: map[string]app.VarDef{
			"counter": {Type: "int", Default: 0},
		},
		Intents: map[string]app.Intent{
			"tick": {},
		},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"tick": {
						{
							Target: "start",
							Effects: []app.Effect{
								{Increment: map[string]int{"counter": 1}},
							},
						},
					},
				},
			},
		},
	}

	m := mustNew(t, def)
	w := world.World{Vars: map[string]any{"counter": int64(0)}}

	res, err := m.Turn(context.Background(), "start", w, intent.IntentCall{
		Intent: "tick",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Equal(t, int64(1), res.World.Vars["counter"])

	// Run again from the new world.
	res2, err := m.Turn(context.Background(), "start", res.World, intent.IntentCall{
		Intent: "tick",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), res2.World.Vars["counter"])
}

// TestEffectInvokeWithTemplatedListArgs covers the recursive resolution of
// invoke `with:` values: a templated string nested inside a list (e.g.
// host.run's `args:`) must be rendered, not passed through verbatim.  This
// is the regression guard for the jira_search bug where
// `args: ["{{ world.jira_query }}"]` left the template unexpanded and the
// handler received the literal string `{{ world.jira_query }}`.
func TestEffectInvokeWithTemplatedListArgs(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "test"},
		Root: "start",
		World: map[string]app.VarDef{
			"q": {Type: "string", Default: "hello world"},
		},
		Intents: map[string]app.Intent{
			"go": {},
		},
		Hosts: []string{"host.run"},
		States: map[string]*app.State{
			"start": {
				On: map[string][]app.Transition{
					"go": {
						{
							Target: "start",
							Effects: []app.Effect{
								{
									Invoke: "host.run",
									With: map[string]any{
										"cmd": "python3",
										"args": []any{
											"script.py",
											"{{ world.q }}",
											"--limit",
											"25",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	m := mustNew(t, def)
	w := world.World{Vars: map[string]any{"q": "hello world"}}

	res, err := m.Turn(context.Background(), "start", w, intent.IntentCall{
		Intent: "go",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Len(t, res.HostCalls, 1, "expected one host invocation")

	hc := res.HostCalls[0]
	require.Equal(t, "host.run", hc.Namespace)
	require.Equal(t, "python3", hc.Args["cmd"])

	gotArgs, ok := hc.Args["args"].([]any)
	require.True(t, ok, "args should be []any, got %T", hc.Args["args"])
	require.Equal(t, []any{"script.py", "hello world", "--limit", "25"}, gotArgs)
}

// ─── parallel state rejection ─────────────────────────────────────────────────

// TestParallelStatesRejected — historical guard against the PoC restriction.
// After §9.4 lifts the bare-rejection, this test was reframed: an empty
// `type: parallel` state (no children) still fails, but on shape grounds
// (regions count) rather than a blanket "parallel not supported" error.
// The expanded parallel-state tests live in parallel_test.go.
func TestParallelStatesRejected(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "start",
		World: map[string]app.VarDef{},
		States: map[string]*app.State{
			"start": {Type: "parallel"},
		},
	}

	_, err := machine.New(def)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 2 child regions")
}

// ─── TryGuards MatchedDefault ────────────────────────────────────────────────

// TestTryGuardsMatchedDefault confirms that GuardDryRunResult.MatchedDefault is
// set when the only arm that fires is a default: branch, and is NOT set when a
// real when: guard matched.
func TestTryGuardsMatchedDefault(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "room",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"go": {Slots: map[string]app.Slot{
				"direction": {Type: "enum", Values: []string{"north", "south", "east"}, Required: true},
			}},
		},
		States: map[string]*app.State{
			"room": {
				On: map[string][]app.Transition{
					"go": {
						{
							When:   "slots.direction == 'north'",
							Target: "north_room",
						},
						{
							Default: true,
							Target:  "room",
						},
					},
				},
			},
			"north_room": {View: app.LegacyView("North room.")},
		},
	}

	m := mustNew(t, def)
	w := world.New()

	// "north" matches a real when: branch → Primary=true, MatchedDefault=false.
	res := m.TryGuards("room", w, "go", map[string]any{"direction": "north"})
	require.True(t, res.Primary, "north should be primary")
	require.False(t, res.MatchedDefault, "north matched a real when: branch, not default:")
	require.Equal(t, "north_room", res.DestinationHint)

	// "south" has no when: branch → only default: fires → MatchedDefault=true.
	res = m.TryGuards("room", w, "go", map[string]any{"direction": "south"})
	require.True(t, res.Primary, "south should be primary (default: fires)")
	require.True(t, res.MatchedDefault, "south only matched default: arm")

	// "east" same as south.
	res = m.TryGuards("room", w, "go", map[string]any{"direction": "east"})
	require.True(t, res.Primary, "east should be primary (default: fires)")
	require.True(t, res.MatchedDefault, "east only matched default: arm")
}

// ─── TestRunEffects ───────────────────────────────────────────────────────────

// TestRunEffects verifies Machine.RunEffects: a small chain of set + say +
// invoke (synchronous host call collected as HostInvocation) is applied and
// returns the expected world/sayText/hostCalls/effectEvents.
//
// RunEffects is the on_complete bridge entry-point. It must:
//   - Apply set effects (updates world).
//   - Collect say text.
//   - Collect HostInvocation entries for invoke effects (not dispatch them).
//   - Return EffectApplied events for set effects.
func TestRunEffects(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "run-effects-test"},
		Root:  "s",
		Hosts: []string{"host.noop"},
		World: map[string]app.VarDef{
			"counter": {Type: "integer", Default: 0},
			"label":   {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{},
		States: map[string]*app.State{
			"s": {View: app.LegacyView("state s")},
		},
	}
	m := mustNew(t, def)

	w := world.New()
	w.Vars["counter"] = 0
	w.Vars["label"] = ""

	effects := []app.Effect{
		{Set: map[string]any{"counter": 42, "label": "hi"}},
		{Say: "you said {{ world.label }}"},
		{Invoke: "host.noop", With: map[string]any{"arg": "val"}},
	}

	newWorld, hostCalls, sayText, evts, err := m.RunEffects(
		context.Background(), "s", w, effects,
	)
	require.NoError(t, err)

	// Set effects.
	require.Equal(t, 42, newWorld.Vars["counter"])
	require.Equal(t, "hi", newWorld.Vars["label"])

	// Say text interpolated against the updated world.
	require.Contains(t, sayText, "you said hi")

	// Invoke effect was collected as HostInvocation, not dispatched.
	require.Len(t, hostCalls, 1, "RunEffects should collect host calls, not dispatch them")
	require.Equal(t, "host.noop", hostCalls[0].Namespace)
	require.Equal(t, "val", hostCalls[0].Args["arg"])

	// EffectApplied events for the set effect.
	foundEffApplied := false
	for _, ev := range evts {
		if ev.Kind == store.EffectApplied {
			foundEffApplied = true
			break
		}
	}
	require.True(t, foundEffApplied, "EffectApplied event should be emitted for set effects")
}

// ─── Machine.Menu ────────────────────────────────────────────────────────────

// TestMenu_EnumExpansionPrimaryVsBlocked exercises the §7.2 menu computation
// inside the machine package (where it now lives). An intent with a required
// enum slot is expanded into per-value rows; rows whose guard dry-run fails
// surface in Blocked with the failing arm's guard_hint.
func TestMenu_EnumExpansionPrimaryVsBlocked(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "room",
		World: map[string]app.VarDef{"unlocked_north": {Type: "bool"}},
		Intents: map[string]app.Intent{
			"go": {Slots: map[string]app.Slot{
				"direction": {Type: "enum", Values: []string{"north", "south"}, Required: true},
			}},
		},
		States: map[string]*app.State{
			"room": {
				On: map[string][]app.Transition{
					"go": {
						{
							When:      "slots.direction == 'north' && world.unlocked_north",
							Target:    "north_room",
							GuardHint: "The north door is locked.",
						},
						{
							When:   "slots.direction == 'south'",
							Target: "south_room",
						},
					},
				},
			},
			"north_room": {},
			"south_room": {},
		},
	}
	m := mustNew(t, def)
	w := world.New()
	w.Vars["unlocked_north"] = false

	menu := m.Menu("room", w)

	// "go south" passes its when arm → primary.
	foundSouth := false
	for _, e := range menu.Primary {
		if e.Display == "go south" {
			foundSouth = true
			require.Equal(t, "south_room", e.DestinationHint)
		}
	}
	require.True(t, foundSouth, "go south should be in primary")

	// "go north" fails its when arm (unlocked_north=false) → blocked with hint.
	foundNorth := false
	for _, e := range menu.Blocked {
		if e.Display == "go north" {
			foundNorth = true
			require.Equal(t, "The north door is locked.", e.Reason)
		}
	}
	require.True(t, foundNorth, "go north should be in blocked")
}

// TestMenu_SlotlessIntentBlockedByGuard mirrors the OT intro / start_journey
// shape: an intent declared in the state with no required slots but with a
// when: arm that fails (and a default: catch-all) surfaces as a blocked row
// carrying the failing arm's guard_hint.
func TestMenu_SlotlessIntentBlockedByGuard(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "lobby",
		World: map[string]app.VarDef{"ready": {Type: "bool"}},
		Intents: map[string]app.Intent{
			"depart": {Description: "Depart the lobby."},
		},
		States: map[string]*app.State{
			"lobby": {
				On: map[string][]app.Transition{
					"depart": {
						{When: "world.ready", Target: "outside"},
						{Default: true, Target: "lobby", GuardHint: "Not ready to depart yet."},
					},
				},
			},
			"outside": {},
		},
	}
	m := mustNew(t, def)

	wNotReady := world.New()
	wNotReady.Vars["ready"] = false
	menu := m.Menu("lobby", wNotReady)

	var blocked *machine.MenuEntry
	for i := range menu.Blocked {
		if menu.Blocked[i].Intent == "depart" {
			blocked = &menu.Blocked[i]
		}
	}
	require.NotNil(t, blocked, "depart should be blocked when ready=false")
	require.Equal(t, "Not ready to depart yet.", blocked.Reason)

	// Flip ready=true and depart should now be primary.
	wReady := world.New()
	wReady.Vars["ready"] = true
	menu = m.Menu("lobby", wReady)
	foundPrimary := false
	for _, e := range menu.Primary {
		if e.Intent == "depart" {
			foundPrimary = true
		}
	}
	require.True(t, foundPrimary, "depart should be primary when ready=true")
}

// TestMenu_TemplateMapShape verifies the contract between machine.Menu and
// the view-template env: MenuToTemplateMap produces a map[string]any with
// "primary" and "blocked" lists whose elements are plain map[string]any
// carrying intent/display/reason/destination_hint/primary keys.
func TestMenu_TemplateMapShape(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "s",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"look": {Description: "Look."},
		},
		States: map[string]*app.State{
			"s": {
				On: map[string][]app.Transition{
					"look": {{Target: "s"}},
				},
			},
		},
	}
	m := mustNew(t, def)
	tm := machine.MenuToTemplateMap(m.Menu("s", world.New()))

	primary, ok := tm["primary"].([]any)
	require.True(t, ok, "primary key must be []any")
	require.NotEmpty(t, primary, "look should produce a primary entry")
	entry, ok := primary[0].(map[string]any)
	require.True(t, ok, "primary entries must be map[string]any")
	require.Equal(t, "look", entry["intent"])
	require.Equal(t, "look", entry["display"])
	require.Equal(t, true, entry["primary"])

	_, ok = tm["blocked"].([]any)
	require.True(t, ok, "blocked key must be []any even when empty")
}
