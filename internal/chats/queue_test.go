package chats_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kitsoki/internal/chats"
)

func enqueueOK(t *testing.T, cs *chats.Store, chatID, payload string) *chats.Drive {
	t.Helper()
	d, err := cs.Enqueue(context.Background(), chats.EnqueueOptions{
		ChatID:    chatID,
		Transport: chats.DriveTransportTUI,
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Enqueue(%q): %v", payload, err)
	}
	return d
}

func TestQueue_EnqueuePopulatesRow(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")

	d, err := cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:        c.ID,
		Transport:     chats.DriveTransportJira,
		Thread:        "PROJ-1#42",
		Actor:         "alice",
		CorrelationID: "corr-1",
		Payload:       "please look at this comment",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if d.DriveID == "" {
		t.Fatal("DriveID should be populated (ULID)")
	}
	if d.Status != chats.DriveStatusPending {
		t.Errorf("status = %q, want pending", d.Status)
	}
	if d.Transport != chats.DriveTransportJira {
		t.Errorf("transport = %q, want jira", d.Transport)
	}
	if d.Thread != "PROJ-1#42" || d.Actor != "alice" || d.CorrelationID != "corr-1" {
		t.Errorf("metadata not persisted: %+v", d)
	}
	if d.DispatchedAt != nil || d.CompletedAt != nil || d.ResultSeq != nil {
		t.Errorf("terminal fields should be nil on pending row: %+v", d)
	}
	if d.ErrorMessage != "" {
		t.Errorf("error_message should be empty on pending row: %q", d.ErrorMessage)
	}
}

func TestQueue_EnqueueRejectsEmptyFields(t *testing.T) {
	cs, _ := openTestStore(t)
	cases := []struct {
		name string
		opts chats.EnqueueOptions
	}{
		{"no chat", chats.EnqueueOptions{Transport: chats.DriveTransportTUI, Payload: "x"}},
		{"no transport", chats.EnqueueOptions{ChatID: "X", Payload: "x"}},
		{"no payload", chats.EnqueueOptions{ChatID: "X", Transport: chats.DriveTransportTUI}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cs.Enqueue(context.Background(), tc.opts); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestQueue_DequeueIsFIFO(t *testing.T) {
	cs, fake := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")

	fake.Advance(1 * time.Millisecond)
	d1 := enqueueOK(t, cs, c.ID, "first")
	fake.Advance(1 * time.Millisecond)
	d2 := enqueueOK(t, cs, c.ID, "second")
	fake.Advance(1 * time.Millisecond)
	d3 := enqueueOK(t, cs, c.ID, "third")

	for i, want := range []*chats.Drive{d1, d2, d3} {
		got, err := cs.Dequeue(ctx, c.ID)
		if err != nil {
			t.Fatalf("Dequeue #%d: %v", i, err)
		}
		if got.DriveID != want.DriveID {
			t.Errorf("dequeue order broken at #%d: got %s, want %s", i, got.DriveID, want.DriveID)
		}
		if got.Status != chats.DriveStatusDispatching {
			t.Errorf("status after dequeue = %q, want dispatching", got.Status)
		}
		if got.DispatchedAt == nil {
			t.Error("dispatched_at should be set after dequeue")
		}
	}

	if _, err := cs.Dequeue(ctx, c.ID); !errors.Is(err, chats.ErrNoPendingDrive) {
		t.Errorf("empty queue: expected ErrNoPendingDrive, got %v", err)
	}
}

func TestQueue_DequeueOnEmptyReturnsErrNoPendingDrive(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")
	if _, err := cs.Dequeue(ctx, c.ID); !errors.Is(err, chats.ErrNoPendingDrive) {
		t.Errorf("expected ErrNoPendingDrive, got %v", err)
	}
}

func TestQueue_DequeueIsolatesChats(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c1, _ := cs.Create(ctx, "app1", "live", "", "t1")
	c2, _ := cs.Create(ctx, "app1", "live", "", "t2")
	enqueueOK(t, cs, c1.ID, "for c1")
	enqueueOK(t, cs, c2.ID, "for c2")

	d, err := cs.Dequeue(ctx, c1.ID)
	if err != nil {
		t.Fatalf("Dequeue c1: %v", err)
	}
	if d.ChatID != c1.ID {
		t.Errorf("got drive for chat %s, want %s", d.ChatID, c1.ID)
	}
	// c2's drive is still pending.
	pending, err := cs.ListDrives(ctx, c2.ID, chats.ListDrivesFilter{
		Statuses: []chats.DriveStatus{chats.DriveStatusPending},
	})
	if err != nil {
		t.Fatalf("ListDrives c2: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("c2 should still have 1 pending drive, got %d", len(pending))
	}
}

// TestQueue_DequeueAtomicUnderContention spawns N goroutines each
// trying to claim from the same queue of N drives. We assert that
// every drive was claimed exactly once and no goroutine got two.
//
// This exercises the CAS UPDATE inside Dequeue under SQLite's
// serialised-writer regime. If the SELECT-then-UPDATE were
// non-atomic, two goroutines could claim the same row and one of the
// UPDATEs would either fail RowsAffected==1 or, worse, both succeed.
func TestQueue_DequeueAtomicUnderContention(t *testing.T) {
	cs, fake := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")

	const N = 50
	for i := 0; i < N; i++ {
		fake.Advance(1 * time.Microsecond)
		enqueueOK(t, cs, c.ID, "payload")
	}

	var (
		seen     sync.Map
		claimed  atomic.Int64
		errCount atomic.Int64
		wg       sync.WaitGroup
	)
	for i := 0; i < N*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := cs.Dequeue(ctx, c.ID)
			if errors.Is(err, chats.ErrNoPendingDrive) {
				return
			}
			if err != nil {
				errCount.Add(1)
				return
			}
			if _, loaded := seen.LoadOrStore(d.DriveID, true); loaded {
				t.Errorf("drive %s claimed twice", d.DriveID)
			}
			claimed.Add(1)
		}()
	}
	wg.Wait()

	if errCount.Load() != 0 {
		t.Errorf("dequeue errors: %d", errCount.Load())
	}
	if claimed.Load() != N {
		t.Errorf("claimed=%d, want %d", claimed.Load(), N)
	}
}

func TestQueue_ClaimDriveByIDSkipsHead(t *testing.T) {
	cs, fake := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")

	// Two pending drives; promote the second one out of order.
	fake.Advance(1 * time.Millisecond)
	dHead := enqueueOK(t, cs, c.ID, "head")
	fake.Advance(1 * time.Millisecond)
	dPromoted := enqueueOK(t, cs, c.ID, "promote me")

	claimed, err := cs.ClaimDrive(ctx, dPromoted.DriveID)
	if err != nil {
		t.Fatalf("ClaimDrive: %v", err)
	}
	if claimed.DriveID != dPromoted.DriveID {
		t.Errorf("claimed wrong row: got %s, want %s", claimed.DriveID, dPromoted.DriveID)
	}
	if claimed.Status != chats.DriveStatusDispatching {
		t.Errorf("status after claim = %q, want dispatching", claimed.Status)
	}
	// dHead is still pending and remains the head for any subsequent Dequeue.
	head, err := cs.Dequeue(ctx, c.ID)
	if err != nil {
		t.Fatalf("Dequeue after promote: %v", err)
	}
	if head.DriveID != dHead.DriveID {
		t.Errorf("Dequeue should still see dHead first: got %s", head.DriveID)
	}
}

func TestQueue_ClaimDriveErrors(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")

	if _, err := cs.ClaimDrive(ctx, "NOPE"); !errors.Is(err, chats.ErrDriveNotFound) {
		t.Errorf("missing drive: expected ErrDriveNotFound, got %v", err)
	}

	d := enqueueOK(t, cs, c.ID, "x")
	if _, err := cs.Dequeue(ctx, c.ID); err != nil {
		t.Fatalf("Dequeue setup: %v", err)
	}
	if _, err := cs.ClaimDrive(ctx, d.DriveID); !errors.Is(err, chats.ErrDriveStateMismatch) {
		t.Errorf("already-dispatching drive: expected ErrDriveStateMismatch, got %v", err)
	}
}

func TestQueue_MarkDriveDoneTransitions(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")
	enqueueOK(t, cs, c.ID, "x")
	d, err := cs.Dequeue(ctx, c.ID)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := cs.MarkDriveDone(ctx, d.DriveID, 7); err != nil {
		t.Fatalf("MarkDriveDone: %v", err)
	}
	got, err := cs.GetDrive(ctx, d.DriveID)
	if err != nil {
		t.Fatalf("GetDrive: %v", err)
	}
	if got.Status != chats.DriveStatusDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if got.ResultSeq == nil || *got.ResultSeq != 7 {
		t.Errorf("result_seq = %v, want 7", got.ResultSeq)
	}
	if got.CompletedAt == nil {
		t.Error("completed_at should be set")
	}
	if got.ErrorMessage != "" {
		t.Errorf("error_message should be empty: %q", got.ErrorMessage)
	}
}

func TestQueue_MarkDriveFailedRecordsErrorMessage(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")
	enqueueOK(t, cs, c.ID, "x")
	d, err := cs.Dequeue(ctx, c.ID)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := cs.MarkDriveFailed(ctx, d.DriveID, "claude exited 1"); err != nil {
		t.Fatalf("MarkDriveFailed: %v", err)
	}
	got, err := cs.GetDrive(ctx, d.DriveID)
	if err != nil {
		t.Fatalf("GetDrive: %v", err)
	}
	if got.Status != chats.DriveStatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.ErrorMessage != "claude exited 1" {
		t.Errorf("error_message = %q", got.ErrorMessage)
	}
	if got.ResultSeq != nil {
		t.Errorf("result_seq should be nil on failed drive, got %v", *got.ResultSeq)
	}
}

func TestQueue_MarkDriveDismissedFromPendingOnly(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")
	d1 := enqueueOK(t, cs, c.ID, "to-dismiss")
	d2 := enqueueOK(t, cs, c.ID, "to-dispatch")

	// d1 is dismissable while pending.
	if err := cs.MarkDriveDismissed(ctx, d1.DriveID); err != nil {
		t.Fatalf("MarkDriveDismissed pending: %v", err)
	}
	got, _ := cs.GetDrive(ctx, d1.DriveID)
	if got.Status != chats.DriveStatusDismissed {
		t.Errorf("status = %q, want dismissed", got.Status)
	}

	// d2 dequeued → dispatching → dismiss must refuse.
	if _, err := cs.Dequeue(ctx, c.ID); err != nil {
		t.Fatalf("Dequeue d2: %v", err)
	}
	if err := cs.MarkDriveDismissed(ctx, d2.DriveID); !errors.Is(err, chats.ErrDriveStateMismatch) {
		t.Errorf("dismiss dispatching: expected ErrDriveStateMismatch, got %v", err)
	}
}

func TestQueue_MarkDoneOnNonDispatchingFails(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")
	d := enqueueOK(t, cs, c.ID, "x")
	if err := cs.MarkDriveDone(ctx, d.DriveID, 0); !errors.Is(err, chats.ErrDriveStateMismatch) {
		t.Errorf("done from pending: expected ErrDriveStateMismatch, got %v", err)
	}
}

func TestQueue_MarkDoneUnknownDriveReturnsNotFound(t *testing.T) {
	cs, _ := openTestStore(t)
	if err := cs.MarkDriveDone(context.Background(), "NOPE", 0); !errors.Is(err, chats.ErrDriveNotFound) {
		t.Errorf("expected ErrDriveNotFound, got %v", err)
	}
}

func TestQueue_ListDrivesFilterByStatus(t *testing.T) {
	cs, fake := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")

	// Three drives in FIFO order: one stays pending (dismiss it), one
	// completes successfully, one fails.
	fake.Advance(1 * time.Millisecond)
	dDismiss := enqueueOK(t, cs, c.ID, "to-dismiss")
	fake.Advance(1 * time.Millisecond)
	dDone := enqueueOK(t, cs, c.ID, "to-be-done")
	fake.Advance(1 * time.Millisecond)
	dFail := enqueueOK(t, cs, c.ID, "to-fail")

	if err := cs.MarkDriveDismissed(ctx, dDismiss.DriveID); err != nil {
		t.Fatalf("MarkDriveDismissed: %v", err)
	}

	// Dequeue dDone (now the oldest pending) and mark it done.
	d2, _ := cs.Dequeue(ctx, c.ID)
	if d2.DriveID != dDone.DriveID {
		t.Fatalf("FIFO broke: got %s, want dDone %s", d2.DriveID, dDone.DriveID)
	}
	if err := cs.MarkDriveDone(ctx, d2.DriveID, 7); err != nil {
		t.Fatalf("MarkDriveDone: %v", err)
	}

	// Dequeue dFail and mark it failed.
	d3, _ := cs.Dequeue(ctx, c.ID)
	if d3.DriveID != dFail.DriveID {
		t.Fatalf("FIFO broke: got %s, want dFail %s", d3.DriveID, dFail.DriveID)
	}
	if err := cs.MarkDriveFailed(ctx, d3.DriveID, "boom"); err != nil {
		t.Fatalf("MarkDriveFailed: %v", err)
	}

	all, err := cs.ListDrives(ctx, c.ID, chats.ListDrivesFilter{})
	if err != nil {
		t.Fatalf("ListDrives all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all: got %d rows, want 3", len(all))
	}

	failed, err := cs.ListDrives(ctx, c.ID, chats.ListDrivesFilter{
		Statuses: []chats.DriveStatus{chats.DriveStatusFailed},
	})
	if err != nil {
		t.Fatalf("ListDrives failed: %v", err)
	}
	if len(failed) != 1 || failed[0].DriveID != dFail.DriveID {
		t.Errorf("failed: expected [dFail], got %+v", failed)
	}

	done, err := cs.ListDrives(ctx, c.ID, chats.ListDrivesFilter{
		Statuses: []chats.DriveStatus{chats.DriveStatusDone},
	})
	if err != nil {
		t.Fatalf("ListDrives done: %v", err)
	}
	if len(done) != 1 || done[0].DriveID != dDone.DriveID {
		t.Errorf("done: expected [dDone], got %+v", done)
	}

	dismissed, err := cs.ListDrives(ctx, c.ID, chats.ListDrivesFilter{
		Statuses: []chats.DriveStatus{chats.DriveStatusDismissed},
	})
	if err != nil {
		t.Fatalf("ListDrives dismissed: %v", err)
	}
	if len(dismissed) != 1 || dismissed[0].DriveID != dDismiss.DriveID {
		t.Errorf("dismissed: expected [dDismiss], got %+v", dismissed)
	}
}

func TestQueue_ListDrivesByOrigin(t *testing.T) {
	cs, fake := openTestStore(t)
	ctx := context.Background()
	c1, _ := cs.Create(ctx, "app1", "live", "", "t1")
	c2, _ := cs.Create(ctx, "app1", "live", "", "t2")
	c3, _ := cs.Create(ctx, "app1", "live", "", "t3")

	fake.Advance(1 * time.Millisecond)
	current, err := cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          c1.ID,
		Transport:       chats.DriveTransportStateMachine,
		Payload:         "current",
		OriginSessionID: "session-a",
	})
	if err != nil {
		t.Fatalf("Enqueue current: %v", err)
	}
	fake.Advance(1 * time.Millisecond)
	other, err := cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          c2.ID,
		Transport:       chats.DriveTransportStateMachine,
		Payload:         "other",
		OriginSessionID: "session-b",
	})
	if err != nil {
		t.Fatalf("Enqueue other: %v", err)
	}
	fake.Advance(1 * time.Millisecond)
	unscoped := enqueueOK(t, cs, c3.ID, "no-origin")
	if err := cs.MarkDriveDismissed(ctx, unscoped.DriveID); err != nil {
		t.Fatalf("MarkDriveDismissed: %v", err)
	}
	done, err := cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          c3.ID,
		Transport:       chats.DriveTransportStateMachine,
		Payload:         "done",
		OriginSessionID: "session-c",
	})
	if err != nil {
		t.Fatalf("Enqueue done: %v", err)
	}
	if _, err := cs.Dequeue(ctx, c3.ID); err != nil {
		t.Fatalf("Dequeue done: %v", err)
	}
	if err := cs.MarkDriveDone(ctx, done.DriveID, 1); err != nil {
		t.Fatalf("MarkDriveDone: %v", err)
	}

	got, err := cs.ListDrivesByOrigin(ctx, []chats.DriveStatus{chats.DriveStatusPending})
	if err != nil {
		t.Fatalf("ListDrivesByOrigin: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].DriveID != current.DriveID || got[1].DriveID != other.DriveID {
		t.Fatalf("unexpected origin drive order: got [%s %s], want [%s %s]",
			got[0].DriveID, got[1].DriveID, current.DriveID, other.DriveID)
	}
}

func TestQueue_ListDrivesRespectsLimit(t *testing.T) {
	cs, fake := openTestStore(t)
	ctx := context.Background()
	c, _ := cs.Create(ctx, "app1", "live", "", "t")
	for i := 0; i < 5; i++ {
		fake.Advance(1 * time.Microsecond)
		enqueueOK(t, cs, c.ID, "x")
	}
	got, err := cs.ListDrives(ctx, c.ID, chats.ListDrivesFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("limit: got %d rows, want 2", len(got))
	}
}
