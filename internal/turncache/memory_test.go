// Tests for the in-memory [Cache] (NewMemory).
//
// Structure:
//   - "round-trip"    — Put -> Get -> CachedVerdict fields preserved.
//   - "eviction"      — RecordRevalidateFail strikes, SweepCold time-based,
//     SweepCold + ConfidenceDecay, TrimLRU.
//   - "synonyms"      — RecordSynonymHit + SynonymStats.
//   - "lifecycle"     — DefaultConfig, Close, context cancellation,
//     concurrent Put + RecordSynonymHit (-race smoke).
//
// Section banners use a sparse `// =====` style so the file stays
// scannable even as new cases land.
package turncache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ====================== helpers ======================

// testConfig returns a small, fast config suitable for unit tests.
// Cap=100, RevalidateStrikes=3, ConfidenceDecay=off match the most
// common production shape without making any test sit at the boundary.
func testConfig() Config {
	return Config{
		MaxAge:            30 * 24 * time.Hour,
		Cap:               100,
		TrimFraction:      0.10,
		RevalidateStrikes: 3,
		ConfidenceDecay:   false,
	}
}

// newCache constructs a memoryCache with t.Cleanup registered so every
// test releases the backend on completion. Returns the Cache interface
// (not *memoryCache) so tests don't accidentally couple to internals.
func newCache(t *testing.T, cfg Config) Cache {
	t.Helper()
	c := NewMemory(cfg)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: unexpected error: %v", err)
		}
	})
	return c
}

// mustPut wraps Cache.Put with a fail-fast assertion. Centralises the
// fatal message so every call site doesn't have to spell it out.
func mustPut(t *testing.T, c Cache, k Key, v CachedVerdict) {
	t.Helper()
	if err := c.Put(context.Background(), k, v); err != nil {
		t.Fatalf("Put(%+v, intent=%q): unexpected error %v", k, v.Intent, err)
	}
}

// mustGet wraps Cache.Get + ok assertion + error check.
func mustGet(t *testing.T, c Cache, k Key) CachedVerdict {
	t.Helper()
	got, ok, err := c.Get(context.Background(), k)
	if err != nil {
		t.Fatalf("Get(%+v): unexpected error %v", k, err)
	}
	if !ok {
		t.Fatalf("Get(%+v): want hit, got miss", k)
	}
	return got
}

// signature returns a stable, distinct string per int — keeps table
// setups readable without dragging fmt into the test file.
func signature(n int) string {
	return fmt.Sprintf("s%d", n)
}

// keyFor builds a Key with sensible defaults; tests only specify the
// fields that matter for the case under test.
func keyFor(app, hash, state, sig string) Key {
	return Key{App: app, AppHash: hash, StatePath: state, Signature: sig}
}

// ====================== round-trip ======================

func TestPutGetRoundTrip(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

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
	mustPut(t, c, k, v)

	got := mustGet(t, c, k)
	// Compare the fields that should round-trip verbatim. HitCount,
	// LastHitAt, LastVerifiedAt and RevalidateFails are policy fields
	// and are exercised separately below.
	if got.Intent != v.Intent {
		t.Errorf("Intent round-trip: want %q, got %q (full=%+v)", v.Intent, got.Intent, got)
	}
	if got.SlotsJSON != v.SlotsJSON {
		t.Errorf("SlotsJSON round-trip: want %q, got %q", v.SlotsJSON, got.SlotsJSON)
	}
	if got.Confidence != v.Confidence {
		t.Errorf("Confidence round-trip: want %v, got %v", v.Confidence, got.Confidence)
	}
	if got.SourceModel != v.SourceModel {
		t.Errorf("SourceModel round-trip: want %q, got %q", v.SourceModel, got.SourceModel)
	}
	if got.SourceTurnID != v.SourceTurnID {
		t.Errorf("SourceTurnID round-trip: want %q, got %q", v.SourceTurnID, got.SourceTurnID)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt round-trip: want %v, got %v", now, got.CreatedAt)
	}
}

// TestPut_AutoSeedsCreatedAt asserts the documented behaviour: when a
// caller leaves CreatedAt zero, Put stamps the current time.
func TestPut_AutoSeedsCreatedAt(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

	k := keyFor("a", "h", "@s", "sig")
	before := time.Now().UTC()
	mustPut(t, c, k, CachedVerdict{Intent: "go"}) // CreatedAt left zero
	after := time.Now().UTC()

	got := mustGet(t, c, k)
	if got.CreatedAt.Before(before) || got.CreatedAt.After(after) {
		t.Errorf("Put: CreatedAt not auto-stamped; got %v, want in [%v, %v]", got.CreatedAt, before, after)
	}
}

// TestPut_StampsLastHitAtFromCreatedAt is the per-backend smoke for
// the C2 fix on the memory cache. After Put-with-zero-LastHitAt, the
// row's LastHitAt equals its CreatedAt — a row that has been Put but
// never RecordHit'd is semantically as fresh as a row that was hit at
// the moment of creation. Without this, the orchestrator's session-
// start SweepCold (which fires once per process) would wipe every
// freshly-Put row on the next restart.
func TestPut_StampsLastHitAtFromCreatedAt(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

	t.Run("auto_stamped_created_at", func(t *testing.T) {
		k := keyFor("a", "h", "@s", "auto")
		mustPut(t, c, k, CachedVerdict{Intent: "go"}) // both CreatedAt and LastHitAt zero
		got := mustGet(t, c, k)
		if got.LastHitAt.IsZero() {
			t.Errorf("LastHitAt: want non-zero (initialised from CreatedAt), got zero value")
		}
		if !got.LastHitAt.Equal(got.CreatedAt) {
			t.Errorf("LastHitAt vs CreatedAt: want equal, got LastHitAt=%v CreatedAt=%v", got.LastHitAt, got.CreatedAt)
		}
	})
	t.Run("caller_supplied_created_at", func(t *testing.T) {
		k := keyFor("a", "h", "@s", "explicit-created")
		created := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
		mustPut(t, c, k, CachedVerdict{Intent: "go", CreatedAt: created})
		got := mustGet(t, c, k)
		if !got.LastHitAt.Equal(created) {
			t.Errorf("LastHitAt: want %v (CreatedAt), got %v", created, got.LastHitAt)
		}
	})
	t.Run("caller_supplied_last_hit_at_preserved", func(t *testing.T) {
		k := keyFor("a", "h", "@s", "explicit-hit")
		created := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		lastHit := time.Date(2026, 5, 10, 9, 30, 0, 0, time.UTC)
		mustPut(t, c, k, CachedVerdict{Intent: "go", CreatedAt: created, LastHitAt: lastHit})
		got := mustGet(t, c, k)
		if !got.LastHitAt.Equal(lastHit) {
			t.Errorf("LastHitAt: want caller-supplied %v, got %v", lastHit, got.LastHitAt)
		}
		if !got.CreatedAt.Equal(created) {
			t.Errorf("CreatedAt: want %v, got %v", created, got.CreatedAt)
		}
	})
	t.Run("last_verified_at_remains_zero", func(t *testing.T) {
		// LastVerifiedAt is owned by RecordHit, not Put — Put must
		// never stamp it.
		k := keyFor("a", "h", "@s", "lv")
		mustPut(t, c, k, CachedVerdict{Intent: "go"})
		got := mustGet(t, c, k)
		if !got.LastVerifiedAt.IsZero() {
			t.Errorf("LastVerifiedAt: want zero (Put must not stamp it), got %v", got.LastVerifiedAt)
		}
	})
}

func TestGetUnknownKeyMisses(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

	k := keyFor("x", "h", "@s", "nope")
	_, ok, err := c.Get(context.Background(), k)
	if err != nil {
		t.Fatalf("Get(%+v): unexpected error %v", k, err)
	}
	if ok {
		t.Errorf("Get(%+v): want miss, got hit", k)
	}
}

// TestPut_OverwriteReplaces asserts a second Put on the same Key
// replaces the row, including resetting any policy counters from the
// new CachedVerdict value (Put is "insert or replace").
func TestPut_OverwriteReplaces(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

	k := keyFor("a", "h", "@s", "sig")
	mustPut(t, c, k, CachedVerdict{Intent: "first", Confidence: 0.7})
	mustPut(t, c, k, CachedVerdict{Intent: "second", Confidence: 0.9})

	got := mustGet(t, c, k)
	if got.Intent != "second" {
		t.Errorf("Put overwrite: want Intent=%q, got %q", "second", got.Intent)
	}
	if got.Confidence != 0.9 {
		t.Errorf("Put overwrite: want Confidence=%v, got %v", 0.9, got.Confidence)
	}
}

// ====================== eviction: revalidate strikes ======================

func TestRecordHitIncrementsAndResetsStrikes(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
	ctx := context.Background()

	k := keyFor("a", "h", "@s", "s")
	mustPut(t, c, k, CachedVerdict{Intent: "go", CreatedAt: time.Now().UTC()})

	// Accumulate two strikes (below RevalidateStrikes=3).
	t0 := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	if evicted, err := c.RecordRevalidateFail(ctx, k, t0); err != nil || evicted {
		t.Fatalf("first strike: want evicted=false err=nil, got evicted=%v err=%v", evicted, err)
	}
	if evicted, err := c.RecordRevalidateFail(ctx, k, t0.Add(time.Minute)); err != nil || evicted {
		t.Fatalf("second strike: want evicted=false err=nil, got evicted=%v err=%v", evicted, err)
	}

	hitAt := t0.Add(time.Hour)
	if err := c.RecordHit(ctx, k, hitAt); err != nil {
		t.Fatalf("RecordHit(%+v, %v): unexpected error %v", k, hitAt, err)
	}
	got := mustGet(t, c, k)
	if got.HitCount != 1 {
		t.Errorf("HitCount: want 1, got %d (full=%+v)", got.HitCount, got)
	}
	if !got.LastHitAt.Equal(hitAt) {
		t.Errorf("LastHitAt: want %v, got %v", hitAt, got.LastHitAt)
	}
	if !got.LastVerifiedAt.Equal(hitAt) {
		t.Errorf("LastVerifiedAt: want %v, got %v", hitAt, got.LastVerifiedAt)
	}
	if got.RevalidateFails != 0 {
		t.Errorf("RevalidateFails after RecordHit: want 0 (reset), got %d", got.RevalidateFails)
	}
}

func TestRecordRevalidateFail_EvictsOnNthStrike(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		revalidateStrikes int
	}{
		{"strikes_1", 1},
		{"strikes_3", 3},
		{"strikes_5", 5},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			cfg.RevalidateStrikes = tc.revalidateStrikes
			c := newCache(t, cfg)
			ctx := context.Background()

			k := keyFor("a", "h", "@s", "s")
			mustPut(t, c, k, CachedVerdict{Intent: "go", CreatedAt: time.Now().UTC()})

			now := time.Now().UTC()
			for i := 1; i < tc.revalidateStrikes; i++ {
				evicted, err := c.RecordRevalidateFail(ctx, k, now)
				if err != nil {
					t.Fatalf("strike %d: unexpected error %v", i, err)
				}
				if evicted {
					t.Fatalf("strike %d of %d: want evicted=false (pre-final), got true", i, tc.revalidateStrikes)
				}
			}
			evicted, err := c.RecordRevalidateFail(ctx, k, now)
			if err != nil {
				t.Fatalf("final strike: unexpected error %v", err)
			}
			if !evicted {
				t.Errorf("strike %d (final): want evicted=true, got false", tc.revalidateStrikes)
			}
			if _, ok, _ := c.Get(ctx, k); ok {
				t.Errorf("row at %+v should be gone after eviction", k)
			}
		})
	}
}

// TestRecordRevalidateFail_PenultimateStrikeKeepsRow encodes the
// "eviction is on the next failure, not pre-emptive" invariant: a Get
// after RevalidateStrikes-1 failures still returns the row.
func TestRecordRevalidateFail_PenultimateStrikeKeepsRow(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.RevalidateStrikes = 3
	c := newCache(t, cfg)
	ctx := context.Background()

	k := keyFor("a", "h", "@s", "s")
	mustPut(t, c, k, CachedVerdict{Intent: "go", CreatedAt: time.Now().UTC()})

	now := time.Now().UTC()
	for i := 0; i < cfg.RevalidateStrikes-1; i++ {
		if evicted, _ := c.RecordRevalidateFail(ctx, k, now); evicted {
			t.Fatalf("strike %d of %d: row pre-emptively evicted", i+1, cfg.RevalidateStrikes)
		}
	}
	got, ok, err := c.Get(ctx, k)
	if err != nil {
		t.Fatalf("Get(%+v): unexpected error %v", k, err)
	}
	if !ok {
		t.Fatalf("Get(%+v) after %d/%d strikes: want hit, got miss", k, cfg.RevalidateStrikes-1, cfg.RevalidateStrikes)
	}
	if got.RevalidateFails != cfg.RevalidateStrikes-1 {
		t.Errorf("RevalidateFails: want %d, got %d", cfg.RevalidateStrikes-1, got.RevalidateFails)
	}
}

func TestRecordRevalidateFail_MissingRowIsNoOp(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

	k := keyFor("a", "h", "@s", "missing")
	evicted, err := c.RecordRevalidateFail(context.Background(), k, time.Now())
	if err != nil {
		t.Errorf("RecordRevalidateFail(%+v): unexpected error %v", k, err)
	}
	if evicted {
		t.Errorf("RecordRevalidateFail(%+v): want evicted=false (missing row), got true", k)
	}
}

func TestRecordHit_MissingRowIsNoOp(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

	k := keyFor("a", "h", "@s", "missing")
	if err := c.RecordHit(context.Background(), k, time.Now()); err != nil {
		t.Errorf("RecordHit(%+v): unexpected error %v", k, err)
	}
}

// ====================== eviction: app-hash + cold ======================

func TestInvalidateOtherHashes(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
	ctx := context.Background()

	keepX := keyFor("X", "abc", "@s", "k1")
	oldX := keyFor("X", "old", "@s", "k2")
	otherY := keyFor("Y", "old", "@s", "k3")

	mustPut(t, c, keepX, CachedVerdict{Intent: "x_new"})
	mustPut(t, c, oldX, CachedVerdict{Intent: "x_old"})
	mustPut(t, c, otherY, CachedVerdict{Intent: "y_old"})

	deleted, err := c.InvalidateOtherHashes(ctx, "X", "abc")
	if err != nil {
		t.Fatalf("InvalidateOtherHashes: unexpected error %v", err)
	}
	if deleted != 1 {
		t.Errorf("InvalidateOtherHashes: want deleted=1, got %d", deleted)
	}
	if _, ok, _ := c.Get(ctx, keepX); !ok {
		t.Errorf("expected keepX (current hash) to remain; key=%+v", keepX)
	}
	if _, ok, _ := c.Get(ctx, oldX); ok {
		t.Errorf("expected oldX (other hash, same app) to be deleted; key=%+v", oldX)
	}
	if _, ok, _ := c.Get(ctx, otherY); !ok {
		t.Errorf("expected otherY (different app) to remain untouched; key=%+v", otherY)
	}
}

func TestSweepCold(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
	ctx := context.Background()

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	old := keyFor("a", "h", "@s", "old")
	fresh := keyFor("a", "h", "@s", "fresh")
	otherApp := keyFor("b", "h", "@s", "old")

	mustPut(t, c, old, CachedVerdict{Intent: "go", LastHitAt: now.Add(-40 * 24 * time.Hour)})
	mustPut(t, c, fresh, CachedVerdict{Intent: "go", LastHitAt: now.Add(-1 * time.Hour)})
	mustPut(t, c, otherApp, CachedVerdict{Intent: "go", LastHitAt: now.Add(-40 * 24 * time.Hour)})

	cutoff := now.Add(-30 * 24 * time.Hour)
	deleted, err := c.SweepCold(ctx, "a", cutoff)
	if err != nil {
		t.Fatalf("SweepCold: unexpected error %v", err)
	}
	if deleted != 1 {
		t.Errorf("SweepCold: want deleted=1, got %d", deleted)
	}
	if _, ok, _ := c.Get(ctx, old); ok {
		t.Errorf("cold row should be swept; key=%+v", old)
	}
	if _, ok, _ := c.Get(ctx, fresh); !ok {
		t.Errorf("fresh row should remain; key=%+v", fresh)
	}
	if _, ok, _ := c.Get(ctx, otherApp); !ok {
		t.Errorf("other-app cold row should remain (SweepCold scoped to one app); key=%+v", otherApp)
	}
}

// TestSweepCold_NothingToDeleteIsClean asserts the documented short-
// circuit: when there's nothing to sweep, SweepCold returns (0, nil).
func TestSweepCold_NothingToDeleteIsClean(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
	ctx := context.Background()
	// Empty cache.
	deleted, err := c.SweepCold(ctx, "a", time.Now())
	if err != nil {
		t.Errorf("SweepCold (empty): unexpected error %v", err)
	}
	if deleted != 0 {
		t.Errorf("SweepCold (empty): want deleted=0, got %d", deleted)
	}
	// Cache with one fresh row.
	k := keyFor("a", "h", "@s", "fresh")
	mustPut(t, c, k, CachedVerdict{Intent: "go", LastHitAt: time.Now()})
	deleted, err = c.SweepCold(ctx, "a", time.Now().Add(-time.Hour))
	if err != nil {
		t.Errorf("SweepCold (no cold rows): unexpected error %v", err)
	}
	if deleted != 0 {
		t.Errorf("SweepCold (no cold rows): want deleted=0, got %d", deleted)
	}
}

// TestSweepCold_ConfidenceDecayMatrix encodes the confidence-decay policy:
// when ConfidenceDecay is on, rows with Confidence<0.7 have their effective
// MaxAge halved.
// The matrix below covers the four corners (low/high confidence ×
// inside/outside half-life window).
func TestSweepCold_ConfidenceDecayMatrix(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.MaxAge = 30 * 24 * time.Hour
	cfg.ConfidenceDecay = true

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-30 * 24 * time.Hour) // normal cutoff

	tests := []struct {
		name        string
		confidence  float64
		ageDays     int
		wantSwept   bool
		wantComment string
	}{
		{"low_conf_past_half_age", 0.5, 20, true, "Confidence<0.7 + age>MaxAge/2=15d -> swept"},
		{"low_conf_under_half_age", 0.5, 10, false, "Confidence<0.7 + age<MaxAge/2 -> survives"},
		{"high_conf_past_half_age", 0.9, 20, false, "Confidence>=0.7 ignores decay -> survives"},
		{"high_conf_past_full_age", 0.9, 40, true, "any row past MaxAge=30d swept"},
		{"boundary_conf_0_7", 0.7, 20, false, "Confidence==0.7 NOT decayed (strict <0.7)"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newCache(t, cfg)
			ctx := context.Background()

			k := keyFor("a", "h", "@s", tc.name)
			mustPut(t, c, k, CachedVerdict{
				Intent:     "go",
				Confidence: tc.confidence,
				LastHitAt:  now.Add(-time.Duration(tc.ageDays) * 24 * time.Hour),
			})

			deleted, err := c.SweepCold(ctx, "a", cutoff)
			if err != nil {
				t.Fatalf("SweepCold: unexpected error %v", err)
			}
			_, present, _ := c.Get(ctx, k)
			swept := !present
			if swept != tc.wantSwept {
				t.Errorf("%s: want swept=%v got swept=%v (deleted=%d, conf=%v, age=%dd) — %s",
					tc.name, tc.wantSwept, swept, deleted, tc.confidence, tc.ageDays, tc.wantComment)
			}
		})
	}
}

// ====================== eviction: TrimLRU ======================

func TestTrimLRU(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
	ctx := context.Background()

	base := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	// Insert 10 rows for app "a" with LastHitAt rising by minute, so
	// row i is the i-th coldest.
	for i := 0; i < 10; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		mustPut(t, c, k, CachedVerdict{Intent: "go", LastHitAt: base.Add(time.Duration(i) * time.Minute)})
	}
	otherApp := keyFor("b", "h", "@s", "b0")
	mustPut(t, c, otherApp, CachedVerdict{Intent: "go", LastHitAt: base})

	deleted, err := c.TrimLRU(ctx, "a", 5, 0.5)
	if err != nil {
		t.Fatalf("TrimLRU: unexpected error %v", err)
	}
	if deleted != 5 {
		t.Fatalf("TrimLRU: want deleted=5, got %d", deleted)
	}
	for i := 0; i < 10; i++ {
		k := keyFor("a", "h", "@s", signature(i))
		_, ok, _ := c.Get(ctx, k)
		if i < 5 && ok {
			t.Errorf("row %d (cold): want trimmed, but still present", i)
		}
		if i >= 5 && !ok {
			t.Errorf("row %d (warm): want present, but trimmed", i)
		}
	}
	if _, ok, _ := c.Get(ctx, otherApp); !ok {
		t.Errorf("other-app row must not be affected by TrimLRU; key=%+v", otherApp)
	}
}

func TestTrimLRU_NothingToDeleteIsClean(t *testing.T) {
	t.Parallel()
	tests := []struct {
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
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newCache(t, testConfig())
			ctx := context.Background()
			for i := 0; i < tc.preloadRows; i++ {
				k := keyFor("a", "h", "@s", signature(i))
				mustPut(t, c, k, CachedVerdict{Intent: "go", LastHitAt: time.Now()})
			}
			deleted, err := c.TrimLRU(ctx, "a", tc.cap, tc.trimFraction)
			if err != nil {
				t.Fatalf("TrimLRU: unexpected error %v", err)
			}
			if deleted != tc.wantDeleted {
				t.Errorf("%s: want deleted=%d, got %d (preload=%d cap=%d frac=%v)",
					tc.name, tc.wantDeleted, deleted, tc.preloadRows, tc.cap, tc.trimFraction)
			}
		})
	}
}

// ====================== synonyms ======================

func TestRecordSynonymHitAndStats(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
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
			t.Fatalf("RecordSynonymHit(%+v, %v): unexpected error %v", call.sk, call.at, err)
		}
	}

	stats, err := c.SynonymStats(ctx, "h")
	if err != nil {
		t.Fatalf("SynonymStats: unexpected error %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("SynonymStats: want len=2 (other-hash row must not leak), got %d (stats=%+v)", len(stats), stats)
	}
	if stats[0].Pattern != "wade across" || stats[0].HitCount != 2 {
		t.Errorf("stats[0]: want Pattern=%q HitCount=2, got Pattern=%q HitCount=%d", "wade across", stats[0].Pattern, stats[0].HitCount)
	}
	if stats[1].Pattern != "shoot game" || stats[1].HitCount != 1 {
		t.Errorf("stats[1]: want Pattern=%q HitCount=1, got Pattern=%q HitCount=%d", "shoot game", stats[1].Pattern, stats[1].HitCount)
	}
	if !stats[0].LastHitAt.Equal(now.Add(time.Hour)) {
		t.Errorf("stats[0].LastHitAt: want %v, got %v", now.Add(time.Hour), stats[0].LastHitAt)
	}
}

func TestSynonymStats_EmptyAppHashReturnsNothing(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
	stats, err := c.SynonymStats(context.Background(), "no-rows-here")
	if err != nil {
		t.Errorf("SynonymStats: unexpected error %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("SynonymStats: want empty for unknown app hash, got %+v", stats)
	}
}

// ====================== lifecycle ======================

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	got := DefaultConfig()
	want := Config{
		MaxAge:            30 * 24 * time.Hour,
		Cap:               10_000,
		TrimFraction:      0.10,
		RevalidateStrikes: 3,
		ConfidenceDecay:   false,
	}
	if got != want {
		t.Errorf("DefaultConfig: want %+v, got %+v", want, got)
	}
}

// TestClose_IsIdempotent pins that calling Close twice is safe — the
// in-memory cache it is a no-op, and the SQLite backend must be safe too.
func TestClose_IsIdempotent(t *testing.T) {
	t.Parallel()
	c := NewMemory(testConfig())
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: unexpected error %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: unexpected error %v", err)
	}
}

func TestContextCancellationHonoured(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
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
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.call(); err == nil {
				t.Errorf("%s: want error from cancelled context, got nil", tc.name)
			}
		})
	}
}

// TestConcurrentMutationsRaceClean is the -race smoke. Hammers Put,
// RecordHit and RecordSynonymHit from many goroutines and confirms the
// final state is consistent, covering both Put and RecordSynonymHit under
// concurrency.
func TestConcurrentMutationsRaceClean(t *testing.T) {
	t.Parallel()
	c := newCache(t, testConfig())
	ctx := context.Background()

	const goroutines = 8
	const perGoroutine = 500
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Writers: each goroutine writes its own slice of the keyspace so
	// the Get phase below can verify every row landed.
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				k := keyFor("a", "h", "@s", signature(g*perGoroutine+i))
				if err := c.Put(ctx, k, CachedVerdict{Intent: "go", LastHitAt: time.Now()}); err != nil {
					t.Errorf("Put: unexpected error %v", err)
					return
				}
			}
		}()
	}
	// RecordHit racers: hammer a single hot key. We expect HitCount to
	// equal goroutines*perGoroutine when this returns (the cache must
	// serialise correctly).
	hotKey := keyFor("a", "h", "@s", "hot")
	mustPut(t, c, hotKey, CachedVerdict{Intent: "go"})
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			now := time.Now()
			for i := 0; i < perGoroutine; i++ {
				if err := c.RecordHit(ctx, hotKey, now); err != nil {
					t.Errorf("RecordHit: unexpected error %v", err)
					return
				}
			}
		}()
	}
	// Synonym-hit racers: hammer a single SynonymKey.
	sk := SynonymKey{AppHash: "h", Intent: "ford", Pattern: "wade", Kind: "bare"}
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			now := time.Now()
			for i := 0; i < perGoroutine; i++ {
				if err := c.RecordSynonymHit(ctx, sk, now); err != nil {
					t.Errorf("RecordSynonymHit: unexpected error %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Sanity: every Put landed.
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			k := keyFor("a", "h", "@s", signature(g*perGoroutine+i))
			if _, ok, err := c.Get(ctx, k); err != nil || !ok {
				t.Fatalf("missing row after concurrent Put: g=%d i=%d ok=%v err=%v", g, i, ok, err)
			}
		}
	}
	// Hot row HitCount.
	hot := mustGet(t, c, hotKey)
	if hot.HitCount != goroutines*perGoroutine {
		t.Errorf("HitCount on hot row: want %d, got %d", goroutines*perGoroutine, hot.HitCount)
	}
	// Synonym hit count.
	stats, err := c.SynonymStats(ctx, "h")
	if err != nil {
		t.Fatalf("SynonymStats: unexpected error %v", err)
	}
	if len(stats) != 1 || stats[0].HitCount != goroutines*perGoroutine {
		t.Errorf("SynonymStats: want one row with HitCount=%d, got %+v", goroutines*perGoroutine, stats)
	}
}
