// Runnable godoc examples for the store surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/store/...`.
package store_test

import (
	"context"
	"encoding/json"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// ExampleStore is the canonical worked example from the package doc: open an
// in-memory store, create a session, append one turn of two events, load the
// history back (observing the store-assigned seq), and fold it into a journey.
func ExampleStore() {
	ctx := context.Background()
	def := &app.AppDef{App: app.AppMeta{ID: "demo", Version: "v0"}}

	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	defer st.Close()

	sid, err := st.CreateSession(ctx, def)
	if err != nil {
		panic(err)
	}

	// Append one turn. Seq is left at zero by the caller; the store assigns
	// dense 0..n-1 seq within the turn.
	if err := st.AppendEvents(sid, []store.Event{
		{Turn: 1, Kind: store.TurnStarted},
		{Turn: 1, Kind: store.TransitionApplied,
			Payload: json.RawMessage(`{"to":"river.scouting"}`)},
	}); err != nil {
		panic(err)
	}

	hist, err := st.LoadHistory(sid)
	if err != nil {
		panic(err)
	}
	for _, ev := range hist {
		fmt.Printf("turn=%d seq=%d kind=%s\n", ev.Turn, ev.Seq, ev.Kind)
	}

	js, err := store.BuildJourney(def, "river.start", world.New(), hist)
	if err != nil {
		panic(err)
	}
	fmt.Printf("state=%s turn=%d\n", js.State, js.Turn)
	// Output:
	// turn=1 seq=0 kind=turn.start
	// turn=1 seq=1 kind=machine.transition
	// state=river.scouting turn=1
}

// ExampleBuildJourney shows the replay fold applying an EffectApplied world
// mutation: an increment lands on the reconstructed world without any live
// machine in the loop.
func ExampleBuildJourney() {
	def := &app.AppDef{App: app.AppMeta{ID: "demo", Version: "v0"}}

	history := store.History{
		{Turn: 1, Seq: 0, Kind: store.TransitionApplied,
			Payload: json.RawMessage(`{"to":"trail.day_2"}`)},
		{Turn: 1, Seq: 1, Kind: store.EffectApplied,
			Payload: json.RawMessage(`{"increment":{"miles":18}}`)},
	}

	js, err := store.BuildJourney(def, "trail.day_1", world.New(), history)
	if err != nil {
		panic(err)
	}
	fmt.Printf("state=%s miles=%v\n", js.State, js.World.Vars["miles"])
	// Output:
	// state=trail.day_2 miles=18
}
