# Runtime: Job dispatch & orchestration

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   kitsoki-github-agent.md

## Why

Slice #1 (`gh-event-ingress.md`) delivers an authenticated, deduped `@kitsoki`
mention — an issue or PR object carrying labels, author, and a comment thread —
plus a substrate for posting back to that thread. What's missing is the bridge
from *that mention* to *a running kitsoki session*: deciding which story the
mention should run, spawning it in isolation, minting a linkable run, and
acking back.

Nothing in the engine does this today. A run is always operator-initiated from
a terminal (`kitsoki run <app.yaml>`, `cmd/kitsoki/main.go:207`), and story
selection is a human typing the path. The label→pipeline mapping kitsoki *needs*
already exists in spirit — `ghClassifyType` (`internal/host/github.go:373`)
collapses an issue's labels to `bug` / `feature` / `epic` — but it's buried in
the ticket host and feeds only dev-story's in-story `drive` arc, not a
dispatcher. There is no job identity, no worktree-per-job isolation, and no
"ambiguous → ask for guidance" exit. This slice adds the dispatcher.

## What changes

A **dispatcher** turns a mention into a **job**, and a job into a **run**: it
classifies the mention against a configured **label→story map**, claims (or
inserts) a row in a **Postgres `jobs` table** keyed on the mention's origin ref,
creates an isolated worktree + per-job `KITSOKI_APP_DIR`, spawns the selected
story's session seeded with world keys derived from the mention, and acks back
to the thread with the run URL. Job **state and the concurrency lock live in
Postgres** ("State in PostgreSQL, artifacts on the filesystem" — epic round-1
decision); a `SELECT … FOR UPDATE SKIP LOCKED` claim ensures exactly one worker
owns a job, so re-mentioning an active job ATTACHES to its existing row rather
than forking a second run (idempotency keyed on issue/PR + comment thread, epic
shared decision #3). Trace + artifact blobs stay on the filesystem (slice #4);
Postgres holds only the index/state pointing at the on-disk run. A mention it
can't classify confidently posts a guidance comment and parks.

One sentence: *every accepted mention resolves through one interpretive
label-routing decision to exactly one deterministically-spawned, isolated run,
serialized by a Postgres row lock.*

## Impact

- **Code seams:** a new `internal/ghagent/dispatch/` package consuming
  `internal/inbox/github.go` (the mention) and the slice-#1 comment substrate;
  a `jobs` table + claim/attach query in a new `internal/ghagent/jobstore/`
  (Postgres, `database/sql` + `pgx`, no ORM); spawn reuses `loadAppWithEnv` +
  the session launcher path that backs `kitsoki run` (`cmd/kitsoki/main.go:282`);
  run registration calls into `internal/runstatus/server/` (slice #4 owns the URL).
- **Vocabulary:** a `label_story_map` config key, a `KITSOKI_PG_DSN` config key,
  the Postgres `jobs` table, job-id world keys (`gh_job_id`, `gh_origin_ref`,
  `gh_run_url`), and a `host.gh.dispatch` host call / `kitsoki gh-agent dispatch`
  command — table below.
- **Stories affected:** none change shape. `stories/bugfix/`,
  `stories/dev-story/` + `stories/prd/`, and the slice-#3 PR-autopilot story
  are *targets* the dispatcher selects and seeds; they run unchanged.
- **Backward compat:** purely additive. No existing story, cassette, or the
  `kitsoki run` path changes. The dispatcher is a new entry point.
- **Docs on ship:** `docs/architecture/github-agent.md` (dispatch section).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| config key | `label_story_map` | `map[label]→{story, world}` | configured (see below); read at dispatch time |
| config key | `KITSOKI_PG_DSN` | `string` (env/config) | Postgres DSN backing the `jobs` table; validated at startup |
| db table | `jobs` | (see DDL below) | holds job state + the claim lock; row-per-job keyed on `origin_ref` |
| world key | `gh_job_id` | `string` | the `jobs.job_id` for this run; seeded into the spawned session's world |
| world key | `gh_origin_ref` | `string` | the slice-#1 dedupe ref (`GitHubOriginRef`, `internal/inbox/github.go:48`); the `jobs` row's natural key |
| world key | `gh_run_url` | `string` | consumed from slice #4 (`…/run/<job-id>`); stored on the row, echoed in the ack |
| host call | `host.gh.dispatch` | `{mention} → {job_id, story, run_url}` | classify → claim/insert → spawn → register; idempotent on `gh_origin_ref` |
| command | `kitsoki gh-agent dispatch` | `--mention @m.json` | CLI/test entry that runs one dispatch turn against fixtures + a local Postgres |

Minimal `jobs` table — one table, no ORM, round 1:

```sql
CREATE TABLE jobs (
  job_id       TEXT PRIMARY KEY,            -- hash(origin_ref)
  origin_ref   TEXT NOT NULL UNIQUE,        -- GitHubOriginRef: github:<repo>/<kind>/<number>
  repo         TEXT NOT NULL,
  object_kind  TEXT NOT NULL,               -- 'issue' | 'pr'
  object_number TEXT NOT NULL,
  story        TEXT,                         -- chosen story path (NULL while guidance-parked)
  state        TEXT NOT NULL,               -- queued|claimed|running|awaiting_guidance|done|failed
  worker_id    TEXT,                         -- holder of the claim (NULL when unclaimed)
  run_id       TEXT,                         -- slice-#4 run handle (on-disk run)
  run_url      TEXT,                         -- …/run/<job_id>
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

The claim is the concurrency primitive (epic shared decision #3): a dispatcher
worker `INSERT … ON CONFLICT (origin_ref) DO NOTHING` to mint, then
`SELECT … FROM jobs WHERE origin_ref = $1 FOR UPDATE SKIP LOCKED` to claim. A
row already locked/claimed by a live worker means the mention is a re-mention →
the dispatcher ATTACHES (returns the existing `run_url`) instead of forking.

The **label→story map** is config, not code (epic: "configured, not
hard-coded"). It lives as a config key on the gh-agent instance, defaulting to:

```yaml
label_story_map:
  bug:         { story: "stories/bugfix",    world: { judge_mode: llm_then_human } }
  feature:     { story: "stories/dev-story",  world: { ticket_type: feature } }
  enhancement: { story: "stories/dev-story",  world: { ticket_type: feature } }
  pull_request:{ story: "stories/pr-autopilot" }   # slice #3; PR objects route here regardless of label
```

`stories/dev-story` is the feature pipeline entry: it imports the PRD→design→
delivery chain (`stories/dev-story/app.yaml`, which composes `stories/prd/`).
Label classification reuses the existing normalization in `ghClassifyType`
(`internal/host/github.go:373`) — `bug`/`kind:bug`, `feature`/`enhancement`/
`kind:feature` — but the *target* is now config-driven rather than the
hard-wired in-story `drive` arc.

## The model

```
@kitsoki mention (slice #1: issue|pr + labels + author + thread)
        │
        ▼
  ┌─────────────┐   is PR? ───yes──▶ label_story_map["pull_request"]  ┐
  │  CLASSIFY   │                                                     │  INTERPRETIVE
  │ (gate)      │   issue: labels ∩ map keys                          │  (recorded
  └─────────────┘        ├─ exactly one match ─▶ that story  ─────────┤   datapoint)
        │                ├─ zero matches ──────▶ guidance ─▶ PARK     │
        │                └─ conflicting ───────▶ guidance ─▶ PARK     ┘
        ▼ (story chosen)
  ┌─────────────┐  INSERT … ON CONFLICT(origin_ref) DO NOTHING;
  │ CLAIM JOB   │  SELECT … FOR UPDATE SKIP LOCKED   ── row locked/claimed? ──yes──▶ ATTACH (return run_url)
  │ (Postgres)  │  job_id = hash(gh_origin_ref)                                       (no fork; epic #3)
  └─────────────┘
        ▼ (claim won — new job)
  ┌─────────────┐   .worktrees/<job_id>/   +   KITSOKI_APP_DIR=<per-job>     ┐
  │  SPAWN      │   loadAppWithEnv(story) → session, world seeded:           │  DETERMINISTIC
  │ (engine)    │   {gh_job_id, gh_origin_ref, ticket_id, ...map.world}      │  (replayable)
  └─────────────┘                                                            ┘
        ▼
  REGISTER run (slice #4, on-disk) → gh_run_url = …/run/<job_id>; persist run_id/run_url/state on the jobs row
        ▼
  ACK back to thread (slice #1 substrate): "running <story>, watch at <run_url>"
```

The **classify** step is the one interpretive decision (which story, and why) —
it is recorded (below). The label→map lookup itself is deterministic table
resolution; the *interpretation* is "do these labels confidently name exactly
one pipeline." Everything downstream of a chosen story — claim, worktree,
spawn, register, ack — is deterministic and replayable from the recorded
decision + the mention fixture + the `jobs` row.

The dispatcher owns **both the Postgres claim lock and the worktree
lifecycle**. State isolation is the `jobs` row (lock + run pointer); execution
isolation is unchanged: create `.worktrees/<job_id>/`
(AGENTS.md convention) on dispatch, assign a per-job `KITSOKI_APP_DIR` so two
concurrent jobs never share renderer/app state (the cross-contamination class
fixed by the per-session renderer; cf. the `parallel-live-drivers-schema-bleed`
memory note — global `KITSOKI_APP_DIR` is the bleed vector, so each job gets its
own), and clean the worktree on job end.

## Decision recording

The classify decision lands in the trace as a labeled datapoint so a reviewer
can reconstruct *why* a given story ran. The dispatch turn emits a
`gh.dispatch` decision event carrying:

- `gh_origin_ref` and the derived `gh_job_id`
- `object_kind` (`issue` | `pr`) and the observed `labels`
- `outcome` (`dispatched` | `attached` | `guidance`) — `attached` when the
  Postgres claim found an existing live job for the origin ref
- `story` chosen (or empty, for guidance) and the matched map key
- `reason` (e.g. `single-label-match:bug`, `pr-object`,
  `ambiguous:no-type-label`, `ambiguous:conflicting[bug,feature]`)

This mirrors how every interpretive arc records a verdict (the moat). A new
event type is a `tracing.md` concern — coordinate the schema with slice #4's
run index. The trace event is the per-run datapoint; the `jobs` table is the
durable cross-run index, and the dispatch event's `gh_job_id` is the foreign key
joining them (the run-list view reads `jobs`, not the trace ring).

## Engine seams & invariants

The dispatcher hooks in *before* the session loop: it is a producer that calls
the existing spawn path (`loadAppWithEnv` → session new,
`cmd/kitsoki/main.go:282`), not a new state in any story.

Load-time invariants on `label_story_map` (fail-fast at gh-agent startup, not at
the first mention):

1. **Every mapped story exists and loads.** Each `story:` value is resolved with
   the same loader `kitsoki run` uses; a missing path or a load error is a
   startup failure with the offending key named — not a runtime bounce when a
   `bug` issue finally arrives.
2. **Every seeded world key is accepted by its target.** The `world:` block for
   a mapping is validated against the target story's declared world schema, so a
   typo like `judge_made` fails at load, not silently as an ignored key.
3. **`pull_request` is mapped.** A PR object must resolve to a story (slice #3);
   an unmapped PR key is a startup failure, since PRs can't fall through to the
   guidance path the way an unlabeled issue does.
4. **Postgres is reachable and migrated.** `KITSOKI_PG_DSN` is required at
   startup; the dispatcher opens the pool and applies the `jobs` migration
   (idempotent `CREATE TABLE IF NOT EXISTS` / versioned migration) before
   accepting any mention — an unreachable or unmigrated DB fails startup, not
   the first dispatch.

These mirror the existing story-load invariants and reuse the same loader so
there's one validation path, not two. The claim/attach query is the only
concurrency primitive — there is no in-process job map to keep in sync with the
DB; the `jobs` row is the single source of truth (least surprise).

## Backward compatibility / migration

Fully additive. `kitsoki run`, every story, and every existing cassette are
untouched — the dispatcher is a new front door, default-off until the gh-agent
service (composed across slices #1/#2/#4) is started. No story migrates onto
anything; `ghClassifyType`'s normalization is reused as-is. The Postgres
dependency is new but scoped to the gh-agent service — local `kitsoki run`,
flows, and cassette tests need no database (the jobstore is dependency-injected;
tests pass a throwaway/local Postgres or an in-memory fake implementing the same
interface). The one new piece of state is the `jobs` table, created by the
service's own migration on first start.

## Tasks

```
## 1. Persistence
- [ ] 1.1 internal/ghagent/jobstore/: the `jobs` table DDL + versioned migration (KITSOKI_PG_DSN), applied idempotently on start
- [ ] 1.2 JobStore interface (DI seam): Claim(origin_ref)→(job, won bool), Attach, UpdateState/run — backed by INSERT…ON CONFLICT + SELECT…FOR UPDATE SKIP LOCKED; plus an in-memory fake for tests

## 2. Engine
- [ ] 2.1 internal/ghagent/dispatch/: mention → classify → claim/attach → spawn → register → ack
- [ ] 2.2 label_story_map config (default map) + load-time validation: every mapped story loads, world keys accepted, pull_request present
- [ ] 2.3 job-id = hash(gh_origin_ref); claim wins → spawn, claim attaches → return existing run_url (idempotency)
- [ ] 2.4 Worktree-per-job + per-job KITSOKI_APP_DIR lifecycle (create on dispatch, clean on job end)
- [ ] 2.5 gh.dispatch decision event wired into the trace; run_id/run_url/state persisted on the jobs row (coordinate schema with slice #4)
- [ ] 2.6 Ambiguous (zero/conflicting label) → guidance comment via slice-#1 substrate → row state awaiting_guidance → park
- [ ] 2.7 host.gh.dispatch + `kitsoki gh-agent dispatch` entry over the cliExec/substrate seams + injected JobStore

## 3. Verification
- [ ] 3.1 Stateless unit: classify table — bug→bugfix, feature/enhancement→dev-story, pr→autopilot, none→guidance, conflicting→guidance
- [ ] 3.2 Flow fixtures (no LLM, no GitHub): bug-label→bugfix, feature-label→design, PR→autopilot, ambiguous→guidance
- [ ] 3.3 Idempotency: re-mention of an active origin_ref attaches (one row, one run, not two) — against a throwaway/local Postgres
- [ ] 3.4 Concurrency: two parallel dispatches of the same origin_ref → exactly one claim wins via FOR UPDATE SKIP LOCKED; distinct origin_refs → two worktrees + two KITSOKI_APP_DIRs, no world bleed
- [ ] 3.5 Load-time invariants: missing story OR unreachable/unmigrated DSN fails startup with a clear message

## 4. Adopt + document
- [ ] 4.1 Compose the dispatcher with slice #1 ingress + slice #4 run registration end to end (gh cassettes, local Postgres)
- [ ] 4.2 Write docs/architecture/github-agent.md dispatch + jobs-table section; trim/delete this proposal
```

## Verification

A reviewer confirms dispatch without an LLM or GitHub. The classify decision is
a pure function of the mention fixture, so the core is a stateless table test
(3.1). The full path is exercised by flow fixtures with the mention as input
and the substrate/spawn calls recorded as exec/host cassettes (3.2–3.3): assert
the chosen story, the seeded world keys, the minted job-id, the run URL echoed
in the ack, and — for the ambiguous case — that a guidance comment was posted
and no session spawned. The jobstore is exercised against a **throwaway/local
Postgres** (a disposable container/temp instance spun up by the test, torn down
after — no shared DB, no network GitHub): idempotency (3.3) asserts one row +
one run on re-mention; concurrency (3.4) asserts the `FOR UPDATE SKIP LOCKED`
claim lets exactly one of two racing dispatches win, and distinct origin_refs
land in distinct `.worktrees/<job_id>/` dirs with distinct `KITSOKI_APP_DIR`s
seeing no shared world. Pure-logic tests use the in-memory JobStore fake. No
test needs a real LLM or real GitHub (epic shared decision #6); none is added.

## Open questions

1. **Job-id derivation** — hash of `gh_origin_ref` (stable, idempotent,
   thread-keyed) vs. a fresh UUID per dispatch. *Lean: hash the origin_ref* so
   `job_id` is a pure derivation and the `INSERT … ON CONFLICT (origin_ref)`
   handles idempotency intrinsically — no side table.
4. **Claim primitive** — `FOR UPDATE SKIP LOCKED` on a held transaction (lock
   lives for the worker's session) vs. `pg_advisory_lock(hash(job_id))` (lock
   keyed independently of any row). *Lean: `FOR UPDATE SKIP LOCKED`* — it
   co-locates the lock with the row state in one query and one source of truth;
   advisory locks add a second keyspace to reason about.
5. **Terminal-job re-mention** — a re-mention whose `jobs` row is `done`/`failed`
   should re-run vs. attach-and-report. *Lean: re-run a fresh job* (the prior
   thread is resolved); but gate behind a follow-up `@kitsoki` so a stale webhook
   replay doesn't refork. Revisit with slice #1's dedup window.
2. **Map override granularity** — global default map vs. per-repo override (a
   repo that wants `chore`-labeled issues to run a different story). *Lean:
   global default, per-repo override merged on top*, since the map is already a
   world key and instance world composition gives this for free.
3. **Worktree reuse on attach** — a re-mention attaches to the run, but does it
   reuse the existing worktree or is the worktree purely a spawn-time artifact?
   *Lean: reuse* — the worktree is owned by the job, not the dispatch event, and
   lives for the job's lifetime.

## Non-goals

- **PR-autopilot story behavior** (CI-watch, rebase, comment-driven implement,
  resolve-parent-comment). The dispatcher only *selects and spawns* it — slice
  #3 (`pr-autopilot-story.md`) owns what it does.
- **The run URL scheme and the serving/persistence of traces + artifacts.**
  Slice #4 (`trace-artifact-service.md`) owns `…/run/<job-id>` and the on-disk
  trace/artifact blobs; this slice owns only the `jobs` state table that points
  at them, and is the producer of the `<job-id>` the URL embeds.
- **Postgres provisioning / deployment.** Where the DB runs and how it's hosted
  is operational; this slice only owns the `jobs` schema + the claim query and
  takes a DSN. Durable run-blob storage is slice #4's cross-cutting question #2.
- **The web viewer + operator-drive surface.** Slice #5
  (`gh-web-operator-viewer.md`).
- **Webhook vs poll ingress, App auth, and comment formatting.** Slice #1
  (`gh-event-ingress.md`) owns the mention substrate this consumes.
