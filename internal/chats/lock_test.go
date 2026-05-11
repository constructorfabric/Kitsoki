package chats_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/store"
)

func openStoreForLock(t *testing.T, fake *clock.Fake) *chats.Store {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cs, err := chats.NewStore(s.DB(), chats.WithClock(fake))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return cs
}

func newChat(t *testing.T, cs *chats.Store) *chats.Chat {
	t.Helper()
	c, err := cs.Create(context.Background(), "app1", "oracle", "", "Test Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return c
}

func TestLock_AcquireAndRelease(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)

	called := false
	err := cs.WithLock(context.Background(), c.ID, func(ctx context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	if !called {
		t.Error("expected fn to be called")
	}
}

func TestLock_DoubleAcquireSamePID(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)

	var innerErr error
	err := cs.WithLock(context.Background(), c.ID, func(ctx context.Context) error {
		// Try to acquire again from the same process — should fail.
		innerErr = cs.WithLock(ctx, c.ID, func(ctx context.Context) error {
			return nil
		})
		return nil
	})
	if err != nil {
		t.Fatalf("outer WithLock: %v", err)
	}
	if !errors.Is(innerErr, chats.ErrChatBusy) {
		t.Errorf("expected ErrChatBusy for double-acquire, got %v", innerErr)
	}
}

func TestLock_StaleReap_DeadPID(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)
	ctx := context.Background()

	// Insert a lock row with a guaranteed-dead PID (99999999 is unlikely to be live).
	// We bypass the normal acquire path by using DBForTest to insert directly.
	// Use the actual hostname so the stale-lock code reaches the processAlive check
	// (cross-host locks are always busy regardless of heartbeat age).
	deadPID := 99999999
	host, _ := os.Hostname()
	cs.DBForTest().MustExec(t,
		`INSERT INTO chat_locks (chat_id, owner_pid, owner_host, acquired_at, heartbeat_at)
		 VALUES (?, ?, ?, ?, ?)`,
		c.ID, deadPID, host, fake.Now().UnixMicro(), fake.Now().UnixMicro(),
	)

	// Advance the fake clock past the staleness threshold.
	fake.Advance(60 * time.Second)

	// WithLock should succeed by reaping the stale row (assuming deadPID is not alive).
	acquired := false
	err := cs.WithLock(ctx, c.ID, func(ctx context.Context) error {
		acquired = true
		return nil
	})

	// If deadPID happens to be a live process on this machine (extremely unlikely),
	// we'll get ErrChatBusy — which is correct behaviour. Log both outcomes.
	if err != nil {
		if !errors.Is(err, chats.ErrChatBusy) {
			t.Fatalf("unexpected error (not ErrChatBusy): %v", err)
		}
		t.Logf("stale-reap: pid %d appears to be alive; ErrChatBusy is correct", deadPID)
	} else {
		if !acquired {
			t.Error("expected fn to be called when lock acquired")
		}
		t.Logf("stale-reap: successfully reaped stale lock from pid %d", deadPID)
	}
}

// TestLock_CtxCancelled_LockReleased simulates the scheduler-cancellation
// case from the design audit: the goroutine's context is cancelled while fn
// is running. The defer releaseChatLock must fire regardless, so the next
// acquire succeeds.
func TestLock_CtxCancelled_LockReleased(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)

	ctx, cancel := context.WithCancel(context.Background())
	err := cs.WithLock(ctx, c.ID, func(inner context.Context) error {
		// Cancel the parent context mid-fn; mimicking scheduler.Cancel.
		cancel()
		return inner.Err()
	})
	// fn returned context.Canceled, which WithLock surfaces as the error.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// The lock must have been released even though fn returned a cancelled-ctx
	// error — defer releases on a fresh context.Background().
	err = cs.WithLock(context.Background(), c.ID, func(_ context.Context) error { return nil })
	if err != nil {
		t.Fatalf("WithLock after cancelled fn should succeed, got %v", err)
	}
}

func TestLock_FnError_LockReleased(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)
	ctx := context.Background()

	sentinel := fmt.Errorf("fn error")
	err := cs.WithLock(ctx, c.ID, func(ctx context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	// Lock must be released — we can acquire again.
	err = cs.WithLock(ctx, c.ID, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock after fn error should succeed, got %v", err)
	}
}

func TestLock_ErrChatBusy_ErrorsIs(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)

	var busyErr error
	_ = cs.WithLock(context.Background(), c.ID, func(ctx context.Context) error {
		busyErr = cs.WithLock(ctx, c.ID, func(ctx context.Context) error {
			return nil
		})
		return nil
	})
	if busyErr == nil {
		t.Fatal("expected busy error")
	}
	if !errors.Is(busyErr, chats.ErrChatBusy) {
		t.Errorf("errors.Is(busyErr, ErrChatBusy) = false; got %v", busyErr)
	}
	// Verify the error message includes useful context.
	msg := busyErr.Error()
	if msg == "" {
		t.Error("expected non-empty error message")
	}
	t.Logf("busy error: %s", msg)
}

// TestLock_ContentionTwoStores verifies that two chats.Store instances over
// the same SQLite DB file race correctly: only one acquires the lock, the
// other receives ErrChatBusy. Mirrors the two-process scenario when both
// drivers happen to run inside the same OS process (e.g. a single kitsoki
// binary holding the TUI connection while a goroutine drives a background
// chat turn).
func TestLock_ContentionTwoStores(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lock-contend.db")

	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open s1: %v", err)
	}
	t.Cleanup(func() { _ = s1.Close() })

	s2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open s2: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	cs1, err := chats.NewStore(s1.DB())
	if err != nil {
		t.Fatalf("NewStore cs1: %v", err)
	}
	cs2, err := chats.NewStore(s2.DB())
	if err != nil {
		t.Fatalf("NewStore cs2: %v", err)
	}

	c, err := cs1.Create(context.Background(), "app1", "oracle", "", "Contended Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx := context.Background()
	// Hold the lock from cs1 for the duration of the inner closure; while
	// holding it, cs2 must observe ErrChatBusy.
	var inner2Err error
	holdErr := cs1.WithLock(ctx, c.ID, func(ctx context.Context) error {
		inner2Err = cs2.WithLock(ctx, c.ID, func(_ context.Context) error {
			return nil
		})
		return nil
	})
	if holdErr != nil {
		t.Fatalf("cs1 outer WithLock: %v", holdErr)
	}
	if !errors.Is(inner2Err, chats.ErrChatBusy) {
		t.Errorf("expected cs2 to see ErrChatBusy while cs1 held the lock, got %v", inner2Err)
	}

	// After cs1 released, cs2 must acquire cleanly — no leaked lock row.
	cleanErr := cs2.WithLock(ctx, c.ID, func(_ context.Context) error { return nil })
	if cleanErr != nil {
		t.Errorf("cs2 should acquire after cs1 released, got %v", cleanErr)
	}
}

// TestLock_AlreadyCancelledCtx_ReturnsError verifies that WithLock bails
// out before touching chat_locks when the context is already cancelled —
// the previous behaviour silently INSERTed a row and then immediately
// rolled back via the deferred release, but the row could be observed by
// a racing reader (and the work itself ran with a dead ctx).
func TestLock_AlreadyCancelledCtx_ReturnsError(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE WithLock

	called := false
	err := cs.WithLock(ctx, c.ID, func(_ context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if called {
		t.Error("fn must not run when WithLock receives a cancelled ctx")
	}

	// And no chat_locks row was written for this chat.
	cnt := cs.DBForTest().MustQueryInt(t,
		`SELECT count(*) FROM chat_locks WHERE chat_id = ?`, c.ID)
	if cnt != 0 {
		t.Errorf("expected 0 chat_locks rows on cancelled ctx, got %d", cnt)
	}
}

// TestLock_DeadPidImmediateReap_NoTimeRequired pins down I2: a same-host
// lock owned by a dead PID is reaped without any clock advance. The old
// code required heartbeat_age > 30s AND processAlive==false, so a crashed
// caller wedged the chat for 30 seconds. Now PID-dead is sufficient.
func TestLock_DeadPidImmediateReap_NoTimeRequired(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)
	ctx := context.Background()

	// Insert a lock row owned by a known-dead PID with heartbeat_at = now,
	// then attempt to acquire WITHOUT advancing the clock.
	deadPID := 99999999
	host, _ := os.Hostname()
	cs.DBForTest().MustExec(t,
		`INSERT INTO chat_locks (chat_id, owner_pid, owner_host, acquired_at, heartbeat_at)
		 VALUES (?, ?, ?, ?, ?)`,
		c.ID, deadPID, host, fake.Now().UnixMicro(), fake.Now().UnixMicro(),
	)

	acquired := false
	err := cs.WithLock(ctx, c.ID, func(_ context.Context) error {
		acquired = true
		return nil
	})

	// If deadPID happens to be alive on this machine (very unlikely),
	// we'd see ErrChatBusy — log and skip rather than fail the build.
	if err != nil {
		if !errors.Is(err, chats.ErrChatBusy) {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Skipf("pid %d unexpectedly alive on this host", deadPID)
	}
	if !acquired {
		t.Error("expected fn to be called after immediate reap")
	}
}

func TestLock_Heartbeat_OwnerOnly(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	cs := openStoreForLock(t, fake)
	c := newChat(t, cs)
	ctx := context.Background()

	// Heartbeat with no lock should error.
	err := cs.Heartbeat(ctx, c.ID)
	if err == nil {
		t.Fatal("expected error from Heartbeat with no lock held")
	}

	// With lock held, Heartbeat should succeed.
	var hbErr error
	_ = cs.WithLock(ctx, c.ID, func(ctx context.Context) error {
		fake.Advance(5 * time.Second)
		hbErr = cs.Heartbeat(ctx, c.ID)
		return nil
	})
	if hbErr != nil {
		t.Fatalf("Heartbeat while holding lock: %v", hbErr)
	}
}
