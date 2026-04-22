-- schema.sql — Hally event-sourced session store DDL (§8).
-- Embedded via //go:embed in sqlite.go; executed idempotently on Open().
-- One SQLite file per session: ~/.hally/sessions/<session-id>.db

-- WAL mode + tuning pragmas are applied by the Go Open() constructor, not here,
-- because SQLite PRAGMAs cannot appear inside the embedded DDL run via Exec.

-- Session metadata (one row per session file).
CREATE TABLE IF NOT EXISTS sessions (
    id           TEXT    NOT NULL,
    app_id       TEXT    NOT NULL,
    app_version  TEXT    NOT NULL,
    started_at   INTEGER NOT NULL,   -- unix microseconds
    last_turn    INTEGER NOT NULL,   -- last turn number written
    status       TEXT    NOT NULL    -- "active" | "completed" | "abandoned"
) STRICT;

-- Append-only event log. One row per event; never UPDATEd or DELETEd.
-- PRIMARY KEY is (session_id, turn, seq) — the composite uniqueness guarantee
-- from §8. session_id ties back to the sessions row.
CREATE TABLE IF NOT EXISTS events (
    session_id   TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    ts           INTEGER NOT NULL,   -- unix microseconds, monotonic-within-turn
    kind         TEXT    NOT NULL,
    payload_json TEXT    NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;

-- Index on (session_id, kind) for efficient event-kind queries (§8, §9 trace view).
CREATE INDEX IF NOT EXISTS events_session_kind_idx ON events (session_id, kind);

-- Periodic materialized snapshots, one per N turns (default N=20).
-- §8 mandates this table for the resumption story.
CREATE TABLE IF NOT EXISTS snapshots (
    session_id   TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    state_path   TEXT    NOT NULL,
    world_json   TEXT    NOT NULL,
    rng_seed     INTEGER NOT NULL,
    PRIMARY KEY (session_id, turn)
) STRICT;
