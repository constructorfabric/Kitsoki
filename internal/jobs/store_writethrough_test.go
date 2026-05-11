package jobs_test

import (
	"context"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

// TestWriteThrough_SubmitPersistsRunning verifies that submitting a job through
// a scheduler with a *JobStore immediately writes a row with status=running.
func TestWriteThrough_SubmitPersistsRunning(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	sched := jobs.NewScheduler(js)

	// Use a handler that blocks until cancelled so we can inspect the
	// "running" row before it transitions to done.
	done := make(chan struct{})
	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: app.SessionID("sess-wt-1"),
		Kind:      "host.test",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			<-ctx.Done()
			close(done)
			return host.Result{}, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Give the goroutine a moment to start and write the row.
	time.Sleep(20 * time.Millisecond)

	running, err := js.ListJobsByStatus(context.Background(), "sess-wt-1", jobs.JobRunning)
	if err != nil {
		t.Fatalf("ListJobsByStatus(running): %v", err)
	}
	if len(running) != 1 {
		t.Fatalf("expected 1 running job, got %d", len(running))
	}
	if running[0].ID != id {
		t.Fatalf("expected id %s, got %s", id, running[0].ID)
	}

	// Cancel and await terminal state.
	if err := sched.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler to exit")
	}
	// Allow the goroutine to persist the terminal status.
	time.Sleep(20 * time.Millisecond)

	// The row should have moved to cancelled; running list should be empty.
	running, err = js.ListJobsByStatus(context.Background(), "sess-wt-1", jobs.JobRunning)
	if err != nil {
		t.Fatalf("ListJobsByStatus(running) after cancel: %v", err)
	}
	if len(running) != 0 {
		t.Fatalf("expected 0 running jobs after cancel, got %d", len(running))
	}

	cancelled, err := js.ListJobsByStatus(context.Background(), "sess-wt-1", jobs.JobCancelled)
	if err != nil {
		t.Fatalf("ListJobsByStatus(cancelled): %v", err)
	}
	if len(cancelled) != 1 {
		t.Fatalf("expected 1 cancelled job, got %d", len(cancelled))
	}
}

// TestWriteThrough_DoneResult verifies that after a successful job the result
// is persisted (status=done) via UpdateJobStatus.
func TestWriteThrough_DoneResult(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	sched := jobs.NewScheduler(js)
	ch, unsub := subscribeAfterSubmitWithSession(t, sched, "sess-wt-2", echoHandler("world"))
	defer unsub()

	select {
	case ev := <-ch:
		if ev.Status != jobs.JobDone {
			t.Fatalf("expected done, got %s", ev.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for job completion")
	}

	// Allow the write-through goroutine to finish.
	time.Sleep(20 * time.Millisecond)

	done, err := js.ListJobsByStatus(context.Background(), "sess-wt-2", jobs.JobDone)
	if err != nil {
		t.Fatalf("ListJobsByStatus(done): %v", err)
	}
	if len(done) != 1 {
		t.Fatalf("expected 1 done job, got %d", len(done))
	}
}

// subscribeAfterSubmitWithSession is a helper matching subscribeAfterSubmit but
// accepting an explicit session ID.
func subscribeAfterSubmitWithSession(t *testing.T, sched jobs.Scheduler, sessionID app.SessionID, h host.Handler) (<-chan jobs.JobEvent, func()) {
	t.Helper()
	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: sessionID,
		Kind:      "host.test",
		Handler:   h,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	ch, unsub := sched.Subscribe(id)
	return ch, unsub
}
