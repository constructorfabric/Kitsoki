// Package clock provides an injectable time source for the kitsoki runtime.
// It sits between the packages that need to track elapsed time (the
// scheduler's debounce loops, the testrunner's advance-and-wait drain, the
// clarification poll loops) and the tests that verify them: production code
// depends on the [Clock] interface and is wired with [Real], while tests wire
// a [*Fake] they can drive forward by hand.
//
// # Algorithm
//
// There are two implementations behind one interface.
//
// [Real] is a thin shim over the standard library — every method forwards
// directly to the matching time.* function, so it carries no state and adds
// no behaviour of its own.
//
// [Fake] is where the design lives. It holds a single "now" instant and a
// slice of pending waiters. Every blocking primitive (After, Sleep, NewTimer,
// NewTicker) registers a waiter carrying a deadline and a capacity-1 channel,
// then returns immediately — nothing fires until a test moves time. When
// [Fake.Advance] or [Fake.Set] moves now forward, the clock walks its waiters
// and fires every one whose deadline is at or before the new now:
//
//   - One-shot waiters (After/Sleep/NewTimer) fire once and are removed.
//   - Tickers fire once per whole period that elapsed, then reschedule their
//     deadline to the next period boundary and stay registered.
//
// Each fire is a non-blocking send on the capacity-1 channel: if the receiver
// has not drained the previous value the send is dropped, matching
// time.Ticker's coalescing semantics. A separate waiter count, signalled
// through a [sync.Cond], lets a test block until a known number of goroutines
// have actually parked on the clock ([Fake.BlockUntil]) — the race-free point
// from which it is safe to Advance.
//
// # Invariants
//
//   - Fake time only moves on an explicit [Fake.Advance] or [Fake.Set]; the
//     clock never advances on its own. Tests are fully in control of time.
//   - Fake time is monotonic. [Fake.Set] panics on a backward move; [Fake.Advance]
//     takes a duration and is expected to be non-negative.
//   - The waiter count is never negative. Stop/Reset and fireExpired coordinate
//     so a stopped waiter is decremented exactly once.
//   - Channels handed out by a Fake have capacity 1 and are written with a
//     non-blocking send, so Advance never blocks on a slow receiver.
//
// # Worked example
//
// A test arms a 5-minute timeout on a Fake, observes that nothing has fired,
// advances exactly 5 minutes, and reads the fired instant off the channel:
//
//	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
//	clk := clock.NewFake(start)
//	ch := clk.After(5 * time.Minute)
//
//	// now == start; the waiter's deadline == start+5m; nothing fired:
//	select {
//	case <-ch:  // not taken
//	default:    // "not yet"
//	}
//
//	clk.Advance(5 * time.Minute) // now == start+5m, deadline reached
//	fired := <-ch                // receives start+5m
//	// fired.Sub(start) == 5m0s
//
// A runnable form of this trace lives in [ExampleFake]; the cross-goroutine
// BlockUntil handoff is shown in [ExampleFake_BlockUntil].
//
// # Lifecycle
//
// Use [Real] everywhere in production code that needs to track wall-clock time
// (scheduler debounces, clarification poll loops, etc.). Never call
// time.Now, time.After, time.Sleep, and friends directly in packages that need
// to be tested deterministically; accept a [Clock] instead and inject [Real]
// at the call site.
//
// Use [NewFake] in tests. A [*Fake] gives you:
//
//   - [Fake.Advance] and [Fake.Set] to move time forward synchronously.
//   - [Fake.BlockUntil] (and [Fake.BlockUntilContext]) to wait until a known
//     number of goroutines are blocked on this clock, giving a race-free point
//     from which to Advance.
//
// Timers and tickers returned by a [*Fake] use buffered channels of capacity 1
// so Advance is non-blocking: if the receiving goroutine has not consumed the
// previous tick yet, the new one is dropped (same semantics as time.Ticker).
//
// # Non-goals
//
//   - Auto-advancing. A Fake must be driven by test logic, never by a
//     background goroutine, so that test outcomes stay deterministic and free
//     of timing flake.
//   - Scheduling. The clock does not order, prioritise, or coalesce events
//     beyond firing each waiter at its exact deadline; it is a time source, not
//     a job runner.
//   - Synchronous re-entry. Handlers fired during Advance/Set must not call
//     back into the same clock from the firing goroutine, because firing holds
//     the clock's lock; re-entry is intentionally unsupported rather than
//     reentrant-locked (see [Fake.Advance]).
//   - Wall-clock fidelity in tests. A Fake's [Fake.Now] reflects only what
//     tests have advanced it to; it has no relationship to real elapsed time.
//
// # Reference
//
// The testrunner's advance-and-wait drain — the principal consumer of
// [Fake.WaitCount] and [Fake.BlockUntilContext] — is documented under
// docs/architecture. There is no separate spec; this package is its own
// reference.
package clock
