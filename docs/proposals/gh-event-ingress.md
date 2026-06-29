# Runtime: GitHub ingress & comment substrate

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [kitsoki-github-agent.md](kitsoki-github-agent.md)

## Why

The epic's "front door" — mention `@kitsoki` in an issue or PR, get a run — has
no inbound trigger and no path for kitsoki to speak back on the thread. Today
GitHub I/O is split across read-only fragments that no long-running service
composes:

- `internal/host/github_inbox.go:32` (`ListGitHubInboxItems`) can *poll* for
  assigned issues / review-requested PRs, and `internal/inbox/github.go:15`
  (`NewGitHubNotification`) maps an item to a `jobs.Notification` with a stable
  dedupe key (`GitHubOriginRef`, `internal/inbox/github.go:48`). Nothing runs
  this on a loop, watches for an `@kitsoki` *mention*, or authenticates as
  anything but the operator's local `gh auth`.
- `host.gh.ticket` (`internal/host/github.go:48`) can post a comment
  (`ghTicketComment`, `:191`) but has no notion of *one rolling status comment*,
  a machine-readable identity block, or marking a parent comment resolved.

Every other GitHub-facing slice (#2 dispatch, #3 PR autopilot, #5 operator
viewer) needs this substrate first. This slice builds it once: a service that
turns `@kitsoki` mentions into jobs, authenticates as a GitHub App
installation, and owns the single-voice comment lifecycle back to the thread.

## What changes

A new long-running service, `kitsoki gh-agent serve`, sits in front of the
epic's **Postgres-backed job queue** (epic shared decision: job state in
Postgres, artifacts on filesystem; the job table + Postgres locking
(`SELECT … FOR UPDATE SKIP LOCKED` / `pg_advisory_lock`) is owned by slice #2,
[gh-job-dispatch.md](gh-job-dispatch.md) — this slice only *enqueues* into it).
In round 1 the sole producer **polls** via `internal/inbox/github.go`; a
GitHub-App webhook is a **round-2** drop-in alternate behind the same `Producer`
seam. Either way the service authenticates against the GitHub App installation
(auth is GitHub-native end to end) and hands downstream stories a
pre-authenticated `gh` environment (`GH_TOKEN` = installation token). A
**comment substrate** posts ack / rolling-status / done / guidance comments back
in one recognizable identity with a fenced `kitsoki` metadata block, editing a
single rolling status comment in place rather than flooding the thread.

One sentence: *every `@kitsoki` mention becomes exactly one queued job under an
installation token, and kitsoki answers on the thread in one rolling voice —
including a first-class "if in doubt, ask" comment when confidence is low.*

This slice owns the **I/O substrate only**. Mapping a job to a story/session
(label → bugfix/feature, PR → autopilot) is slice #2; this slice stops at the
queue boundary.

## Impact

- **Code seams:**
  - New service `internal/ghagent/` (producer + auth; the Postgres job table
    itself is slice #2) + `kitsoki gh-agent serve` command in `cmd/kitsoki`. The
    round-2 webhook listener will mirror the runstatus HTTP pattern
    (`internal/runstatus/server/server.go:501` `Handler()`, `:322` `New`).
  - Round-1 poll producer reuses the fragments at
    `internal/host/github_inbox.go:32` and `internal/inbox/github.go:15`/`:48`.
  - Comment substrate extends `host.gh.ticket` comment ops
    (`internal/host/github.go:191`) through the existing `cliExec` seam
    (`internal/host/cli_exec.go:28`).
- **Vocabulary:** new `host.gh.comment` status verbs + a poll producer +
  installation-token auth + the `gh-agent serve` command surface — table below.
- **Stories affected:** none change behavior. Stories keep calling
  `host.gh.ticket` / `host.git`; they only gain a pre-authenticated `GH_TOKEN`
  in env. The rolling-status substrate is opt-in via a new `host.gh.comment`
  verb.
- **Backward compat:** poll producer and all `gh` ops degrade cleanly when the
  App is unconfigured (fall back to local `gh auth`, the existing clean
  `Result.Error` path, `internal/host/github.go:54`). Default off — no service
  runs unless `gh-agent serve` is launched.
- **Docs on ship:** `docs/architecture/github-agent.md` (the service + auth +
  comment substrate); a backlink from `docs/architecture/hosts.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| command | `kitsoki gh-agent serve` | `--repo --poll-interval` (round 1: poll only; `--producer=webhook --addr` is round 2) | long-running; mirrors `kitsoki web` |
| host call | `host.gh.comment` | `{op: status\|ack\|done\|guidance, job_id, repo, issue, body, [confidence]} → {comment_id, kind}` | edits the one rolling status comment in place; `guidance` parks the job |
| host call | `host.gh.comment` | `{op: resolve_parent, repo, issue, parent_ids[]} → {resolved[]}` | marks the parent comment(s) resolved |
| world key | `gh.job_id` | `string` | the one job/run id a mention mints (idempotency key; scheme owned by #2) |
| world key | `gh.installation_token` | `string` (redacted in trace) | injected as `GH_TOKEN` for downstream `gh` calls |
| world key | `gh.status_comment_id` | `string` | the rolling comment the substrate edits; absent → first post creates it |
| world key | `gh.parent_comment_ids` | `[]string` | non-owner comments to resolve on the **owner's** `@kitsoki` follow-up (round 1: owner-only authority) |
| producer | `poll` (round 1) | `ListGitHubInboxItems` + `@kitsoki` filter, `OriginRef` dedupe | reuses `internal/inbox/github.go`; the only v1 producer |
| producer | `webhook` (round 2) | HMAC-verified `issue_comment` / `pull_request_review_comment` payload | deferred drop-in alternate behind the same queue; not in v1 |

## The model

```
                          ┌─────────── producer (Producer seam) ─────────┐
  poll (ROUND 1) ─────────┤  ListGitHubInboxItems + @kitsoki filter      │
                          │  → NewGitHubNotification → OriginRef dedupe  │──┐
  webhook (ROUND 2) ─ ─ ─ ┤  HMAC-verify(secret) → issue_comment event   │  │
                          └──────────────────────────────────────────────┘  │
                                                                             ▼
                                                    ┌──── Postgres job queue (slice #2) ────┐
                                                    │ key = OriginRef + thread              │
                                                    │ re-mention → attach, not fork (#3)    │
                                                    │ THIS slice only ENQUEUES              │
                                                    └────────────┬───────────────────────────┘
                                                                 │  (boundary of THIS slice)
                                  installation-token auth ───────┤  GH_TOKEN minted here
                                                                 ▼
                                       ┌──────── slice #2 dispatch (NOT here) ────────┐
                                       │  job → story/session, spawn run, web link    │
                                       └──────────────────────────────────────────────┘

  comment substrate (host.gh.comment) — used by every consumer slice:
    ack ─▶ [edit-in-place rolling status: queued → running → done] ─▶ done
                        │ low confidence
                        ▼
                 guidance comment ──park job──▶ wait for follow-up @kitsoki ──▶ resume
```

**Interpretive vs deterministic.** Producing, dedup-keying, enqueuing, token
minting, and edit-vs-new-comment selection are all
**deterministic** (engine, replayable, cassette-driven). The only *interpretive*
moment is the **low-confidence decision** to take the guidance exit — that is a
recorded datapoint (below). A *story* makes that call; the substrate only
executes the resulting `host.gh.comment{op:guidance}` and parks the job.

### Auth & the App manifest

kitsoki holds no per-user PAT (epic shared decision #1): auth is GitHub-native
even though round 1 *reads* mentions by polling. An installation token is minted
from the App's private key + installation id and injected as `GH_TOKEN` so
downstream `host.gh.ticket` / `host.git` shell-outs through `cliExec`
(`internal/host/cli_exec.go:28`) authenticate as the App — stories never see App
internals (epic shared decision #2). The HMAC webhook secret applies only to the
round-2 webhook producer; the round-1 poll producer needs no inbound secret.
Pinned App permission floor:

```yaml
# kitsoki GitHub App manifest (permissions floor)
permissions:
  issues:         write   # comment, label, close
  pull_requests:  write   # comment, review, resolve threads
  contents:       write   # rebase / push (downstream slices)
  checks:         read    # CI status (slice #3)
default_events: [issue_comment, pull_request_review_comment, pull_request]
```

### Comment substrate

One recognizable identity, one rolling status comment edited in place. The
machine-readable block reuses the body-metadata convention pinned in
[github-issues-tracker.md](github-issues-tracker.md) shared decision #3 (the
fenced `kitsoki` block `host.gh.ticket` already writes on create and parses on
get, `internal/host/github.go:164` `ghParseMetadata`):

````
<!-- rolling status comment body -->
🤖 **kitsoki** — running `bugfix` · [live trace](…/run/<job-id>)

- [x] picked story
- [ ] implementing
...

```kitsoki
job_id: <job-id>
kind: status
state: running
trace_ref: …
```
````

**Edit-vs-new policy** (owned here): `host.gh.comment{op:status}` edits
`gh.status_comment_id` if present (looking it up by the `kind: status` block on
first run via `ghParseMetadata`), else creates it and records the id. `ack` /
`done` reuse the same rolling comment (state transitions). `guidance` posts a
**new** comment (a question must be answerable independently of the status line)
and parks the job.

**Parent-comment resolution.** When a non-owner comments and the **issue/PR
owner** follows up with `@kitsoki`, the producer records the intervening
non-owner comment ids on `gh.parent_comment_ids`;
`host.gh.comment{op:resolve_parent}` marks them resolved (`gh api …/pulls/comments`
thread resolution; on a plain issue, a reply that quotes + ✅-acknowledges, since
issue comments have no native resolve). **Round 1 authorizes the owner only**;
broadening to any repo-write collaborator is round 2 (epic decision #4),
enforced in slice #3 — this slice only provides the mechanism.

## Decision recording

The low-confidence/guidance exit is the one interpretive decision and **must
land in the trace as a labeled datapoint** — it is the GitHub-thread analogue of
the operator-ask bridge ([operator-ask.md](../architecture/operator-ask.md)),
which records `operator.question.{asked,answered,unanswered}`. This substrate
emits the parallel family, each carrying `job_id`, `repo`, `issue`, `confidence`,
`comment_id`, and `outcome`:

| Event | Meaning |
|---|---|
| `gh.guidance.asked` | low-confidence exit taken; a guidance comment was posted and the job parked |
| `gh.guidance.answered` | a follow-up `@kitsoki` reply arrived; the parked job resumes |
| `gh.guidance.unanswered` | the job timed out / was cancelled while parked |

These mirror the operator-ask events one-for-one so `kitsoki-debugging` and the
trace UI surface a GitHub-parked job identically to an operator-parked turn. The
substrate also emits `gh.comment.posted` / `gh.comment.edited` (deterministic,
for audit) carrying the rolling `comment_id` so re-mention idempotency is
reconstructable.

## Engine seams & invariants

- **Producer interface (DI seam).** The service consumes a `Producer` interface;
  round 1 ships one impl, `pollProducer` (reusing `github_inbox.go` +
  `inbox/github.go`). `webhookProducer` (round 2) drops in behind the same seam.
  Mirrors the `OperatorPrompter` DI pattern: the enqueue path never knows which
  producer fed it.
- **Enqueue seam (DI).** The service writes jobs through a `JobSink` interface
  owned by slice #2 (the Postgres-backed table). A test impl is an in-memory
  sink, so ingress is tested without a database.
- **Auth seam (DI).** An `InstallationTokenSource` interface mints `GH_TOKEN`;
  the production impl signs the App JWT, a test impl returns a canned token. The
  token never enters a cassette key (redacted in trace).
- **`cliExec` seam (existing).** All downstream `gh`/`git` go through
  `internal/host/cli_exec.go:28`, so tests never hit real GitHub.
- **Load-time invariants (fail fast at `serve` startup, not per-event):** poll
  producer requires a `--repo` or a resolvable remote and a usable installation
  token source; the `JobSink` (Postgres DSN) must connect. A misconfigured
  service exits with a clear message rather than silently dropping mentions. (The
  round-2 webhook adds: non-empty HMAC secret + bindable `--addr`.)

## Backward compatibility / migration

Purely additive. No existing story, cassette, or world key changes. `host.gh.ticket`
keeps its current ops; `host.gh.comment` is new and opt-in. With no App
configured, `host.gh.comment` falls back to the operator's local `gh auth`
(today's behavior) and the guidance/rolling-status verbs degrade to plain
`ghTicketComment` posts. No service runs unless `gh-agent serve` is launched, so
default behavior is unchanged. The webhook producer being deferred to round 2
costs nothing: the `Producer` seam is present from day one, so it lands later
with no change to the queue or the comment substrate.

## Tasks

```
## 1. Engine (round 1 = poll-only)
- [ ] 1.1 internal/ghagent: Producer + JobSink seams; enqueue (OriginRef + thread
          idempotency key, re-mention → attach per epic decision #3) into the
          Postgres queue defined by slice #2 (no schema here)
- [ ] 1.2 pollProducer (reuse github_inbox.go + inbox/github.go, add the
          @kitsoki mention filter) — the only round-1 producer
- [ ] 1.3 InstallationTokenSource seam; inject GH_TOKEN; pin the App manifest
- [ ] 1.4 host.gh.comment verbs (status/ack/done/guidance/resolve_parent) on the
          rolling comment, edit-vs-new policy, kitsoki metadata block;
          resolve_parent authorizes the issue/PR OWNER only
- [ ] 1.5 Decision recording: gh.guidance.{asked,answered,unanswered} +
          gh.comment.{posted,edited} into the trace
- [ ] 1.6 Load-time invariants + clear error messages at `serve` startup

## 2. Verification
- [ ] 2.1 kitsoki turn exercises host.gh.comment edit-in-place + guidance park
          (exec cassette, no real gh)
- [ ] 2.2 Poll fixture → @kitsoki filter → one enqueued job (in-memory JobSink);
          re-mention → attach not fork; owner-only resolve_parent
- [ ] 2.3 Flow fixture: low-confidence → guidance comment → follow-up @kitsoki → resume
          (cassette-driven, gh.guidance.* events asserted)

## 3. Adopt + document
- [ ] 3.1 Slice #2 wires its Postgres JobSink to this enqueue path (boundary verified)
- [ ] 3.2 Write docs/architecture/github-agent.md; backlink hosts.md;
          trim this proposal to a summary, delete when #2 lands on it

## 4. Round 2 (deferred — out of this slice's v1)
- [ ] 4.1 webhookProducer: HMAC-verify + HTTP listener (mirror runstatus Handler),
          webhook fixtures; broaden resolve_parent to repo-write collaborators
```

## Verification

No real GitHub, ever (CLAUDE.md; epic shared decision #6):

- **Poll input is a `gh` exec cassette.** A recorded `gh issue list` /
  `gh search` response with an `@kitsoki` mention drives the round-1 poll
  producer; the test asserts exactly one job is enqueued (in-memory `JobSink`)
  and a re-mention attaches rather than forks — no network. (Round-2 webhook
  payloads will be signed JSON fixtures; deferred with that producer.)
- **Every `gh`/`git` call is an exec cassette** through `cliExec`
  (`internal/host/cli_exec.go:28`) — `host.gh.comment` posting, editing, and
  resolve_parent are recorded, replayed deterministically.
- **The guidance arc is a flow fixture**: a stub low-confidence decision posts a
  guidance comment, the job parks, a scripted follow-up `@kitsoki` reply
  resumes it — asserting the three `gh.guidance.*` trace events (the same shape
  the operator-ask tests assert for `operator.question.*`).
- **`InstallationTokenSource` is stubbed** with a canned token; no App private
  key in tests.
- Stateless probe: `kitsoki turn --state … --intent comment --world @w.json`
  drives a single `host.gh.comment` op against a cassette.

No test needs an LLM; the only deferred live test is a gated, real-App poll
round-trip, run only on explicit request.

## Open questions

1. **Issue-comment "resolution" has no native API.** PR review threads resolve
   cleanly (`gh api … resolveReviewThread`); plain issue comments don't.
   Options: (a) reply + ✅ react + a `resolved:` line in the metadata block;
   (b) only support resolution on PRs, no-op on issues. *Lean: (a)* — keeps the
   semantic uniform and machine-readable via the block.
2. **Poll cadence for round 1.** Polling adds seconds of lag and `gh` rate cost.
   *Lean:* default `--poll-interval=30s`; the round-2 webhook producer is the
   eventual low-latency path (epic cross-cutting Q1 decides poll-first).
3. **One rolling comment per job, or per (job, phase)?** A long run might want a
   fresh status comment per phase. *Lean:* one per job for v1 (least surprise,
   no flood); revisit if the checklist grows unwieldy.

## Non-goals

- **Mapping a job to a story/session** (label → bugfix/feature, PR → autopilot,
  session spawn, web-UI link). That is slice #2 — this slice stops at the queue
  boundary.
- **Autonomous merging.** kitsoki never merges; a human does (epic non-goal).
- **The GitHub-App webhook producer.** Round 2 / deferred — round 1 ships
  poll-only; the `Producer` seam reserves its drop-in slot.
- **The Postgres job table + locking** (`FOR UPDATE SKIP LOCKED` /
  `pg_advisory_lock`) — owned by slice #2; this slice only enqueues.
- **Broadening driving authority beyond the issue/PR owner** to repo-write
  collaborators — round 2; round 1 is owner-only. Mechanism only here;
  enforcement is slice #3 (epic decision #4).
- **Other forges** (Jira / Linear / GitLab) — GitHub only (epic non-goal).
- **The public web/trace serving surface** — slice #4; this slice only mints the
  `gh.job_id` the link will later key on.
