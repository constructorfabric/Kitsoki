-- schema.sql — Kitsoki event-sourced session store DDL (§8).
-- Embedded via //go:embed in sqlite.go; executed idempotently on Open().
-- All sessions share one SQLite file: $XDG_DATA_HOME/kitsoki/sessions.db
-- (default ~/.local/share/kitsoki/sessions.db). Every table keys on session_id.

-- WAL mode + tuning pragmas are applied by the Go Open() constructor, not here,
-- because SQLite PRAGMAs cannot appear inside the embedded DDL run via Exec.

-- Session metadata (one row per session).
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

-- External-key index: maps (transport, thread) to a session_id so loop.py
-- and other orchestrators can address sessions by their inbound surface
-- key (e.g. ("jira", "PLTFRM-12345")). One session may carry multiple keys
-- (a Jira ticket plus the Bitbucket PR thread it spawns); the (transport,
-- thread) pair is unique. Proposal §3.2.
CREATE TABLE IF NOT EXISTS external_keys (
    transport   TEXT    NOT NULL,
    thread      TEXT    NOT NULL,
    session_id  TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (transport, thread)
) STRICT;
CREATE INDEX IF NOT EXISTS external_keys_session_idx ON external_keys(session_id);

-- Session-level writer lock: row-keyed by session_id. Acquired by
-- WithWriterLock around the load → run → post → commit critical section
-- so two `kitsoki session continue` invocations on the same key serialize.
-- Stale locks (owner pid no longer alive) are reaped on the next acquire
-- attempt. See WithWriterLock in external_keys.go.
CREATE TABLE IF NOT EXISTS session_locks (
    session_id   TEXT    NOT NULL PRIMARY KEY,
    owner_pid    INTEGER NOT NULL,
    owner_host   TEXT    NOT NULL,
    acquired_at  INTEGER NOT NULL
) STRICT;

-- Durable session journal. Written atomically alongside events by
-- AppendEventsAndJournal; see internal/journal for the reader/replay side.
-- One row per journal entry. Patch entries carry a (doc, doc_version) pair;
-- typed-only entries leave doc and doc_version NULL. Checkpoints are stored
-- here too, using the "<doc>.checkpoint" kind value.
-- The (session_id, doc, doc_version) index enables efficient per-document
-- replay starting from the latest checkpoint.
CREATE TABLE IF NOT EXISTS journal (
    session_id   TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    ts           INTEGER NOT NULL,   -- unix microseconds
    kind         TEXT    NOT NULL,
    doc          TEXT,               -- nullable for typed-only entries
    doc_version  INTEGER,            -- nullable for typed-only entries
    body_json    TEXT    NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;

CREATE INDEX IF NOT EXISTS journal_doc_idx ON journal (session_id, doc, doc_version);
