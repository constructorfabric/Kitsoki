// Package machine_test contains unit tests for the machine package.
// Each test uses a hand-crafted minimal AppDef to stay small and focused.
package machine_test

import (
	"context"
	"testing"

	"hally/internal/app"
	"hally/internal/intent"
	"hally/internal/machine"
	"hally/internal/store"
	"hally/internal/world"

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
				View: "You are at the start.",
				On: map[string][]app.Transition{
					"proceed": {
						{Target: "finish"},
					},
				},
			},
			"finish": {
				Terminal: true,
				View:     "You have finished.",
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
			"dest_a":       {View: "Destination A"},
			"dest_b":       {View: "Destination B"},
			"dest_default": {View: "Default destination"},
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
				Initial: "{{ world.lit ? 'bright' : 'dim' }}",
				States: map[string]*app.State{
					"bright": {View: "It's bright here."},
					"dim":    {View: "It's dim here."},
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

// ─── parallel state rejection ─────────────────────────────────────────────────

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
	require.Contains(t, err.Error(), "parallel")
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
			"north_room": {View: "North room."},
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
