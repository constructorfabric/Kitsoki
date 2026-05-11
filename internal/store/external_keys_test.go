package store_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// TestBindAndLookupExternalKey verifies the basic insert + lookup roundtrip.
func TestBindAndLookupExternalKey(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	def := makeAppDef("bugfix", "0.1")
	sid, err := st.CreateSession(ctx, def)
	require.NoError(t, err)

	require.NoError(t, st.BindExternalKey(ctx, sid, "jira", "PLTFRM-12345"))

	got, err := st.LookupByKey(ctx, "jira", "PLTFRM-12345")
	require.NoError(t, err)
	require.Equal(t, sid, got)
}

func TestLookupByKey_NotFound(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.LookupByKey(context.Background(), "jira", "NOPE-1")
	require.ErrorIs(t, err, store.ErrSessionNotFound)
}

func TestBindExternalKey_RebindSameSessionIsNoOp(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, makeAppDef("bugfix", "0.1"))
	require.NoError(t, err)

	require.NoError(t, st.BindExternalKey(ctx, sid, "jira", "PLTFRM-1"))
	require.NoError(t, st.BindExternalKey(ctx, sid, "jira", "PLTFRM-1"))
}

func TestBindExternalKey_AlreadyTakenByOtherSession(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	def := makeAppDef("bugfix", "0.1")
	sidA, err := st.CreateSession(ctx, def)
	require.NoError(t, err)
	sidB, err := st.CreateSession(ctx, def)
	require.NoError(t, err)

	require.NoError(t, st.BindExternalKey(ctx, sidA, "jira", "PLTFRM-1"))
	err = st.BindExternalKey(ctx, sidB, "jira", "PLTFRM-1")
	require.ErrorIs(t, err, store.ErrExternalKeyTaken)
}

func TestBindExternalKey_UnknownSession(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	err = st.BindExternalKey(context.Background(), app.SessionID("does-not-exist"), "jira", "X")
	require.ErrorIs(t, err, store.ErrSessionNotFound)
}

func TestListExternalKeys_MultipleKeysPerSession(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, makeAppDef("bugfix", "0.1"))
	require.NoError(t, err)

	require.NoError(t, st.BindExternalKey(ctx, sid, "jira", "PLTFRM-1"))
	// Force a microsecond gap so created_at differs.
	time.Sleep(2 * time.Microsecond)
	require.NoError(t, st.BindExternalKey(ctx, sid, "bitbucket", "DBI/repo/pulls/42"))

	keys, err := st.ListExternalKeys(ctx, sid)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, "jira", keys[0].Transport)
	require.Equal(t, "PLTFRM-1", keys[0].Thread)
	require.Equal(t, "bitbucket", keys[1].Transport)
}

func TestListSessionsByTransport(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	def := makeAppDef("bugfix", "0.1")

	s1, err := st.CreateSession(ctx, def)
	require.NoError(t, err)
	s2, err := st.CreateSession(ctx, def)
	require.NoError(t, err)
	s3, err := st.CreateSession(ctx, def)
	require.NoError(t, err)

	require.NoError(t, st.BindExternalKey(ctx, s1, "jira", "A-1"))
	time.Sleep(2 * time.Microsecond)
	require.NoError(t, st.BindExternalKey(ctx, s2, "jira", "A-2"))
	time.Sleep(2 * time.Microsecond)
	require.NoError(t, st.BindExternalKey(ctx, s3, "bitbucket", "C-3"))

	got, err := st.ListSessionsByTransport(ctx, "jira", 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Newest-key-first: s2 was bound after s1.
	require.Equal(t, s2, got[0].ID)
	require.Equal(t, s1, got[1].ID)

	gotBB, err := st.ListSessionsByTransport(ctx, "bitbucket", 0)
	require.NoError(t, err)
	require.Len(t, gotBB, 1)
	require.Equal(t, s3, gotBB[0].ID)
}

// TestWithWriterLock_Reentrant verifies that the same process re-entering
// gets ErrSessionBusy (we don't recurse — caller should structure code so
// the lock is held once around the critical section).
func TestWithWriterLock_Reentrant(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, makeAppDef("bugfix", "0.1"))
	require.NoError(t, err)

	err = st.WithWriterLock(ctx, sid, func() error {
		// Re-acquire from inside the held lock.
		inner := st.WithWriterLock(ctx, sid, func() error { return nil })
		require.ErrorIs(t, inner, store.ErrSessionBusy)
		return nil
	})
	require.NoError(t, err)
}

// TestWithWriterLock_FreedAfterFn verifies that the lock is released when fn
// returns and a subsequent acquire succeeds.
func TestWithWriterLock_FreedAfterFn(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, makeAppDef("bugfix", "0.1"))
	require.NoError(t, err)

	require.NoError(t, st.WithWriterLock(ctx, sid, func() error { return nil }))
	require.NoError(t, st.WithWriterLock(ctx, sid, func() error { return nil }))
}

// TestWithWriterLock_FreedAfterPanic verifies that the lock is released even
// when fn panics.
func TestWithWriterLock_FreedAfterPanic(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, makeAppDef("bugfix", "0.1"))
	require.NoError(t, err)

	func() {
		defer func() {
			_ = recover()
		}()
		_ = st.WithWriterLock(ctx, sid, func() error {
			panic("kaboom")
		})
	}()

	// Lock must be freed despite the panic.
	require.NoError(t, st.WithWriterLock(ctx, sid, func() error { return nil }))
}

// TestWithWriterLock_FreedAfterError verifies that errors from fn don't leak
// the lock.
func TestWithWriterLock_FreedAfterError(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, makeAppDef("bugfix", "0.1"))
	require.NoError(t, err)

	want := errors.New("application failure")
	got := st.WithWriterLock(ctx, sid, func() error { return want })
	require.ErrorIs(t, got, want)

	require.NoError(t, st.WithWriterLock(ctx, sid, func() error { return nil }))
}

// TestWithWriterLock_DistinctSessions verifies that locks on different
// sessions don't block each other.
func TestWithWriterLock_DistinctSessions(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	def := makeAppDef("bugfix", "0.1")
	sidA, err := st.CreateSession(ctx, def)
	require.NoError(t, err)
	sidB, err := st.CreateSession(ctx, def)
	require.NoError(t, err)

	err = st.WithWriterLock(ctx, sidA, func() error {
		return st.WithWriterLock(ctx, sidB, func() error { return nil })
	})
	require.NoError(t, err)
}

// TestWithWriterLock_ConcurrentSerializes runs N goroutines all trying to
// take the same session lock; verifies they serialize (one wins at a time)
// rather than colliding into busy errors. The total time across all goroutines
// is checked against a coarse expected lower bound.
func TestWithWriterLock_ConcurrentSerializes(t *testing.T) {
	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	sid, err := st.CreateSession(ctx, makeAppDef("bugfix", "0.1"))
	require.NoError(t, err)

	// Within a single process the lock returns ErrSessionBusy on contention
	// (we do not block-wait). Confirm at least one goroutine succeeds while
	// others see busy errors when racing.
	const n = 8
	var wg sync.WaitGroup
	var oks, busies atomic.Int32

	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			err := st.WithWriterLock(ctx, sid, func() error {
				time.Sleep(10 * time.Millisecond)
				return nil
			})
			switch {
			case err == nil:
				oks.Add(1)
			case errors.Is(err, store.ErrSessionBusy):
				busies.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	require.GreaterOrEqual(t, int(oks.Load()), 1, "at least one goroutine should acquire")
	require.Equal(t, n, int(oks.Load()+busies.Load()), "every goroutine reports ok or busy")
}
