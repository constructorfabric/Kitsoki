// Integration tests for the Phase-7 turn-cache wiring (see
// docs/architecture/semantic-routing.md "Turn cache").
//
// Race note: these tests deliberately do NOT call t.Parallel. The
// machine package owns a package-level `eventSeq` counter (see
// internal/machine/machine.go:1426) that increments on every
// newEvent call. Running multiple test goroutines that each spin up
// their own Orchestrator + Machine and exercise the
// orchestrator → machine.Turn path concurrently triggers `-race`
// because newEvent's `eventSeq++` is unsynchronised. This is a
// pre-existing condition in the machine package, not a bug
// introduced by the cache wiring — but the cache tests' SubmitDirect
// path inside tryTurnCache makes it MORE likely to fire when
// `-race` is set. Production code paths serialise through the
// session lock, but tests that use different sessions don't.
// Surfaced and documented for follow-up.
//
// The unit tests for the cache itself live in
// internal/turncache/*_test.go; these exercise the orchestrator
// glue:
//
//   - On a clean (uncached) free-form input the LLM is hit and the
//     verdict is written to the cache.
//   - The same input on the same state hits the cache on the next
//     turn — no LLM call, the cache short-circuits via SubmitDirect.
//   - A cached row at a different state_path is a miss — the cache
//     key is (app, hash, state, signature) so a verdict learned at
//     state A never leaks into state B.
//   - When the underlying machine.Validate would reject the cached
//     verdict (e.g. the state's allowed-intent list changed), the
//     row's revalidate_fails increments and after RevalidateStrikes
//     the row is evicted.
package orchestrator_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/turncache"
)

// newCacheTestApp builds a tiny two-state app where the harness routes
// any free-form input to the `proceed` intent. Routing is on but no
// synonym matches the LLM-only inputs we use, so every fresh turn
// pays the harness call cost — letting the test assert the cache's
// short-circuit by counting harness invocations.
func newCacheTestApp(t *testing.T, cache turncache.Cache) (*orchestrator.Orchestrator, *countingHarness, app.SessionID) {
	t.Helper()
	const appYAML = `
app:
  id: cache-test
  version: 0.1.0

world: {}

routing:
  enabled: true

intents:
  proceed:
    title: "Proceed"
    examples: ["proceed"]
  rest:
    title: "Rest"
    examples: ["rest"]

root: start

states:
  start:
    view: "at start"
    on:
      proceed:
        - target: middle
      rest:
        - target: middle
  middle:
    view: "at middle"
    on:
      proceed:
        - target: ended
      rest:
        - target: ended
  ended:
    terminal: true
    view: "done"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h := &countingHarness{fall: staticHarness{intentName: "proceed"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithTurnCache(cache))
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	return orch, h, sid
}

// TestCache_FirstTurnLLM_SecondTurnCache is the canonical happy path:
// the first turn pays the LLM cost and writes a row keyed by
// (app, hash, state, signature); a fresh session at the SAME starting
// state issuing the SAME input hits the cache and skips the LLM
// entirely.
//
// We use two sessions rather than two turns in one session because
// the state advances after the first turn, so a same-session second
// turn lands at state=middle (a cache miss by design — different
// state_path).
func TestCache_FirstTurnLLM_SecondTurnCache(t *testing.T) {
	// NOTE: not t.Parallel — see the note at the top of this file
	// (machine.eventSeq is a package-level int that races under parallel
	// tests that all spin up their own Orchestrator).
	cache := turncache.NewMemory(turncache.DefaultConfig())
	t.Cleanup(func() { _ = cache.Close() })

	orch, h, sid := newCacheTestApp(t, cache)
	ctx := context.Background()

	// First turn (session A): no cache row → LLM hit.
	out, err := orch.Turn(ctx, sid, "let's keep going friend")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.EqualValues(t, 1, h.calls.Load(), "first turn must hit the LLM")

	// Second session, same starting state, same input: cache hit.
	sid2, err := orch.NewSession(ctx)
	require.NoError(t, err)
	out2, err := orch.Turn(ctx, sid2, "let's keep going friend")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out2.Mode)
	require.EqualValues(t, 1, h.calls.Load(),
		"second identical turn must hit the cache, not the LLM")
}

// TestCache_DifferentStatePath_IsMiss pins the worked example:
// a cached row at state A must not serve a turn at state B with the
// same input.
func TestCache_DifferentStatePath_IsMiss(t *testing.T) {
	// NOTE: not t.Parallel — see the note at the top of this file
	// (machine.eventSeq is a package-level int that races under parallel
	// tests that all spin up their own Orchestrator).
	cache := turncache.NewMemory(turncache.DefaultConfig())
	t.Cleanup(func() { _ = cache.Close() })

	orch, h, _ := newCacheTestApp(t, cache)
	ctx := context.Background()

	// Two separate sessions exercise two states without smearing the
	// same session through both. Session-A's terminal-state guard
	// would short-circuit any second turn for it; using independent
	// sessions keeps the LLM-call accounting clean.
	sidA, err := orch.NewSession(ctx)
	require.NoError(t, err)
	sidB, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// At state=start, input X resolves via LLM. The cache writes a
	// row keyed (app, hash, "start", sig(X)).
	_, err = orch.Turn(ctx, sidA, "carry forward chums")
	require.NoError(t, err)
	require.EqualValues(t, 1, h.calls.Load(), "first session: LLM hit")

	// Now drive session B to state=middle by issuing a normal turn,
	// then re-issue the same input. Because the cache key includes
	// state_path, the row at state=start does NOT serve state=middle.
	_, err = orch.Turn(ctx, sidB, "proceed")
	require.NoError(t, err)
	// staticHarness short-circuits "proceed" via the deterministic
	// tier — no LLM call needed.
	require.EqualValues(t, 1, h.calls.Load(),
		"after deterministic match, LLM call count unchanged")

	_, err = orch.Turn(ctx, sidB, "carry forward chums")
	require.NoError(t, err)
	require.EqualValues(t, 2, h.calls.Load(),
		"different state_path: cache miss → LLM hit")
}

// TestCache_SQLite_StrikesEvictAfterRevalidateFails exercises the
// strike-out path: a cached row that re-validates against a
// machine whose allowed-intent list has shifted is struck three
// times, then evicted. We can't easily mutate the machine
// definition mid-test, so we drive the strike count up directly via
// the cache backend's API to confirm RecordRevalidateFail does its
// job. (The orchestrator-side wiring is covered by the first two
// tests above, which exercise the happy path; this test pins the
// strike threshold itself.)
func TestCache_SQLite_StrikesEvictAfterRevalidateFails(t *testing.T) {
	t.Parallel() // SQLite-only path; no Orchestrator → no shared eventSeq.
	dir := t.TempDir()
	cache, err := turncache.NewSQLite(filepath.Join(dir, "cache.sqlite"),
		turncache.Config{RevalidateStrikes: 3})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	ctx := context.Background()
	k := turncache.Key{App: "x", AppHash: "h", StatePath: "s", Signature: "sig"}
	require.NoError(t, cache.Put(ctx, k, turncache.CachedVerdict{
		Intent:    "rest",
		SlotsJSON: "{}",
		CreatedAt: time.Now(),
	}))

	// Strike 1 — row stays.
	evicted, err := cache.RecordRevalidateFail(ctx, k, time.Now())
	require.NoError(t, err)
	require.False(t, evicted, "first strike must not evict")
	// Strike 2 — row stays.
	evicted, err = cache.RecordRevalidateFail(ctx, k, time.Now())
	require.NoError(t, err)
	require.False(t, evicted, "second strike must not evict")
	// Strike 3 — row gone.
	evicted, err = cache.RecordRevalidateFail(ctx, k, time.Now())
	require.NoError(t, err)
	require.True(t, evicted, "third strike must evict")
	_, found, err := cache.Get(ctx, k)
	require.NoError(t, err)
	require.False(t, found, "row must be absent after eviction")
}

// TestCache_AppHashInvalidationAtSessionStart confirms the
// session-start invalidation behaviour the orchestrator owns: opening a session for an app
// whose hash has changed wipes the stale rows on first cache use.
//
// Because the orchestrator's session-start sweep fires on first
// Turn (when the cache is consulted), we seed a stale-hash row,
// then issue a turn for a different hash and verify the row is gone.
func TestCache_AppHashInvalidationAtSessionStart(t *testing.T) {
	// NOTE: not t.Parallel — see the note at the top of this file
	// (machine.eventSeq is a package-level int that races under parallel
	// tests that all spin up their own Orchestrator).
	cache := turncache.NewMemory(turncache.DefaultConfig())
	t.Cleanup(func() { _ = cache.Close() })

	// Seed: a row under app "cache-test" but with a fake hash that
	// differs from whatever ComputeAppHash returns for our test app.
	ctx := context.Background()
	staleKey := turncache.Key{App: "cache-test", AppHash: "stale-hash", StatePath: "start", Signature: "x"}
	require.NoError(t, cache.Put(ctx, staleKey, turncache.CachedVerdict{
		Intent:    "rest",
		SlotsJSON: "{}",
		CreatedAt: time.Now(),
	}))
	// Sanity: row is present.
	_, found, err := cache.Get(ctx, staleKey)
	require.NoError(t, err)
	require.True(t, found, "seeded stale-hash row must be present pre-sweep")

	// Drive a turn — the sweep fires on first cache use.
	orch, _, sid := newCacheTestApp(t, cache)
	_, err = orch.Turn(ctx, sid, "go forth")
	require.NoError(t, err)

	// Stale row should be gone.
	_, found, err = cache.Get(ctx, staleKey)
	require.NoError(t, err)
	require.False(t, found, "stale-hash row must be evicted by session-start sweep")
}
