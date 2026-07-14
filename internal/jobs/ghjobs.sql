CREATE TABLE IF NOT EXISTS gh_jobs (
    job_id        TEXT PRIMARY KEY,        -- ULID (or hash of origin_ref); minted on first insert
    origin_ref    TEXT NOT NULL UNIQUE,    -- inbox.GitHubOriginRef: github:<repo>/<kind>/<number> — the natural key
    repo          TEXT NOT NULL,
    object_kind   TEXT NOT NULL,           -- 'issue' | 'pr'
    object_number TEXT NOT NULL,
    story         TEXT,                     -- chosen story path (NULL while guidance-parked)
    state         TEXT NOT NULL,            -- queued|claimed|running|awaiting_guidance|done|failed
    worker_id     TEXT,                     -- holder of the claim (NULL when unclaimed)
    run_id        TEXT,
    run_url       TEXT,
    comment_id    TEXT,                     -- the rolling-status comment id (captured on first Post)
    attempt_count INTEGER NOT NULL DEFAULT 0,
    incident_url  TEXT,
    metadata_json TEXT,                     -- producer-supplied context for story preflight/triage
    err_msg       TEXT,
    created_at    INTEGER NOT NULL,         -- unix ms
    updated_at    INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS gh_jobs_origin ON gh_jobs(origin_ref);

CREATE TABLE IF NOT EXISTS gh_job_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     TEXT NOT NULL,
    state      TEXT NOT NULL,
    message    TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    FOREIGN KEY(job_id) REFERENCES gh_jobs(job_id)
) STRICT;
CREATE INDEX IF NOT EXISTS gh_job_events_job ON gh_job_events(job_id, id);

CREATE TABLE IF NOT EXISTS gh_job_assets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     TEXT NOT NULL,
    name       TEXT NOT NULL,
    mime_type  TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    FOREIGN KEY(job_id) REFERENCES gh_jobs(job_id),
    UNIQUE(job_id, name)
) STRICT;
CREATE INDEX IF NOT EXISTS gh_job_assets_job ON gh_job_assets(job_id);
