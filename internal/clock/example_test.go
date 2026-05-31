// Runnable godoc examples for the clock package. Each Example function's
// // Output: block is compiled and checked by
// `go test -run "^Example" ./internal/clock/...`, so these cannot drift
// from the package's real behaviour.
package clock_test

import (
	"fmt"
	"time"

	"kitsoki/internal/clock"
)

// ExampleFake shows the core test pattern: construct a Fake at a known
// instant, hand the Clock to the code under test, then drive time forward
// deterministically with Advance — no real sleeping, no flake.
func ExampleFake() {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)

	ch := clk.After(5 * time.Minute)

	// Nothing has fired yet — the fake clock only moves when we move it.
	select {
	case <-ch:
		fmt.Println("fired early?!")
	default:
		fmt.Println("not yet")
	}

	clk.Advance(5 * time.Minute)
	fired := <-ch
	fmt.Println("elapsed:", fired.Sub(start))
	// Output:
	// not yet
	// elapsed: 5m0s
}

// ExampleFake_BlockUntil shows the race-free handoff for code that parks
// on the clock from another goroutine. BlockUntil waits until the worker
// has actually registered its After/Sleep/timer before the test advances
// time, so the test never advances into a goroutine that hasn't armed its
// wait yet.
func ExampleFake_BlockUntil() {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	done := make(chan string, 1)

	go func() {
		clk.Sleep(time.Hour) // worker parks on the fake clock
		done <- "worker woke"
	}()

	clk.BlockUntil(1) // wait until the worker is parked, race-free
	clk.Advance(time.Hour)
	fmt.Println(<-done)
	// Output:
	// worker woke
}
