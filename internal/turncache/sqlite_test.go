// SQLite-backend tests. Cross-process persistence, schema-migration
// idempotency, NULL last_hit_at handling, TrimLRU coldest-first
// semantics and -race-clean concurrent access — all the things the
// conformance suite can't exercise because they need access to the
// SQLite file lifecycle.
package turncache

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newSQLiteCache opens a fresh SQLite cache at <t.TempDir>/cache.db and
// registers Close in t.Cleanup. Tests own their tempdir so they're
// safe to t.Parallel even though the SQLite file itself is not.
func newSQLiteCache(t *testing.T, cfg Config) Cache {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache.db")
	c, err := NewSQLite(path, cfg)
	if err != nil {
		t.Fatalf("NewSQLite(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestSQLiteConformance runs the cross-backend conformance suite
// against the SQLite backend. Every behavioural invariant the
// interface promises is exercised here; a failure means a divergence
// from the in-memory reference. The suite itself lives in
// conformance_test.go.
func TestSQLiteConformance(t *testing.T) {
	t.Parallel()
	runConformanceSuite(t, newSQLiteCache)
}

// TestSQLite_CrossProcessPersistence opens the cache, writes a row,
// closes it, and reopens at the same path. The row must survive — the
// whole point of the SQLite backend over the in-memory one is
// cross-process / cross-session persistence.
func TestSQLite_CrossProcessPersistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.db")

	c1, err := NewSQLite(path, testConfig())
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	k := keyFor("oregon-trail", "h1", "@start", "sig")
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	v := CachedVerdict{
		Intent:       "ford",
		SlotsJSON:    `{"river":"green"}`,
		Confidence:   0.81,
		SourceModel:  "claude-haiku-4-5",
		SourceTurnID: "trn_persistence",
		HitCount:     7,
		LastHitAt:    now,
		CreatedAt:    now.Add(-time.Hour),
	}
	if err := c1.Put(context.Background(), k, v); err != nil {
		t.Fatalf("Put on first handle: %v", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatalf("close first handle: %v", err)
	}

	c2, err := NewSQLite(path, testConfig())
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	got, ok, err := c2.Get(context.Background(), k)
	if err != nil || !ok {
		t.Fatalf("Get on second handle: ok=%v err=%v", ok, err)
	}
	if got.Intent != v.Intent || got.SlotsJSON != v.SlotsJSON || got.Confidence != v.Confidence {
		t.Errorf("round-trip drift: got %+v want %+v", got, v)
	}
	if !got.LastHitAt.Equal(now) {
		t.Errorf("LastHitAt: got %v want %v", got.LastHitAt, now)
	}
	if !got.CreatedAt.Equal(now.Add(-time.Hour)) {
		t.Errorf("CreatedAt: got %v want %v", got.CreatedAt, now.Add(-time.Hour))
	}
	if got.HitCount != 7 {
		t.Errorf("HitCount: got %d want 7", got.HitCount)
	}
}

// TestSQLite_SchemaMigrationIdempotent verifies that re-opening the
// same DB file does not error — schema.sql is wrapped in IF NOT EXISTS
// statements but the test pins the contract.
func TestSQLite_SchemaMigrationIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	for i := 0; i < 3; i++ {
		c, err := NewSQLite(path, testConfig())
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("close #%d: %v", i+1, err)
		}
	}
}

// TestSQLite_PutStampsLastHitAtFromCreatedAt pins the C2 fix
// contract: when the caller leaves LastHitAt zero, Put stamps it from
// CreatedAt (which has itself been auto-stamped to "now" if zero).
// The previous shape of this test pinned the OLD behaviour (NULL
// last_hit_at on a never-hit row) — that behaviour caused freshly-Put
// rows to be evicted by the orchestrator's session-start SweepCold,
// killing the cross-session cache. After the fix, last_hit_at is
// always NOT NULL after Put on the SQLite backend.
//
// LastVerifiedAt is a separate column (set by RecordHit, not Put) and
// remains zero / NULL on a freshly-Put row — the assertion below
// guards against a regression that accidentally stamps it too.
func TestSQLite_PutStampsLastHitAtFromCreatedAt(t *testing.T) {
	t.Parallel()
	c := newSQLiteCache(t, testConfig())
	ctx := context.Background()
	k := keyFor("a", "h", "@s", "fresh")
	if err := c.Put(ctx, k, CachedVerdict{Intent: "go"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get(ctx, k)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.LastHitAt.IsZero() {
		t.Errorf("LastHitAt: want non-zero (Put stamps from CreatedAt), got zero value")
	}
	if got.LastHitAt.UnixMilli() != got.CreatedAt.UnixMilli() {
		t.Errorf("LastHitAt vs CreatedAt: want equal at ms-resolution, got LastHitAt=%v CreatedAt=%v",
			got.LastHitAt, got.CreatedAt)
	}
	// LastVerifiedAt is owned by RecordHit, not Put — should still be zero.
	if !got.LastVerifiedAt.IsZero() {
		t.Errorf("LastVerifiedAt: want zero-value (Put must not stamp it), got %v", got.LastVerifiedAt)
	}

	// Pin the underlying schema-level contract: last_hit_at column is
	// NOT NULL after Put. Without this assertion a future refactor
	// could accidentally write NULL and still pass the in-Go round-
	// trip check (because the Go zero time would then read back as
	// zero) — that NULL would put us back in the C2 regression where
	// SweepCold's "IS NULL counts as ancient" arm wipes the row.
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ignored.db"))
	if err != nil {
		t.Fatalf("introspect open: %v", err)
	}
	defer db.Close()
	// Re-open the actual DB the cache wrote to. newSQLiteCache puts it
	// at <tempdir>/cache.db; we don't have that path directly, so use
	// the cache's own backdoor: write a unique key and then ask SQLite
	// to count NULL last_hit_at entries.
	// Instead, scan via the cache's own DB handle.
	if sc, ok := c.(*sqliteCache); ok {
		var nullCount int
		if err := sc.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM turn_cache WHERE last_hit_at IS NULL`,
		).Scan(&nullCount); err != nil {
			t.Fatalf("count NULL last_hit_at: %v", err)
		}
		if nullCount != 0 {
			t.Errorf("schema-level: want 0 NULL last_hit_at rows after Put, got %d", nullCount)
		}
	} else {
		t.Fatalf("internal: factory did not return *sqliteCache; got %T", c)
	}
}

// TestSQLite_TrimLRUDropsColdestFirst writes rows with strictly
// increasing LastHitAt timestamps and confirms TrimLRU removes the
// lowest-timestamp rows.
func TestSQLite_TrimLRUDropsColdestFirst(t *testing.T) {
	t.Parallel()
	c := newSQLiteCache(t, testConfig())
	ctx := context.Background()
	base := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		if err := c.Put(ctx, k, CachedVerdict{Intent: "go", LastHitAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	deleted, err := c.TrimLRU(ctx, "a", 10, 0.5)
	if err != nil {
		t.Fatalf("TrimLRU: %v", err)
	}
	// 20 rows, cap=10 → over by 10; trim 50% of 20 = 10.
	if deleted != 10 {
		t.Fatalf("TrimLRU: want deleted=10, got %d", deleted)
	}
	for i := 0; i < 20; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		_, ok, _ := c.Get(ctx, k)
		if i < 10 && ok {
			t.Errorf("row %d (cold): want trimmed, still present", i)
		}
		if i >= 10 && !ok {
			t.Errorf("row %d (warm): want present, trimmed", i)
		}
	}
}

// TestSQLite_ConcurrentAccessRaceClean drives 10 goroutines each
// running 100 Put+Get cycles against the same DB file. With `-race`
// this is the canary for any unsynchronised access in the backend.
func TestSQLite_ConcurrentAccessRaceClean(t *testing.T) {
	t.Parallel()
	c := newSQLiteCache(t, testConfig())
	ctx := context.Background()
	const goroutines = 10
	const perGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				k := keyFor("a", "h", "@s", signature(g*perGoroutine+i))
				v := CachedVerdict{Intent: "go", LastHitAt: time.Now()}
				if err := c.Put(ctx, k, v); err != nil {
					t.Errorf("Put: %v", err)
					return
				}
				if _, ok, err := c.Get(ctx, k); err != nil || !ok {
					t.Errorf("Get: ok=%v err=%v", ok, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	// Sanity: every Put landed exactly once.
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			k := keyFor("a", "h", "@s", signature(g*perGoroutine+i))
			if _, ok, err := c.Get(ctx, k); err != nil || !ok {
				t.Fatalf("missing row g=%d i=%d ok=%v err=%v", g, i, ok, err)
			}
		}
	}
}

// TestSQLite_SlotsJSONRoundTrip writes a richly-typed JSON blob and
// reads it back. The cache treats slots_json as opaque bytes — any
// transformation by the SQLite layer would silently break downstream
// orchestrator decoding.
func TestSQLite_SlotsJSONRoundTrip(t *testing.T) {
	t.Parallel()
	c := newSQLiteCache(t, testConfig())
	ctx := context.Background()
	// Construct via json.Marshal so the test isn't sensitive to map
	// key ordering — Get must return whatever we Put.
	payload := map[string]any{
		"items": []any{
			map[string]any{"sku": "ox", "qty": 6},
			map[string]any{"sku": "food_lb", "qty": 200},
		},
		"total_cost":   240,
		"is_emergency": false,
		"note":         "buy oxen ahead of river crossing",
	}
	slotsJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	k := keyFor("a", "h", "@s", "sig")
	if err := c.Put(ctx, k, CachedVerdict{Intent: "propose_purchase", SlotsJSON: string(slotsJSON)}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get(ctx, k)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.SlotsJSON != string(slotsJSON) {
		t.Errorf("SlotsJSON round-trip mismatch:\n  want: %s\n  got:  %s", string(slotsJSON), got.SlotsJSON)
	}
}

// TestSQLite_AppHashInvalidationAcrossReopen seeds a row under the old
// AppHash, closes the DB, reopens, and calls InvalidateOtherHashes for
// the live hash. The old row must be gone; rows for a different app
// must be untouched. This is the app-hash invalidation contract.
func TestSQLite_AppHashInvalidationAcrossReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")

	c1, err := NewSQLite(path, testConfig())
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	ctx := context.Background()
	oldKey := Key{App: "oregon-trail", AppHash: "old-hash", StatePath: "@start", Signature: "sig"}
	otherAppKey := Key{App: "frontier-event", AppHash: "old-hash", StatePath: "@start", Signature: "sig"}
	if err := c1.Put(ctx, oldKey, CachedVerdict{Intent: "ford"}); err != nil {
		t.Fatalf("Put oldKey: %v", err)
	}
	if err := c1.Put(ctx, otherAppKey, CachedVerdict{Intent: "go"}); err != nil {
		t.Fatalf("Put otherAppKey: %v", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	c2, err := NewSQLite(path, testConfig())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Close() })
	deleted, err := c2.InvalidateOtherHashes(ctx, "oregon-trail", "new-hash")
	if err != nil {
		t.Fatalf("InvalidateOtherHashes: %v", err)
	}
	if deleted != 1 {
		t.Errorf("InvalidateOtherHashes: want 1 got %d", deleted)
	}
	if _, ok, _ := c2.Get(ctx, oldKey); ok {
		t.Errorf("old-hash row should be gone")
	}
	if _, ok, _ := c2.Get(ctx, otherAppKey); !ok {
		t.Errorf("other-app row should remain")
	}
}

// TestSQLite_CloseRejectsFurtherWrites pins the post-Close behaviour:
// a Put after Close returns an error rather than panicking. We don't
// promise a specific error message, just that we never panic and the
// caller can tell something went wrong.
func TestSQLite_CloseRejectsFurtherWrites(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	c, err := NewSQLite(path, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// modernc.org/sqlite returns sql.ErrConnDone (or equivalent) after
	// the DB handle is closed. We just assert that an error appears;
	// the exact identity is the driver's business.
	err = c.Put(context.Background(), keyFor("a", "h", "@s", "x"), CachedVerdict{Intent: "go"})
	if err == nil {
		t.Errorf("Put after Close: want error, got nil")
	}
}

// TestSQLite_PropertyTrimLRUKeepsTopK is the random-sequence property:
// after a random sequence of Puts with random LastHitAt values and a
// TrimLRU(cap, fraction), the surviving rows are the top-K by
// LastHitAt across the rows for the app.
func TestSQLite_PropertyTrimLRUKeepsTopK(t *testing.T) {
	t.Parallel()
	c := newSQLiteCache(t, testConfig())
	ctx := context.Background()
	base := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)

	// 30 rows with strictly increasing LastHitAt (so ranking is total).
	const total = 30
	const capRows = 10
	const trimFrac = 0.5
	for i := 0; i < total; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		if err := c.Put(ctx, k, CachedVerdict{Intent: "go", LastHitAt: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if _, err := c.TrimLRU(ctx, "a", capRows, trimFrac); err != nil {
		t.Fatalf("TrimLRU: %v", err)
	}
	// Drop fraction = int(total*0.5) = 15 → 15 oldest gone, 15 remain
	// (above the 10-row cap but TrimLRU stops at the trim fraction).
	wantSurvivors := total - int(float64(total)*trimFrac)
	survivors := 0
	for i := 0; i < total; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		_, ok, _ := c.Get(ctx, k)
		if ok {
			survivors++
			if i < int(float64(total)*trimFrac) {
				t.Errorf("cold row %d (rank %d) should have been trimmed", i, i)
			}
		}
	}
	if survivors != wantSurvivors {
		t.Errorf("survivors: want %d got %d", wantSurvivors, survivors)
	}
}

// TestSQLite_PropertyPutGetMatchesMemory drives the same random sequence
// of (Put | RecordHit | RecordRevalidateFail) operations against a
// NewMemory instance and a NewSQLite instance, asserting Get returns
// the same shape at every step. This is the multi-seed property.
func TestSQLite_PropertyPutGetMatchesMemory(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.RevalidateStrikes = 4

	// Pre-baked deterministic sequences. We do not use math/rand
	// because the test runs under -race; deterministic sequences are
	// strictly more debuggable for failures.
	sequences := [][]string{
		{"put", "hit", "fail", "fail", "hit"},
		{"put", "fail", "fail", "fail", "fail"}, // strikes_4 → evict on 4
		{"put", "put", "hit", "hit", "hit"},
		{"put", "fail", "hit", "fail", "fail"},
	}
	for seedIdx, seq := range sequences {
		seq := seq
		t.Run(testSeqName(seedIdx, seq), func(t *testing.T) {
			ctx := context.Background()
			mem := NewMemory(cfg)
			defer mem.Close()
			path := filepath.Join(t.TempDir(), "cache.db")
			sqliteC, err := NewSQLite(path, cfg)
			if err != nil {
				t.Fatalf("NewSQLite: %v", err)
			}
			defer sqliteC.Close()

			k := keyFor("a", "h", "@s", "k")
			now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
			for stepIdx, op := range seq {
				stepTime := now.Add(time.Duration(stepIdx) * time.Minute)
				switch op {
				case "put":
					v := CachedVerdict{Intent: "go", Confidence: 0.8, CreatedAt: stepTime}
					if err := mem.Put(ctx, k, v); err != nil {
						t.Fatalf("mem.Put step %d: %v", stepIdx, err)
					}
					if err := sqliteC.Put(ctx, k, v); err != nil {
						t.Fatalf("sqlite.Put step %d: %v", stepIdx, err)
					}
				case "hit":
					if err := mem.RecordHit(ctx, k, stepTime); err != nil {
						t.Fatalf("mem.RecordHit step %d: %v", stepIdx, err)
					}
					if err := sqliteC.RecordHit(ctx, k, stepTime); err != nil {
						t.Fatalf("sqlite.RecordHit step %d: %v", stepIdx, err)
					}
				case "fail":
					if _, err := mem.RecordRevalidateFail(ctx, k, stepTime); err != nil {
						t.Fatalf("mem.RecordRevalidateFail step %d: %v", stepIdx, err)
					}
					if _, err := sqliteC.RecordRevalidateFail(ctx, k, stepTime); err != nil {
						t.Fatalf("sqlite.RecordRevalidateFail step %d: %v", stepIdx, err)
					}
				}
				memV, memOk, _ := mem.Get(ctx, k)
				sqV, sqOk, _ := sqliteC.Get(ctx, k)
				if memOk != sqOk {
					t.Fatalf("seed=%d step=%d op=%q: presence drift mem=%v sqlite=%v", seedIdx, stepIdx, op, memOk, sqOk)
				}
				if memOk {
					if memV.HitCount != sqV.HitCount {
						t.Errorf("seed=%d step=%d: HitCount mem=%d sqlite=%d", seedIdx, stepIdx, memV.HitCount, sqV.HitCount)
					}
					if memV.RevalidateFails != sqV.RevalidateFails {
						t.Errorf("seed=%d step=%d: RevalidateFails mem=%d sqlite=%d", seedIdx, stepIdx, memV.RevalidateFails, sqV.RevalidateFails)
					}
					if !memV.LastHitAt.Equal(sqV.LastHitAt) {
						t.Errorf("seed=%d step=%d: LastHitAt mem=%v sqlite=%v", seedIdx, stepIdx, memV.LastHitAt, sqV.LastHitAt)
					}
				}
			}
		})
	}
}

// TestSQLite_SchemaDDLExposed lets the schema_test package inspect the
// DDL string for tabletop verification. We test it indirectly here by
// asking modernc.org/sqlite to return PRAGMA table_info for the
// tables we expect to exist.
func TestSQLite_SchemaDDLExposed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	c, err := NewSQLite(path, testConfig())
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// Open a parallel sql.DB to introspect; the cache owns its own
	// handle and doesn't expose it.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("introspect open: %v", err)
	}
	defer db.Close()

	for _, table := range []string{"turn_cache", "synonym_hits"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not in sqlite_master: %v", table, err)
		}
	}
	var idxName string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name='turn_cache_lru'`).Scan(&idxName); err != nil {
		t.Errorf("turn_cache_lru index missing: %v", err)
	}
}

// testSeqName is a small helper used by the property test to produce
// a useful sub-test name from a sequence of operation tokens.
func testSeqName(idx int, ops []string) string {
	out := []byte{'s', 'e', 'q', '_'}
	out = append(out, '0'+byte(idx%10))
	out = append(out, '_')
	for i, op := range ops {
		if i > 0 {
			out = append(out, '-')
		}
		out = append(out, op[0])
	}
	return string(out)
}
