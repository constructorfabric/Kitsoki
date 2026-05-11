package jobs_test

import (
	"context"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/jobs"
)

// TestSubscribeSession verifies that a session-level subscription receives
// exactly the terminal events for jobs belonging to that session and not
// events from a different session.
func TestSubscribeSession(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()

	sessA := app.SessionID("sess-A")
	sessB := app.SessionID("sess-B")

	// Subscribe to session A before submitting any jobs.
	chA, ackA, unsubA := sched.SubscribeSession(sessA)
	defer unsubA()

	// Submit two jobs for session A.
	_, err := sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: sessA,
		Kind:      "host.test",
		Handler:   echoHandler("a1"),
	})
	if err != nil {
		t.Fatalf("submit A1: %v", err)
	}

	_, err = sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: sessA,
		Kind:      "host.test",
		Handler:   echoHandler("a2"),
	})
	if err != nil {
		t.Fatalf("submit A2: %v", err)
	}

	// Submit one job for session B.
	_, err = sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: sessB,
		Kind:      "host.test",
		Handler:   echoHandler("b1"),
	})
	if err != nil {
		t.Fatalf("submit B1: %v", err)
	}

	// Collect exactly 2 events from session A's channel.
	deadline := time.After(3 * time.Second)
	received := 0
	for received < 2 {
		select {
		case ev := <-chA:
			ackA()
			if ev.Status != jobs.JobDone {
				t.Fatalf("expected done, got %s", ev.Status)
			}
			received++
		case <-deadline:
			t.Fatalf("timeout: only received %d/2 events on session-A channel", received)
		}
	}

	// The channel should now be empty (session-B event must not arrive).
	select {
	case ev := <-chA:
		t.Fatalf("unexpected extra event on session-A channel: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Good: no stray events.
	}
}
