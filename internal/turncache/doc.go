// Package turncache stores prior LLM-resolved turn verdicts keyed by a
// deterministic lexical signature, plus per-synonym hit counters for the
// routing-inspect views. It sits at the bottom of the routing stack: after
// the deterministic, bare-synonym, and template tiers in
// [kitsoki/internal/semroute] all miss, the orchestrator consults this
// cache before paying for an LLM turn, and writes the LLM's verdict back
// here so the next identical phrasing skips the model.
//
// The user-facing reference for the routing stack — the four tiers, the
// cache key, and the eviction policy — is the "Turn cache" section of
// docs/architecture/semantic-routing.md.
//
// # Algorithm
//
// A row is addressed by a four-part [Key] of (App, AppHash, StatePath,
// Signature):
//
//   - App is the app id (e.g. "oregon-trail"); it namespaces rows so one
//     store can serve many apps.
//   - AppHash is the hash of the AppDef intent/slot/synonym surface; it
//     lets [Cache.InvalidateOtherHashes] purge an app's stale snapshots
//     the moment routing-relevant YAML changes, without touching
//     unrelated apps sharing the same store.
//   - StatePath scopes the row to one machine state, so "let's hunt" at
//     the trail does not leak into "let's hunt" at a fort.
//   - Signature is the caller-supplied lexical signature of the input
//     (a sorted, stopword-stripped stem bag); identical phrasings in the
//     same state collide on it and reuse the cached verdict.
//
// Five eviction policies keep the table bounded and honest. The cache only
// executes them when the orchestrator calls the matching method; it never
// sweeps on a timer of its own:
//
//   - App-hash invalidation ([Cache.InvalidateOtherHashes]) drops rows for
//     an app whose AppHash is not the live one — run once at process start.
//   - Re-validate strikes ([Cache.RecordRevalidateFail]) delete a row once
//     it has failed live re-validation [Config.RevalidateStrikes] times in
//     a row; a [Cache.RecordHit] resets the counter.
//   - LRU + size cap ([Cache.TrimLRU]) drops the coldest
//     [Config.TrimFraction] of rows once an app exceeds [Config.Cap].
//   - Time-based expiry ([Cache.SweepCold]) deletes rows whose LastHitAt
//     is older than the [Config.MaxAge] cutoff the caller passes in.
//   - Confidence decay ([Config.ConfidenceDecay], opt-in) halves the
//     effective MaxAge for rows whose originating Confidence is below
//     [confidenceDecayThreshold] during a sweep.
//
// # Contracts
//
// Zero values. The zero [CachedVerdict] is never a valid cache result —
// callers learn "no row" from the boolean [Cache.Get] returns, not from
// inspecting fields. The zero [Config] disables both MaxAge expiry and the
// LRU Cap (each treats zero as "no limit"); a [Config.RevalidateStrikes]
// below 1 is clamped to 1 by both constructors so a row always survives at
// least one strike. [DefaultConfig] supplies the recommended thresholds.
//
// Nil receivers. There are none; always construct through [NewMemory] or
// [NewSQLite].
//
// Concurrency. Both backends are safe for concurrent use. [NewMemory]
// serialises every operation under a single mutex. [NewSQLite] relies on
// modernc.org/sqlite's driver-level write serialisation plus one extra
// package-level lock around the composite read-increment-evict in
// [Cache.RecordRevalidateFail], so two concurrent strike bumps cannot race
// past the eviction threshold.
//
// Errors. Get/Put/RecordHit and the sweep methods return an error only for
// context cancellation or a backend failure (SQLite I/O); a missing row is
// never an error — RecordHit and RecordRevalidateFail are documented no-ops
// on an absent key. SlotsJSON is stored and returned verbatim; the cache
// never parses it, so a malformed blob is the caller's problem, not an
// error here.
//
// # Worked example
//
// One Put then a matching Get round-trips the verdict the orchestrator
// reuses on the next identical turn:
//
//	key:     {App:"oregon-trail", AppHash:"h1",
//	          StatePath:"@river_crossing.scouting", Signature:"06ad...c1"}
//	put:     {Intent:"ford", SlotsJSON:"{}", Confidence:0.92}
//	get(key) -> ok=true, {Intent:"ford", Confidence:0.92, ...}
//
// A runnable form of this trace lives in [ExampleNewMemory]; the
// persistent variant is [ExampleNewSQLite].
//
// # Lifecycle
//
// A backend is created once per process (or per cache file) through
// [NewMemory] or [NewSQLite]. The orchestrator then, in order:
//
//  1. Calls [Cache.InvalidateOtherHashes] at start to drop stale snapshots.
//  2. On each turn, [Cache.Get]s by signature; on a miss it runs the LLM,
//     then [Cache.Put]s the verdict.
//  3. On a hit it re-validates against the live machine and records the
//     outcome via [Cache.RecordHit] or [Cache.RecordRevalidateFail].
//  4. Periodically runs [Cache.SweepCold] and [Cache.TrimLRU] to bound the
//     table.
//  5. Calls [Cache.Close] at shutdown — a no-op for the in-memory backend,
//     a DB close for SQLite.
//
// # Backends
//
// Two implementations of [Cache] ship in the package:
//
//   - [NewMemory] — pure in-process map+mutex. No persistence; the cache
//     evaporates with the process. Cheapest path (Get is a map lookup);
//     appropriate for drive-mode reproducibility runs, unit tests, and any
//     flow where the orchestrator wants cache semantics without
//     cross-session leakage. The default for code that does not opt in to
//     durability.
//
//   - [NewSQLite] — single-file SQLite via modernc.org/sqlite. Adds
//     cross-session reuse (the row survives a process restart) at the cost
//     of one disk-bound op per call (WAL-mode, NORMAL fsync). The bench in
//     sqlite_bench_test.go tracks the read-path latency in CI. Pick this
//     backend when the orchestrator wants the cache to survive process
//     restarts (production runs, multi-session apps), and accept the
//     marginal latency.
//
// Both backends satisfy the same [Cache] interface. The conformance suite
// in conformance_test.go runs every behavioural assertion against both, so
// a regression on one backend that doesn't affect the other surfaces as a
// divergent test outcome rather than silent drift.
//
// # Non-goals
//
//   - Does not decode SlotsJSON. The cache stores the resolved slot map as
//     an opaque blob so the package carries no AppDef type dependency;
//     callers JSON-encode before [Cache.Put] and decode after [Cache.Get].
//   - Does not re-validate verdicts. Whether a cached row is still legal
//     against the live machine is the orchestrator's call; the cache only
//     records the verdict it is told via [Cache.RecordHit] /
//     [Cache.RecordRevalidateFail]. Mixing validation into the store would
//     couple it to the machine and make eviction non-deterministic.
//   - Does not dispatch turns or know the routing tiers. It is the bottom
//     of the stack, not a participant in it — keeping it dumb is what lets
//     both backends share one conformance suite.
//   - Does not sweep on its own clock. Eviction runs only when the
//     orchestrator calls a sweep method, so cache lifetime stays a policy
//     the caller owns and tests can drive deterministically.
package turncache
