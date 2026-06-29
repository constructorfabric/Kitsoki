# Tracing: Trace + artifact service

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   kitsoki-github-agent.md

## Why

A GitHub mention mints one job whose progress a requester must *watch and
steer* without leaving the thread (epic shared decision #3). Today there is no
durable, linkable surface to point them at. Traces are an in-memory ring buffer
plus a per-run JSONL file (`internal/journal/`), served by `kitsoki web` /
`kitsoki status serve` for the *one* session the process holds
(`internal/runstatus/server/server.go:55` — "v1 served a single session,
`sessions.list` returns 0–1 entries"), or frozen into a one-off HTML artifact
(`kitsoki export-status`, `runstatus-proposal.md`). When the process exits the
run is gone; nothing answers "which runs exist" or "show me run #42 from an
hour ago."

The web tier already knows how to serve *one* run's trace (SSE + the
`runstatus.session.*` RPCs) and *one* run's artifacts by handle
(`GET /artifact/{id}` via `JournalArtifactResolver`,
`internal/runstatus/server/provider.go:190`). What's missing is the layer
underneath: a **persistent store of many completed + live runs** and a
**queryable index** over them, keyed by the job-id slice #2 mints, so the
GitHub agent can link `…/run/<job-id>` and the slice-#5 viewer can list runs
and browse a run's artifacts.

## What changes

This slice is **mostly a new consumer**, not a new recorder. It records no new
trace event: the producer side — the JSONL trace (`internal/journal/`) and the
`artifact.emitted` event + by-handle resolution
(`internal/journal/types.go:213`, `provider.go:198`) — already exists and ships
in the media-artifact-substrate work. This slice adds:

1. A **persistence split** (new epic shared decision): the **queryable index**
   — which runs exist, run status, the per-run artifact list/metadata, and the
   job→run mapping — lives in **PostgreSQL tables**; the **trace JSONL and
   artifact/media blobs stay on the filesystem** under a configurable blob root
   (still mirroring `artifacts_dir` root resolution: arg → `$KITSOKI_RUNS_ROOT`
   → `cwd/.runs`). Postgres rows **point at** on-disk paths. There is no on-disk
   index file as source of truth; an exported `run.json` is at most a derived
   convenience. Object storage for blobs is future work — round 1 is one schema,
   no storage abstraction.
2. A **stable per-run URL** `…/run/<job-id>` served by a long-lived
   multi-run server. Live runs stream over SSE exactly as runstatus does now;
   completed runs replay from the on-disk JSONL the Postgres row points at.
3. **Artifacts served by handle** for *any* stored run (not just the live
   in-process one): the handle→path lookup goes through Postgres, then the
   blob streams from the filesystem, with the existing `.semantic.json` /
   `.poster.png` / `.chapters.json` siblings co-located per the `artifacts_dir`
   layout (`artifacts_dir_transport.go:309-337`).
4. A **run/artifact index in Postgres** — queryable for listing which runs
   exist, each run's status, and which artifacts a run produced. This is the
   backend the slice-#5 web viewer reads.

## Impact

- **Producers:** unchanged. `internal/journal/` (JSONL + ring buffer);
  `host.artifacts_dir` media-emit (`artifacts_dir_transport.go:249`); the
  `artifact.emitted` journal event (`internal/journal/types.go:213`).
- **Consumers:** this service's Postgres index + blob store; the multi-run
  server extending `runstatus.sessions.list` / `runstatus.session.*`
  (`server.go:691,790`) over *stored* runs; the by-handle HTTP serving
  (`server.go:508` `handleArtifact`); the slice-#5 SPA.
- **Format:** no new trace event. Two new Postgres tables (`runs`, `artifacts`)
  indexing existing on-disk data; both are a derived projection of the trace.
- **Backward compat:** fully additive. Old JSONL traces, old exported HTML,
  and local `kitsoki web` / `kitsoki status serve` stay FS-only and untouched
  (no Postgres dependency for the local flow). Postgres is additive, only for
  the hosted multi-run service. Ingest of an existing JSONL is unchanged.
- **Docs on ship:** `docs/tracing/trace-artifact-service.md`.

## Event / format model

No new **trace** event. The artifact datapoint this slice serves already
exists (`internal/journal/types.go:215`, `ArtifactEvent`):

```jsonc
// one line in the run's existing trace.jsonl — CONSUMED, not added here
{ "ev": "artifact.emitted", "ts": "2026-06-24T18:03:11Z", "session_id": "…",
  "turn": 7, "seq": 3,
  "body": { "id": "walkthrough#3f9a1c20", "kind": "video", "mime": "video/mp4",
            "label": "Architecture walkthrough", "path": "/…/run-42/walkthrough.mp4",
            "producer": "host.artifacts_dir", "size_bytes": 1843221,
            "created_at": "2026-06-24T18:03:11Z" } }
```

The new records are **two Postgres tables** — the index. Both are a derived
projection of the on-disk trace (status from the last terminal state; the
artifact rows folded from every `artifact.emitted` body), never a second source
of truth. Rows **point at** filesystem paths; blobs never enter the database.
The `jobs` table (and `job_id`) is owned by **slice #2** — these tables FK to
it, they do not duplicate it:

```sql
-- the run/artifact index this slice ADDS (one schema, round 1)
CREATE TABLE runs (
  job_id      text PRIMARY KEY REFERENCES jobs(id),  -- FK to slice-#2's table
  session_id  text NOT NULL,
  story       text NOT NULL,
  status      text NOT NULL,                          -- live | completed | failed
  started_at  timestamptz NOT NULL,
  ended_at    timestamptz,
  last_turn   int NOT NULL DEFAULT 0,
  trace_path  text NOT NULL                            -- abs path to trace.jsonl on the blob FS
);
CREATE TABLE artifacts (
  handle      text NOT NULL,                           -- ArtifactEvent.id
  job_id      text NOT NULL REFERENCES runs(job_id),
  kind        text NOT NULL,                           -- video | image | pdf | html | slideshow
  mime        text NOT NULL,
  label       text,
  path        text NOT NULL,                           -- abs path to the blob on the FS
  size_bytes  bigint NOT NULL,
  created_at  timestamptz NOT NULL,
  PRIMARY KEY (job_id, handle)
);
```

| Record | When written | Key fields |
|---|---|---|
| `runs` row (**new**, Postgres) | run start; updated on terminal transition (status, `ended_at`, `last_turn`) | `job_id`→jobs FK, `status`, `trace_path` |
| `artifacts` row (**new**, Postgres) | on each `artifact.emitted` | `handle`, `job_id`, `kind`, `mime`, `path` |
| `artifact.emitted` (trace, **consumed**) | by `host.artifacts_dir` media-emit (producer) | `id`, `kind`, `mime`, `path` — folded into an `artifacts` row |

## Determinism

The invariant the whole epic leans on: **a stored run replays to a
byte-identical trace.** The store treats `trace.jsonl` as immutable — it is
the run's verbatim journal, copied/streamed in, never rewritten — so replaying
a completed run through `SnapshotFromTrace` (the same path the live SPA and
`export-status` share, `runstatus-proposal.md`) yields the identical snapshot
it would have live. Postgres only **indexes** the JSONL — it never rewrites or
re-serves trace bytes. The index is **derived**: dropping every `runs`/
`artifacts` row and reindexing from the on-disk `trace.jsonl` files must
reproduce the same rows (status from the last terminal transition, artifacts in
trace order). The reindexer is the determinism test.

Artifact ids and paths are already deterministic upstream: the handle id is a
SHA-256 prefix of the destination path (`artifacts_dir_transport.go:421`), and
destinations are stem-based under the root. The store preserves the run-dir
layout so a handle resolves to the same file on every replay — the `artifacts`
row's `path` (folded from the trace `path` the `JournalArtifactResolver`,
`provider.go:198`, would have found) points at the same on-disk blob. No
timestamp or absolute-path nondeterminism enters the served bytes.

## Producers & consumers

**Producers (unchanged, all upstream of this slice):**

- `internal/journal/` writes the JSONL trace + ring buffer.
- `host.artifacts_dir` media-emit copies the artifact under the artifacts root
  and co-locates `.semantic.json` / `.poster.png` / `.chapters.json` siblings
  (`artifacts_dir_transport.go:249-337`).
- The `artifact.emitted` event records each produced artifact
  (`internal/journal/types.go:213`).

**Consumers (this slice):**

- **Blob store + index** — a `RunStore` interface (`Put(jobID, traceReader)`
  lands the JSONL + blobs on the FS and writes the `runs`/`artifacts` rows;
  `Get(jobID)`, `List()` query Postgres). The blob root mirrors the
  `artifacts_dir` root resolver (`artifacts_dir_transport.go:224`): arg →
  `$KITSOKI_RUNS_ROOT` → `cwd/.runs`. Postgres rows point at paths under it.
- **Multi-run server** — a long-lived server backing a `SessionProvider`
  (`provider.go:29`) whose `Get`/`List` resolve **stored** runs by `job_id`
  from the `runs` table, not just live in-process ones. Live runs route to the
  live `Source` (SSE as today); completed runs route to a trace-replay `Source`
  reading the on-disk JSONL the row points at. The `…/run/<job-id>` route maps
  to a `session_id` for the existing `runstatus.session.*` RPCs.
- **By-handle HTTP serving** — `handleArtifact` (`server.go:508`) resolves
  handle→path via a Postgres-backed `ArtifactResolver` (the `artifacts` row's
  `path`, replacing the per-run journal scan of `provider.go:190` for stored
  runs), then streams the blob from the FS; the `/poster` +
  `runstatus.artifact.semantic` sibling routes (`server.go:997`) read the
  co-located siblings.
- **Index queries** — `runstatus.sessions.list` (`server.go:691`) selects from
  `runs`, and a thin `runstatus.run.artifacts` selects a run's `artifacts`
  rows. The **slice-#5 SPA** reads both.

## Backward compatibility

Additive and parallel — it does not touch the local single-run path, which
stays **FS-only with no Postgres dependency**. Existing JSONL traces ingest
unchanged (the store copies bytes, derives the index rows). Old `export-status`
HTML artifacts still open standalone. `kitsoki web` / `kitsoki status serve`
keep serving the one in-process session exactly as before; the multi-run
server is a *new*, Postgres-backed surface reusing the same SPA and the same
RPC contract (epic non-goal: not replacing the local flow). A run on disk that
is not yet in the index (e.g. a hand-dropped JSONL) is indexed lazily by the
reindexer, so pre-existing traces are listable without a data migration.

## Fixtures / golden traces

The regression contract is replay stability across the store boundary.

Tests run against a **throwaway local Postgres** (a temp database spun up per
suite — e.g. an ephemeral container or `pg_tmp`, migrations applied at start,
torn down after); no shared/real instance, no LLM, no real GitHub.

1. **Golden multi-run store** — a checked-in `<runs-root>/` blob fixture with 3
   run dirs (one `live`-shaped via a partial trace, one `completed`, one
   `failed`), each with `trace.jsonl` + media. A test ingests them, asserts
   `List()` returns the three `runs` rows, and asserts reindexing from the
   on-disk traces reproduces **identical** `runs`/`artifacts` rows.
2. **Served-artifact fixture** — one run whose trace records an
   `artifact.emitted` for a tiny checked-in PNG (+ a `.poster.png` sibling). A
   server test fetches `/artifact/<id>` and `/artifact/<id>/poster` against the
   *stored* run (handle→path via Postgres, blob from FS) and asserts the bytes
   + MIME — mirroring `artifact_integration_test.go` but sourced from the store.
3. **Replay equivalence** — `SnapshotFromTrace` over a stored `trace.jsonl`
   equals the snapshot the live session produced for the same run.

Fixtures are recorded traces + tiny media files (epic shared decision #6,
CLAUDE.md).

## Tasks

```
## 1. Consume / persist
- [ ] 1.1 Postgres schema + migrations for runs + artifacts (FK job_id → slice-#2 jobs table)
- [ ] 1.2 RunStore: land trace.jsonl + blobs on FS (root resolver arg → $KITSOKI_RUNS_ROOT → cwd/.runs); write runs/artifacts rows pointing at paths
- [ ] 1.3 Index lifecycle: runs row at start, update on terminal transition; artifacts row on each artifact.emitted
- [ ] 1.4 Deterministic reindexer: rebuild runs/artifacts rows from on-disk traces (identical rows)
- [ ] 1.5 Multi-run SessionProvider: Get/List from runs table (live → SSE source, completed → trace-replay source)
- [ ] 1.6 …/run/<job-id> route → session_id for the existing runstatus.session.* RPCs
- [ ] 1.7 Postgres-backed ArtifactResolver behind handleArtifact + /poster + artifact.semantic (handle→path via SQL, stream blob from FS)
- [ ] 1.8 runstatus.sessions.list from runs; runstatus.run.artifacts from artifacts

## 2. Prove
- [ ] 2.1 Golden multi-run fixture against throwaway Postgres; List + reindex row-stable
- [ ] 2.2 Served-artifact fixture: /artifact/<id> (+ /poster) from a STORED run
- [ ] 2.3 Replay equivalence: SnapshotFromTrace(stored) == live snapshot

## 3. Document
- [ ] 3.1 Migrate to docs/tracing/trace-artifact-service.md; trim/delete this proposal
```

## Open questions

1. **Blob backend for v1.** Filesystem under the runs root vs. object storage
   from the start. *Lean: filesystem (round 1, one schema, no abstraction);
   object storage is a later `RunStore` impl + a `path`-vs-URL column choice.*
2. **Retention / GC.** Completed runs accumulate forever — now a Postgres
   concern (a sweep deletes `runs`/`artifacts` rows *and* unlinks the on-disk
   blobs/trace they point at; the two must stay consistent). *Lean: none in v1
   (manual prune); add a max-age/max-runs sweep — transactional row delete then
   blob unlink — once the demo needs it.*
3. **Redaction.** Traces can carry secrets in host I/O; `runstatus-proposal.md`
   flagged this unimplemented and still is. Ingesting into a *publicly-linkable*
   store raises the stakes. *Lean: v1 stores only from sanitized runs and
   documents the gap; a store-time redaction pass is a follow-up, not this
   slice.*

## Non-goals

- **Requiring the media-artifact-substrate draft.** That work (the
  `artifact.emitted` event + `JournalArtifactResolver`) is already present in
  the tree (`internal/journal/types.go:213`, `provider.go:190`,
  `artifacts_dir_transport.go:249`), so v1 serves by handle directly. A run
  whose trace has *no* `artifact.emitted` events simply has zero `artifacts`
  rows; the trace store still works. **v1 does not require the draft
  and does not fall back to raw `.artifacts/` path serving** — it serves the
  handles the trace already records.
- **Artifact schema validation** — that's `artifact-format.md`.
- **Publish / share / instance lifecycle** — `artifact-publish-lifecycle.md`
  and `artifact-instances.md` (draft; future, out of scope here).
- **The viewer UI** — slice #5 (`gh-web-operator-viewer.md`).
- **The GitHub auth gate on the URL** — OAuth gating of `…/run/<job-id>` is
  slice #1 / #5 (epic shared decision #1); this slice serves the bytes, it does
  not authorize the viewer.
```
