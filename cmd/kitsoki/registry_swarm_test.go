package main

// registry_swarm_test.go exercises the swarm-session-cap behavior added to
// SessionRegistry: a configurable max live-session count, least-recently-active
// IDLE eviction when session.new would exceed it, mid-turn protection (a
// session with a driver call in flight is never evicted even if it is the
// oldest), and the cleanup an eviction performs (the entry is fully
// unreachable via Get, and its trace sink is actually closed rather than
// merely orphaned).
//
// The single test name TestSessionRegistry_CapAndEviction is required verbatim
// by the swarm-session-cap gate (it greps for the literal string), so the
// scenarios below are table-driven subtests under that one Test function
// rather than split into separately named tests.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/webconfig"
)

// swarmCapRegistry builds a registry in the same nil-harness (no-LLM) posture
// deterministicBase uses, over a single mini-story, and caps it at maxLive.
func swarmCapRegistry(t *testing.T, maxLive int) (*SessionRegistry, string) {
	t.Helper()
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)
	reg.SetMaxSessions(maxLive)
	_, err := reg.Rescan()
	require.NoError(t, err)
	return reg, appPath
}

// setLastActive backdates (or forward-dates) id's activity clock directly, so
// tests can control eviction order deterministically instead of racing
// wall-clock resolution between back-to-back NewSession calls.
func setLastActive(t *testing.T, reg *SessionRegistry, id string, when time.Time) {
	t.Helper()
	reg.mu.Lock()
	defer reg.mu.Unlock()
	e, ok := reg.sessions[id]
	require.True(t, ok, "setLastActive: unknown session %q", id)
	e.lastActive = when
}

// setBusy marks id mid-turn (or clears the mark), exactly as trackingDriver's
// beginTurn/endTurn would while a real Turn/SubmitDirect/etc. call is in
// flight, so tests can prove mid-turn protection without needing a real
// long-running driver call racing the eviction check.
func setBusy(t *testing.T, reg *SessionRegistry, id string, busy bool) {
	t.Helper()
	reg.mu.Lock()
	e, ok := reg.sessions[id]
	reg.mu.Unlock()
	require.True(t, ok, "setBusy: unknown session %q", id)
	if busy {
		atomic.AddInt32(&e.turnsInFlight, 1)
	} else {
		atomic.AddInt32(&e.turnsInFlight, -1)
	}
}

// fakeNotifier records AttachSession/EmitCurrentSession calls so the cleanup
// subtest can prove the relay was actually registered while the session was
// live (the thing an eviction must stop leaking), without pulling in the real
// runstatus server.
type fakeNotifier struct {
	attached []string
}

func (f *fakeNotifier) AttachSession(_ *orchestrator.Orchestrator, _ app.SessionID, publicID string, _ *jobs.JobStore) {
	f.attached = append(f.attached, publicID)
}
func (f *fakeNotifier) EmitCurrentSession(string, bool) {}

func TestSessionRegistry_CapAndEviction(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "cap enforcement evicts the oldest idle session",
			run: func(t *testing.T) {
				reg, appPath := swarmCapRegistry(t, 2)

				idA, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)
				idB, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)
				require.Len(t, reg.List(), 2, "both sessions live under the cap")

				// A is the oldest by construction; make that unambiguous against
				// wall-clock resolution.
				setLastActive(t, reg, idA, time.Now().Add(-time.Hour))
				setLastActive(t, reg, idB, time.Now())

				idC, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err, "session.new beyond the cap must evict, not error, when an idle victim exists")

				assert.Len(t, reg.List(), 2, "the live count must stay at the cap")
				_, ok := reg.Get(idA)
				assert.False(t, ok, "the least-recently-active idle session (A) must be evicted")
				_, ok = reg.Get(idB)
				assert.True(t, ok, "B must survive (more recently active than A)")
				_, ok = reg.Get(idC)
				assert.True(t, ok, "the newly created session must be live")
			},
		},
		{
			name: "LRU-idle choice ranks by activity, not creation order",
			run: func(t *testing.T) {
				reg, appPath := swarmCapRegistry(t, 2)

				idA, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)
				idB, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)

				// B was created AFTER A, but stamp B as the less recently
				// active of the two — eviction must follow activity, not
				// insertion order.
				setLastActive(t, reg, idA, time.Now())
				setLastActive(t, reg, idB, time.Now().Add(-time.Hour))

				_, err = reg.NewSession(ctx, appPath)
				require.NoError(t, err)

				_, ok := reg.Get(idB)
				assert.False(t, ok, "B (least recently active) must be evicted even though it is younger than A")
				_, ok = reg.Get(idA)
				assert.True(t, ok, "A (more recently active) must survive")
			},
		},
		{
			name: "mid-turn protection never evicts a busy session even if it is oldest",
			run: func(t *testing.T) {
				reg, appPath := swarmCapRegistry(t, 2)

				idA, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)
				idB, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)

				// A is by far the oldest / least active, but mark it mid-turn.
				setLastActive(t, reg, idA, time.Now().Add(-time.Hour))
				setLastActive(t, reg, idB, time.Now().Add(-time.Minute))
				setBusy(t, reg, idA, true)
				t.Cleanup(func() { setBusy(t, reg, idA, false) })

				idC, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err, "B is idle and evictable, so session.new must still succeed")

				_, ok := reg.Get(idA)
				assert.True(t, ok, "a mid-turn session must never be evicted, even though it is the oldest")
				_, ok = reg.Get(idB)
				assert.False(t, ok, "the idle session (B) must be evicted instead")
				_, ok = reg.Get(idC)
				assert.True(t, ok)
			},
		},
		{
			name: "no evictable session returns a clear error instead of blocking or exceeding the cap",
			run: func(t *testing.T) {
				reg, appPath := swarmCapRegistry(t, 1)

				idA, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)
				setBusy(t, reg, idA, true)
				t.Cleanup(func() { setBusy(t, reg, idA, false) })

				_, err = reg.NewSession(ctx, appPath)
				require.Error(t, err, "cap reached with the only live session mid-turn must fail, not hang or exceed the cap")
				assert.ErrorIs(t, err, ErrNoEvictableSession)

				assert.Len(t, reg.List(), 1, "the cap must not be silently exceeded")
				_, ok := reg.Get(idA)
				assert.True(t, ok, "the busy session must remain live and untouched")
			},
		},
		{
			name: "eviction cleans up the notification relay registration and closes the trace sink",
			run: func(t *testing.T) {
				reg, appPath := swarmCapRegistry(t, 1)
				notif := &fakeNotifier{}
				reg.SetNotifier(notif)

				idA, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)
				assert.Contains(t, notif.attached, idA, "NewSession must attach the notification relay while the session is live")

				reg.mu.Lock()
				entryA := reg.sessions[idA]
				reg.mu.Unlock()
				require.NotNil(t, entryA)
				require.NotNil(t, entryA.sink, "session must have a trace sink to prove gets closed on eviction")

				// A second session.new beyond the cap of 1 evicts A.
				idB, err := reg.NewSession(ctx, appPath)
				require.NoError(t, err)
				assert.Contains(t, notif.attached, idB)

				_, ok := reg.Get(idA)
				assert.False(t, ok, "evicted session must be fully gone from Get (server maps this to a clear session-gone error)")

				// The trace sink underlying the evicted session's Source must
				// actually be released, not merely orphaned in memory — proves
				// cleanupEvicted ran rather than just deleting the map entry.
				appendErr := entryA.sink.Append(store.Event{Kind: store.TransitionApplied, Turn: 0})
				assert.Error(t, appendErr, "the evicted session's trace sink must be closed")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
