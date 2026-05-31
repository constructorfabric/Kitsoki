// Runnable godoc examples for the machine package. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/machine/...`, so the docs cannot drift
// from behaviour.
package machine_test

import (
	"context"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// ExampleNew is the package's worked example in code: a two-state app driven
// one turn. The "proceed" intent fires the only transition, the machine lands
// on the terminal "finish" state, and the target state's view is rendered.
func ExampleNew() {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "demo"},
		Root: "start",
		Intents: map[string]app.Intent{
			"proceed": {Title: "Proceed"},
		},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("You are at the start."),
				On: map[string][]app.Transition{
					"proceed": {{Target: "finish"}},
				},
			},
			"finish": {
				Terminal: true,
				View:     app.LegacyView("You have finished."),
			},
		},
	}

	m, err := machine.New(def)
	if err != nil {
		panic(err)
	}

	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{
		Intent: "proceed",
		Slots:  world.Slots{},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("new_state:", res.NewState)
	fmt.Println("view:     ", res.View)
	fmt.Println("rejected: ", res.ValidationError != nil)
	// Output:
	// new_state: finish
	// view:      You have finished.
	// rejected:  false
}

// ExampleMachine_menu shows the read-only menu surface on a guarded room.
// The "go" intent has a when:-guarded arm (north) and a default: arm; the
// machine reports the intent as allowed and TryGuards resolves the destination
// for the matching slot — all without mutating state or world.
func ExampleMachine_menu() {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "demo"},
		Root: "room",
		Intents: map[string]app.Intent{
			"go": {Slots: map[string]app.Slot{
				"direction": {Type: "enum", Values: []string{"north", "south"}, Required: true},
			}},
		},
		States: map[string]*app.State{
			"room": {
				On: map[string][]app.Transition{
					"go": {
						{When: "slots.direction == 'north'", Target: "north_room"},
						{Default: true, Target: "room"},
					},
				},
			},
			"north_room": {View: app.LegacyView("North room.")},
		},
	}

	m, err := machine.New(def)
	if err != nil {
		panic(err)
	}
	w := world.New()

	// The intent is offered in this state.
	for _, ai := range m.AllowedIntents("room", w) {
		fmt.Println("allowed:", ai.Name)
	}

	// A dry-run of the north arm resolves its destination without transitioning.
	res := m.TryGuards("room", w, "go", map[string]any{"direction": "north"})
	fmt.Println("primary:    ", res.Primary)
	fmt.Println("destination:", res.DestinationHint)
	// Output:
	// allowed: go
	// primary:     true
	// destination: north_room
}

// ExampleIsParallelPath shows the parallel state-path encoding helpers. A
// parallel state path carries the parallel root plus each region's leaf,
// joined by the "#" / "|" sigils; IsParallelPath detects that form and
// StripParallel recovers the structural parent for terminal/lookup checks.
func ExampleIsParallelPath() {
	plain := app.StatePath("bar.lit")
	parallel := app.StatePath("world_clock#world_clock.calendar.day1|world_clock.weather.dry")

	fmt.Println("plain is parallel:   ", machine.IsParallelPath(plain))
	fmt.Println("parallel is parallel:", machine.IsParallelPath(parallel))
	fmt.Println("structural parent:   ", machine.StripParallel(parallel))
	// Output:
	// plain is parallel:    false
	// parallel is parallel: true
	// structural parent:    world_clock
}
