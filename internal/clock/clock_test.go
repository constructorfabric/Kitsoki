package clock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"kitsoki/internal/clock"
)

// ─── Real clock smoke test ───────────────────────────────────────────────────

func TestRealClock_AfterFiresAfterSleep(t *testing.T) {
	clk := clock.Real()
	start := clk.Now()

	ch := clk.After(20 * time.Millisecond)
	select {
	case fired := <-ch:
		if fired.Before(start) {
			t.Fatalf("fired time %v is before start %v", fired, start)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("real clock After did not fire")
	}
}

func TestRealClock_Since(t *testing.T) {
	clk := clock.Real()
	before := clk.Now()
	time.Sleep(5 * time.Millisecond)
	d := clk.Since(before)
	if d < 5*time.Millisecond {
		t.Fatalf("Since returned %v, expected >= 5ms", d)
	}
}

// ─── Fake clock: After / Advance ────────────────────────────────────────────

func TestFakeClock_After(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ch := f.After(100 * time.Millisecond)

	// Should not fire before advance.
	select {
	case <-ch:
		t.Fatal("After fired before Advance")
	default:
	}

	f.Advance(100 * time.Millisecond)

	select {
	case got := <-ch:
		want := time.Unix(0, 0).Add(100 * time.Millisecond)
		if !got.Equal(want) {
			t.Fatalf("After fired with %v, want %v", got, want)
		}
	default:
		t.Fatal("After did not fire after Advance")
	}
}

func TestFakeClock_AdvancePartial(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ch := f.After(200 * time.Millisecond)

	f.Advance(100 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("After fired too early")
	default:
	}

	f.Advance(100 * time.Millisecond)
	select {
	case <-ch:
		// OK
	default:
		t.Fatal("After did not fire after second Advance")
	}
}

// ─── Fake clock: Sleep ───────────────────────────────────────────────────────

func TestFakeClock_Sleep(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))

	done := make(chan struct{})
	go func() {
		f.Sleep(50 * time.Millisecond)
		close(done)
	}()

	f.BlockUntil(1)
	f.Advance(50 * time.Millisecond)

	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("Sleep did not unblock after Advance")
	}
}

// ─── Fake clock: NewTimer (incl. Reset) ──────────────────────────────────────

func TestFakeClock_NewTimer(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	timer := f.NewTimer(100 * time.Millisecond)

	select {
	case <-timer.C():
		t.Fatal("timer fired before Advance")
	default:
	}

	f.Advance(100 * time.Millisecond)
	select {
	case <-timer.C():
		// OK
	default:
		t.Fatal("timer did not fire after Advance")
	}
}

func TestFakeClock_Timer_Stop(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	timer := f.NewTimer(100 * time.Millisecond)

	active := timer.Stop()
	if !active {
		t.Fatal("Stop should return true for an active timer")
	}

	f.Advance(200 * time.Millisecond)
	select {
	case <-timer.C():
		t.Fatal("stopped timer should not fire")
	default:
		// OK
	}
}

func TestFakeClock_Timer_Reset(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	timer := f.NewTimer(100 * time.Millisecond)

	// Stop then reset to a longer duration.
	timer.Stop()
	timer.Reset(200 * time.Millisecond)

	f.Advance(100 * time.Millisecond)
	select {
	case <-timer.C():
		t.Fatal("timer fired before reset deadline")
	default:
	}

	f.Advance(100 * time.Millisecond)
	select {
	case <-timer.C():
		// OK
	default:
		t.Fatal("timer did not fire after reset deadline")
	}
}

// ─── Fake clock: NewTicker (incl. multiple fires per Advance) ───────────────

func TestFakeClock_NewTicker_SingleFire(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ticker := f.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	f.Advance(100 * time.Millisecond)
	select {
	case <-ticker.C():
		// OK
	default:
		t.Fatal("ticker did not fire after one period")
	}
}

func TestFakeClock_NewTicker_MultipleFiresPerAdvance(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ticker := f.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Advance 3 full periods; channel is buffered=1, so we get 1 send but
	// the extra ticks are dropped (same as real time.Ticker overflow semantics).
	f.Advance(300 * time.Millisecond)

	// Must have at least one tick available.
	fired := 0
	drain:
	for {
		select {
		case <-ticker.C():
			fired++
		default:
			break drain
		}
	}
	if fired == 0 {
		t.Fatal("ticker should have fired at least once for 3 periods")
	}
}

func TestFakeClock_NewTicker_Reschedules(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ticker := f.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for i := 0; i < 3; i++ {
		f.Advance(100 * time.Millisecond)
		select {
		case <-ticker.C():
			// OK
		default:
			t.Fatalf("ticker did not fire on period %d", i+1)
		}
	}
}

func TestFakeClock_Ticker_Stop(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ticker := f.NewTicker(100 * time.Millisecond)
	ticker.Stop()

	f.Advance(500 * time.Millisecond)
	select {
	case <-ticker.C():
		t.Fatal("stopped ticker should not fire")
	default:
		// OK
	}
}

func TestFakeClock_Ticker_Reset(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ticker := f.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	ticker.Reset(100 * time.Millisecond)

	f.Advance(100 * time.Millisecond)
	select {
	case <-ticker.C():
		// OK — new period applied.
	default:
		t.Fatal("ticker did not fire after Reset to shorter period")
	}
}

// ─── Fake clock: BlockUntil ───────────────────────────────────────────────────

func TestFakeClock_BlockUntil(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.Sleep(50 * time.Millisecond)
		}()
	}

	// BlockUntil(3) guarantees all 3 goroutines are parked before we advance.
	f.BlockUntil(3)
	f.Advance(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("not all Sleep goroutines woke up")
	}
}

// ─── Fake clock: concurrent waiters ──────────────────────────────────────────

func TestFakeClock_ConcurrentWaiters(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		d := time.Duration(i+1) * 10 * time.Millisecond
		go func(dur time.Duration) {
			defer wg.Done()
			f.Sleep(dur)
		}(d)
	}

	f.BlockUntil(n)
	// Advance past all deadlines in one shot.
	f.Advance(time.Duration(n+1) * 10 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent waiters did not all wake up")
	}
}

// ─── Fake clock: Set ─────────────────────────────────────────────────────────

func TestFakeClock_Set(t *testing.T) {
	base := time.Unix(1000, 0)
	f := clock.NewFake(base)
	ch := f.After(500 * time.Millisecond)

	target := base.Add(time.Second)
	f.Set(target)

	if !f.Now().Equal(target) {
		t.Fatalf("Now() = %v, want %v", f.Now(), target)
	}
	select {
	case <-ch:
		// OK
	default:
		t.Fatal("After did not fire after Set")
	}
}

func TestFakeClock_Set_Panic(t *testing.T) {
	f := clock.NewFake(time.Unix(1000, 0))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Set backwards should panic")
		}
	}()
	f.Set(time.Unix(0, 0))
}

// TestFakeClock_StopThenAdvance_WaitCountNotNegative is a regression test for
// the double-decrement bug: Stop() decrements waitCount immediately; if
// fireExpired also decremented for the same stopped waiter, waitCount would
// go negative and BlockUntil(n) would never unblock for subsequent waiters.
//
// Sequence:
//  1. NewTimer(100ms)  → waitCount 0→1
//  2. Stop()           → waitCount 1→0
//  3. Advance(200ms)   → fireExpired sees the stopped waiter; must NOT decrement
//  4. BlockUntil(0)    → always satisfied; we assert indirectly via Sleep below
//  5. Spawn Sleep(50ms) → waitCount 0→1
//  6. BlockUntil(1)    → must unblock immediately (waitCount==1, not -1)
//  7. Advance(50ms)    → fires Sleep; goroutine exits
func TestFakeClock_StopThenAdvance_WaitCountNotNegative(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	timer := f.NewTimer(100 * time.Millisecond)

	// Stop the timer — waitCount drops 1→0.
	if !timer.Stop() {
		t.Fatal("Stop should return true for an active timer")
	}

	// Advance past the stopped timer's deadline.  fireExpired must skip the
	// stopped waiter without decrementing waitCount (it's already 0).
	f.Advance(200 * time.Millisecond)

	// Now park a fresh Sleep waiter and use BlockUntil to confirm waitCount
	// is 1 (not ≤0 from a double-decrement).  If waitCount were -1, BlockUntil(1)
	// would never return.
	done := make(chan struct{})
	go func() {
		f.Sleep(50 * time.Millisecond)
		close(done)
	}()

	// BlockUntil(1) must return — the goroutine incremented waitCount to 1.
	// A negative waitCount would prevent this from ever satisfying.
	f.BlockUntil(1)

	// Fire the Sleep and let the goroutine exit cleanly.
	f.Advance(50 * time.Millisecond)
	select {
	case <-done:
		// OK — waitCount was not corrupted.
	case <-time.After(time.Second):
		t.Fatal("goroutine did not exit — waitCount appears corrupted (may be negative)")
	}
}

// ─── Fake clock: WaitCount snapshot ──────────────────────────────────────────

// TestFakeClock_WaitCount asserts that WaitCount tracks the number of live
// waiters across After registrations, expirations, and Stop calls.
func TestFakeClock_WaitCount(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))

	if got := f.WaitCount(); got != 0 {
		t.Fatalf("initial WaitCount: got %d want 0", got)
	}

	// Two After waiters → WaitCount==2.
	chA := f.After(50 * time.Millisecond)
	chB := f.After(150 * time.Millisecond)
	if got := f.WaitCount(); got != 2 {
		t.Fatalf("after 2 After: got %d want 2", got)
	}

	// Advance past A only — WaitCount drops to 1.
	f.Advance(50 * time.Millisecond)
	<-chA
	if got := f.WaitCount(); got != 1 {
		t.Fatalf("after A fires: got %d want 1", got)
	}

	// Advance past B — WaitCount drops to 0.
	f.Advance(150 * time.Millisecond)
	<-chB
	if got := f.WaitCount(); got != 0 {
		t.Fatalf("after B fires: got %d want 0", got)
	}

	// Now create a Timer and Stop it: WaitCount goes 0→1→0.
	tm := f.NewTimer(100 * time.Millisecond)
	if got := f.WaitCount(); got != 1 {
		t.Fatalf("after NewTimer: got %d want 1", got)
	}
	if !tm.Stop() {
		t.Fatal("Stop should return true for an active timer")
	}
	if got := f.WaitCount(); got != 0 {
		t.Fatalf("after Stop: got %d want 0", got)
	}
}

// ─── Fake clock: BlockUntilContext ───────────────────────────────────────────

// TestFakeClock_BlockUntilContext_AlreadySatisfied asserts that
// BlockUntilContext returns nil immediately when waitCount already meets n.
func TestFakeClock_BlockUntilContext_AlreadySatisfied(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	_ = f.After(50 * time.Millisecond)
	_ = f.After(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("BlockUntilContext when already satisfied: %v", err)
	}
}

// TestFakeClock_BlockUntilContext_BlocksAndReleases verifies the call blocks
// until enough waiters register, then returns nil.
func TestFakeClock_BlockUntilContext_BlocksAndReleases(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- f.BlockUntilContext(ctx, 1)
	}()

	// Goroutine should still be blocked — no waiters yet.
	select {
	case err := <-done:
		t.Fatalf("BlockUntilContext returned early: err=%v", err)
	case <-time.After(20 * time.Millisecond):
		// Still blocked — OK.
	}

	// Register a waiter; BlockUntilContext should unblock.
	_ = f.After(time.Second)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("BlockUntilContext: unexpected err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("BlockUntilContext did not unblock after waiter registered")
	}
}

// TestFakeClock_BlockUntilContext_CtxCancelled verifies that a cancelled
// context causes BlockUntilContext to return ctx.Err() (and not park forever).
func TestFakeClock_BlockUntilContext_CtxCancelled(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call.

	err := f.BlockUntilContext(ctx, 5)
	if err == nil {
		t.Fatal("expected ctx.Err(), got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestFakeClock_BlockUntilContext_NSatisfied verifies BlockUntilContext
// satisfies the exact count threshold, and that asking for more than registered
// blocks until the deadline.
func TestFakeClock_BlockUntilContext_NSatisfied(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))

	for i := 0; i < 3; i++ {
		_ = f.After(time.Second)
	}

	// Asking for 3 returns immediately.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.BlockUntilContext(ctx, 3); err != nil {
		t.Fatalf("BlockUntilContext(ctx, 3): %v", err)
	}

	// Asking for 4 with no further waiters must block until the deadline.
	short, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	err := f.BlockUntilContext(short, 4)
	if err == nil {
		t.Fatal("expected timeout error when n exceeds registered waiters")
	}
}
