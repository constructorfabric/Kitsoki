// Runnable godoc examples for the [history.Stack] surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/history/...`.
package history_test

import (
	"fmt"

	"kitsoki/internal/history"
)

// ExampleStack is the canonical worked example from the package doc: two
// room visits are pushed with their on-arrival slots, then unwound by
// back, with the final empty-stack back falling through to the
// construction-time main room.
func ExampleStack() {
	s := history.New("main_room")

	s.Push("general_store", map[string]any{"budget": 240})
	s.Push("wagon_inventory", map[string]any{"tab": "food"})

	state, slots, ok := s.Pop()
	fmt.Printf("pop: %-15s slots=%v ok=%v\n", state, slots, ok)

	state, slots, ok = s.Pop()
	fmt.Printf("pop: %-15s slots=%v ok=%v\n", state, slots, ok)

	// Empty stack: back falls through to the main room with ok=false.
	state, slots, ok = s.Pop()
	fmt.Printf("pop: %-15s slots=%v ok=%v\n", state, slots, ok)
	// Output:
	// pop: wagon_inventory slots=map[tab:food] ok=true
	// pop: general_store   slots=map[budget:240] ok=true
	// pop: main_room       slots=map[] ok=false
}

// ExampleStack_bounded shows the depth bound: pushing more than the
// stack's maximum evicts the OLDEST entries, so the most recent
// navigation always survives.
func ExampleStack_bounded() {
	s := history.New("main_room")
	for i := 0; i < 14; i++ {
		s.Push("room", nil)
	}
	top, _ := s.Peek()
	fmt.Println("depth:", s.Len())
	fmt.Println("top:  ", top.State)
	// Output:
	// depth: 10
	// top:   room
}
