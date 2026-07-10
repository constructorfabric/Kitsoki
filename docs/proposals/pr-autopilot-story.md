# Story: PR autopilot

**Status:** Draft v1. Nothing implemented yet — confirmed on a re-audit
alongside the shipped `docs/architecture/github-agent.md` dispatch write-up: there is no
`stories/pr-autopilot/` directory, and PR mentions still route through the
honest stub path in `internal/ghagent` (`Stubbed: true`, never "Done"). The
four `host.git`/`host.gh` gaps this story needs (wait-for-checks, `rebase`,
force-push, thread `resolve`) are unchanged from the epic's "Decided (round
1)" item 5.
**Kind:**   story
**Epic:**   kitsoki-github-agent.md

## Why

A PR `@kitsoki` mention (dispatched by epic slice #2) lands a contributor
in the place they want help: CI is red, the branch drifted off its target
and won't merge, a reviewer asked for changes, or the owner wants a
non-owner's review thread addressed. In v1 the **PR owner is the sole
driving authority** — only the PR author's `@kitsoki` authorizes action;
others' comments are context surfaced to the owner (repo-write collaborators
become authorizers in round 2). Today every one of those is a manual
loop — pull the branch, reproduce the failure, fix it, rebase, push,
reply on the thread. kitsoki already owns each of those mechanisms
(`stories/bugfix/` reproduce→implement→test, `delivery-tail/integrate`'s
rebase-onto-target, `host.git`, `host.gh.ticket`). This story composes
them into one PR-shaped pipeline that leaves a **green, rebased,
comments-addressed PR for a human to merge** — and asks on the thread the
moment it is unsure rather than guessing.

## What changes

A new `stories/pr-autopilot/` — a pipeline-shaped story structurally
identical to `bugfix`, entered with the PR already known (number, target
branch, worktree on the PR's branch; dispatch slice #2 seeds the world).
Its root `watch_ci` room reads CI status once per entry and routes to one
of four worker rooms (`fix_ci`, `rebase`, `implement_comment`,
`resolve_parent`), each a checkpoint room lifting bugfix's
`accept`/`refine(feedback)`/`restart_from(stage)`/`quit` intent set and
per-phase cycle budget verbatim. Every room's low-confidence arc and
every worker's exhausted-budget arc route through one `ask_guidance` room
that posts a question to the thread (epic shared decision #5) and parks at
`@exit:awaiting-guidance`. kitsoki **never merges** (epic non-goal); the
terminal `@exit:ready` means "handed back to a human, green + rebased +
addressed".

## Impact

- **Net-new:** 7 rooms, ~6 prompts, ~5 schemas, a 7-persona agents table,
  6 flow fixtures — all under `stories/pr-autopilot/`.
- **Engine/host changes:** composes existing mechanisms. **Two gaps the
  story works *around* rather than papering over — both flagged to the
  epic as runtime-slice candidates (see Open questions):**
  1. **No "watch / wait-for-checks" capability.** `host.git.pr_status`
     (`internal/host/git_vcs.go:306`) is a single-shot poll of
     `gh pr view --json state,statusCheckRollup`; there is no block-until-checks-settle
     effect. This story treats CI as **edge-triggered**: it reads status
     once per `@kitsoki` mention and parks at `@exit:awaiting-guidance` /
     `@exit:ready` between mentions — the dispatch substrate (#2) re-enters
     on the next webhook/poll tick. A true watch loop is a **runtime slice**.
  2. **No `rebase` op on `host.git`.** `host.git`
     (`internal/host/git_vcs.go:42`) exposes branch/diff/commit/push/open_pr/pr_status/pr_comment
     — **no rebase**. But `stories/delivery-tail/rooms/integrate.yaml:113`
     already performs `git rebase --onto <base> <mergebase> <branch>` (with
     `rebase --abort` on conflict + a conflict-resolve sub-agent) via
     `host.run` shelling to `git`. The `rebase` room **reuses that exact
     mechanism** (`host.run` + the integrate script shape), so no new host
     verb is introduced here. Promoting it to a first-class `host.git.rebase`
     op is a **runtime-slice nicety**, not a blocker.
  Additionally, `host.gh.ticket` (`internal/host/github.go:48`) and
  `host.git.pr_comment` (`git_vcs.go:331`) can **post** comments but expose
  **no PR-review-thread "resolve"** op (`gh api …/pulls/comments` /
  GraphQL `resolveReviewThread`). The `resolve_parent` room posts a reply
  + a machine-readable resolution marker through slice #1's comment
  substrate; the actual thread-resolution API call is **owned by slice #1**
  (epic shared decision #4 — stories emit *intent to speak*, the substrate
  owns the GitHub mechanics). The story never calls the resolve API directly.
- **Docs on ship:** `docs/stories/pr-autopilot.md`; epic slice table row #3.
- **Live deliverable:** a throwaway PR seeded with known issues (failing CI,
  a target-branch merge conflict, a change-requesting review comment) used as
  the **gated live integration test** alongside the no-LLM flow fixtures (see
  Tasks phase 4). Job-state persistence (Postgres + filesystem artifacts) is
  not this story's concern — it lives in slices #2/#4.

## Reuse inventory

| Pipeline step | Mechanism | Reference |
|---|---|---|
| Read CI status on the PR | `iface.vcs.pr_status` (`gh pr view --json statusCheckRollup`) | `internal/host/git_vcs.go:306` (`ghPRStatus`) |
| Reproduce → implement → test a CI failure | the bugfix maker triad (reproducer `task`, implementer `task`, test_author `task`) | `stories/bugfix/rooms/{reproducing,implementing,testing}.yaml`; agents table `stories/bugfix/app.yaml:87` |
| Rebase branch onto target + abort-on-conflict + conflict-resolve agent | `host.run` shelling `git rebase --onto` | `stories/delivery-tail/rooms/integrate.yaml:113` (reused, not reinvented) |
| Push rebased / fixed branch | `iface.vcs.push` (`git push -u origin HEAD`, installation token from slice #1) | `internal/host/git_vcs.go:238` (`gitPush`) |
| Parse review comments into work | `host.agent.decide` with a comment-triage schema (read PR comments off `pr_status`/`ticket.get`) | bugfix triager persona `stories/bugfix/app.yaml:99`; `iface.ticket.get` comments `internal/host/github.go:177` |
| Checkpoint: accept / refine / restart_from / quit + cycle budget | lifted verbatim | `stories/bugfix/rooms/proposing.yaml:119-195`; budgets `stories/bugfix/app.yaml:380` |
| Post status / guidance / resolution comment (one voice, rolling) | slice #1 comment substrate via `iface.vcs.pr_comment` / `iface.ticket.comment` | `internal/host/git_vcs.go:331`; `internal/host/github.go:191`; epic shared decision #4 |
| Driving authority (v1 = PR owner only) | `world.pr_owner == world.mention_author` gate; repo-write collaborators are round 2 (epic decision #1 / Q4 — deferred, not redesigned) | `stories/bugfix/app.yaml:260` (`allowed_authors`) |
| "If unsure, ask the thread and park" | guidance-comment exit = the GitHub-thread analogue of operator-ask | epic shared decision #5; `docs/architecture/operator-ask.md` |

## Story graph

```
                          (dispatch slice #2 seeds world: pr_number, target_branch, worktree)
                                              │ start
                                              ▼
                                       ┌─────────────┐
                                       │  watch_ci   │  reads iface.vcs.pr_status once
                                       │  (router)   │  + classifies pending PR work
                                       └─────────────┘
            ┌──────────────┬──────────────┼───────────────┬──────────────┐
   ci_red   │   conflict   │   has_review │   owner_       │  low         │ ci_green &
            ▼              ▼   _comments   │   resolve_req  │  confidence  │ nothing pending
      ┌──────────┐   ┌──────────┐   ┌──────────────┐  ┌──────────────┐    │
      │  fix_ci  │   │  rebase  │   │implement_    │  │resolve_      │     │
      │   [CK]   │   │   [CK]   │   │comment [CK]  │  │parent [CK]   │     │
      └──────────┘   └──────────┘   └──────────────┘  └──────────────┘     │
        │ accept       │ accept        │ accept          │ accept          │
        │ (push)       │ (push)        │ (push)          │ (reply+resolve) │
        └──────────────┴───────┬───────┴─────────────────┘                 │
                               ▼ re-enter (status comment mirrored)        ▼
                          ┌─────────────┐                          ┌─────────────┐
                          │  watch_ci   │ ── all clear ──────────▶ │ @exit:ready │
                          └─────────────┘                          └─────────────┘
   any room, low-confidence  │ ask
   OR refine-budget-exhausted ▼
                       ┌──────────────┐
                       │ ask_guidance │ posts question to thread, parks
                       └──────────────┘
                               │
                               ▼
                     @exit:awaiting-guidance   (re-entered by next @kitsoki via #2)

   [CK] = checkpoint room: accept / refine(feedback) / restart_from(stage) / quit, per-phase cycle budget
   quit (any room) ─▶ @exit:abandoned
```

`watch_ci` is the **router**; the four `[CK]` rooms are **checkpoints**;
`ask_guidance` is the **guidance/ask exit**. After any worker `accept`,
control returns to `watch_ci` (re-poll), so one mention can chain
fix→rebase→implement before exiting.

## World schema (sketch)

```yaml
world:
  # ── PR identity (seeded by dispatch slice #2) ───────────────────────────
  pr_number:        { type: string, default: "" }   # the PR (== iface.vcs pr_id)
  pr_url:           { type: string, default: "" }
  repo:             { type: string, default: "" }    # owner/repo slug for gh --repo
  target_branch:    { type: string, default: "main" }# rebase/merge target (PR base)
  feature_branch:   { type: string, default: "" }    # the PR head branch
  workdir:          { type: string, default: "" }    # per-job worktree on the head branch (epic Q3)

  # ── Driving authority (v1 = PR OWNER ONLY; repo-write collaborators = round 2) ──
  pr_owner:         { type: string, default: "" }    # the PR author — the ONLY authorizer in v1
  mention_author:   { type: string, default: "" }    # who typed the @kitsoki that re-entered
  allowed_authors:  { type: string, default: "" }    # round-2 seam: CSV of repo-write logins (unused in v1)

  # ── CI snapshot (single-shot poll; no watch — see Impact) ────────────────
  ci_status:        { type: string, default: "" }    # passing | failing | pending | unknown
  ci_failed_checks: { type: list,   default: [] }     # {name, summary, log_url} from statusCheckRollup
  ci_log:           { type: string, default: "" }     # scratch — last failing check tail

  # ── Conflict / rebase state ──────────────────────────────────────────────
  has_conflict:     { type: bool,   default: false }  # mergeable==CONFLICTING
  rebase_ok:        { type: bool,   default: false }
  rebase_conflicts: { type: string, default: "" }     # files the rebase couldn't resolve

  # ── Review comments → work ───────────────────────────────────────────────
  review_comments:  { type: list,   default: [] }     # {id, author, body, parent_id, is_owner}
  pending_comment:  { type: object, default: {} }     # the comment currently being implemented
  parent_to_resolve:{ type: object, default: {} }     # non-owner parent the owner asked to address

  # ── Per-room artifacts (object: {summary_title, summary_markdown, …}) ────
  ci_fix_artifact:        { type: object, default: {} }
  comment_work_artifact:  { type: object, default: {} }
  resolve_artifact:       { type: object, default: {} }

  # ── Confidence / guidance (epic shared decision #5) ──────────────────────
  llm_verdict:        { type: object, default: {} }   # {intent, reason, confidence, verdict}
  judge_confidence_threshold: { type: float, default: 0.8 }
  guidance_question:  { type: string, default: "" }   # what kitsoki posts when unsure
  guidance_reason:    { type: string, default: "" }   # machine code: ci_unfixable | rebase_conflict | …
  abandon_reason:     { type: string, default: "" }

  # ── Rolling status comment (epic shared decision #4) ─────────────────────
  status_comment_id:  { type: string, default: "" }   # the single comment the substrate edits
  status_line:        { type: string, default: "" }   # current human-readable progress

  # ── Cycle budgets (lifted from bugfix app.yaml:380) ──────────────────────
  fix_ci_cycle:     { type: int, default: 0 }
  rebase_cycle:     { type: int, default: 0 }
  implement_cycle:  { type: int, default: 0 }
  resolve_cycle:    { type: int, default: 0 }
  fix_ci_budget:    { type: int, default: 3 }
  rebase_budget:    { type: int, default: 2 }
  implement_budget: { type: int, default: 3 }
  resolve_budget:   { type: int, default: 2 }
  judge_mode:       { type: string, default: "llm_then_human" }
```

`exits:` —
`ready: { requires: [] }` (CI green, no conflict, no pending comments — handed to a human),
`awaiting-guidance: { requires: [guidance_question] }`,
`abandoned: { requires: [abandon_reason] }`.

## Per-room detail

### `watch_ci` — root router; read CI status once and classify pending work

- **`on_enter`:** `iface.vcs.pr_status` (`pr_id: world.pr_number`,
  `repo`), `bind: { ci_status, ci_failed_checks ← statusCheckRollup }`;
  then `iface.ticket.get` (`id: pr_number`) to pull `comments` →
  `bind: review_comments`. A `host.agent.decide` (persona `triager`,
  read-only) classifies the snapshot into one guarded `emit_intent`:
  `ci_red` | `conflict` | `has_review_comments` | `owner_resolve_req` |
  `all_clear`, plus `low_confidence` when the classification is ambiguous.
  `once: true` keyed on the snapshot so re-render doesn't re-poll.
- **Intents (internal, guarded emits):** route to `fix_ci` / `rebase` /
  `implement_comment` / `resolve_parent` / `ask_guidance` / `@exit:ready`.
  The `owner_resolve_req` guard requires `mention_author == pr_owner`
  (v1 = PR owner only — epic decision #1; repo-write collaborators are
  round 2); a non-owner mention is *context only* and falls through to
  `all_clear`/`ask`.
- **View:** `relevant_world: [pr_number, ci_status, ci_failed_checks,
  has_conflict, review_comments, status_line]` — a `kv` PR header + a
  `code` block of the failing checks.

### `fix_ci` — reproduce → fix → test a failing check `[checkpoint]`

- **`on_enter`:** the bugfix maker triad against `world.workdir` —
  `reproducer` (`task`, read-only) reads `ci_failed_checks`/`ci_log` and
  reproduces locally, `implementer` (`task`) applies the fix, `test_author`
  (`task`) re-runs the failing target (`iface.ci.run_tests`).
  `bind: ci_fix_artifact ← submitted`. `once: true` (re-arm on refine).
- **Intents:** `accept` → `iface.vcs.commit(stage_all:true)` +
  `iface.vcs.push` (installation token, contents:write) → `watch_ci`,
  mirroring `status_line` via the substrate. `refine(feedback)` re-runs
  with `fix_ci_cycle++`; at `fix_ci_budget` → `ask_guidance`
  (`guidance_reason: ci_unfixable`). `restart_from`, `quit`.
- **View:** the `ci_fix_artifact` summary + changed files.

### `rebase` — rebase head onto target, push `[checkpoint]`

- **`on_enter`:** `host.run` running the `delivery-tail/integrate`
  rebase script shape — `git rebase --onto <target_branch> <merge-base>
  <feature_branch>` in `world.workdir`; on conflict, `git rebase --abort`
  then a `conflict_resolver` (`task`) agent attempts resolution (mirrors
  `integrate.yaml:172`). `bind: { rebase_ok, rebase_conflicts }`.
- **Intents:** `accept` (when `rebase_ok`) → `iface.vcs.push`
  (`--force-with-lease` via the push args) → `watch_ci`. `refine` re-runs
  resolution with `rebase_cycle++`; at `rebase_budget` (default 2) →
  `ask_guidance` (`guidance_reason: rebase_conflict`, posting the
  conflicted file list). `quit`.
- **View:** rebase outcome + `rebase_conflicts`.

### `implement_comment` — implement one requested change from a comment `[checkpoint]`

- **`on_enter`:** pick the next actionable `review_comments` item
  (authored or endorsed by a repo-write identity) into `pending_comment`;
  `implementer` (`task`) implements it against `world.workdir`,
  `test_author` (`task`) verifies. `bind: comment_work_artifact`.
- **Intents:** `accept` → commit + push → reply on the comment thread
  (substrate) → `watch_ci`. `refine(feedback)` with `implement_cycle++`;
  budget → `ask_guidance` (`guidance_reason: comment_unclear`, quoting the
  comment). `restart_from`, `quit`.
- **View:** `pending_comment` body + the produced diff summary.

### `resolve_parent` — address + resolve a non-owner parent comment `[checkpoint]`

- **Authority gate (v1 = PR owner only; epic decision #1 / Q4):** entered
  only when the **PR owner** (`mention_author == pr_owner`) followed up with
  `@kitsoki` on a thread whose **parent** comment is from someone else. The
  owner's mention *authorizes*; the other comment is the *context* to
  address. Repo-write collaborators as authorizers is round 2.
- **`on_enter`:** `implementer`/`test_author` address the parent
  comment's substance against `world.workdir`; `bind: resolve_artifact`.
- **Intents:** `accept` → commit + push + post a reply to the parent
  thread **and emit intent-to-resolve** through slice #1's substrate
  (`resolve_artifact.resolution_marker`; the substrate owns the actual
  `resolveReviewThread` API call — this story does not call it) →
  `watch_ci`. `refine`, budget → `ask_guidance`, `quit`.
- **View:** the parent comment + what was done.

### `ask_guidance` — post a question to the thread and park

- **`on_enter`:** compose `guidance_question` from `guidance_reason` +
  the room's artifact; post via the slice #1 substrate
  (`iface.vcs.pr_comment`), recorded as kitsoki's one voice (decision #4).
  No agent call — this is a deterministic post + park.
- **Intents:** none driveable here; terminal transition to
  `@exit:awaiting-guidance`. The dispatch substrate (#2) re-enters
  `watch_ci` on the next `@kitsoki` follow-up (epic decision #3 — same job,
  same run). This is the thread analogue of operator-ask
  (`docs/architecture/operator-ask.md`) with no live operator attached.
- **View:** the posted question + `guidance_reason`.

### Net-new files

```
stories/pr-autopilot/
├── app.yaml
├── rooms/
│   ├── watch_ci.yaml
│   ├── fix_ci.yaml
│   ├── rebase.yaml
│   ├── implement_comment.yaml
│   ├── resolve_parent.yaml
│   └── ask_guidance.yaml
├── prompts/
│   ├── classify_pr_work.md       # watch_ci triager
│   ├── reproduce_ci_failure.md
│   ├── fix_ci_failure.md
│   ├── triage_review_comments.md
│   ├── implement_comment.md
│   └── resolve_parent.md
├── schemas/
│   ├── pr_work_classification.json
│   ├── ci_fix_artifact.json
│   ├── comment_work_artifact.json
│   ├── resolve_artifact.json
│   └── judge_verdict.json        # reuse bugfix shape
├── flows/
│   ├── ci_green_passthrough.yaml
│   ├── ci_failure_fix_loop.yaml
│   ├── conflict_rebase.yaml
│   ├── comment_implement.yaml
│   ├── parent_resolve.yaml
│   └── ask_when_unsure.yaml
└── README.md
```

## Flow fixtures

Mode-2, intent-only, no-LLM, no-GitHub — `host.git`/`host.gh`/`host.run`
stubbed by `host_handlers`, agents by mock cassettes (CLAUDE.md / epic
decision #6). The regression contract:

- `ci_green_passthrough` — `pr_status` stub returns `passing`, no
  conflict, no comments → `watch_ci` → `@exit:ready` in one turn (the
  "nothing to do, hand to human" path; proves kitsoki never merges).
- `ci_failure_fix_loop` — `pr_status` returns a failing check →
  `watch_ci`→`fix_ci`; `accept` commits+pushes (stubbed) → back to
  `watch_ci` which now stubs `passing` → `@exit:ready`. Proves the
  reproduce→fix→test→push→re-poll cycle.
- `conflict_rebase` — `pr_status` reports `CONFLICTING` → `rebase`;
  rebase stub succeeds → `accept` (force-push stub) → `watch_ci` clear →
  `@exit:ready`. Proves the integrate-script rebase reuse + push.
- `comment_implement` — a repo-write-authored review comment present →
  `implement_comment`; `accept` pushes + replies → re-poll → ready.
- `parent_resolve` — a non-owner parent comment + a PR-owner `@kitsoki`
  follow-up (`mention_author == pr_owner`) → `resolve_parent`; `accept`
  posts reply + resolution marker → ready. Twin assertion: a non-owner
  `mention_author` does **not** enter `resolve_parent` (v1 owner-only
  authority).
- `ask_when_unsure` — `fix_ci` refined to `fix_ci_budget` → `ask_guidance`
  → `@exit:awaiting-guidance` carrying `guidance_question` +
  `guidance_reason: ci_unfixable`. Proves the low-confidence/exhausted-budget
  guidance exit and that the job parks (not abandons).

## Tasks

```
## 1. Scaffold
- [ ] 1.1 app.yaml: agents table (reproducer/implementer/test_author/conflict_resolver/triager/judge), iface bindings (vcs→host.git, ticket→host.gh.ticket, ci→host.local), world schema, exits, intents (lift bugfix's accept/refine/restart_from/quit + internal guarded emits)
- [ ] 1.2 room files with typed `extends: "base"` views; schemas/*.json; stub prompts

## 2. Lock the graph
- [ ] 2.1 Probe each room: `kitsoki turn … --state <room> --intent <x> --world @w.json`
- [ ] 2.2 Confirm the rebase room's host.run reuses the integrate.yaml script shape verbatim (no new host verb)
- [ ] 2.3 Flow fixtures pass (all six)

## 3. Live + document
- [ ] 3.1 `kitsoki run` end-to-end against a cassette fixture PR (gh/git/run replayed, no real GitHub)
- [ ] 3.2 README (entry contract from slice #2, exits, world contract, host requirements, the two host gaps)

## 4. Dogfood: throwaway PR with known issues  (GATED live integration — real LLM only when explicitly requested)
- [ ] 4.1 Seed a throwaway PR against a scratch/fixture repo or branch with KNOWN issues: a failing CI check, a merge conflict against the target branch, and a review comment requesting a concrete change
- [ ] 4.2 Run the autopilot story live against it (PR owner = the seeding identity, so v1 owner-only authority is exercised)
- [ ] 4.3 Confirm it fixes CI (re-poll green), rebases onto target, addresses the review comment, and leaves the PR GREEN + UNMERGED (kitsoki never merges)
- [ ] 4.4 Confirm the low-confidence/exhausted path posts a guidance comment and parks (not abandons) when a seeded issue is deliberately unfixable
      # CLAUDE.md intact: the flow/cassette fixtures (phase 2) stay no-LLM and are the CI regression contract; THIS phase is the gated live check — never run automatically or without an explicit request.

## 5. Document
- [ ] 5.1 Migrate to docs/stories/pr-autopilot.md; trim/delete this proposal
```

## Open questions

1. **CI watch: edge-triggered re-entry vs a runtime watch effect.** v1
   reads `pr_status` once per `@kitsoki` mention and parks between
   mentions (no new host call). *Lean:* ship edge-triggered (composes what
   exists); fold a `host.git.watch_checks` / wait-for-checks effect into
   the epic as a **runtime slice** if latency hurts. **Flag to epic open
   questions.**
2. **`host.git.rebase` op vs `host.run` rebase script.** v1 reuses
   `delivery-tail/integrate`'s `host.run` rebase. *Lean:* reuse now;
   propose promoting it to a first-class `host.git.rebase` op (force-push
   semantics, conflict surfacing) as a **runtime-slice nicety**. **Flag to
   epic.**
3. **Force-push safety on rebase.** Rebasing rewrites the PR head;
   pushing needs `--force-with-lease`. `gitPush` (`git_vcs.go:238`) hard-codes
   `git push -u origin HEAD` with no force flag. *Lean:* add a `force` arg
   to the `push` op (small, story-adjacent runtime touch) — flag to slice #1
   (it owns the push identity/token anyway) **or** the rebase room does the
   force-push via `host.run` git directly (consistent with reusing the
   rebase script). Lean: `host.run`, no host change.
4. **Resolving review threads.** Posting a reply is supported; marking a
   review thread *resolved* (`resolveReviewThread` GraphQL) is not in
   `host.gh.ticket`/`host.git`. *Lean:* slice #1 owns it (decision #4 —
   stories emit intent-to-resolve, substrate executes). Confirm slice #1's
   substrate exposes a `resolve_thread` intent before this story binds it.

## Non-goals

- **Merging the PR.** kitsoki leaves a green, rebased, comments-addressed
  PR; a human merges (epic non-goal, restated).
- **Auth / authority design beyond owner-only.** v1 implements PR-owner-only
  authority (`pr_owner == mention_author`); extending authorization to
  repo-write collaborators is round 2, deferred wholesale to epic shared
  decision #1 and cross-cutting Q4. This story only *consumes* `pr_owner` +
  `mention_author`.
- **The comment substrate / parent-comment-resolution API.** Owned by
  slice #1 (`gh-event-ingress.md`); this story emits intent-to-speak /
  intent-to-resolve only (epic decision #4).
- **Dispatch & job lifecycle / worktree.** Owned by slice #2; this story
  is *entered* with the world seeded.
- **A real watch loop / first-class rebase op.** Runtime-slice candidates
  (Open questions 1–3), not designed here.
- **Real GitHub / real LLM in tests.** Fixtures + cassettes only (epic
  decision #6).
```
