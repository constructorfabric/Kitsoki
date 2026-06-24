-- Jobs and notifications tables.
-- Introduced via a migration applied on top of the existing store schema.
-- Persistence model documented in docs/stories/background-jobs/runtime.md.

CREATE TABLE IF NOT EXISTS jobs (
  id                   TEXT PRIMARY KEY,        -- ULID
  session_id           TEXT NOT NULL,
  kind                 TEXT NOT NULL,           -- handler name, e.g., "host.run_tests"
  status               TEXT NOT NULL,           -- running|awaiting_input|done|failed|cancelled
  origin_state         TEXT NOT NULL,           -- room where spawned
  origin_proposal_id   TEXT,                    -- nullable; jobs spawned outside a proposal
  payload              TEXT NOT NULL,           -- JSON: the `with` args passed to execute
  progress             TEXT,                    -- JSON: latest snapshot (overwritten)
  result               TEXT,                    -- JSON: on terminal status
  error                TEXT,                    -- string: on failed
  clarification_schema TEXT,                    -- JSON: set while awaiting_input
  clarification_answer TEXT,                    -- JSON: once submitted
  retry_count          INTEGER NOT NULL DEFAULT 0,
  created_at           INTEGER NOT NULL,        -- unix ms (queued)
  updated_at           INTEGER NOT NULL,
  started_at           INTEGER,                 -- actual handler start
  finished_at          INTEGER                  -- terminal timestamp
) STRICT;

CREATE INDEX IF NOT EXISTS jobs_session_status  ON jobs(session_id, status);
CREATE INDEX IF NOT EXISTS jobs_session_created ON jobs(session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS notifications (
  id                   TEXT PRIMARY KEY,        -- ULID
  session_id           TEXT NOT NULL,
  created_at           INTEGER NOT NULL,
  read_at              INTEGER,                 -- NULL if unread
  dismissed_at         INTEGER,                 -- NULL if active
  snoozed_until        INTEGER,                 -- NULL if not snoozed
  severity             TEXT NOT NULL,           -- info|success|warn|error|action_required
  title                TEXT NOT NULL,
  body                 TEXT,                    -- markdown
  teleport_state       TEXT NOT NULL,
  teleport_slots       TEXT,                    -- JSON
  teleport_proposal_id TEXT,                    -- nullable
  teleport_job_id      TEXT,                    -- nullable
  origin_kind          TEXT NOT NULL,           -- job|external
  origin_ref           TEXT NOT NULL,           -- e.g., "job:abc", "github:pr/123"
  origin_url           TEXT                     -- external deep link if any
) STRICT;

CREATE INDEX IF NOT EXISTS notif_session_unread  ON notifications(session_id, read_at, severity);
CREATE INDEX IF NOT EXISTS notif_session_created ON notifications(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS notif_dedup           ON notifications(session_id, origin_kind, origin_ref);
