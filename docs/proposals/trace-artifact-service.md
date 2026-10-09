# Tracing: persistent artifact-job trace and artifact service

**Status:** Draft v2. Reparented from the GitHub-agent epic into
`artifact-driven-stories.md` so dev-story, local jobs, and GitHub jobs share one
durable run/artifact substrate. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   ../artifact-driven-stories.md
**Depends on:** [`artifact-job-registry.md`](artifact-job-registry.md)

## Why

Runstatus can serve one live in-process session, and journal artifacts can be
opened by handle while that session is available. The GitHub agent also has a
minimal `/run/<job-id>` HTML/JSON page backed directly by `jobs.GHJobStore`.
Neither is the durable, queryable service dev-story jobs need:

- completed runs are not listed as a durable catalog across sessions;
- a process exit loses the live runstatus source even though trace JSONL exists;
- artifacts are not indexed by job and replayable run;
- stable run URLs are currently a GitHub-specific concern rather than the
  artifact-job substrate every story can use.

## What changes

Add a run/artifact store that consumes existing trace and artifact events,
indexes them by artifact `job_id`, and serves both live and completed runs
through the existing runstatus RPC and by-handle artifact routes.

This slice is mostly a **consumer**, not a new recorder:

- Producers stay unchanged: `internal/journal/` writes trace JSONL, and
  `artifact.emitted` records artifact handles/paths.
- The service lands trace JSONL and blobs under a filesystem runs root.
- A queryable index stores runs and artifacts by `job_id`.
- Live runs stream from the active source; completed runs replay from the stored
  trace JSONL.
- `/run/<job-id>` maps to the runstatus session source for that job.
- `/artifact/<handle>`, `/poster`, and semantic siblings resolve through the
  artifact index for stored runs.

Local dev-story usage uses a SQLite-backed index beside the session store.
Hosted/GitHub usage uses Postgres for the same logical rows. Blobs stay on the
filesystem in v1.

## Impact

- **Code seams:** `RunStore` / `ArtifactIndex` interfaces, local SQLite and
  hosted Postgres implementations, trace ingestion/reindexer, multi-run
  `SessionProvider`, by-handle `ArtifactResolver`, and `runstatus.run.artifacts`.
- **Format:** no new trace event is required for artifact rows; the index folds
  existing `artifact.emitted` events. Run lifecycle rows are bound to the
  artifact-job registry from slice #1.
- **Backward compat:** local single-session `kitsoki web`, `kitsoki status
  serve`, and exported HTML keep working without a store. The multi-run service
  is additive and only activates when a run store is configured.
- **Docs on ship:** `docs/tracing/trace-artifact-service.md`.

## Event / storage model

The existing artifact trace event is consumed as-is:

```jsonc
{ "ev": "artifact.emitted", "session_id": "...", "turn": 7, "seq": 3,
  "body": { "id": "walkthrough#3f9a1c20", "kind": "video",
            "mime": "video/mp4", "label": "Architecture walkthrough",
            "path": "/.../run-42/walkthrough.mp4",
            "producer": "host.artifacts_dir", "size_bytes": 1843221 } }
```

Logical rows:

```sql
CREATE TABLE runs (
  job_id      text PRIMARY KEY,
  session_id  text NOT NULL,
  story       text NOT NULL,
  status      text NOT NULL,
  started_at  timestamptz NOT NULL,
  ended_at    timestamptz,
  last_turn   int NOT NULL DEFAULT 0,
  trace_path  text NOT NULL
);

CREATE TABLE artifacts (
  handle      text NOT NULL,
  job_id      text NOT NULL,
  kind        text NOT NULL,
  mime        text NOT NULL,
  label       text,
  path        text NOT NULL,
  size_bytes  bigint NOT NULL,
  created_at  timestamptz NOT NULL,
  PRIMARY KEY (job_id, handle)
);
```

The concrete DDL differs only where SQLite/Postgres need dialect-specific types
or FKs. Hosted Postgres rows may FK to the artifact-job registry; local SQLite
uses the local registry table.

## Determinism

The trace JSONL is immutable. Dropping the index and rebuilding it from
on-disk traces must reproduce the same `runs` and `artifacts` rows. Replay of a
completed run uses the same `SnapshotFromTrace` path as live runstatus and
`export-status`, so a stored run renders the same state it rendered live.

The index is not a second source of truth. It answers "which runs exist" and
"which artifacts did this run emit" quickly; the trace explains what happened.

## Tasks

```
## 1. Store / index
- [ ] 1.1 RunStore + ArtifactIndex interfaces bound to artifact-job job_id
- [ ] 1.2 Local SQLite schema and migration
- [ ] 1.3 Hosted Postgres schema and migration
- [ ] 1.4 Filesystem runs-root resolver: arg -> $KITSOKI_RUNS_ROOT -> cwd/.runs
- [ ] 1.5 Deterministic reindexer from trace JSONL + co-located blobs

## 2. Serve
- [ ] 2.1 Multi-run SessionProvider: live source or completed trace-replay source by job_id
- [ ] 2.2 `/run/<job-id>` route maps to the existing runstatus session RPCs
- [ ] 2.3 ArtifactResolver resolves handle -> path from the index for stored runs
- [ ] 2.4 `runstatus.sessions.list` includes stored artifact jobs
- [ ] 2.5 `runstatus.run.artifacts` returns ordered artifact metadata for the console/gallery

## 3. Prove + document
- [ ] 3.1 Golden multi-run fixture: list + reindex row-stable
- [ ] 3.2 Served-artifact fixture: `/artifact/<id>` and `/poster` from a stored run
- [ ] 3.3 Replay equivalence: stored SnapshotFromTrace equals live snapshot
- [ ] 3.4 Document docs/tracing/trace-artifact-service.md; trim/delete this proposal
```

## Open questions

1. **Blob backend for v1.** Filesystem vs object storage. *Lean: filesystem*,
   with object storage as a later `RunStore` implementation.
2. **Redaction.** Store-time scrub vs auth-gated/private links only. *Lean:
   auth-gated/private in v1; public unauthenticated sharing needs a follow-up
   redaction gate.*
3. **Run list source.** Should `runstatus.sessions.list` merge live and stored
   runs in one result or expose separate filters? *Lean: one result with
   `live|completed|failed|interrupted` status.*

## Non-goals

- **No artifact file schema.** [`artifact-format.md`](artifact-format.md) owns
  artifact validation and round-trip.
- **No workspace lifecycle.** Slices #3 and #4 own draft/share/publish/archive.
- **No GitHub auth or comments.** The GitHub-agent epic consumes this service
  and owns OAuth/comment behavior.
- **No object storage in v1.**
