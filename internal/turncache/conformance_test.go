// Conformance-suite helper. Every behavioural invariant the [Cache]
// interface promises is exercised here against a backend-factory, then
// called from both memory_test.go (TestMemoryConformance) and
// sqlite_test.go (TestSQLiteConformance). When this suite passes
// against a backend, the backend is interface-equivalent to the
// in-memory reference implementation for orchestrator integration.
//
// The matrix here intentionally overlaps with the cases in
// memory_test.go — that file is the per-backend smoke for the memory
// implementation, and this file is the cross-backend contract. Keeping
// them as two layers means a regression in either backend is caught by
// the failure-relevant tests.
package turncache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// cacheFactory builds a Cache against the supplied Config. The
// returned Cache must be safe to close from t.Cleanup; the factory is
// expected to register that cleanup itself.
type cacheFactory func(t *testing.T, cfg Config) Cache

// runConformanceSuite hammers every behavioural invariant of the
// [Cache] interface against the supplied backend. Tests inside use
// t.Run so a backend regression points at the precise behaviour that
// broke.
func runConformanceSuite(t *testing.T, mk cacheFactory) {
	t.Helper()

	t.Run("PutGetRoundTrip", func(t *testing.T) {
		conformancePutGetRoundTrip(t, mk)
	})
	t.Run("GetUnknownKeyMisses", func(t *testing.T) {
		conformanceGetUnknownKeyMisses(t, mk)
	})
	t.Run("PutAutoSeedsCreatedAt", func(t *testing.T) {
		conformancePutAutoSeedsCreatedAt(t, mk)
	})
	t.Run("PutOverwriteReplaces", func(t *testing.T) {
		conformancePutOverwriteReplaces(t, mk)
	})
	t.Run("RecordHitIncrementsAndResetsStrikes", func(t *testing.T) {
		conformanceRecordHitIncrementsAndResetsStrikes(t, mk)
	})
	t.Run("RecordRevalidateFailEvicts", func(t *testing.T) {
		conformanceRecordRevalidateFailEvicts(t, mk)
	})
	t.Run("RecordRevalidateFailMissingIsNoop", func(t *testing.T) {
		conformanceRecordRevalidateFailMissingIsNoop(t, mk)
	})
	t.Run("RecordHitMissingIsNoop", func(t *testing.T) {
		conformanceRecordHitMissingIsNoop(t, mk)
	})
	t.Run("InvalidateOtherHashes", func(t *testing.T) {
		conformanceInvalidateOtherHashes(t, mk)
	})
	t.Run("InvalidateOtherHashesClearsSynonymHits", func(t *testing.T) {
		conformanceInvalidateOtherHashesClearsSynonymHits(t, mk)
	})
	t.Run("SweepCold", func(t *testing.T) {
		conformanceSweepCold(t, mk)
	})
	t.Run("SweepColdNeverHit", func(t *testing.T) {
		conformanceSweepColdNeverHit(t, mk)
	})
	t.Run("SweepColdConfidenceDecay", func(t *testing.T) {
		conformanceSweepColdConfidenceDecay(t, mk)
	})
	t.Run("PutStampsLastHitAtFromCreatedAt", func(t *testing.T) {
		conformancePutStampsLastHitAtFromCreatedAt(t, mk)
	})
	t.Run("PutPreservesCallerLastHitAt", func(t *testing.T) {
		conformancePutPreservesCallerLastHitAt(t, mk)
	})
	t.Run("PropertyLastHitAtNotBeforeCreatedAt", func(t *testing.T) {
		conformancePropertyLastHitAtNotBeforeCreatedAt(t, mk)
	})
	t.Run("TrimLRU", func(t *testing.T) {
		conformanceTrimLRU(t, mk)
	})
	t.Run("TrimLRUNoop", func(t *testing.T) {
		conformanceTrimLRUNoop(t, mk)
	})
	t.Run("RecordSynonymHitAndStats", func(t *testing.T) {
		conformanceRecordSynonymHitAndStats(t, mk)
	})
	t.Run("SynonymStatsEmpty", func(t *testing.T) {
		conformanceSynonymStatsEmpty(t, mk)
	})
	t.Run("ContextCancellation", func(t *testing.T) {
		conformanceContextCancellation(t, mk)
	})
	t.Run("CloseIdempotent", func(t *testing.T) {
		conformanceCloseIdempotent(t, mk)
	})
	t.Run("ConcurrentMutations", func(t *testing.T) {
		conformanceConcurrentMutations(t, mk)
	})
	t.Run("FinalStateMatchesMemory", func(t *testing.T) {
		conformanceFinalStateMatchesMemory(t, mk)
	})
}

// conformanceCloseIdempotent intentionally creates the cache outside
// the helper-supplied factory so the second Close happens on the same
// instance without t.Cleanup re-closing it.
func conformanceCloseIdempotent(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: unexpected error %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: unexpected error %v", err)
	}
}

func conformancePutGetRoundTrip(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	k := keyFor("oregon-trail", "h1", "@start", "sig-buy-oxen")
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	v := CachedVerdict{
		Intent:       "propose_purchase",
		SlotsJSON:    `{"items":[{"sku":"ox","qty":6}],"total_cost":240}`,
		Confidence:   0.92,
		SourceModel:  "claude-haiku-4-5",
		SourceTurnID: "trn_01",
		CreatedAt:    now,
	}
	if err := c.Put(context.Background(), k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get(context.Background(), k)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Intent != v.Intent {
		t.Errorf("Intent: want %q got %q", v.Intent, got.Intent)
	}
	if got.SlotsJSON != v.SlotsJSON {
		t.Errorf("SlotsJSON: want %q got %q", v.SlotsJSON, got.SlotsJSON)
	}
	if got.Confidence != v.Confidence {
		t.Errorf("Confidence: want %v got %v", v.Confidence, got.Confidence)
	}
	if got.SourceModel != v.SourceModel {
		t.Errorf("SourceModel: want %q got %q", v.SourceModel, got.SourceModel)
	}
	if got.SourceTurnID != v.SourceTurnID {
		t.Errorf("SourceTurnID: want %q got %q", v.SourceTurnID, got.SourceTurnID)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt: want %v got %v", now, got.CreatedAt)
	}
}

func conformanceGetUnknownKeyMisses(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	_, ok, err := c.Get(context.Background(), keyFor("x", "h", "@s", "nope"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Errorf("Get unknown: want miss, got hit")
	}
}

func conformancePutAutoSeedsCreatedAt(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	k := keyFor("a", "h", "@s", "sig")
	before := time.Now().UTC()
	if err := c.Put(context.Background(), k, CachedVerdict{Intent: "go"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	after := time.Now().UTC()
	got, ok, err := c.Get(context.Background(), k)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	// SQLite round-trips through unix-millis so the comparison must
	// be at millisecond resolution. Round both ends to be safe.
	if got.CreatedAt.UnixMilli() < before.Add(-time.Millisecond).UnixMilli() ||
		got.CreatedAt.UnixMilli() > after.Add(time.Millisecond).UnixMilli() {
		t.Errorf("CreatedAt not auto-stamped: got %v, want in [%v, %v]", got.CreatedAt, before, after)
	}
}

func conformancePutOverwriteReplaces(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	k := keyFor("a", "h", "@s", "sig")
	if err := c.Put(context.Background(), k, CachedVerdict{Intent: "first", Confidence: 0.7}); err != nil {
		t.Fatalf("Put first: %v", err)
	}
	if err := c.Put(context.Background(), k, CachedVerdict{Intent: "second", Confidence: 0.9}); err != nil {
		t.Fatalf("Put second: %v", err)
	}
	got, ok, err := c.Get(context.Background(), k)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Intent != "second" {
		t.Errorf("Intent: want %q got %q", "second", got.Intent)
	}
	if got.Confidence != 0.9 {
		t.Errorf("Confidence: want %v got %v", 0.9, got.Confidence)
	}
}

func conformanceRecordHitIncrementsAndResetsStrikes(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	k := keyFor("a", "h", "@s", "s")
	if err := c.Put(ctx, k, CachedVerdict{Intent: "go", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t0 := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	if evicted, err := c.RecordRevalidateFail(ctx, k, t0); err != nil || evicted {
		t.Fatalf("first strike: evicted=%v err=%v", evicted, err)
	}
	if evicted, err := c.RecordRevalidateFail(ctx, k, t0.Add(time.Minute)); err != nil || evicted {
		t.Fatalf("second strike: evicted=%v err=%v", evicted, err)
	}
	hitAt := t0.Add(time.Hour)
	if err := c.RecordHit(ctx, k, hitAt); err != nil {
		t.Fatalf("RecordHit: %v", err)
	}
	got, ok, err := c.Get(ctx, k)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.HitCount != 1 {
		t.Errorf("HitCount: want 1 got %d", got.HitCount)
	}
	if !got.LastHitAt.Equal(hitAt) {
		t.Errorf("LastHitAt: want %v got %v", hitAt, got.LastHitAt)
	}
	if !got.LastVerifiedAt.Equal(hitAt) {
		t.Errorf("LastVerifiedAt: want %v got %v", hitAt, got.LastVerifiedAt)
	}
	if got.RevalidateFails != 0 {
		t.Errorf("RevalidateFails after RecordHit: want 0, got %d", got.RevalidateFails)
	}
}

func conformanceRecordRevalidateFailEvicts(t *testing.T, mk cacheFactory) {
	t.Helper()
	cases := []int{1, 3, 5}
	for _, strikes := range cases {
		strikes := strikes
		t.Run(fmt.Sprintf("strikes_%d", strikes), func(t *testing.T) {
			cfg := testConformanceConfig()
			cfg.RevalidateStrikes = strikes
			c := mk(t, cfg)
			ctx := context.Background()
			k := keyFor("a", "h", "@s", "s")
			if err := c.Put(ctx, k, CachedVerdict{Intent: "go", CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatalf("Put: %v", err)
			}
			now := time.Now().UTC()
			for i := 1; i < strikes; i++ {
				evicted, err := c.RecordRevalidateFail(ctx, k, now)
				if err != nil {
					t.Fatalf("strike %d: %v", i, err)
				}
				if evicted {
					t.Fatalf("strike %d/%d: pre-emptive eviction", i, strikes)
				}
			}
			evicted, err := c.RecordRevalidateFail(ctx, k, now)
			if err != nil {
				t.Fatalf("final strike: %v", err)
			}
			if !evicted {
				t.Errorf("strike %d (final): want evicted=true", strikes)
			}
			if _, ok, _ := c.Get(ctx, k); ok {
				t.Errorf("row should be gone after eviction")
			}
		})
	}
}

func conformanceRecordRevalidateFailMissingIsNoop(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	evicted, err := c.RecordRevalidateFail(context.Background(), keyFor("a", "h", "@s", "missing"), time.Now())
	if err != nil {
		t.Errorf("RecordRevalidateFail missing: %v", err)
	}
	if evicted {
		t.Errorf("RecordRevalidateFail missing: want evicted=false")
	}
}

func conformanceRecordHitMissingIsNoop(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	if err := c.RecordHit(context.Background(), keyFor("a", "h", "@s", "missing"), time.Now()); err != nil {
		t.Errorf("RecordHit missing: %v", err)
	}
}

func conformanceInvalidateOtherHashes(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	keepX := keyFor("X", "abc", "@s", "k1")
	oldX := keyFor("X", "old", "@s", "k2")
	otherY := keyFor("Y", "old", "@s", "k3")
	if err := c.Put(ctx, keepX, CachedVerdict{Intent: "x_new"}); err != nil {
		t.Fatalf("Put keepX: %v", err)
	}
	if err := c.Put(ctx, oldX, CachedVerdict{Intent: "x_old"}); err != nil {
		t.Fatalf("Put oldX: %v", err)
	}
	if err := c.Put(ctx, otherY, CachedVerdict{Intent: "y_old"}); err != nil {
		t.Fatalf("Put otherY: %v", err)
	}
	deleted, err := c.InvalidateOtherHashes(ctx, "X", "abc")
	if err != nil {
		t.Fatalf("InvalidateOtherHashes: %v", err)
	}
	if deleted != 1 {
		t.Errorf("InvalidateOtherHashes: want 1 got %d", deleted)
	}
	if _, ok, _ := c.Get(ctx, keepX); !ok {
		t.Errorf("keepX should remain")
	}
	if _, ok, _ := c.Get(ctx, oldX); ok {
		t.Errorf("oldX should be deleted")
	}
	if _, ok, _ := c.Get(ctx, otherY); !ok {
		t.Errorf("otherY should remain")
	}
}

func conformanceSweepCold(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	old := keyFor("a", "h", "@s", "old")
	fresh := keyFor("a", "h", "@s", "fresh")
	otherApp := keyFor("b", "h", "@s", "old")
	if err := c.Put(ctx, old, CachedVerdict{Intent: "go", LastHitAt: now.Add(-40 * 24 * time.Hour)}); err != nil {
		t.Fatalf("Put old: %v", err)
	}
	if err := c.Put(ctx, fresh, CachedVerdict{Intent: "go", LastHitAt: now.Add(-1 * time.Hour)}); err != nil {
		t.Fatalf("Put fresh: %v", err)
	}
	if err := c.Put(ctx, otherApp, CachedVerdict{Intent: "go", LastHitAt: now.Add(-40 * 24 * time.Hour)}); err != nil {
		t.Fatalf("Put otherApp: %v", err)
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	deleted, err := c.SweepCold(ctx, "a", cutoff)
	if err != nil {
		t.Fatalf("SweepCold: %v", err)
	}
	if deleted != 1 {
		t.Errorf("SweepCold: want 1 got %d", deleted)
	}
	if _, ok, _ := c.Get(ctx, old); ok {
		t.Errorf("old should be swept")
	}
	if _, ok, _ := c.Get(ctx, fresh); !ok {
		t.Errorf("fresh should remain")
	}
	if _, ok, _ := c.Get(ctx, otherApp); !ok {
		t.Errorf("otherApp should remain")
	}
}

func conformanceSweepColdConfidenceDecay(t *testing.T, mk cacheFactory) {
	t.Helper()
	cfg := testConformanceConfig()
	cfg.MaxAge = 30 * 24 * time.Hour
	cfg.ConfidenceDecay = true
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-30 * 24 * time.Hour)
	tests := []struct {
		name       string
		confidence float64
		ageDays    int
		wantSwept  bool
	}{
		{"low_conf_past_half_age", 0.5, 20, true},
		{"low_conf_under_half_age", 0.5, 10, false},
		{"high_conf_past_half_age", 0.9, 20, false},
		{"high_conf_past_full_age", 0.9, 40, true},
		{"boundary_conf_0_7", 0.7, 20, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := mk(t, cfg)
			ctx := context.Background()
			k := keyFor("a", "h", "@s", tc.name)
			if err := c.Put(ctx, k, CachedVerdict{
				Intent:     "go",
				Confidence: tc.confidence,
				LastHitAt:  now.Add(-time.Duration(tc.ageDays) * 24 * time.Hour),
			}); err != nil {
				t.Fatalf("Put: %v", err)
			}
			if _, err := c.SweepCold(ctx, "a", cutoff); err != nil {
				t.Fatalf("SweepCold: %v", err)
			}
			_, present, _ := c.Get(ctx, k)
			swept := !present
			if swept != tc.wantSwept {
				t.Errorf("%s: swept=%v want=%v (conf=%v age=%dd)",
					tc.name, swept, tc.wantSwept, tc.confidence, tc.ageDays)
			}
		})
	}
}

// conformanceSweepColdNeverHit pins the cross-session cache value
// proposition: a row that has been Put but never RecordHit'd must NOT
// be swept by a SweepCold pass whose cutoff is in the past. The
// in-memory backend used to evict these rows because time.Time{}
// zero-value compares as "Before" any non-zero cutoff; the SQLite
// backend used to treat last_hit_at IS NULL the same way. With the
// Put-side fix that stamps LastHitAt from CreatedAt at insert time,
// neither backend should sweep a freshly-Put row.
func conformanceSweepColdNeverHit(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	k := keyFor("a", "h", "@s", "never-hit")
	// Caller leaves LastHitAt zero — Put must treat the row as
	// freshly created, not "ancient because never touched".
	if err := c.Put(ctx, k, CachedVerdict{Intent: "go"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Cutoff in the past — a fresh row must NOT be older than this.
	cutoff := time.Now().Add(-1 * time.Hour)
	deleted, err := c.SweepCold(ctx, "a", cutoff)
	if err != nil {
		t.Fatalf("SweepCold: %v", err)
	}
	if deleted != 0 {
		t.Errorf("SweepCold deleted %d freshly-Put rows; want 0 (a never-hit row must survive a past-cutoff sweep)", deleted)
	}
	if _, ok, err := c.Get(ctx, k); err != nil || !ok {
		t.Errorf("Get after SweepCold: ok=%v err=%v; want ok=true (a never-hit row must survive a past-cutoff sweep)", ok, err)
	}
}

// conformancePutStampsLastHitAtFromCreatedAt asserts the documented
// behaviour of the Put-side fix: when the caller leaves LastHitAt
// zero, Put initialises it to CreatedAt (which is itself auto-stamped
// to "now" when zero). The row is semantically as fresh as one that
// was hit at the moment of creation.
func conformancePutStampsLastHitAtFromCreatedAt(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	t.Run("auto_stamped_created_at", func(t *testing.T) {
		k := keyFor("a", "h", "@s", "auto")
		if err := c.Put(ctx, k, CachedVerdict{Intent: "go"}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok, err := c.Get(ctx, k)
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if got.LastHitAt.IsZero() {
			t.Errorf("LastHitAt: want non-zero (initialised from CreatedAt), got zero value")
		}
		// SQLite round-trips through unix-millis; compare at that resolution.
		if got.LastHitAt.UnixMilli() != got.CreatedAt.UnixMilli() {
			t.Errorf("LastHitAt vs CreatedAt: want equal (ms-resolution), got LastHitAt=%v CreatedAt=%v", got.LastHitAt, got.CreatedAt)
		}
	})
	t.Run("explicit_created_at", func(t *testing.T) {
		k := keyFor("a", "h", "@s", "explicit")
		created := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
		if err := c.Put(ctx, k, CachedVerdict{Intent: "go", CreatedAt: created}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok, err := c.Get(ctx, k)
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if !got.LastHitAt.Equal(created) {
			t.Errorf("LastHitAt: want %v (CreatedAt), got %v", created, got.LastHitAt)
		}
	})
}

// conformancePutPreservesCallerLastHitAt asserts that callers who
// already pass a non-zero LastHitAt have it round-trip unchanged. The
// Put-side fix only stamps when LastHitAt is zero.
func conformancePutPreservesCallerLastHitAt(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	k := keyFor("a", "h", "@s", "caller-set")
	lastHit := time.Date(2026, 5, 10, 9, 30, 0, 0, time.UTC)
	created := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := c.Put(ctx, k, CachedVerdict{
		Intent:    "go",
		CreatedAt: created,
		LastHitAt: lastHit,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get(ctx, k)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !got.LastHitAt.Equal(lastHit) {
		t.Errorf("LastHitAt: want caller-supplied %v, got %v (Put must not clobber a non-zero value)", lastHit, got.LastHitAt)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt: want %v, got %v", created, got.CreatedAt)
	}
}

// conformancePropertyLastHitAtNotBeforeCreatedAt is the property test
// the spec calls out: for any sequence of Put followed by Get, the
// returned LastHitAt is never strictly before CreatedAt. Holds after
// the Put-side fix (because LastHitAt is either stamped from
// CreatedAt at insert, or supplied by the caller — and we don't try
// to "fix" caller-supplied values).
func conformancePropertyLastHitAtNotBeforeCreatedAt(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	// Deterministic mini-sequence: each row has a different (CreatedAt,
	// caller-supplied-LastHitAt) shape. "zero" means "leave it zero so
	// the Put-side fix has to do the right thing."
	base := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		createdAt time.Time
		lastHitAt time.Time
	}{
		{"both_zero", time.Time{}, time.Time{}},
		{"created_only", base, time.Time{}},
		{"both_set_equal", base, base},
		{"both_set_lasthit_after_created", base, base.Add(time.Hour)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			k := keyFor("a", "h", "@s", tc.name)
			if err := c.Put(ctx, k, CachedVerdict{
				Intent:    "go",
				CreatedAt: tc.createdAt,
				LastHitAt: tc.lastHitAt,
			}); err != nil {
				t.Fatalf("Put: %v", err)
			}
			got, ok, err := c.Get(ctx, k)
			if err != nil || !ok {
				t.Fatalf("Get: ok=%v err=%v", ok, err)
			}
			// The invariant: LastHitAt is never strictly before CreatedAt.
			// SQLite round-trips through unix-millis, so compare there.
			if got.LastHitAt.UnixMilli() < got.CreatedAt.UnixMilli() {
				t.Errorf("invariant violated: LastHitAt %v < CreatedAt %v", got.LastHitAt, got.CreatedAt)
			}
			if got.LastHitAt.IsZero() {
				t.Errorf("LastHitAt: want non-zero, got zero (Put must stamp it)")
			}
		})
	}
}

// conformanceInvalidateOtherHashesClearsSynonymHits asserts that the
// app-hash invalidation pass also clears synonym_hits rows for the
// old hash. Without this, every YAML edit silently orphans the
// synonym-hit data and the synonym_hits table grows unbounded.
func conformanceInvalidateOtherHashesClearsSynonymHits(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	// Seed synonym_hits rows under both the current hash and an old hash.
	keepSK := SynonymKey{AppHash: "new", Intent: "ford", Pattern: "wade", Kind: "bare"}
	oldSK := SynonymKey{AppHash: "old", Intent: "ford", Pattern: "swim", Kind: "bare"}
	if err := c.RecordSynonymHit(ctx, keepSK, now); err != nil {
		t.Fatalf("RecordSynonymHit keep: %v", err)
	}
	if err := c.RecordSynonymHit(ctx, oldSK, now); err != nil {
		t.Fatalf("RecordSynonymHit old: %v", err)
	}
	// Also seed turn_cache rows so the canonical invalidate signal fires.
	keepK := keyFor("X", "new", "@s", "k1")
	oldK := keyFor("X", "old", "@s", "k2")
	if err := c.Put(ctx, keepK, CachedVerdict{Intent: "go"}); err != nil {
		t.Fatalf("Put keepK: %v", err)
	}
	if err := c.Put(ctx, oldK, CachedVerdict{Intent: "go"}); err != nil {
		t.Fatalf("Put oldK: %v", err)
	}
	if _, err := c.InvalidateOtherHashes(ctx, "X", "new"); err != nil {
		t.Fatalf("InvalidateOtherHashes: %v", err)
	}
	// Keep-hash synonym row survives.
	keepStats, err := c.SynonymStats(ctx, "new")
	if err != nil {
		t.Fatalf("SynonymStats keep: %v", err)
	}
	if len(keepStats) != 1 || keepStats[0].Pattern != "wade" {
		t.Errorf("keep-hash synonym_hits drifted: want exactly one row for pattern=wade, got %+v", keepStats)
	}
	// Old-hash synonym row is gone.
	oldStats, err := c.SynonymStats(ctx, "old")
	if err != nil {
		t.Fatalf("SynonymStats old: %v", err)
	}
	if len(oldStats) != 0 {
		t.Errorf("old-hash synonym_hits leaked: want 0 rows, got %+v", oldStats)
	}
}

func conformanceTrimLRU(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	base := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		if err := c.Put(ctx, k, CachedVerdict{Intent: "go", LastHitAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	otherApp := keyFor("b", "h", "@s", "b0")
	if err := c.Put(ctx, otherApp, CachedVerdict{Intent: "go", LastHitAt: base}); err != nil {
		t.Fatalf("Put otherApp: %v", err)
	}
	deleted, err := c.TrimLRU(ctx, "a", 5, 0.5)
	if err != nil {
		t.Fatalf("TrimLRU: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("TrimLRU: want 5, got %d", deleted)
	}
	for i := 0; i < 10; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		_, ok, _ := c.Get(ctx, k)
		if i < 5 && ok {
			t.Errorf("row %d (cold): want trimmed, still present", i)
		}
		if i >= 5 && !ok {
			t.Errorf("row %d (warm): want present, trimmed", i)
		}
	}
	if _, ok, _ := c.Get(ctx, otherApp); !ok {
		t.Errorf("other-app row must be untouched")
	}
}

func conformanceTrimLRUNoop(t *testing.T, mk cacheFactory) {
	t.Helper()
	cases := []struct {
		name         string
		preloadRows  int
		cap          int
		trimFraction float64
		wantDeleted  int
	}{
		{"empty_cache", 0, 10, 0.5, 0},
		{"under_cap", 3, 10, 0.5, 0},
		{"at_cap", 5, 5, 0.5, 0},
		{"cap_zero_disables", 5, 0, 0.5, 0},
		{"fraction_zero_disables", 5, 10, 0, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := mk(t, testConformanceConfig())
			ctx := context.Background()
			for i := 0; i < tc.preloadRows; i++ {
				k := keyFor("a", "h", "@s", signature(i))
				if err := c.Put(ctx, k, CachedVerdict{Intent: "go", LastHitAt: time.Now()}); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
			deleted, err := c.TrimLRU(ctx, "a", tc.cap, tc.trimFraction)
			if err != nil {
				t.Fatalf("TrimLRU: %v", err)
			}
			if deleted != tc.wantDeleted {
				t.Errorf("%s: deleted=%d want=%d", tc.name, deleted, tc.wantDeleted)
			}
		})
	}
}

func conformanceRecordSynonymHitAndStats(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	hot := SynonymKey{AppHash: "h", Intent: "ford", Pattern: "wade across", Kind: "bare"}
	cold := SynonymKey{AppHash: "h", Intent: "hunt", Pattern: "shoot game", Kind: "bare"}
	otherHash := SynonymKey{AppHash: "other", Intent: "ford", Pattern: "wade across", Kind: "bare"}
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	for _, call := range []struct {
		sk SynonymKey
		at time.Time
	}{
		{hot, now},
		{hot, now.Add(time.Hour)},
		{cold, now},
		{otherHash, now},
	} {
		if err := c.RecordSynonymHit(ctx, call.sk, call.at); err != nil {
			t.Fatalf("RecordSynonymHit: %v", err)
		}
	}
	stats, err := c.SynonymStats(ctx, "h")
	if err != nil {
		t.Fatalf("SynonymStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("SynonymStats: want 2 got %d (%+v)", len(stats), stats)
	}
	if stats[0].Pattern != "wade across" || stats[0].HitCount != 2 {
		t.Errorf("stats[0]: want wade across/2, got %s/%d", stats[0].Pattern, stats[0].HitCount)
	}
	if stats[1].Pattern != "shoot game" || stats[1].HitCount != 1 {
		t.Errorf("stats[1]: want shoot game/1, got %s/%d", stats[1].Pattern, stats[1].HitCount)
	}
	if !stats[0].LastHitAt.Equal(now.Add(time.Hour)) {
		t.Errorf("stats[0].LastHitAt: want %v got %v", now.Add(time.Hour), stats[0].LastHitAt)
	}
}

func conformanceSynonymStatsEmpty(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	stats, err := c.SynonymStats(context.Background(), "no-rows-here")
	if err != nil {
		t.Errorf("SynonymStats: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("SynonymStats empty: want 0 got %d", len(stats))
	}
}

func conformanceContextCancellation(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := []struct {
		name string
		call func() error
	}{
		{"Get", func() error { _, _, err := c.Get(ctx, Key{}); return err }},
		{"Put", func() error { return c.Put(ctx, Key{}, CachedVerdict{}) }},
		{"RecordHit", func() error { return c.RecordHit(ctx, Key{}, time.Now()) }},
		{"RecordRevalidateFail", func() error { _, err := c.RecordRevalidateFail(ctx, Key{}, time.Now()); return err }},
		{"InvalidateOtherHashes", func() error { _, err := c.InvalidateOtherHashes(ctx, "a", "h"); return err }},
		{"SweepCold", func() error { _, err := c.SweepCold(ctx, "a", time.Now()); return err }},
		{"TrimLRU", func() error { _, err := c.TrimLRU(ctx, "a", 10, 0.1); return err }},
		{"RecordSynonymHit", func() error { return c.RecordSynonymHit(ctx, SynonymKey{}, time.Now()) }},
		{"SynonymStats", func() error { _, err := c.SynonymStats(ctx, "h"); return err }},
	}
	for _, tc := range calls {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Errorf("%s: want error from cancelled context, got nil", tc.name)
			}
		})
	}
}

func conformanceConcurrentMutations(t *testing.T, mk cacheFactory) {
	t.Helper()
	c := mk(t, testConformanceConfig())
	ctx := context.Background()
	// Smaller numbers than the in-memory smoke — SQLite write throughput
	// is bounded by WAL fsync and we don't want the test to be slow.
	const goroutines = 4
	const perGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				k := keyFor("a", "h", "@s", signature(g*perGoroutine+i))
				if err := c.Put(ctx, k, CachedVerdict{Intent: "go", LastHitAt: time.Now()}); err != nil {
					t.Errorf("Put: %v", err)
					return
				}
			}
		}()
	}
	hotKey := keyFor("a", "h", "@s", "hot")
	if err := c.Put(ctx, hotKey, CachedVerdict{Intent: "go"}); err != nil {
		t.Fatalf("Put hotKey: %v", err)
	}
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			now := time.Now()
			for i := 0; i < perGoroutine; i++ {
				if err := c.RecordHit(ctx, hotKey, now); err != nil {
					t.Errorf("RecordHit: %v", err)
					return
				}
			}
		}()
	}
	sk := SynonymKey{AppHash: "h", Intent: "ford", Pattern: "wade", Kind: "bare"}
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			now := time.Now()
			for i := 0; i < perGoroutine; i++ {
				if err := c.RecordSynonymHit(ctx, sk, now); err != nil {
					t.Errorf("RecordSynonymHit: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			k := keyFor("a", "h", "@s", signature(g*perGoroutine+i))
			if _, ok, err := c.Get(ctx, k); err != nil || !ok {
				t.Fatalf("missing row after concurrent Put g=%d i=%d ok=%v err=%v", g, i, ok, err)
			}
		}
	}
	hot, ok, err := c.Get(ctx, hotKey)
	if err != nil || !ok {
		t.Fatalf("Get hotKey: ok=%v err=%v", ok, err)
	}
	if hot.HitCount != goroutines*perGoroutine {
		t.Errorf("HitCount on hot row: want %d, got %d", goroutines*perGoroutine, hot.HitCount)
	}
	stats, err := c.SynonymStats(ctx, "h")
	if err != nil {
		t.Fatalf("SynonymStats: %v", err)
	}
	if len(stats) != 1 || stats[0].HitCount != goroutines*perGoroutine {
		t.Errorf("SynonymStats: want one row HitCount=%d, got %+v", goroutines*perGoroutine, stats)
	}
}

// conformanceFinalStateMatchesMemory drives an identical sequence of
// operations on the supplied backend and on a NewMemory reference, then
// asserts the final Get result agrees. This is the property test: the
// SQLite backend cannot drift from the in-memory agent.
func conformanceFinalStateMatchesMemory(t *testing.T, mk cacheFactory) {
	t.Helper()
	cfg := testConformanceConfig()
	cfg.RevalidateStrikes = 4
	mem := NewMemory(cfg)
	t.Cleanup(func() { _ = mem.Close() })
	other := mk(t, cfg)

	ctx := context.Background()
	k := keyFor("a", "h", "@s", "seq")

	// A fixed deterministic mini-script of operations.
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	ops := []func(c Cache){
		func(c Cache) {
			_ = c.Put(ctx, k, CachedVerdict{Intent: "go", Confidence: 0.8, CreatedAt: now})
		},
		func(c Cache) { _ = c.RecordHit(ctx, k, now.Add(time.Minute)) },
		func(c Cache) { _, _ = c.RecordRevalidateFail(ctx, k, now.Add(2*time.Minute)) },
		func(c Cache) { _ = c.RecordHit(ctx, k, now.Add(3*time.Minute)) },
		func(c Cache) { _, _ = c.RecordRevalidateFail(ctx, k, now.Add(4*time.Minute)) },
		func(c Cache) { _, _ = c.RecordRevalidateFail(ctx, k, now.Add(5*time.Minute)) },
	}
	for _, op := range ops {
		op(mem)
		op(other)
	}

	memV, memOk, _ := mem.Get(ctx, k)
	otherV, otherOk, _ := other.Get(ctx, k)
	if memOk != otherOk {
		t.Fatalf("presence drift: mem ok=%v other ok=%v", memOk, otherOk)
	}
	if memOk {
		// Compare the policy/counter columns. CreatedAt agrees by
		// construction; LastHitAt agrees because both backends store
		// the same input time.
		if memV.HitCount != otherV.HitCount {
			t.Errorf("HitCount: mem=%d other=%d", memV.HitCount, otherV.HitCount)
		}
		if memV.RevalidateFails != otherV.RevalidateFails {
			t.Errorf("RevalidateFails: mem=%d other=%d", memV.RevalidateFails, otherV.RevalidateFails)
		}
		if !memV.LastHitAt.Equal(otherV.LastHitAt) {
			t.Errorf("LastHitAt: mem=%v other=%v", memV.LastHitAt, otherV.LastHitAt)
		}
	}
}

// testConformanceConfig is a small, fast config — same shape as
// testConfig() but local to this file so it doesn't accidentally
// re-export the helper. RevalidateStrikes=3 keeps the strike matrix
// readable.
func testConformanceConfig() Config {
	return Config{
		MaxAge:            30 * 24 * time.Hour,
		Cap:               100,
		TrimFraction:      0.10,
		RevalidateStrikes: 3,
		ConfidenceDecay:   false,
	}
}

// TestMemoryConformance ties the in-memory backend into the
// cross-backend conformance suite. memory_test.go contains the
// per-backend matrix; this test ensures the contract-level matrix
// passes as well.
func TestMemoryConformance(t *testing.T) {
	t.Parallel()
	runConformanceSuite(t, func(t *testing.T, cfg Config) Cache {
		t.Helper()
		c := NewMemory(cfg)
		t.Cleanup(func() { _ = c.Close() })
		return c
	})
}
