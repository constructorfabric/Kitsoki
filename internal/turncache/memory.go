package turncache

import (
	"context"
	"sort"
	"sync"
	"time"
)

// cancellationCheckInterval is the number of rows the in-memory sweeps walk
// between context-cancellation checks. The map iterations hold the cache
// mutex, so a per-row ctx.Err() call would dominate the loop; checking once
// per this many rows keeps cancellation responsive on large tables without
// turning the hot path into a syscall storm. The value is a power of two
// purely so the i%N test compiles to a mask; its magnitude is not load-bearing.
const cancellationCheckInterval = 1024

// confidenceDecayThreshold is the originating-verdict confidence below which
// a row is treated as low-confidence for [Config.ConfidenceDecay]: such rows
// have their effective MaxAge halved during [Cache.SweepCold]. The SQLite
// backend hard-codes the same 0.7 in its WHERE clause; the two must agree for
// the conformance suite to pass.
const confidenceDecayThreshold = 0.7

// NewMemory returns an in-process [Cache] with no persistence. It uses a
// single mutex around two maps — the cache table and the synonym-hit
// table — so it is safe for concurrent use and cheapest on the read path.
// Use [NewSQLite] instead when rows must survive a process restart.
func NewMemory(cfg Config) Cache {
	if cfg.RevalidateStrikes < 1 {
		cfg.RevalidateStrikes = 1
	}
	return &memoryCache{
		cfg:      cfg,
		rows:     make(map[Key]*CachedVerdict),
		synonyms: make(map[SynonymKey]*SynonymStat),
	}
}

type memoryCache struct {
	cfg Config

	mu       sync.Mutex
	rows     map[Key]*CachedVerdict
	synonyms map[SynonymKey]*SynonymStat
}

func (c *memoryCache) Get(ctx context.Context, k Key) (CachedVerdict, bool, error) {
	if err := ctx.Err(); err != nil {
		return CachedVerdict{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.rows[k]
	if !ok {
		return CachedVerdict{}, false, nil
	}
	// Return a copy; the cache row is owned by the map.
	return *v, true, nil
}

func (c *memoryCache) Put(ctx context.Context, k Key, v CachedVerdict) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	// C2 fix: stamp LastHitAt from CreatedAt when the caller leaves it
	// zero. A row that has been Put but never RecordHit'd is
	// semantically as fresh as a row that was hit at the moment of
	// creation; without this, time.Time{}.Before(anyNonZeroCutoff) is
	// true and the next SweepCold wipes every freshly-Put row — the
	// orchestrator runs that sweep once per process start, so every
	// restart would otherwise nuke the cross-session cache.
	// Callers that already supply a non-zero LastHitAt have it
	// round-trip unchanged.
	if v.LastHitAt.IsZero() {
		v.LastHitAt = v.CreatedAt
	}
	row := v
	c.rows[k] = &row
	return nil
}

func (c *memoryCache) RecordHit(ctx context.Context, k Key, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	row, ok := c.rows[k]
	if !ok {
		return nil
	}
	row.HitCount++
	row.LastHitAt = at
	row.LastVerifiedAt = at
	row.RevalidateFails = 0
	return nil
}

func (c *memoryCache) RecordRevalidateFail(ctx context.Context, k Key, at time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	row, ok := c.rows[k]
	if !ok {
		return false, nil
	}
	row.RevalidateFails++
	if row.RevalidateFails >= c.cfg.RevalidateStrikes {
		delete(c.rows, k)
		return true, nil
	}
	return false, nil
}

func (c *memoryCache) InvalidateOtherHashes(ctx context.Context, app string, keepHash string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	deleted := 0
	// Cheap-check cancellation periodically while iterating.
	i := 0
	for k := range c.rows {
		if i%cancellationCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return deleted, err
			}
		}
		i++
		if k.App == app && k.AppHash != keepHash {
			delete(c.rows, k)
			deleted++
		}
	}
	// M1 fix: also clear synonym_hits rows whose app_hash is not the
	// keep-hash. The synonym_hits table is intentionally app-less
	// (keyed by app_hash, intent, pattern, kind) under the v1
	// "one cache file per app" working assumption — every app_hash
	// that is not the live one belongs to a stale snapshot, regardless
	// of which app it came from. Without this cleanup the table grows
	// unbounded across YAML edits.
	for sk := range c.synonyms {
		if i%cancellationCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return deleted, err
			}
		}
		i++
		if sk.AppHash != keepHash {
			delete(c.synonyms, sk)
		}
	}
	return deleted, nil
}

func (c *memoryCache) SweepCold(ctx context.Context, app string, olderThan time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	deleted := 0
	i := 0
	for k, v := range c.rows {
		if i%cancellationCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return deleted, err
			}
		}
		i++
		if k.App != app {
			continue
		}
		cutoff := olderThan
		if c.cfg.ConfidenceDecay && v.Confidence < confidenceDecayThreshold && c.cfg.MaxAge > 0 {
			// Halve the effective max age by pulling the cutoff forward
			// by half of MaxAge.
			cutoff = olderThan.Add(c.cfg.MaxAge / 2)
		}
		if v.LastHitAt.Before(cutoff) {
			delete(c.rows, k)
			deleted++
		}
	}
	return deleted, nil
}

func (c *memoryCache) TrimLRU(ctx context.Context, app string, capRows int, trimFraction float64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if capRows <= 0 || trimFraction <= 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	type row struct {
		k  Key
		at time.Time
	}
	var rows []row
	for k, v := range c.rows {
		if k.App == app {
			rows = append(rows, row{k: k, at: v.LastHitAt})
		}
	}
	if len(rows) <= capRows {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].at.Before(rows[j].at)
	})
	// Drop the coldest trimFraction of rows. Always drop at least one
	// row when we are over cap; otherwise a tiny trim fraction against
	// a barely-over-cap table is a no-op forever.
	drop := int(float64(len(rows)) * trimFraction)
	if drop < 1 {
		drop = 1
	}
	if drop > len(rows) {
		drop = len(rows)
	}
	for i := 0; i < drop; i++ {
		delete(c.rows, rows[i].k)
	}
	return drop, nil
}

func (c *memoryCache) RecordSynonymHit(ctx context.Context, sk SynonymKey, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	stat, ok := c.synonyms[sk]
	if !ok {
		stat = &SynonymStat{SynonymKey: sk}
		c.synonyms[sk] = stat
	}
	stat.HitCount++
	stat.LastHitAt = at
	return nil
}

func (c *memoryCache) SynonymStats(ctx context.Context, appHash string) ([]SynonymStat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []SynonymStat
	for _, s := range c.synonyms {
		if s.AppHash != appHash {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HitCount != out[j].HitCount {
			return out[i].HitCount > out[j].HitCount
		}
		return out[i].LastHitAt.After(out[j].LastHitAt)
	})
	return out, nil
}

func (c *memoryCache) Close() error { return nil }
