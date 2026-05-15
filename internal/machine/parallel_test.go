// parallel_test.go covers `type: parallel` runtime semantics
// (proposal §9.4).  See parallel.go for the design notes.
package machine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/world"

	"github.com/stretchr/testify/require"
)

// makeParallelApp builds a small two-region parallel state.
//
//   - region `calendar` (compound, initial: day) ticks via the `tick` intent.
//   - region `weather` (compound, initial: dry) flips on `make_it_rain`
//     intent; emits `precip_heavy` on the dry→rain transition.
//   - region `calendar` also has `on: precip_heavy` to advance into `soggy`.
//
// The parallel parent lives under root `outside` (compound) to exercise
// nested entry from a compound parent.
func makeParallelApp() *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "parallel-test"},
		Root: "outside",
		World: map[string]app.VarDef{
			"emit_count":    {Type: "int", Default: 0},
			"weather_state": {Type: "string", Default: "dry"},
		},
		Intents: map[string]app.Intent{
			"enter":         {Title: "Enter"},
			"tick":          {Title: "Tick"},
			"make_it_rain":  {Title: "Make it rain"},
			"precip_heavy":  {Title: "Precipitation"},
			"leave":         {Title: "Leave"},
		},
		States: map[string]*app.State{
			"outside": {
				View: app.LegacyView("Outside"),
				On: map[string][]app.Transition{
					"enter": {{Target: "world_clock"}},
				},
			},
			"world_clock": {
				Type: "parallel",
				View: app.LegacyView("World clock — header."),
				OnEnter: []app.Effect{
					{Set: map[string]any{"entered_parallel": true}},
				},
				States: map[string]*app.State{
					"calendar": {
						Type:    "compound",
						Initial: "day",
						OnEnter: []app.Effect{
							{Set: map[string]any{"calendar_entered": true}},
						},
						States: map[string]*app.State{
							"day": {
								View: app.LegacyView("Calendar: day."),
								OnEnter: []app.Effect{
									{Set: map[string]any{"calendar_leaf_entered": true}},
								},
								On: map[string][]app.Transition{
									"tick": {{
										Target:  "../night",
										Effects: []app.Effect{{Increment: map[string]int{"emit_count": 0}}},
									}},
									"precip_heavy": {{
										Target: "../soggy",
										Effects: []app.Effect{
											{Set: map[string]any{"calendar_saw_emit": true}},
										},
									}},
								},
							},
							"night": {View: app.LegacyView("Calendar: night.")},
							"soggy": {View: app.LegacyView("Calendar: soggy.")},
						},
					},
					"weather": {
						Type:    "compound",
						Initial: "dry",
						States: map[string]*app.State{
							"dry": {
								View: app.LegacyView("Weather: dry."),
								On: map[string][]app.Transition{
									"make_it_rain": {{
										Target: "../rain",
										Effects: []app.Effect{
											{Set: map[string]any{"weather_state": "rain"}},
											{Emit: "precip_heavy"},
										},
									}},
								},
							},
							"rain": {View: app.LegacyView("Weather: rain.")},
						},
					},
				},
				On: map[string][]app.Transition{
					"leave": {{Target: "../outside"}},
				},
			},
		},
	}
}

// TestParallelStateInitialEntry: entering a parallel state from outside
// resolves each region to its initial leaf and fires on_enter for both
// the parallel parent AND each region.
func TestParallelStateInitialEntry(t *testing.T) {
	def := makeParallelApp()
	m := mustNew(t, def)
	w := world.New()

	res, err := m.Turn(context.Background(), "outside", w, intent.IntentCall{
		Intent: "enter",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)

	// The new state must be parallel-encoded with both region leaves.
	require.True(t, machine.IsParallelPath(res.NewState),
		"expected parallel-encoded state, got %q", res.NewState)
	require.Contains(t, string(res.NewState), "world_clock.calendar.day")
	require.Contains(t, string(res.NewState), "world_clock.weather.dry")

	// on_enter fired on the parent and on the entered regions.
	require.Equal(t, true, res.World.Vars["entered_parallel"])
	require.Equal(t, true, res.World.Vars["calendar_entered"])
	require.Equal(t, true, res.World.Vars["calendar_leaf_entered"])

	// StateEntered events fired for parent and every leaf chain.
	enteredPaths := collectStateEntered(res.Events)
	require.Contains(t, enteredPaths, "world_clock")
	require.Contains(t, enteredPaths, "world_clock.calendar")
	require.Contains(t, enteredPaths, "world_clock.calendar.day")
	require.Contains(t, enteredPaths, "world_clock.weather")
	require.Contains(t, enteredPaths, "world_clock.weather.dry")
}

// TestParallelIntentDispatchFirstRegionWins: an intent only handled by one
// region fires that region's transition without affecting the other.
func TestParallelIntentDispatchFirstRegionWins(t *testing.T) {
	def := makeParallelApp()
	m := mustNew(t, def)
	w := world.New()

	// Enter the parallel state.
	res, err := m.Turn(context.Background(), "outside", w, intent.IntentCall{
		Intent: "enter", Slots: world.Slots{},
	})
	require.NoError(t, err)
	startState := res.NewState

	// `tick` is only handled by `calendar`.  Should move calendar.day → calendar.night
	// and leave weather alone.
	res, err = m.Turn(context.Background(), startState, res.World, intent.IntentCall{
		Intent: "tick", Slots: world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.True(t, machine.IsParallelPath(res.NewState))
	require.Contains(t, string(res.NewState), "world_clock.calendar.night")
	require.Contains(t, string(res.NewState), "world_clock.weather.dry") // unchanged
}

// TestParallelEmitPropagationCrossRegion: a transition that emits an event
// fires a matching binding in *every other* region, not the emitting one.
func TestParallelEmitPropagationCrossRegion(t *testing.T) {
	def := makeParallelApp()
	m := mustNew(t, def)
	w := world.New()

	// Enter parallel.
	res, err := m.Turn(context.Background(), "outside", w, intent.IntentCall{
		Intent: "enter", Slots: world.Slots{},
	})
	require.NoError(t, err)

	// `make_it_rain` is in the weather region; on the transition it emits
	// `precip_heavy`.  The calendar region's day-state has a binding for
	// `precip_heavy` → soggy.
	res, err = m.Turn(context.Background(), res.NewState, res.World, intent.IntentCall{
		Intent: "make_it_rain", Slots: world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)

	// Weather moved to rain.
	require.Contains(t, string(res.NewState), "world_clock.weather.rain")
	// Calendar reacted to the emit and moved to soggy.
	require.Contains(t, string(res.NewState), "world_clock.calendar.soggy")
	// World flag for the cross-region emit was set.
	require.Equal(t, true, res.World.Vars["calendar_saw_emit"])
	// world.weather_state was updated by the weather transition.
	require.Equal(t, "rain", res.World.Vars["weather_state"])
}

// TestParallelEmitDoesNotSelfTrigger: the emitting region's own bindings
// for that event MUST NOT fire (to avoid infinite ping-pong loops).
func TestParallelEmitDoesNotSelfTrigger(t *testing.T) {
	// Build a degenerate app where the weather region ALSO has a binding
	// for its own `precip_heavy` emit — we expect that binding to be
	// IGNORED so the weather stays in rain (not bounced back to dry).
	def := &app.AppDef{
		App:   app.AppMeta{ID: "self-emit-test"},
		Root:  "pair",
		World: map[string]app.VarDef{"self_trigger": {Type: "bool", Default: false}},
		Intents: map[string]app.Intent{
			"go":          {},
			"precip_heavy": {},
		},
		States: map[string]*app.State{
			"pair": {
				Type: "parallel",
				States: map[string]*app.State{
					"a": {
						Type:    "compound",
						Initial: "x",
						States: map[string]*app.State{
							"x": {
								On: map[string][]app.Transition{
									"go": {{
										Target: "../y",
										Effects: []app.Effect{{Emit: "precip_heavy"}},
									}},
									// SELF-binding for the emitted event.
									"precip_heavy": {{
										Target: ".",
										Effects: []app.Effect{
											{Set: map[string]any{"self_trigger": true}},
										},
									}},
								},
							},
							"y": {},
						},
					},
					"b": {
						Type:    "compound",
						Initial: "z",
						States: map[string]*app.State{
							"z": {},
						},
					},
				},
			},
		},
	}
	m := mustNew(t, def)
	// Seed straight into the parallel state.
	startPath := app.StatePath("pair#pair.a.x|pair.b.z")
	w := world.World{Vars: map[string]any{"self_trigger": false}}

	res, err := m.Turn(context.Background(), startPath, w, intent.IntentCall{
		Intent: "go", Slots: world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	// The emit MUST NOT have ricocheted back into region a.
	require.Equal(t, false, res.World.Vars["self_trigger"])
	require.Contains(t, string(res.NewState), "pair.a.y")
}

// TestParallelEmitDepthCap: a cyclic emit must error rather than loop.
func TestParallelEmitDepthCap(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "cycle-test"},
		Root: "pair",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"start": {},
			"ping":  {},
			"pong":  {},
		},
		States: map[string]*app.State{
			"pair": {
				Type: "parallel",
				States: map[string]*app.State{
					"a": {
						Type:    "compound",
						Initial: "idle",
						States: map[string]*app.State{
							"idle": {
								On: map[string][]app.Transition{
									"start": {{
										Target:  ".",
										Effects: []app.Effect{{Emit: "ping"}},
									}},
									"pong": {{
										Target:  ".",
										Effects: []app.Effect{{Emit: "ping"}}, // ricochet
									}},
								},
							},
						},
					},
					"b": {
						Type:    "compound",
						Initial: "idle",
						States: map[string]*app.State{
							"idle": {
								On: map[string][]app.Transition{
									"ping": {{
										Target:  ".",
										Effects: []app.Effect{{Emit: "pong"}}, // ricochet
									}},
								},
							},
						},
					},
				},
			},
		},
	}
	m := mustNew(t, def)
	startPath := app.StatePath("pair#pair.a.idle|pair.b.idle")
	w := world.New()

	_, err := m.Turn(context.Background(), startPath, w, intent.IntentCall{
		Intent: "start", Slots: world.Slots{},
	})
	require.Error(t, err, "expected emit-depth cap to trip on a→b→a→b… cycle")
	require.Contains(t, err.Error(), "max depth")
}

// TestParallelLoadCleanlyNoInitialOnParent: the parallel parent loads OK
// without an `initial:` field.  Sanity check that the rejection lift is
// in place — under the old code New() would have errored.
func TestParallelLoadCleanlyNoInitialOnParent(t *testing.T) {
	def := makeParallelApp()
	_, err := machine.New(def)
	require.NoError(t, err)
}

// TestParallelValidationRejectsBadShapes verifies the New()-time validation
// catches malformed parallel states.
func TestParallelValidationRejectsBadShapes(t *testing.T) {
	t.Run("single-region", func(t *testing.T) {
		def := &app.AppDef{
			App:   app.AppMeta{ID: "bad"},
			Root:  "x",
			States: map[string]*app.State{
				"x": {
					Type: "parallel",
					States: map[string]*app.State{
						"only_one": {Type: "compound", Initial: "leaf", States: map[string]*app.State{"leaf": {}}},
					},
				},
			},
		}
		_, err := machine.New(def)
		require.Error(t, err)
		require.Contains(t, err.Error(), "at least 2 child regions")
	})

	t.Run("parent-has-initial", func(t *testing.T) {
		def := &app.AppDef{
			App:  app.AppMeta{ID: "bad"},
			Root: "x",
			States: map[string]*app.State{
				"x": {
					Type:    "parallel",
					Initial: "r1",
					States: map[string]*app.State{
						"r1": {Type: "compound", Initial: "l", States: map[string]*app.State{"l": {}}},
						"r2": {Type: "compound", Initial: "l", States: map[string]*app.State{"l": {}}},
					},
				},
			},
		}
		_, err := machine.New(def)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must not declare an initial")
	})

	t.Run("nested-parallel", func(t *testing.T) {
		def := &app.AppDef{
			App:  app.AppMeta{ID: "bad"},
			Root: "x",
			States: map[string]*app.State{
				"x": {
					Type: "parallel",
					States: map[string]*app.State{
						"r1": {
							Type: "parallel",
							States: map[string]*app.State{
								"a": {Type: "compound", Initial: "l", States: map[string]*app.State{"l": {}}},
								"b": {Type: "compound", Initial: "l", States: map[string]*app.State{"l": {}}},
							},
						},
						"r2": {Type: "compound", Initial: "l", States: map[string]*app.State{"l": {}}},
					},
				},
			},
		}
		_, err := machine.New(def)
		require.Error(t, err)
		require.Contains(t, err.Error(), "nested parallel")
	})
}

// TestParallelViewComposition: the rendered view stacks the parent header
// (if any) above each region leaf view, separated by blank lines.
func TestParallelViewComposition(t *testing.T) {
	def := makeParallelApp()
	m := mustNew(t, def)
	w := world.New()

	res, err := m.Turn(context.Background(), "outside", w, intent.IntentCall{
		Intent: "enter", Slots: world.Slots{},
	})
	require.NoError(t, err)
	require.Contains(t, res.View, "World clock — header.")
	require.Contains(t, res.View, "Calendar: day.")
	require.Contains(t, res.View, "Weather: dry.")
	// Ordering: parent header first.
	require.Less(t, strings.Index(res.View, "World clock"), strings.Index(res.View, "Calendar:"))
}

// TestParallelExitToOuterState: a transition declared on the parallel
// parent itself (via `on:` lookup walking ancestors) escapes the parallel
// state.  Verifies the encoded path drops back to a plain dotted path.
func TestParallelExitToOuterState(t *testing.T) {
	def := makeParallelApp()
	m := mustNew(t, def)
	w := world.New()

	res, err := m.Turn(context.Background(), "outside", w, intent.IntentCall{
		Intent: "enter", Slots: world.Slots{},
	})
	require.NoError(t, err)
	require.True(t, machine.IsParallelPath(res.NewState))

	// `leave` is declared on the parallel parent — should walk the region
	// ancestor chain and find it.
	res, err = m.Turn(context.Background(), res.NewState, res.World, intent.IntentCall{
		Intent: "leave", Slots: world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.False(t, machine.IsParallelPath(res.NewState))
	require.Equal(t, app.StatePath("outside"), res.NewState)
}

// collectStateEntered walks the events and returns the "state" payload
// of every StateEntered event.  Helper for tests.
func collectStateEntered(events []store.Event) []string {
	var out []string
	for _, ev := range events {
		if ev.Kind != store.StateEntered {
			continue
		}
		var p struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		out = append(out, p.State)
	}
	return out
}
