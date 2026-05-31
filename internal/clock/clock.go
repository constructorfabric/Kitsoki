package clock

import (
	"context"
	"sync"
	"time"
)

// ─── interfaces ──────────────────────────────────────────────────────────────

// Clock is an injectable time source.  Production code should depend on this
// interface; tests inject a *Fake.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Since returns the elapsed time since t (equivalent to Now().Sub(t)).
	Since(t time.Time) time.Duration
	// After returns a channel that will receive the current time after d has
	// elapsed.  The channel is buffered with capacity 1.
	After(d time.Duration) <-chan time.Time
	// Sleep blocks the calling goroutine for d.
	Sleep(d time.Duration)
	// NewTimer creates a new Timer that will fire after duration d.
	NewTimer(d time.Duration) Timer
	// NewTicker creates a new Ticker that fires on every period d.
	NewTicker(d time.Duration) Ticker
}

// Timer mirrors the subset of *time.Timer needed by the runtime.
type Timer interface {
	// C returns the channel on which the timer fires.
	C() <-chan time.Time
	// Stop prevents the timer from firing.  Returns true if the timer was
	// stopped before it fired, false if it had already expired.
	Stop() bool
	// Reset resets the timer to fire after d.  Returns true if the timer was
	// active and was successfully reset before firing.
	Reset(d time.Duration) bool
}

// Ticker mirrors the subset of *time.Ticker needed by the runtime.
type Ticker interface {
	// C returns the channel on which the ticker fires.
	C() <-chan time.Time
	// Stop turns off the ticker.
	Stop()
	// Reset resets the ticker period to d.
	Reset(d time.Duration)
}

// ─── real clock ──────────────────────────────────────────────────────────────

// realClock wraps the standard library time functions.
type realClock struct{}

// Real returns a Clock backed by the standard library time package.
// Use this everywhere in production code.
func Real() Clock { return realClock{} }

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) Sleep(d time.Duration)                  { time.Sleep(d) }
func (realClock) NewTimer(d time.Duration) Timer         { return &realTimer{t: time.NewTimer(d)} }
func (realClock) NewTicker(d time.Duration) Ticker       { return &realTicker{t: time.NewTicker(d)} }

// realTimer wraps *time.Timer.
type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time        { return r.t.C }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

// realTicker wraps *time.Ticker.
type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time   { return r.t.C }
func (r *realTicker) Stop()                 { r.t.Stop() }
func (r *realTicker) Reset(d time.Duration) { r.t.Reset(d) }

// ─── fake clock ──────────────────────────────────────────────────────────────

// waiter represents a pending After/Sleep/Timer/Ticker registration.
type waiter struct {
	deadline time.Time
	// ch receives the time value when fired.  For tickers period > 0.
	ch     chan time.Time
	period time.Duration // >0 for tickers
	// stopped is set to true when Stop is called on a timer/ticker.
	stopped bool
}

// Fake is a deterministic clock for testing.  Unlike the real clock it does
// not advance on its own; callers drive time forward with Advance or Set.
//
// A Fake must be constructed with [NewFake]; the zero value is not usable,
// because its condition variable is nil and every method that touches the
// waiter count would panic. Do not copy a Fake after first use — it carries a
// [sync.Mutex] and a [sync.Cond] bound to that mutex.
//
// All methods are safe for concurrent use once the Fake has been built by
// NewFake.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
	// waitCount is the number of goroutines currently blocked waiting on
	// After/Sleep/NewTimer channels.  Tickers count once when registered.
	waitCount int
	// cond is signalled whenever waitCount changes, so BlockUntil can wake.
	cond *sync.Cond
}

// NewFake creates a Fake clock starting at start.
func NewFake(start time.Time) *Fake {
	f := &Fake{now: start}
	f.cond = sync.NewCond(&f.mu)
	return f
}

// Now returns the current fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Since returns the elapsed time since t according to the fake clock.
func (f *Fake) Since(t time.Time) time.Duration {
	return f.Now().Sub(t)
}

// After returns a channel that will receive the fake time after d has elapsed
// on the fake clock.  The channel is buffered (capacity 1); if the receiver
// isn't ready when Advance fires the event, the send is dropped.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	w := &waiter{deadline: f.now.Add(d), ch: ch}
	f.waiters = append(f.waiters, w)
	f.waitCount++
	f.cond.Broadcast()
	return ch
}

// Sleep blocks until d has elapsed on the fake clock.
func (f *Fake) Sleep(d time.Duration) {
	ch := f.After(d)
	<-ch
}

// NewTimer creates a fake Timer that fires after d.
func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	w := &waiter{deadline: f.now.Add(d), ch: ch}
	f.waiters = append(f.waiters, w)
	f.waitCount++
	f.cond.Broadcast()
	return &fakeTimer{f: f, w: w}
}

// NewTicker creates a fake Ticker that fires every period d.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	w := &waiter{deadline: f.now.Add(d), ch: ch, period: d}
	f.waiters = append(f.waiters, w)
	f.waitCount++
	f.cond.Broadcast()
	return &fakeTicker{f: f, w: w}
}

// Advance moves the fake clock forward by d and fires all pending waiters
// whose deadline is ≤ the new time.  For tickers, each elapsed period fires
// one send (with overflow drops).  Safe to call from any goroutine.
//
// Re-entry contract: waiters fire while Advance holds the clock's lock, so a
// handler that receives a fired tick must not call back into this same clock
// (After, Sleep, NewTimer, NewTicker, Advance, Set) from the firing goroutine
// — doing so deadlocks. If a handler needs to re-arm the clock, it must do so
// from a separate goroutine.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.fireExpired()
	f.mu.Unlock()
}

// Set moves the fake clock to t (must be ≥ Now(); panics if t is before the
// current fake time) and fires all pending waiters whose deadline ≤ t.
//
// Set carries the same re-entry contract as [Fake.Advance]: waiters fire under
// the clock's lock, so a fired handler must not call back into this clock from
// the firing goroutine or it will deadlock.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	if t.Before(f.now) {
		f.mu.Unlock()
		panic("clock.Fake.Set: cannot move clock backwards")
	}
	f.now = t
	f.fireExpired()
	f.mu.Unlock()
}

// BlockUntil blocks until at least n goroutines are registered as waiters on
// this clock (via After, Sleep, NewTimer, or NewTicker).  This is the
// race-free primitive that lets a test advance the clock with confidence that
// the goroutines it's waiting for have already called into the clock.
//
//	f.BlockUntil(1) // wait until the goroutine under test is parked on f.After(...)
//	f.Advance(200 * time.Millisecond)
func (f *Fake) BlockUntil(n int) {
	f.mu.Lock()
	for f.waitCount < n {
		f.cond.Wait()
	}
	f.mu.Unlock()
}

// WaitCount returns the current number of registered waiters (goroutines
// blocked on After/Sleep, or holding a live Timer/Ticker).  This is a
// non-blocking snapshot intended for drain-loop coordination — see e.g. the
// testrunner's advanceAndWait, which must wait for handler goroutines to be
// parked on the clock before advancing time.
func (f *Fake) WaitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waitCount
}

// BlockUntilContext is the context-aware form of BlockUntil: it blocks until at
// least n goroutines are registered as waiters on this clock OR ctx is
// cancelled.  Returns ctx.Err() in the latter case, nil otherwise.
//
// Unlike BlockUntil, BlockUntilContext does not park indefinitely if the
// expected number of waiters is never reached (e.g. a handler that returned
// before calling into the clock); callers can use a short outer context to
// re-evaluate the wait count.
func (f *Fake) BlockUntilContext(ctx context.Context, n int) error {
	// A goroutine watches ctx.Done and broadcasts on the cond so the wait loop
	// below can re-check ctx.Err and exit.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			f.mu.Lock()
			f.cond.Broadcast()
			f.mu.Unlock()
		case <-stop:
		}
	}()

	f.mu.Lock()
	defer f.mu.Unlock()
	for f.waitCount < n {
		if err := ctx.Err(); err != nil {
			return err
		}
		f.cond.Wait()
	}
	return nil
}

// fireExpired fires all waiters whose deadline ≤ f.now.  Must be called with
// f.mu held.
//
// Note: fireExpired runs entirely under f.mu.  If a fired timer's channel
// receiver calls back into f.After, f.Sleep, f.NewTimer, or f.NewTicker from
// the same goroutine, it will deadlock on f.mu.  Handlers must not re-enter
// the clock synchronously; use a separate goroutine if re-entry is needed.
func (f *Fake) fireExpired() {
	now := f.now
	var remaining []*waiter
	for _, w := range f.waiters {
		if w.stopped {
			// Already stopped — Stop() already decremented waitCount when it set
			// w.stopped, so we must NOT decrement again here. Just discard the
			// waiter without touching waitCount; doing otherwise would make
			// waitCount go negative and break BlockUntil.
			continue
		}
		if !w.deadline.After(now) {
			// Fire this waiter.
			select {
			case w.ch <- now:
			default:
				// Channel full; drop.
			}
			if w.period > 0 {
				// Ticker: calculate how many periods fit between original deadline and now.
				// We already sent one above; fire additional ticks for each extra period elapsed.
				extra := int(now.Sub(w.deadline) / w.period)
				for i := 0; i < extra; i++ {
					select {
					case w.ch <- now:
					default:
					}
				}
				// Reschedule to the next boundary.
				w.deadline = w.deadline.Add(w.period * time.Duration(extra+1))
				remaining = append(remaining, w)
			} else {
				// One-shot timer/after: decrement waiter count.
				f.waitCount--
				f.cond.Broadcast()
			}
		} else {
			remaining = append(remaining, w)
		}
	}
	f.waiters = remaining
}

// ─── fakeTimer ───────────────────────────────────────────────────────────────

type fakeTimer struct {
	f *Fake
	w *waiter
}

func (t *fakeTimer) C() <-chan time.Time { return t.w.ch }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if t.w.stopped {
		return false
	}
	t.w.stopped = true
	// waitCount will be decremented when fireExpired next sees this waiter.
	// Decrement here proactively so BlockUntil doesn't count stopped timers.
	t.f.waitCount--
	t.f.cond.Broadcast()
	return true
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	wasActive := !t.w.stopped
	t.w.stopped = false
	t.w.deadline = t.f.now.Add(d)
	if !wasActive {
		t.f.waitCount++
		t.f.cond.Broadcast()
	}
	// Drain any pending send so Reset starts clean.
	select {
	case <-t.w.ch:
	default:
	}
	return wasActive
}

// ─── fakeTicker ──────────────────────────────────────────────────────────────

type fakeTicker struct {
	f *Fake
	w *waiter
}

func (t *fakeTicker) C() <-chan time.Time { return t.w.ch }

func (t *fakeTicker) Stop() {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if !t.w.stopped {
		t.w.stopped = true
		t.f.waitCount--
		t.f.cond.Broadcast()
	}
}

func (t *fakeTicker) Reset(d time.Duration) {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	wasActive := !t.w.stopped
	t.w.stopped = false
	t.w.period = d
	t.w.deadline = t.f.now.Add(d)
	if !wasActive {
		t.f.waitCount++
		t.f.cond.Broadcast()
	}
	select {
	case <-t.w.ch:
	default:
	}
}
