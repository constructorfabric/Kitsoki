package jobs_test

// Hidden behavioural oracle for the background-job caller-cancellation bug.
// It intentionally states only the external scheduler contract: background
// work survives the submitting request context, while explicit scheduler
// cancellation remains authoritative.

import (
	"context"
	"testing"
	"time"

	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

func TestRepro_BackgroundJobSurvivesSubmittingContextCancellation(t *testing.T) {
	fakeClock := clock.NewFake(time.Unix(0, 0))
	scheduler := jobs.NewInMemoryScheduler(jobs.WithClock(fakeClock))
	callerCtx, cancelCaller := context.WithCancel(context.Background())

	id, err := scheduler.Submit(callerCtx, jobs.JobSpec{
		Kind: "host.oracle.decide",
		Handler: func(ctx context.Context, _ map[string]any) (host.Result, error) {
			select {
			case <-ctx.Done():
				return host.Result{}, ctx.Err()
			case <-fakeClock.After(10 * time.Second):
				return host.Result{Data: map[string]any{"output": "done"}}, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	updates, unsubscribe := scheduler.Subscribe(id)
	defer unsubscribe()

	fakeClock.BlockUntil(1)
	cancelCaller()

	select {
	case event := <-updates:
		t.Fatalf("background job terminated after caller cancellation: %s/%q", event.Status, event.Error)
	case <-time.After(200 * time.Millisecond):
		// The caller went away; the scheduler-owned job is still alive.
	}

	if err := scheduler.Cancel(context.Background(), id); err != nil {
		t.Fatalf("explicit cancel: %v", err)
	}
	deadline, cancelDeadline := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDeadline()
	select {
	case event := <-updates:
		if event.Status != jobs.JobCancelled {
			t.Fatalf("explicit cancel status = %s, want %s", event.Status, jobs.JobCancelled)
		}
	case <-deadline.Done():
		t.Fatal("timed out waiting for explicit scheduler cancellation")
	}
}
