package turncache

import (
	"context"
	"time"
)

// Cache is the abstraction the orchestrator and both backends ([NewMemory]
// and [NewSQLite]) satisfy. All methods are context-aware; implementations
// honour cancellation at cheap loop boundaries but are not required to
// interrupt single-row mutations mid-flight.
type Cache interface {
	// Get returns the cached verdict for k. If no row exists the boolean
	// return is false and the CachedVerdict is the zero value.
	Get(ctx context.Context, k Key) (CachedVerdict, bool, error)

	// Put inserts or replaces the row at k with v. Put is the canonical
	// place to seed CreatedAt — implementations may overwrite a
	// zero-valued v.CreatedAt with the current time.
	Put(ctx context.Context, k Key, v CachedVerdict) error

	// RecordHit notes that the row at k was used and successfully
	// re-validated at the given timestamp. Increments HitCount, advances
	// LastHitAt and LastVerifiedAt, and resets RevalidateFails to 0. A
	// no-op if the row does not exist.
	RecordHit(ctx context.Context, k Key, at time.Time) error

	// RecordRevalidateFail notes that a row at k was retrieved but its
	// re-validation against the live Machine state failed. Increments
	// RevalidateFails; once it reaches the configured RevalidateStrikes
	// the row is deleted and evicted is true. A no-op (evicted=false)
	// if the row does not exist.
	RecordRevalidateFail(ctx context.Context, k Key, at time.Time) (evicted bool, err error)

	// InvalidateOtherHashes deletes every row whose App matches app but
	// whose AppHash differs from keepHash. This is the app-hash
	// invalidation policy and is typically called once at process start.
	InvalidateOtherHashes(ctx context.Context, app string, keepHash string) (deleted int, err error)

	// SweepCold deletes every row for app whose LastHitAt is strictly
	// older than olderThan. This is the time-based expiry policy.
	SweepCold(ctx context.Context, app string, olderThan time.Time) (deleted int, err error)

	// TrimLRU brings the row count for app below cap by deleting the
	// bottom trimFraction of rows sorted by LastHitAt ascending. This is
	// the LRU size-cap policy. A no-op when the current row count is
	// already ≤ cap.
	TrimLRU(ctx context.Context, app string, cap int, trimFraction float64) (deleted int, err error)

	// RecordSynonymHit records a hit on the declared synonym described by
	// sk. Backs the per-synonym hit counters the routing-inspect views
	// read via SynonymStats.
	RecordSynonymHit(ctx context.Context, sk SynonymKey, at time.Time) error

	// SynonymStats returns every synonym-hit row for appHash, sorted by
	// HitCount descending (ties broken by LastHitAt descending).
	SynonymStats(ctx context.Context, appHash string) ([]SynonymStat, error)

	// Close releases backend resources. For the in-memory cache this is
	// a no-op; the SQLite cache closes the underlying DB.
	Close() error
}

// Config controls the eviction-policy thresholds. The per-app YAML knobs
// that map onto these fields are documented under "Per-app routing config"
// in docs/architecture/semantic-routing.md; see [DefaultConfig] for the
// recommended values.
type Config struct {
	// MaxAge is the time-based expiry cutoff. A row whose LastHitAt is
	// older than this is swept on the next [Cache.SweepCold] call. Zero
	// disables time-based expiry entirely.
	MaxAge time.Duration

	// Cap is the LRU row cap per app. Zero disables the cap.
	Cap int

	// TrimFraction is the fraction of rows discarded when [Cache.TrimLRU]
	// runs against an over-cap app. 0.10 means "drop the coldest 10%".
	TrimFraction float64

	// RevalidateStrikes is the strike count: after this many consecutive
	// [Cache.RecordRevalidateFail] calls the row is evicted. Must be ≥ 1;
	// values < 1 are treated as 1.
	RevalidateStrikes int

	// ConfidenceDecay, when set, halves the effective MaxAge during
	// [Cache.SweepCold] for rows whose originating
	// CachedVerdict.Confidence is below confidenceDecayThreshold. Opt-in;
	// default off.
	ConfidenceDecay bool
}

// DefaultConfig returns the recommended defaults: 30-day expiry, 10k-row
// cap, 10% trim fraction, 3 strikes, no confidence decay.
func DefaultConfig() Config {
	return Config{
		MaxAge:            30 * 24 * time.Hour,
		Cap:               10_000,
		TrimFraction:      0.10,
		RevalidateStrikes: 3,
		ConfidenceDecay:   false,
	}
}
