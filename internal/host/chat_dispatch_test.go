package host_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/clock"
	"kitsoki/internal/host"
)

// TestDispatchDrive_HappyPath enqueues a drive, dispatches it, and
// asserts the drive transitions pending → dispatching → done and that
// DispatchResult carries the answer + result_seq from the chat turn.
func TestDispatchDrive_HappyPath(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithClaudeRunner(host.WithChatStore(context.Background(), cs), stubOracleRunner())

	d, err := cs.Enqueue(ctx, host.EnqueueDriveOptions{
		ChatID:    "chat-1",
		Transport: "tui",
		Payload:   "what's up?",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	res, err := host.DispatchDrive(ctx, cs, d.DriveID, "")
	if err != nil {
		t.Fatalf("DispatchDrive: %v", err)
	}
	if res.Status != "done" {
		t.Errorf("status = %q, want done", res.Status)
	}
	if res.ChatID != "chat-1" {
		t.Errorf("chat_id = %q, want chat-1", res.ChatID)
	}
	if res.Answer == "" {
		t.Error("Answer should be populated on success")
	}
	if res.ClaudeSessionID == "" {
		t.Error("ClaudeSessionID should be populated on success")
	}
	// Drive row reflects the same terminal state.
	got, err := cs.GetDrive(ctx, d.DriveID)
	if err != nil {
		t.Fatalf("GetDrive: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("drive row status = %q, want done", got.Status)
	}
	if got.ResultSeq == nil {
		t.Error("result_seq should be set on done drive")
	} else if *got.ResultSeq != res.ResultSeq {
		t.Errorf("drive.result_seq = %d, dispatch result_seq = %d", *got.ResultSeq, res.ResultSeq)
	}
	// Transcript has the user payload + assistant reply.
	msgs := cs.messages["chat-1"]
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages in transcript, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "what's up?" {
		t.Errorf("user message wrong: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("assistant message wrong role: %q", msgs[1].Role)
	}
}

// TestDispatchDrive_ClaudeNonZeroExitMarksFailed runs the fake binary
// with a payload it knows to error on, verifying that the drive is
// transitioned to failed (not done) and the error message is captured.
func TestDispatchDrive_ClaudeNonZeroExitMarksFailed(t *testing.T) {
	t.Parallel()
	// The fake-oracle stub always succeeds; use the one-shot stub which
	// honours a "FAIL" trigger in the prompt. doOracleChatTurn pipes the
	// user question on stdin, so a payload containing FAIL will exit
	// non-zero.
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithClaudeRunner(host.WithChatStore(context.Background(), cs), stubOneShotRunner())

	d, err := cs.Enqueue(ctx, host.EnqueueDriveOptions{
		ChatID:    "chat-1",
		Transport: "tui",
		Payload:   "FAIL please",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	res, err := host.DispatchDrive(ctx, cs, d.DriveID, "")
	if err != nil {
		t.Fatalf("DispatchDrive: %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("status = %q, want failed", res.Status)
	}
	if res.ErrorMessage == "" {
		t.Error("ErrorMessage should be populated on failed drive")
	}

	got, _ := cs.GetDrive(ctx, d.DriveID)
	if got.Status != "failed" {
		t.Errorf("drive row status = %q, want failed", got.Status)
	}
	if got.ErrorMessage == "" {
		t.Error("drive.error_message should be set on failed drive")
	}
	if got.ResultSeq != nil {
		t.Errorf("drive.result_seq should be nil on failed drive, got %v", *got.ResultSeq)
	}
}

// TestDispatchDrive_LockBusyLeavesDrivePending verifies that when the
// chat lock is held by someone else, DispatchDrive returns ErrChatBusy
// without claiming the drive — the row stays pending so a retry / the
// next ambient drainer can pick it up.
func TestDispatchDrive_LockBusyLeavesDrivePending(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	cs.withLockErr = host.NewChatBusyError(errors.New("chats: chat busy"))
	ctx := host.WithChatStore(context.Background(), cs)

	d, _ := cs.Enqueue(ctx, host.EnqueueDriveOptions{
		ChatID: "chat-1", Transport: "tui", Payload: "x",
	})

	_, err := host.DispatchDrive(ctx, cs, d.DriveID, "")
	if !errors.Is(err, host.ErrChatBusy) {
		t.Fatalf("expected ErrChatBusy, got %v", err)
	}
	got, _ := cs.GetDrive(ctx, d.DriveID)
	if got.Status != "pending" {
		t.Errorf("drive status after busy = %q, want pending", got.Status)
	}
}

// TestDispatchDrive_UnknownDriveErrors makes sure the dispatcher bails
// before touching the lock if the drive id is bogus.
func TestDispatchDrive_UnknownDriveErrors(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	ctx := host.WithChatStore(context.Background(), cs)
	_, err := host.DispatchDrive(ctx, cs, "NOPE", "")
	if !errors.Is(err, host.ErrDriveNotFound) {
		t.Fatalf("expected ErrDriveNotFound, got %v", err)
	}
}

// TestDispatchDrive_AlreadyDispatchingErrors covers the race where two
// processes try to dispatch the same drive_id concurrently. The second
// one sees ClaimDrive return ErrDriveStateMismatch.
func TestDispatchDrive_AlreadyDispatchingErrors(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithChatStore(context.Background(), cs)

	d, _ := cs.Enqueue(ctx, host.EnqueueDriveOptions{
		ChatID: "chat-1", Transport: "tui", Payload: "x",
	})
	// Pre-claim simulates "another dispatcher beat us to it."
	if _, err := cs.ClaimDrive(ctx, d.DriveID); err != nil {
		t.Fatalf("pre-claim: %v", err)
	}

	_, err := host.DispatchDrive(ctx, cs, d.DriveID, "")
	if err == nil {
		t.Fatal("expected an error for already-dispatching drive")
	}
	if !errors.Is(err, host.ErrDriveStateMismatch) {
		t.Errorf("expected ErrDriveStateMismatch, got %v", err)
	}
}

// TestDispatchDrive_NoChatStoreErrors is a misuse check: calling
// DispatchDrive without a ChatStore should return a clear error, not
// panic.
func TestDispatchDrive_NoChatStoreErrors(t *testing.T) {
	t.Parallel()
	_, err := host.DispatchDrive(context.Background(), nil, "x", "")
	if err == nil || !strings.Contains(err.Error(), "no chat store") {
		t.Errorf("expected nil-store error, got %v", err)
	}
}

// TestDispatchDriveWithTimeout_BusyThenFree covers the retry loop:
// the lock is held on the first attempt, then released. The
// dispatcher polls (against an injected fake clock) and succeeds on
// the second swing.
func TestDispatchDriveWithTimeout_BusyThenFree(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	// The lock starts busy. We arrange to flip it after one tick.
	cs.withLockErr = host.NewChatBusyError(errors.New("chats: chat busy"))

	clk := clock.NewFake(time.Unix(0, 0))
	ctx := host.WithClaudeRunner(
		host.WithClock(host.WithChatStore(context.Background(), cs), clk),
		stubOracleRunner(),
	)

	d, err := cs.Enqueue(ctx, host.EnqueueDriveOptions{
		ChatID: "chat-1", Transport: "tui", Payload: "go",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	type result struct {
		res *host.DispatchResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := host.DispatchDriveWithTimeout(ctx, cs, d.DriveID, "", 5*time.Second)
		done <- result{r, err}
	}()

	// Wait until the dispatcher has parked on clk.After — that's the
	// signal that the first attempt hit ErrChatBusy and the retry
	// loop is now waiting. Using BlockUntil keeps the test
	// deterministic across CI.
	clk.BlockUntil(1)

	// Free the lock, then wake the sleeper so the next iteration of
	// the loop runs and succeeds.
	cs.withLockErr = nil
	clk.Advance(2 * time.Second)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("DispatchDriveWithTimeout: %v", r.err)
		}
		if r.res.Status != "done" {
			t.Errorf("status = %q, want done", r.res.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DispatchDriveWithTimeout did not return after lock freed")
	}
}

// TestDispatchDriveWithTimeout_TimesOutWithoutFree covers the
// permanently-busy case: the dispatcher waits for the budget then
// returns ErrChatBusy and leaves the drive pending.
func TestDispatchDriveWithTimeout_TimesOutWithoutFree(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	cs.withLockErr = host.NewChatBusyError(errors.New("chats: chat busy"))

	clk := clock.NewFake(time.Unix(0, 0))
	ctx := host.WithClock(host.WithChatStore(context.Background(), cs), clk)

	d, _ := cs.Enqueue(ctx, host.EnqueueDriveOptions{
		ChatID: "chat-1", Transport: "tui", Payload: "x",
	})

	type result struct {
		res *host.DispatchResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := host.DispatchDriveWithTimeout(ctx, cs, d.DriveID, "", 3*time.Second)
		done <- result{r, err}
	}()

	// Drive the loop forward in 1-s ticks. Each iteration of the
	// dispatcher's retry loop parks on clk.After exactly once, so
	// BlockUntilContext(1) succeeds as long as the goroutine is still
	// running. Once the deadline elapses inside the dispatcher, the
	// goroutine returns ErrChatBusy and stops parking — at that point
	// BlockUntilContext times out and we stop advancing.
	advanceCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 6; i++ {
		if err := clk.BlockUntilContext(advanceCtx, 1); err != nil {
			// No more waiters → goroutine finished. Done channel
			// should have a result.
			break
		}
		clk.Advance(1 * time.Second)
	}

	select {
	case r := <-done:
		if !errors.Is(r.err, host.ErrChatBusy) {
			t.Errorf("expected ErrChatBusy after budget, got %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DispatchDriveWithTimeout did not return after budget elapsed")
	}
	got, _ := cs.GetDrive(ctx, d.DriveID)
	if got.Status != "pending" {
		t.Errorf("drive status = %q, want pending (no claim should have happened)", got.Status)
	}
}
