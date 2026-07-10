# Runtime: artifact job registry

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../artifact-driven-stories.md

## Why

Kitsoki has several partial notions of "job":

- `internal/jobs` persists background handler state for one session, but the
  row is a scheduler materialized view and live work is process-bound.
- operation handles expose useful progress and terminal artifact actions, but
  they are scoped to the active session surfaces.
- the GitHub agent mints external job IDs and links, but that shape is
  front-door-specific.
- dev-story workspaces hold durable files, but they do not have a durable job
  row that another session can list and reattach to.

The missing primitive is a small registry record that says: this named piece of
work exists, this is the session/run that owns it, these are its current status
and artifacts, and this is how an operator resumes or shares it.

## What changes

Add an artifact-job registry behind a narrow store interface. It records durable
job identity and attachment metadata without taking over scheduler execution.

An artifact job row carries:

| Field | Notes |
|---|---|
| `job_id` | stable user-facing ID, generated once |
| `session_id` | primary kitsoki session/run; nullable only while registering |
| `app_id` / `story` | loaded app/story identity |
| `origin_kind` / `origin_ref` / `origin_url` | dev-story, background, operation, GitHub, CLI, etc. |
| `status` | `running`, `awaiting_input`, `interrupted`, `done`, `failed`, `cancelled`, `archived` |
| `run_url` | stable local or hosted `/run/<job-id>` URL |
| `workspace_instance_id` | optional artifact workspace handle from slice #3 |
| `terminal_artifact_handle` | optional journaled artifact handle |
| `summary` / `phase` | compact status facts for TUI/web lists |
| `visibility` / `owner` | local/private/shared/auth-gated metadata |
| `created_at` / `updated_at` / `finished_at` | sorting and retention |

Local storage lives beside the session store; hosted storage uses the same
logical schema in Postgres. The registry exposes get/list/update/attach
operations to the orchestrator, dev-story, runstatus, and GitHub dispatch
without making them depend on one concrete database.

## Impact

- **Code seams:** new `internal/artifactjob/` package, local SQLite-backed store,
  hosted Postgres-backed store, and a fake store for tests; small integration
  adapters from background jobs, operation lifecycle events, and session launch.
- **Vocabulary:** `artifact_job` as a durable product record; `job_id` remains
  stable across sessions; `run_url` is minted by the trace service but stored
  here.
- **Stories affected:** none until adopted. Dev-story adoption is slice #5.
- **Backward compat:** additive. Existing `internal/jobs` rows, operation
  handles, and runstatus sources keep working when no artifact-job store is
  configured.
- **Docs on ship:** background-job runtime docs gain the distinction between
  scheduler jobs and artifact jobs; state-machine docs cover register/attach.

## Model

```
story starts long work
  -> register artifact job
  -> bind job_id into world / operation handle
  -> trace service binds run_url

operator opens another session
  -> artifactjob.list
  -> select job
  -> attach by job_id
  -> runstatus replays/streams the recorded run

process dies while active
  -> startup sweep marks process-bound work interrupted
  -> story-specific resume action may start a follow-up job
```

The registry never claims a dead goroutine is still running. Process-bound
background handlers that were running at shutdown become `interrupted`; durable
story workflows can offer explicit resume actions using their workspace and
trace state.

## Determinism

Registry writes are deterministic facts: register, bind-run, progress update,
terminal update, attach, archive. Each write also emits or is derived from a
trace lifecycle event so replay can explain why the row changed. The event log
remains authoritative for session world state; the registry is a queryable
projection plus attachment handle.

## Tasks

```
## 1. Store
- [ ] 1.1 Define `internal/artifactjob.Store` with Register, BindRun, Update, Attach, List, Get, Archive
- [ ] 1.2 Local SQLite schema/migration beside the session store
- [ ] 1.3 Hosted Postgres schema with the same logical fields
- [ ] 1.4 In-memory fake for deterministic unit and flow tests

## 2. Integrate
- [ ] 2.1 Session launch/register path for story-declared artifact jobs
- [ ] 2.2 Background-job and operation lifecycle adapters update status/phase/terminal_artifact_handle
- [ ] 2.3 Startup sweep marks process-bound active rows interrupted with a clear resume reason
- [ ] 2.4 Run URL binding hook consumed by trace-artifact-service

## 3. Prove + document
- [ ] 3.1 Unit tests for register/attach/list/update across local fake + SQLite
- [ ] 3.2 Restart test: active process-bound job becomes interrupted, terminal jobs remain terminal
- [ ] 3.3 No-LLM flow fixture: register a dev-story job, leave, list, attach by job_id
- [ ] 3.4 Document artifact jobs vs scheduler jobs in docs/stories/background-jobs/runtime.md
```

## Open questions

1. **Package name.** `internal/artifactjob` vs extending `internal/jobs`.
   *Lean: new package* so the scheduler remains about execution and this record
   remains about durable product identity.
2. **Status vocabulary.** Reuse `internal/jobs` statuses exactly vs. add
   `interrupted` / `archived`. *Lean: artifact jobs add those two because they
   describe durable records, not handler execution.*
3. **Origin dedup.** Should dev-story origin refs be stable hashes of the task
   brief, explicit operator-provided names, or always fresh IDs? *Lean: explicit
   story-declared key when present; fresh ID otherwise.*

## Non-goals

- **No scheduler rewrite.** `internal/jobs.Scheduler` still runs handlers and
  handles clarification.
- **No cross-process adoption of live goroutines.** Dead processes cannot be
  resumed invisibly.
- **No artifact serving.** Slice #2 owns trace replay and by-handle HTTP.
- **No workspace lifecycle.** Slices #3 and #4 own draft/share/publish/archive.
