package turncache

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var sqliteSchemaDDL string

// NewSQLite opens (or creates) a [Cache] backed by a SQLite file at the
// given path. The schema is created on first open and re-applied
// idempotently on every subsequent open (every statement in schema.sql
// is guarded with IF NOT EXISTS).
//
// On Open the backend leaves rows in place — [Cache.InvalidateOtherHashes]
// is the caller's responsibility once the live AppDef hash for each app is
// known. A single file stores rows for many apps, so the SQLite layer has
// no way to know which hashes are stale on its own; the orchestrator runs
// the app-hash purge at start.
//
// The returned Cache is safe for concurrent use from many goroutines.
// modernc.org/sqlite serialises writes at the driver level; we add an
// extra package-level lock around composed UPDATE-then-DELETE
// operations (RecordRevalidateFail) to avoid TOCTOU between the strike
// increment and the row deletion.
//
// Close is idempotent: the second call is a no-op.
func NewSQLite(path string, cfg Config) (Cache, error) {
	if cfg.RevalidateStrikes < 1 {
		cfg.RevalidateStrikes = 1
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("turncache.NewSQLite: open %q: %w", path, err)
	}
	// Single connection: SQLite has a single-writer model. The
	// database/sql pool would otherwise hand out fresh connections
	// without our pragmas, and concurrent goroutines would see
	// SQLITE_BUSY despite busy_timeout being set. This matches the
	// convention in internal/store/sqlite.go.
	db.SetMaxOpenConns(1)

	// Pragmas borrowed from internal/store/sqlite.go conventions:
	//   - journal_mode=WAL    : concurrent reads, sane write throughput.
	//   - synchronous=NORMAL  : the WAL provides crash-safety; full
	//                          sync is overkill for a cache.
	//   - busy_timeout=5s     : avoid SQLITE_BUSY under contention.
	//   - foreign_keys=ON     : defensive, even though we declare none.
	pragmas := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("turncache.NewSQLite: %s: %w", p, err)
		}
	}
	if _, err := db.Exec(sqliteSchemaDDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("turncache.NewSQLite: schema migration: %w", err)
	}
	return &sqliteCache{cfg: cfg, db: db}, nil
}

// sqliteCache is the SQLite-backed [Cache]. The mutex guards the
// closed flag (so Close is idempotent) and the composite
// RecordRevalidateFail UPDATE+DELETE so concurrent strike-bumps on the
// same key cannot race past the eviction threshold.
type sqliteCache struct {
	cfg Config

	mu     sync.Mutex
	db     *sql.DB
	closed bool
}

// nullableMillis converts a time.Time to a nullable unix-millis int64
// for SQLite. The zero time encodes as NULL — this is the schema-level
// contract for "never set" used by created/hit/verified columns.
func nullableMillis(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

// fromNullableMillis is the inverse: a NULL becomes the zero time, a
// valid unix-millis becomes a UTC time.Time.
func fromNullableMillis(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64).UTC()
}

func (c *sqliteCache) Get(ctx context.Context, k Key) (CachedVerdict, bool, error) {
	if err := ctx.Err(); err != nil {
		return CachedVerdict{}, false, err
	}
	row := c.db.QueryRowContext(ctx, `
		SELECT intent, slots_json, confidence,
		       COALESCE(source_model, ''), COALESCE(source_turn_id, ''),
		       hit_count, last_hit_at, last_verified_at,
		       revalidate_fails, created_at
		FROM turn_cache
		WHERE app = ? AND app_hash = ? AND state_path = ? AND signature = ?`,
		k.App, k.AppHash, k.StatePath, k.Signature,
	)
	var (
		v                     CachedVerdict
		lastHit, lastVerified sql.NullInt64
		createdAt             int64
	)
	err := row.Scan(
		&v.Intent, &v.SlotsJSON, &v.Confidence,
		&v.SourceModel, &v.SourceTurnID,
		&v.HitCount, &lastHit, &lastVerified,
		&v.RevalidateFails, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CachedVerdict{}, false, nil
	}
	if err != nil {
		return CachedVerdict{}, false, fmt.Errorf("turncache.Get: %w", err)
	}
	v.LastHitAt = fromNullableMillis(lastHit)
	v.LastVerifiedAt = fromNullableMillis(lastVerified)
	v.CreatedAt = time.UnixMilli(createdAt).UTC()
	return v, true, nil
}

func (c *sqliteCache) Put(ctx context.Context, k Key, v CachedVerdict) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	// C2 fix: mirror the memory backend — stamp LastHitAt from
	// CreatedAt when the caller leaves it zero. Without this, a
	// freshly-Put row stores last_hit_at = NULL, and the SweepCold
	// "NULL counts as ancient" arm wipes it on the very next
	// orchestrator start. Callers that pass a non-zero LastHitAt
	// (e.g. cross-process restore tests) have it round-trip unchanged.
	if v.LastHitAt.IsZero() {
		v.LastHitAt = v.CreatedAt
	}
	// "INSERT OR REPLACE" rewrites every column — the surrounding
	// orchestrator's Put semantic is "replace the row wholesale," which
	// matches the in-memory cache. CreatedAt has already been
	// auto-stamped above if the caller left it zero.
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO turn_cache (
			app, app_hash, state_path, signature,
			intent, slots_json, confidence,
			source_model, source_turn_id,
			hit_count, last_hit_at, last_verified_at,
			revalidate_fails, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(app, app_hash, state_path, signature) DO UPDATE SET
			intent           = excluded.intent,
			slots_json       = excluded.slots_json,
			confidence       = excluded.confidence,
			source_model     = excluded.source_model,
			source_turn_id   = excluded.source_turn_id,
			hit_count        = excluded.hit_count,
			last_hit_at      = excluded.last_hit_at,
			last_verified_at = excluded.last_verified_at,
			revalidate_fails = excluded.revalidate_fails,
			created_at       = excluded.created_at
		`,
		k.App, k.AppHash, k.StatePath, k.Signature,
		v.Intent, v.SlotsJSON, v.Confidence,
		nullableString(v.SourceModel), nullableString(v.SourceTurnID),
		v.HitCount, nullableMillis(v.LastHitAt), nullableMillis(v.LastVerifiedAt),
		v.RevalidateFails, v.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("turncache.Put: %w", err)
	}
	return nil
}

func (c *sqliteCache) RecordHit(ctx context.Context, k Key, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	atMillis := at.UnixMilli()
	_, err := c.db.ExecContext(ctx, `
		UPDATE turn_cache
		SET hit_count = hit_count + 1,
		    last_hit_at = ?,
		    last_verified_at = ?,
		    revalidate_fails = 0
		WHERE app = ? AND app_hash = ? AND state_path = ? AND signature = ?`,
		atMillis, atMillis,
		k.App, k.AppHash, k.StatePath, k.Signature,
	)
	if err != nil {
		return fmt.Errorf("turncache.RecordHit: %w", err)
	}
	return nil
}

func (c *sqliteCache) RecordRevalidateFail(ctx context.Context, k Key, at time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Hold the package-level lock for the duration of the
	// read-increment-evict cycle. Without this, two concurrent strike
	// bumps could each see revalidate_fails=2 (under cfg.Strikes=3),
	// both UPDATE to 3, but only one would DELETE — leaving the row at
	// 4 fails undetected. The mutex is held inside Begin/Commit so the
	// transaction has exclusive logical ownership of the row.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false, errors.New("turncache.RecordRevalidateFail: cache is closed")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("turncache.RecordRevalidateFail: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE turn_cache
		SET revalidate_fails = revalidate_fails + 1
		WHERE app = ? AND app_hash = ? AND state_path = ? AND signature = ?`,
		k.App, k.AppHash, k.StatePath, k.Signature,
	)
	if err != nil {
		return false, fmt.Errorf("turncache.RecordRevalidateFail: update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("turncache.RecordRevalidateFail: rows affected: %w", err)
	}
	if affected == 0 {
		// Missing row: documented no-op.
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("turncache.RecordRevalidateFail: commit: %w", err)
		}
		return false, nil
	}

	var fails int
	if err := tx.QueryRowContext(ctx, `
		SELECT revalidate_fails FROM turn_cache
		WHERE app = ? AND app_hash = ? AND state_path = ? AND signature = ?`,
		k.App, k.AppHash, k.StatePath, k.Signature,
	).Scan(&fails); err != nil {
		return false, fmt.Errorf("turncache.RecordRevalidateFail: read back: %w", err)
	}
	evicted := false
	if fails >= c.cfg.RevalidateStrikes {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM turn_cache
			WHERE app = ? AND app_hash = ? AND state_path = ? AND signature = ?`,
			k.App, k.AppHash, k.StatePath, k.Signature,
		); err != nil {
			return false, fmt.Errorf("turncache.RecordRevalidateFail: delete: %w", err)
		}
		evicted = true
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("turncache.RecordRevalidateFail: commit: %w", err)
	}
	_ = at // at is part of the interface signature but the in-memory cache also ignores it for strike-only updates.
	return evicted, nil
}

func (c *sqliteCache) InvalidateOtherHashes(ctx context.Context, app string, keepHash string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := c.db.ExecContext(ctx, `
		DELETE FROM turn_cache
		WHERE app = ? AND app_hash != ?`,
		app, keepHash,
	)
	if err != nil {
		return 0, fmt.Errorf("turncache.InvalidateOtherHashes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("turncache.InvalidateOtherHashes: rows affected: %w", err)
	}
	// M1 fix: also drop synonym_hits rows for any app_hash that is not
	// the keep-hash. The synonym_hits table is intentionally app-less
	// (PK is (app_hash, intent, pattern, kind)) — under v1 the working
	// assumption is one cache file per app, so any old app_hash is
	// stale by definition. The reported count remains the turn_cache
	// row count for interface backward-compatibility; the synonym_hits
	// drop is best-effort and surfaces only via SynonymStats afterwards.
	if _, err := c.db.ExecContext(ctx, `
		DELETE FROM synonym_hits
		WHERE app_hash != ?`,
		keepHash,
	); err != nil {
		return int(n), fmt.Errorf("turncache.InvalidateOtherHashes: synonym_hits: %w", err)
	}
	return int(n), nil
}

func (c *sqliteCache) SweepCold(ctx context.Context, app string, olderThan time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// The in-memory cache treats LastHitAt zero (never set) as "before
	// any cutoff," so a freshly-Put row would be swept. The SQLite
	// implementation must mirror that exactly: zero-time → NULL
	// last_hit_at → "older than anything that has a timestamp"
	// is the wrong reading. Confirmed by re-reading internal/memory.go:
	// `v.LastHitAt.Before(cutoff)` — a zero time.Time IS before any
	// non-zero cutoff, so the in-memory backend DOES sweep
	// never-hit rows when their app matches. To preserve behavioural
	// equivalence we mirror that here: a NULL last_hit_at counts as
	// "before any cutoff" and is eligible for sweep.
	//
	// Two delete statements; one for rows with low confidence under a
	// half-MaxAge cutoff (when ConfidenceDecay is on), and the regular
	// MaxAge cutoff for everything else. Conceptually:
	//
	//   for each row with app=?:
	//     cutoff := olderThan
	//     if ConfidenceDecay && confidence<0.7 && MaxAge>0:
	//       cutoff = olderThan + MaxAge/2
	//     if last_hit_at < cutoff: delete
	//
	// SQLite handles "NULL < <int>" as UNKNOWN (filter excludes), so
	// we have to write the "never-hit" arm explicitly with IS NULL.
	cutoff := olderThan.UnixMilli()
	var (
		decayActive bool
		decayCutoff int64
	)
	if c.cfg.ConfidenceDecay && c.cfg.MaxAge > 0 {
		decayActive = true
		decayCutoff = olderThan.Add(c.cfg.MaxAge / 2).UnixMilli()
	}

	var (
		query string
		args  []any
	)
	if decayActive {
		// Either: (high-conf OR decay disabled by confidence) row is older than cutoff
		//     OR: (low-conf) row is older than decayCutoff
		// Plus the IS NULL arm for never-hit rows: it ALWAYS counts
		// as "before any cutoff," matching the in-memory backend.
		query = `
			DELETE FROM turn_cache
			WHERE app = ?
			  AND (
			        last_hit_at IS NULL
			     OR (confidence >= 0.7 AND last_hit_at < ?)
			     OR (confidence <  0.7 AND last_hit_at < ?)
			  )`
		args = []any{app, cutoff, decayCutoff}
	} else {
		query = `
			DELETE FROM turn_cache
			WHERE app = ?
			  AND (last_hit_at IS NULL OR last_hit_at < ?)`
		args = []any{app, cutoff}
	}
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("turncache.SweepCold: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("turncache.SweepCold: rows affected: %w", err)
	}
	return int(n), nil
}

func (c *sqliteCache) TrimLRU(ctx context.Context, app string, capRows int, trimFraction float64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if capRows <= 0 || trimFraction <= 0 {
		return 0, nil
	}
	// Count rows for this app first; cheap and avoids the DELETE
	// transaction in the common case where the app is under cap.
	var total int
	if err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM turn_cache WHERE app = ?`, app,
	).Scan(&total); err != nil {
		return 0, fmt.Errorf("turncache.TrimLRU: count: %w", err)
	}
	if total <= capRows {
		return 0, nil
	}
	drop := int(float64(total) * trimFraction)
	if drop < 1 {
		drop = 1
	}
	if drop > total {
		drop = total
	}
	// Delete the bottom `drop` rows by last_hit_at ASC. SQLite
	// orders NULL first under ASC by default, which matches the
	// in-memory backend's interpretation of "never-hit rows are the
	// coldest." Confirmed by re-reading memory.go's sort which
	// compares time.Time values and treats the zero value as the
	// oldest.
	res, err := c.db.ExecContext(ctx, `
		DELETE FROM turn_cache
		WHERE rowid IN (
			SELECT rowid FROM turn_cache
			WHERE app = ?
			ORDER BY last_hit_at ASC
			LIMIT ?
		)`,
		app, drop,
	)
	if err != nil {
		return 0, fmt.Errorf("turncache.TrimLRU: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("turncache.TrimLRU: rows affected: %w", err)
	}
	return int(n), nil
}

func (c *sqliteCache) RecordSynonymHit(ctx context.Context, sk SynonymKey, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO synonym_hits (app_hash, intent, pattern, kind, hit_count, last_hit_at)
		VALUES (?, ?, ?, ?, 1, ?)
		ON CONFLICT(app_hash, intent, pattern, kind) DO UPDATE SET
			hit_count = hit_count + 1,
			last_hit_at = excluded.last_hit_at`,
		sk.AppHash, sk.Intent, sk.Pattern, sk.Kind, at.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("turncache.RecordSynonymHit: %w", err)
	}
	return nil
}

func (c *sqliteCache) SynonymStats(ctx context.Context, appHash string) ([]SynonymStat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT app_hash, intent, pattern, kind, hit_count, last_hit_at
		FROM synonym_hits
		WHERE app_hash = ?
		ORDER BY hit_count DESC, last_hit_at DESC`,
		appHash,
	)
	if err != nil {
		return nil, fmt.Errorf("turncache.SynonymStats: %w", err)
	}
	defer rows.Close()
	var out []SynonymStat
	for rows.Next() {
		var (
			s   SynonymStat
			lha sql.NullInt64
		)
		if err := rows.Scan(&s.AppHash, &s.Intent, &s.Pattern, &s.Kind, &s.HitCount, &lha); err != nil {
			return nil, fmt.Errorf("turncache.SynonymStats: scan: %w", err)
		}
		s.LastHitAt = fromNullableMillis(lha)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("turncache.SynonymStats: rows: %w", err)
	}
	return out, nil
}

func (c *sqliteCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.db == nil {
		return nil
	}
	if err := c.db.Close(); err != nil {
		return fmt.Errorf("turncache.Close: %w", err)
	}
	return nil
}

// nullableString returns sql.NullString from a plain string; an empty
// string maps to NULL so the round-trip preserves "no value set."
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
