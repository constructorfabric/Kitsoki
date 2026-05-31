-- SQLite schema for the persistent turn-cache backend.
-- Applied idempotently by turncache.NewSQLite via //go:embed; every
-- statement is CREATE … IF NOT EXISTS so re-running this DDL on an
-- existing DB is a no-op.
--
-- The schema carries two tables — turn_cache (cached verdicts) and
-- synonym_hits (per-synonym counters) — with one deliberate addition:
-- the `app` column is part of the
-- turn_cache primary key alongside `app_hash`. The interface in
-- turncache.Cache treats App and AppHash as separate fields on Key and
-- several methods (InvalidateOtherHashes, SweepCold, TrimLRU) operate on
-- an app id, so the SQLite layout has to follow suit.
--
-- Timestamps are stored as unix-millis INTEGER (not TIMESTAMP) — easier
-- to round-trip through time.UnixMilli without any driver-level type
-- conversion games.

CREATE TABLE IF NOT EXISTS turn_cache (
    app              TEXT    NOT NULL,
    app_hash         TEXT    NOT NULL,
    state_path       TEXT    NOT NULL,
    signature        TEXT    NOT NULL,
    intent           TEXT    NOT NULL,
    slots_json       TEXT    NOT NULL,
    confidence       REAL    NOT NULL,
    source_model     TEXT,
    source_turn_id   TEXT,

    hit_count        INTEGER NOT NULL DEFAULT 0,
    last_hit_at      INTEGER,                            -- unix-millis; NULL means never hit.
    last_verified_at INTEGER,                            -- unix-millis; NULL means never verified.
    revalidate_fails INTEGER NOT NULL DEFAULT 0,

    created_at       INTEGER NOT NULL,                   -- unix-millis.
    PRIMARY KEY (app, app_hash, state_path, signature)
) STRICT;
CREATE INDEX IF NOT EXISTS turn_cache_lru ON turn_cache (app, last_hit_at);

-- synonym_hits is intentionally app-less: the PK is (app_hash, intent,
-- pattern, kind) with no `app` column. v1 working assumption is one
-- cache file per app, so every app_hash is unambiguously this app's.
-- InvalidateOtherHashes(app, keepHash) deletes every synonym_hits row
-- whose app_hash != keepHash (no app filter) — without this cleanup
-- the table would grow unbounded across YAML edits (M1).
-- If multi-app coexistence in one cache file ever becomes a
-- requirement, add an `app` column here and a matching filter in
-- InvalidateOtherHashes / SynonymStats / RecordSynonymHit.
CREATE TABLE IF NOT EXISTS synonym_hits (
    app_hash    TEXT    NOT NULL,
    intent      TEXT    NOT NULL,
    pattern     TEXT    NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('bare','example','template','enum_value')),
    hit_count   INTEGER NOT NULL DEFAULT 0,
    last_hit_at INTEGER,                                  -- unix-millis.
    PRIMARY KEY (app_hash, intent, pattern, kind)
) STRICT;
