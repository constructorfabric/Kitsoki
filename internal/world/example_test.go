// Runnable godoc examples for the [World] and [Slots] surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/world/...`.
package world_test

import (
	"fmt"

	"kitsoki/internal/world"
)

// ExampleNew shows that a fresh World starts empty: Get on any key returns
// nil because nothing has been written yet.
func ExampleNew() {
	w := world.New()
	fmt.Printf("%v\n", w.Get("miles"))
	// Output:
	// <nil>
}

// ExampleWorld_Get reads a value back out of a populated snapshot, and shows
// that an absent key is indistinguishable from a key set to nil.
func ExampleWorld_Get() {
	w := world.New().With("party_size", 5)
	fmt.Println("party_size:", w.Get("party_size"))
	fmt.Println("rations:   ", w.Get("rations"))
	// Output:
	// party_size: 5
	// rations:    <nil>
}

// ExampleWorld_With is the copy-on-write worked example from the package doc:
// each With returns a new snapshot and leaves the earlier ones untouched.
func ExampleWorld_With() {
	w0 := world.New()
	w1 := w0.With("miles", 0)
	w2 := w1.With("miles", 18)

	fmt.Println("w0.miles:", w0.Get("miles"))
	fmt.Println("w1.miles:", w1.Get("miles"))
	fmt.Println("w2.miles:", w2.Get("miles"))
	// Output:
	// w0.miles: <nil>
	// w1.miles: 0
	// w2.miles: 18
}

// ExampleSlots_MarshalJSON shows the stable wire shape: a populated Slots
// marshals as a JSON object with sorted keys.
func ExampleSlots_MarshalJSON() {
	s := world.Slots{"items": "6 oxen", "total_cost": 240}
	b, err := s.MarshalJSON()
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
	// Output:
	// {"items":"6 oxen","total_cost":240}
}
