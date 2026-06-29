# Epic: @kitsoki — a GitHub-native agent with a trace & artifact web service

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 6 (0/6 shipped)

## Why

kitsoki can already *run* the work GitHub users want done — it has a
`gh`-backed ticket provider (`host.gh.ticket`, `internal/host/github.go`), a
bugfix pipeline (`stories/bugfix/`), a PRD→design→delivery pipeline
(`stories/dev-story/` + `stories/prd/`), a git host (`host.git`), and a
shipped trace web UI (`tools/runstatus/` + `internal/runstatus/`). What it
**lacks is the front door**: a way for a collaborator to summon kitsoki *from
where they already work* — a GitHub issue or PR — and then *watch and steer*
the run without leaving GitHub.

Today every run is operator-initiated from a terminal, the trace UI is a
local `kitsoki web` / `export-status` artifact (`runstatus-proposal.md`), and
artifacts live as un-served files under `.artifacts/`
(`internal/host/artifacts_dir_transport.go`). There is no inbound trigger, no
publicly-linkable run surface, and no path for kitsoki to acknowledge progress
on the thread that asked for it. This epic closes that loop:

> Mention **@kitsoki** in an issue or PR → kitsoki picks the right story,
> runs it, posts an ack with a link to a live web UI, and the requester can
> watch the trace, browse artifacts, or drive the conversation directly —
> with kitsoki mirroring status back to the thread.

## What changes

Once every slice ships:

- **Inbound `@kitsoki` mentions** on a GitHub issue or PR reach kitsoki
  (GitHub App webhook, with a poll fallback), are authenticated against the
  installation, and are turned into a **job**. Mentions kitsoki can't act on
  confidently produce a **"requesting guidance" comment** rather than a guess.
- **Issues dispatch by label:** a `bug`-tagged issue runs the bugfix pipeline;
  a `feature`/`enhancement`-tagged issue runs the PRD→design pipeline. The
  label→story map is configured, not hard-coded.
- **PRs run an autopilot story:** kitsoki **watches CI**, attempts to
  fix/resolve failures, **rebases the branch onto its target** when merge
  conflicts appear, **implements requested changes from PR comments**, and —
  when a non-owner comments and the owner follows up with `@kitsoki` —
  **resolves the parent comment(s)**. When unsure, it asks on the thread.
- **Every job links to a public kitsoki web UI** served by a persistent
  **trace + artifact service**: traces stream live, artifacts (screenshots,
  decks, PRDs, rendered media) are browsable by handle, and the requester can
  **drive the conversation directly** (the operator-ask bridge), with kitsoki
  posting **status / ack comments** back to the issue or PR.
- The whole thing is demonstrated by a **tour-driven demo video** (built with
  `kitsoki-ui-demo`, validated with `kitsoki-ui-qa`) using **slidey** as the
  worked case study, and the per-feature clips are **composited into one
  slidey presentation deck**.

## Impact

- **Spans:** runtime (ingress + dispatch substrate), story (PR autopilot),
  tracing (the public trace/artifact service), tui/web (the viewer +
  operator-drive surface), plus a demo/deliverable slice.
- **Net surface:** one new long-running service (`kitsoki gh-agent serve`)
  composing the existing webhook/inbox plumbing (`internal/inbox/github.go`),
  `host.gh.ticket`, `host.git`, and the runstatus server; one new PR-autopilot
  story; an artifact index + by-handle HTTP serving on top of the runstatus
  SPA; and a `features/` tour + slidey composite. Every GitHub call goes
  through the existing `cliExec` cassette seam (`internal/host/cli_exec.go`) so
  flows/tests never touch real GitHub (CLAUDE.md).
- **Docs on ship:** `docs/architecture/github-agent.md` (the service + auth +
  dispatch), `docs/stories/pr-autopilot.md`, `docs/tracing/trace-artifact-service.md`,
  `docs/tui/web-ui.md` (artifact + operator-drive surfaces). Each slice migrates
  its own detail per its child plan.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | GitHub ingress & comment substrate | runtime | `@kitsoki` webhook/poll listener, GitHub-App auth, ack/status/guidance comment posting, parent-comment resolution semantics | — | Draft | [`gh-event-ingress.md`](gh-event-ingress.md) |
| 2 | Job dispatch & orchestration | runtime | Map a mention → a job: issue label → bugfix/feature story, PR → autopilot; spawn session, mint the run, generate the web-UI link | 1 | Draft | [`gh-job-dispatch.md`](gh-job-dispatch.md) |
| 3 | PR autopilot story | story | CI-watch + auto-fix, rebase-on-conflict, comment-driven implement, resolve-parent-comment, ask-when-unsure | 1, 2 | Draft | [`pr-autopilot-story.md`](pr-autopilot-story.md) |
| 4 | Trace + artifact service | tracing | Persistent, publicly-linkable serving of traces + artifacts by handle, with a queryable run/artifact index | — | Draft | [`trace-artifact-service.md`](trace-artifact-service.md) |
| 5 | Web viewer: artifacts + operator drive | tui | Artifact gallery / media rendering + "drive the conversation directly" surface that posts acks back to GitHub | 4, 1 | Draft | [`gh-web-operator-viewer.md`](gh-web-operator-viewer.md) |
| 6 | Demo: tour video + slidey composite | story | `kitsoki-ui-demo` tour over the GitHub↔kitsoki loop (slidey case study), QA-gated, composited into one slidey deck | 1–5 | Draft | [`kitsoki-github-demo.md`](kitsoki-github-demo.md) |

## Sequencing

```
#1 (ingress + comments) ──▶ #2 (dispatch) ──▶ #3 (PR autopilot story)
        │                        │
        │                        └──▶ #5 (web viewer + operator drive)
#4 (trace/artifact service) ─────────▶ #5
                                            └──▶ #6 (demo + slidey composite)
```

#1 is the GitHub I/O substrate (auth + read mentions + post comments) every
other GitHub-facing slice consumes. #4 (the public serving substrate) is
independent of the GitHub slices and can land in parallel. #2 needs #1 to read
the mention and #4 to mint the linkable run. #3 is the first real consumer
story. #5 joins the service (#4) to the GitHub loop (#1) for live operator
drive. #6 is the deliverable and lands last, demonstrating #1–#5 end to end.

## Shared decisions

1. **Auth is GitHub-native, end to end.** Per the operator's instruction:
   - **Ingress** authenticates as a **GitHub App installation** — the webhook
     payload is HMAC-verified with the app's secret, and kitsoki acts using an
     installation token (not a personal PAT). This is the multi-user analogue
     of the existing `gh auth` convention (shared decision #3 of
     [`github-issues-tracker.md`](github-issues-tracker.md)): kitsoki holds no
     per-user tokens. A **poll fallback** (reusing `internal/inbox/github.go`)
     covers environments without webhook reachability and authenticates with
     the same app token.
   - **Web-UI access** is gated by **GitHub OAuth** (the same App's OAuth
     flow): a run's web link is viewable only by GitHub users with access to
     the originating repo. Identity from OAuth is what authorizes the
     *operator-drive* surface (#5) and is recorded on every driven action.
     **Driving authority — round 1:** only the **issue/PR author (owner)** may
     drive a run. The design target is "repo-write collaborators **and** the
     owner may drive" (round 2); v1 ships the narrower owner-only check and
     leaves the collaborator-membership lookup as a follow-on. The trust
     boundary for *acting on others' comments* (shared decision below / Q4) is
     the same gate.
   - The App's permissions floor: `issues:write`, `pull_requests:write`,
     `contents:write` (for rebase/push), `checks:read`. Slice #1 pins the exact
     manifest.
2. **`gh` CLI under an installation token, never raw REST in stories.** All
   GitHub reads/writes a *story* makes still go through `host.gh.ticket` /
   `host.git` and the `cliExec` seam so they stay cassette-recordable. The
   **service** (#1) does the App-level webhook/OAuth/installation-token work
   that `gh` can't, but it hands stories a pre-authenticated `gh` environment
   (`GH_TOKEN` = installation token) — stories never see App internals.
3. **One job = one run = one linkable surface.** A mention mints exactly one
   kitsoki session whose trace + artifacts are served at a stable URL
   (`…/run/<job-id>`). Re-mentioning an active job attaches to the existing run
   rather than forking a second one (idempotency keyed on
   issue/PR + comment thread). Slice #2 owns the job-id scheme; #4 owns the URL.
4. **kitsoki speaks on the thread in one voice.** Acks, status updates,
   guidance requests, and "done" notices are all posted through the slice-#1
   comment substrate using a single recognizable identity + a machine-readable
   `kitsoki` metadata block (mirroring the body-block convention from
   `github-issues-tracker.md` shared decision #3). Stories emit *intent to
   speak*; the substrate owns formatting, dedup, and the edit-vs-new-comment
   policy (a single rolling status comment, not a flood).
5. **"If in doubt, ask" is a first-class arc, not an error.** Every dispatch
   and PR-autopilot decision point has an explicit *low-confidence →
   guidance-comment* exit. This is the GitHub-thread analogue of the
   operator-ask bridge (`docs/architecture/operator-ask.md`): with no live
   operator attached, kitsoki posts the question to the thread and parks the
   job until a reply (a follow-up `@kitsoki`) arrives.
6. **No real GitHub / no real LLM in tests.** Webhook payloads are fixtures;
   `gh`/`git` calls are exec cassettes; agent steps are mock cassettes
   (CLAUDE.md). Each slice ships the fixtures for its new calls; the demo (#6)
   runs the no-LLM replay+tour harness, never live.

## Decided (round 1)

These were open; the operator has settled them for the first round. Children
implement them, not re-litigate them.

1. **Polling, not webhooks, for round 1.** Reuse `internal/inbox/github.go` —
   no public HTTPS endpoint, no App webhook registration needed to get a
   working loop. The ingress (#1) keeps a pluggable `Producer` seam so a
   webhook can drop in later behind the same job queue, but **round 1 ships
   poll-only.** (Decided in #1.)
2. **State in PostgreSQL, artifacts on the filesystem.** Durable run/job/index
   **state** lives in **Postgres**; trace JSONL and artifact blobs (screenshots,
   decks, PRDs, media) stay on the **filesystem** under a configurable root
   (mirroring `artifacts_dir` root resolution), with Postgres rows pointing at
   the on-disk paths. Keep it simple: one schema, no object-storage abstraction
   in round 1. (Persistence shape decided in #4; the job/lock tables in #2.)
3. **Job locking via Postgres, one worktree per job.** Concurrency control is a
   **Postgres lock** — a `SELECT … FOR UPDATE SKIP LOCKED` job-claim (or a
   `pg_advisory_lock` keyed on the job id) so exactly one worker owns a job and
   re-mentions attach rather than fork (shared decision #3). Job *execution* is
   still isolated with one worktree per job (`.worktrees/`, AGENTS.md) + per-job
   `KITSOKI_APP_DIR` to avoid the renderer cross-contamination
   (`parallel-live-drivers-schema-bleed`). The dispatcher (#2) owns both the
   lock and the worktree lifecycle. (Decided in #2.)
4. **Driving authority: PR/issue owner only in round 1.** The design target is
   "repo-write collaborators **and** the owner may drive / authorize acting on
   others' comments"; **round 1 implements owner-only** (the author of the
   issue/PR), deferring the collaborator-membership lookup. Non-owner comments
   are *context* surfaced to the owner, never auto-acted. (Decided in #3 + #5,
   building on shared decision #1.)
5. **The PR-autopilot story (#3) needs host capabilities that don't exist yet.**
   Authoring #3 against the real hosts surfaced four gaps in `host.git` /
   `host.gh`: (a) **no wait-for-checks** — `host.git.pr_status`
   (`internal/host/git_vcs.go:306`) is a single-shot poll, no block-until-CI-settles;
   (b) **no `rebase` op** on `host.git` (`git_vcs.go:42`); (c) **`host.git.push`
   can't force-push** (`gitPush`, `git_vcs.go:238`, hard-codes `push -u origin HEAD`)
   — required after a rebase rewrites the PR head; (d) **no PR-review-thread
   `resolve` op** (`host.gh.ticket` is Issues-only, `github.go:48`;
   `host.git.pr_comment` only posts). *Lean:* #3 ships **composing what exists** —
   edge-triggered re-entry per `@kitsoki` mention instead of (a), a `host.run`
   `git rebase --onto` + force-push (per `delivery-tail/integrate.yaml:113`)
   instead of (b)/(c), and the slice-#1 comment substrate's `resolve_parent` verb
   for (d). Promoting these into first-class `host.git`/`host.gh` ops is a small
   **follow-on runtime slice** — tracked here, not blocking #3. Slice #1 must
   expose the `resolve_thread`/`resolve_parent` intent #3 depends on
   (`ghPRStatus` also returns `checks`/`comments` as empty stubs today,
   `git_vcs.go:324` — a real impl must fill those projections).

## Non-goals

- **Other forges (Jira / Linear / GitLab).** GitHub only; the `ticket`/`vcs`
  interface seams leave room for siblings later (cf.
  [`external-project-targeting.md`](external-project-targeting.md)).
- **Autonomous merging.** kitsoki prepares a PR (green CI, rebased, comments
  addressed) but **does not merge**; a human merges. Stated again per-slice.
- **Replacing the local `kitsoki web` / `export-status` flow.** Those stay for
  local runs; #4 adds a *hosted, multi-run* service alongside them, reusing the
  same SPA.
- **A general GitHub Actions marketplace integration.** v1 is a single
  self-hosted service + App; packaging as a reusable Action is future work.
- **Real-GitHub or real-LLM CI.** Everything is cassette/flow-driven (shared
  decision #6).
