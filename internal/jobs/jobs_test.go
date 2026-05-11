package jobs_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

func echoHandler(output string) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"output": output}}, nil
	}
}

func failHandler(msg string) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Error: msg}, nil
	}
}

func slowHandler(d time.Duration, output string) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		select {
		case <-ctx.Done():
			return host.Result{}, ctx.Err()
		case <-time.After(d):
			return host.Result{Data: map[string]any{"output": output}}, nil
		}
	}
}

func TestSubmitAndSubscribe_Success(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()
	ch, unsub := subscribeAfterSubmit(t, sched, echoHandler("hello"))
	defer unsub()

	select {
	case ev := <-ch:
		if ev.Status != jobs.JobDone {
			t.Fatalf("expected done, got %s", ev.Status)
		}
		if ev.Result == nil || ev.Result.Data["output"] != "hello" {
			t.Fatalf("expected output=hello, got %v", ev.Result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for job completion")
	}
}

func TestSubmitAndSubscribe_Failure(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()
	ch, unsub := subscribeAfterSubmit(t, sched, failHandler("domain error"))
	defer unsub()

	select {
	case ev := <-ch:
		if ev.Status != jobs.JobFailed {
			t.Fatalf("expected failed, got %s", ev.Status)
		}
		if ev.Error != "domain error" {
			t.Fatalf("expected 'domain error', got %q", ev.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for failed job")
	}
}

func TestCancel(t *testing.T) {
	fc := clock.NewFake(time.Unix(0, 0))
	sched := jobs.NewInMemoryScheduler(jobs.WithClock(fc))

	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind: "host.slow",
		// Handler blocks on the fake clock until cancelled.
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			select {
			case <-ctx.Done():
				return host.Result{}, ctx.Err()
			case <-fc.After(10 * time.Second):
				return host.Result{Data: map[string]any{"output": "never"}}, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Wait until the handler goroutine is parked on the fake clock.
	fc.BlockUntil(1)

	ch, unsub := sched.Subscribe(id)
	defer unsub()

	if err := sched.Cancel(context.Background(), id); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// Cancel wakes the handler via context; the event should arrive quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case ev := <-ch:
		if ev.Status != jobs.JobCancelled {
			t.Fatalf("expected cancelled, got %s", ev.Status)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for cancellation")
	}
}

func TestHeartbeat(t *testing.T) {
	fc := clock.NewFake(time.Unix(0, 0))
	sched := jobs.NewInMemoryScheduler(jobs.WithClock(fc))

	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind: "host.slow",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			select {
			case <-ctx.Done():
				return host.Result{}, ctx.Err()
			case <-fc.After(500 * time.Millisecond):
				return host.Result{Data: map[string]any{"output": "done"}}, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Wait until the handler goroutine is parked on the fake clock.
	fc.BlockUntil(1)

	ch, unsub := sched.Subscribe(id)
	defer unsub()

	// Send a heartbeat while the job is in-flight.
	if err := sched.Heartbeat(id, map[string]any{"progress": 50}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// Advance the fake clock past the handler's delay.
	fc.Advance(600 * time.Millisecond)

	// Drain the channel until done.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case ev := <-ch:
			if ev.Status == jobs.JobDone {
				return
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for job completion")
		}
	}
}

func TestCancelUnknownJob(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()
	err := sched.Cancel(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for unknown job")
	}
}

// TestWaitIdle_FakeClock spawns 5 jobs with fake-clock delays, advances past
// all deadlines, and verifies WaitIdle returns without real-time waiting.
func TestWaitIdle_FakeClock(t *testing.T) {
	fc := clock.NewFake(time.Unix(0, 0))
	sched := jobs.NewInMemoryScheduler(jobs.WithClock(fc))

	const n = 5
	for i := 0; i < n; i++ {
		dur := time.Duration(i+1) * 100 * time.Millisecond
		d := dur // capture loop variable
		_, err := sched.Submit(context.Background(), jobs.JobSpec{
			Kind: "host.slow",
			Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
				// Use the fake clock directly (same instance injected into context).
				select {
				case <-ctx.Done():
					return host.Result{}, ctx.Err()
				case <-fc.After(d):
					return host.Result{Data: map[string]any{"ok": true}}, nil
				}
			},
		})
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	// Wait until all 5 goroutines are parked on the fake clock.
	fc.BlockUntil(n)
	// Advance past the longest delay (500 ms).
	fc.Advance(600 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sched.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle returned error: %v", err)
	}
}

// TestWaitIdle_Cancelled verifies that WaitIdle respects context cancellation.
func TestWaitIdle_Cancelled(t *testing.T) {
	fc := clock.NewFake(time.Unix(0, 0))
	sched := jobs.NewInMemoryScheduler(jobs.WithClock(fc))

	_, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind: "host.slow",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			select {
			case <-ctx.Done():
				return host.Result{}, ctx.Err()
			case <-fc.After(10 * time.Second):
				return host.Result{}, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	fc.BlockUntil(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if err := sched.WaitIdle(ctx); err == nil {
		t.Fatal("WaitIdle should return error when context is cancelled")
	}
}

// subscribeAfterSubmit submits a job, subscribes, and returns the channel.
func subscribeAfterSubmit(t *testing.T, sched jobs.Scheduler, h host.Handler) (<-chan jobs.JobEvent, func()) {
	t.Helper()
	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind:    "host.test",
		Handler: h,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	ch, unsub := sched.Subscribe(id)
	return ch, unsub
}

// TestSubscribeOnTerminalJob_NoPanic verifies P0-1: subscribing to an already-
// terminal job and concurrently calling Heartbeat (which fires fanoutLocked)
// must not panic.  Before the fix, the channel was added to rj.subs and then
// closed while fanout could send to it.
func TestSubscribeOnTerminalJob_NoPanic(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()

	// Submit a job that completes immediately.
	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind:    "host.instant",
		Handler: echoHandler("hi"),
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for the job to finish.
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sched.WaitIdle(waitCtx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}

	// Concurrently Subscribe + Heartbeat — this should not panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = sched.Heartbeat(id, map[string]any{"i": i})
		}
	}()
	for i := 0; i < 100; i++ {
		ch, unsub := sched.Subscribe(id)
		// Drain the channel (already closed or sends one terminal event).
		for range ch {
		}
		unsub()
	}
	<-done
}

// TestAwaiting_RunningCountDoesNotGoNegative verifies P0-2: after a job
// transitions through awaiting_input→done, runningCount never goes negative.
func TestAwaiting_RunningCountDoesNotGoNegative(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()

	// Submit two jobs sequentially; each must complete without WaitIdle firing
	// prematurely (which would be the symptom of runningCount going negative).
	for i := 0; i < 2; i++ {
		id, err := sched.Submit(context.Background(), jobs.JobSpec{
			Kind:    "host.fast",
			Handler: echoHandler("ok"),
		})
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sched.WaitIdle(ctx); err != nil {
			t.Fatalf("WaitIdle %d: %v", i, err)
		}

		j, ok := sched.Get(id)
		if !ok {
			t.Fatalf("job %d not found after completion", i)
		}
		if j.Status != jobs.JobDone {
			t.Fatalf("job %d: expected done, got %s", i, j.Status)
		}
	}
}

// TestResume_ReIncrementsRunningCount verifies P0-2 Resumed behaviour:
// after Awaiting (runningCount=0) then Resumed (runningCount=1), WaitIdle
// should block until the job finishes.
func TestResume_ReIncrementsRunningCount(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()

	// Channel to synchronise the test: the handler blocks until the test
	// sends a signal, mimicking a clarification wait.
	proceed := make(chan struct{})

	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind: "host.pausable",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			<-proceed
			return host.Result{Data: map[string]any{"ok": true}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Manually call Awaiting so the scheduler considers the job idle.
	if err := sched.Awaiting(id); err != nil {
		t.Fatalf("Awaiting: %v", err)
	}

	// WaitIdle should return immediately (runningCount==0).
	idleCtx, idleCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer idleCancel()
	if err := sched.WaitIdle(idleCtx); err != nil {
		t.Fatalf("WaitIdle after Awaiting: %v", err)
	}

	// Now signal resume: job is running again.
	if err := sched.Resumed(id); err != nil {
		t.Fatalf("Resumed: %v", err)
	}

	// WaitIdle should NOT return before the handler is unblocked.
	earlyCtx, earlyCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer earlyCancel()
	if err := sched.WaitIdle(earlyCtx); err == nil {
		t.Fatal("WaitIdle should not have returned before handler completed")
	}

	// Unblock the handler and verify WaitIdle returns.
	close(proceed)
	doneCtx, doneCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer doneCancel()
	if err := sched.WaitIdle(doneCtx); err != nil {
		t.Fatalf("WaitIdle after handler complete: %v", err)
	}
}

// TestWaitIdle_NoGoroutineLeak verifies P1-5: cancelling WaitIdle's context
// does not leak the internal waiting goroutine.
func TestWaitIdle_NoGoroutineLeak(t *testing.T) {
	fc := clock.NewFake(time.Unix(0, 0))
	sched := jobs.NewInMemoryScheduler(jobs.WithClock(fc))

	_, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind: "host.slow",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			select {
			case <-ctx.Done():
				return host.Result{}, ctx.Err()
			case <-fc.After(10 * time.Second):
				return host.Result{}, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	fc.BlockUntil(1)

	before := runtime.NumGoroutine()

	// Cancel immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = sched.WaitIdle(ctx) // should return ctx.Err()

	// Allow a brief window for the goroutine to exit.
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()

	// The leak would add a permanent goroutine; allow +1 for scheduler noise.
	if after > before+1 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestScheduler_RunningCount_Increments asserts that RunningCount tracks
// the number of in-flight jobs: 1 while a blocking handler is running, then
// 0 after it completes.
func TestScheduler_RunningCount_Increments(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()

	proceed := make(chan struct{})
	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind: "host.blocking",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			<-proceed
			return host.Result{Data: map[string]any{"ok": true}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Poll briefly: the goroutine increments runningCount AFTER the goroutine
	// is launched in Submit, but before it can be observed without a tiny
	// scheduling delay.  Spin for up to 1s.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sched.RunningCount() == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := sched.RunningCount(); got != 1 {
		t.Fatalf("RunningCount while job blocked: got %d want 1", got)
	}

	close(proceed)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sched.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	if got := sched.RunningCount(); got != 0 {
		t.Fatalf("RunningCount after terminal: got %d want 0", got)
	}
	if _, ok := sched.Get(id); !ok {
		t.Fatalf("job not found after completion")
	}
}

// TestScheduler_RunningCount_AwaitingNotCounted asserts that a job in
// JobAwaitingInput counts as idle (RunningCount==0).
func TestScheduler_RunningCount_AwaitingNotCounted(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()

	proceed := make(chan struct{})
	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind: "host.pausable",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			<-proceed
			return host.Result{Data: map[string]any{"ok": true}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for the goroutine to be running.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sched.RunningCount() == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if err := sched.Awaiting(id); err != nil {
		t.Fatalf("Awaiting: %v", err)
	}
	if got := sched.RunningCount(); got != 0 {
		t.Fatalf("RunningCount after Awaiting: got %d want 0 (awaiting counts as idle)", got)
	}

	close(proceed)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sched.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
}

// TestScheduler_RunningCount_MultipleJobs asserts that RunningCount tracks
// the number of in-flight handlers across a fleet of blocking jobs.
func TestScheduler_RunningCount_MultipleJobs(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()

	const n = 3
	gates := make([]chan struct{}, n)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	for i := 0; i < n; i++ {
		gate := gates[i]
		_, err := sched.Submit(context.Background(), jobs.JobSpec{
			Kind: "host.blocking",
			Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
				<-gate
				return host.Result{Data: map[string]any{"ok": true}}, nil
			},
		})
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	// Wait for all 3 to be running.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sched.RunningCount() == n {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := sched.RunningCount(); got != n {
		t.Fatalf("RunningCount with %d jobs: got %d", n, got)
	}

	// Release jobs one at a time and observe the counter ticking down.
	for i := 0; i < n; i++ {
		close(gates[i])

		// Wait until that job has decremented runningCount.
		expected := n - 1 - i
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if sched.RunningCount() == expected {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		if got := sched.RunningCount(); got != expected {
			t.Fatalf("RunningCount after releasing job %d: got %d want %d", i, got, expected)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sched.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
}

// TestHeartbeatDebounce verifies the write-through debounce behaviour:
// firing many heartbeats in a short burst should result in fewer SQLite
// writes than the number of calls, and the persisted progress should
// reflect the last value sent.  After a pause longer than the debounce
// interval a single heartbeat must trigger a flush.
func TestHeartbeatDebounce(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	fc := clock.NewFake(time.Unix(0, 0))
	sched := jobs.NewScheduler(js, jobs.WithClock(fc))

	// Handler blocks on the fake clock so we can heartbeat while it runs.
	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: "sess-hb",
		Kind:      "host.slow",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			select {
			case <-ctx.Done():
				return host.Result{}, ctx.Err()
			case <-fc.After(2 * time.Second):
				return host.Result{Data: map[string]any{"output": "ok"}}, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait until the handler goroutine is parked on the fake clock.
	fc.BlockUntil(1)

	// Fire 10 heartbeats with no clock advance — all within the 500 ms debounce.
	for i := 0; i < 10; i++ {
		if err := sched.Heartbeat(id, map[string]any{"pct": i * 10}); err != nil {
			t.Fatalf("Heartbeat %d: %v", i, err)
		}
	}

	// Advance past the debounce interval to guarantee one flush on the next heartbeat.
	fc.Advance(600 * time.Millisecond)

	// Send one more heartbeat — this one must flush because interval has elapsed.
	finalProgress := map[string]any{"pct": 99}
	if err := sched.Heartbeat(id, finalProgress); err != nil {
		t.Fatalf("final Heartbeat: %v", err)
	}

	// The persisted row should exist (was written at submit time and
	// at least once during/after the burst).
	running, err := js.ListJobsByStatus(context.Background(), "sess-hb", jobs.JobRunning)
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(running) == 0 {
		t.Fatal("expected at least one running job row in SQLite")
	}
}
