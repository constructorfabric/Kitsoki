# Epic: Delivery loop — ship-it + fleet

**Status:** Implemented. Slices 1, 2, 3, and 4 all have code/story surfaces in
the current tree (`stories/git-ops/`, `stories/ship-it/`, `stories/fleet/`, and
bugfix convergence work). `stories/fleet` validates and its no-LLM
`happy_path_two_briefs` flow passes. Follow-up is proposal lifecycle cleanup:
migrate any remaining design rationale into `docs/stories/` and delete this
proposal plus shipped child proposals.
**Kind:**   epic
**Slices:** 4 (4/4 shipped)

<!--
  Authoring note: this epic was itself produced by hand-driving the very
  pattern it proposes to automate (scoped brief → background maker in an
  isolated worktree → deterministic gate → lost-work-safe integrate →
  independent re-verify → cleanup), fanned out over N briefs with a single
  merge-lock. See the Dogfood note at the bottom: ship-it/fleet, once built,
  IS that orchestrator.
-->

## Why

We have every piece of the delivery pipeline **except the glue that runs it**,
and that glue currently lives in the operator's head. The proven loop is:

> scoped brief → a background agent implements it in an isolated `.worktrees/`
> branch → a **deterministic** gate (vitest / `go test` / no-LLM flow fixtures,
> never an LLM) proves the work → integrate to main (ff-merge, or
> **rebase/cherry-pick onto _current_ main** if a concurrent session moved it —
> lost-work-safe) → an **independent** re-verify re-runs the gate on the
> _merged_ commit (trust the gate, not the agent's "done") → clean up the
> worktree + branch. Fan that out over N briefs behind a single **merge-lock**
> so integrations serialize.

That is deterministic orchestration. The interpretive part (writing the code)
is already a story — `stories/cherny-loop/` is a goal-gated maker loop that
converges in an isolated worktree. The deterministic part — _integrate the
result safely, prove it independently, clean up, repeat over a list_ — is what
kitsoki exists to turn from un-recorded chat-glue into a replayable state
machine (`docs/proposals/process-design.md` §6, the meta-story principle).

The sharpest failure mode this targets is **lost work in rebases**: when a
concurrent session moves main between maker-start and integrate, a naive
ff-merge fails and the manual recovery (rebase the stale branch onto the new
main, re-run the gate) is exactly where work silently disappears. The best
solution is the most deterministic one — encode the recovery as a room, not a
runbook in someone's memory.

## What changes

Two new importable stories plus a smoothing pass on an existing one:

- **`stories/git-ops/`** gains parameterized, single-shot, **lost-work-safe**
  integrate + cleanup primitives (the gaps catalogued in
  `.context/gitops-ux-review.md`). This is the substrate the loop composes on.
- **`stories/ship-it/`** — a delivery-loop story that imports `cherny-loop`
  (the maker), the smoothed `git-ops` (integrate + cleanup), and a **new
  independent `verify` room**, chaining: `configure{brief, gate_command}` →
  `maker[cherny-loop, in worktree]` → `integrate[git-ops, rebase-onto-moved-main
  safe]` → `re-verify[re-run the gate on the MERGED commit]` → `cleanup` →
  `@exit:shipped | needs-human` (surfacing the real error on any failure).
- **`stories/fleet/`** — a parent story that fans `ship-it` over a brief list
  (the work-decomposition `decomposition.yaml` output), caps N concurrent
  makers, and holds a **merge-lock** so `integrate`/`re-verify` serialize.

Once all three ship, an operator hands the hub a `decomposition.yaml` and walks
away; kitsoki drives every brief to `shipped` or stops at `needs-human` with the
real error — no hand-driven git, no trust-the-agent integration, no lost work.

## Impact

- **Spans:** story (ship-it, fleet) + story-as-runtime-smoothing (git-ops). The
  git-ops slice has one engine-adjacent edge (stop swallowing `host.run`
  failures, surface `last_error`) flagged as an open question for a possible
  runtime micro-slice — see #1.
- **Net surface:** 2 new stories (~6 rooms ship-it, ~4 rooms fleet), ~3
  reworked git-ops rooms, ~12 no-LLM flow fixtures, 1 end-to-end mocked-maker
  flow.
- **Docs on ship:** `docs/stories/ship-it.md`, `docs/stories/fleet.md`,
  smoothing folded into `docs/stories/git-ops.md`; queue entries in
  `docs/proposals/README.md`.

## Design artifacts

Static representations of the key screens (the goal's "static HTML and TUI
representation") + the QA plan live alongside this proposal in
[`notes/delivery-loop/`](notes/delivery-loop/):

- [`ship-it-tui.txt`](notes/delivery-loop/ship-it-tui.txt) — ASCII/TUI render of
  every ship-it room state (configure → maker → integrate → verify → report, plus
  the `needs-human` anti-false-success terminal).
- [`fleet-tui.txt`](notes/delivery-loop/fleet-tui.txt) — the fleet board mid-fan-out
  (merge-lock held) + summary.
- [`ship-it-web-mock.html`](notes/delivery-loop/ship-it-web-mock.html) — a static
  HTML mock of the web pipeline-rail surface. Spatial-oracle anchors (the stage
  rail order, the verbatim gate block, the "on MERGED commit" badge, the red
  `last_error` card) are noted inline so a mock regression is pinpointable against
  a `render_web` capture.
- [`qa-and-demo-plan.md`](notes/delivery-loop/qa-and-demo-plan.md) — per-room flow
  coverage, the end-to-end mocked-maker fixture, and the kitsoki-ui-demo /
  kitsoki-ui-qa showcase outline.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | git-ops smoothing | story | Operand-as-slot, stop swallowing failures, force/confirm cleanup, one-shot lost-work-safe rebase-onto-moved-main | — | Shipped (`stories/git-ops/`) | [`git-ops-smoothing.md`](git-ops-smoothing.md) |
| 2 | ship-it loop | story | Import cherny-loop + git-ops + new `verify` room; chain configure→maker→integrate→re-verify→cleanup→shipped\|needs-human | 1 | Shipped (`stories/ship-it/`) | [`ship-it.md`](ship-it.md) |
| 3 | fleet fan-out | story | Fan ship-it over a brief list; cap N concurrent; merge-lock serializes integrate/verify | 2 (+ work-decomposition output) | Shipped (`stories/fleet/`) | [`fleet.md`](fleet.md) |
| 4 | bugfix convergence | story | Bugfix composes ship-it's `integrate+re-verify+cleanup` tail (stops reinventing a weaker one); RED→GREEN regression-gate (test fails pre-fix, passes post-fix); `direct-ship` vs `open-PR` exit | 2 | **Shipped** | narrative in [`stories/bugfix/README.md`](../../stories/bugfix/README.md) |

## Sequencing

```
#1 git-ops smoothing ──▶ #2 ship-it loop ──▶ #3 fleet fan-out
   (substrate: safe          (composes #1's   │    (fans #2 over a
    integrate/cleanup)         integrate+      │     brief list,
                               cleanup +       │     merge-lock)
                               cherny maker)   ▼
                                          #4 bugfix convergence
                                          (bugfix reuses #2's tail)
```

#1 ships and lands alone (it makes git-ops driveable as the goal demands,
independent of the loop). #2 is the smallest unit that proves the *single-brief*
loop end-to-end. #3 is pure orchestration over #2 — it adds no new git or maker
mechanism, only fan-out + a lock. #4 retrofits the *existing* bug-fix pipeline
onto #2's `integrate`/`re-verify`/`cleanup` tail and fixes its regression-gate
discipline — it depends only on the ship-it tail (#2), not on fleet.

**Dogfood the dogfood:** slice #4 is **implemented BY ship-it itself** once #2
lands — its brief ("converge bugfix onto the ship-it tail + RED→GREEN gate")
runs through `configure → maker → integrate → re-verify → cleanup → shipped`. The
delivery loop ships its own bug-fix convergence; that is the acceptance test for #2.

## Shared decisions

1. **The maker stays cherny-loop; ship-it never re-implements iteration.**
   cherny-loop already mints a worktree (`launch` → `iface.workspace.create`),
   runs red-before-green baseline, loops maker→checker→budget-guard, and exits
   `@exit:achieved` leaving `worktree_path` + `workspace_branch` populated in
   world (`stories/cherny-loop/rooms/gating.yaml:71`). ship-it imports it and
   reads that handoff seam — it does not fork the loop.

2. **Gate is deterministic by construction; the re-verify is the same command,
   re-run on the merged commit.** ship-it's `configure` carries one
   `gate_command` (a script gate — `go test`, `vitest`, `kitsoki test flows`).
   The maker uses it as cherny's checker; `re-verify` re-runs the *identical*
   command from the integration worktree after merge. Same gate, two evaluation
   sites — the maker's worktree and the merged main — so "passed in the branch"
   and "passes on main" are both proven. No LLM is on the gate path.

3. **`needs-human` is a first-class exit, and it always carries the real error.**
   Every failure arc (integrate conflict the auto-rebase can't resolve, gate red
   on the merged commit, cleanup refused on a dirty tree) routes to
   `@exit:needs-human` with `last_error` populated — never a swallowed `|| true`
   false-success (the `.context/gitops-ux-review.md` smoking gun). fleet parks
   that brief and continues the others.

4. **Merge-lock is fleet-scoped world state, not an engine primitive.** v1
   models the lock as a `merge_lock_held` world bool that fleet's `integrate`
   dispatch checks and the post-verify cleanup releases. If a second story ever
   needs cross-session mutual exclusion, extract a runtime lock then
   (`process-design.md` §7 open-question discipline) — do not invent it now.

## Cross-cutting open questions

1. **Concurrency model for fleet's N makers.** cherny-loop's autonomous
   self-loop completes in one turn only for small budgets (the
   `EmitIntentMaxDepth` cap, `stories/cherny-loop/app.yaml:13`); larger runs
   want the background-job runner. fleet's "N concurrent makers" therefore
   depends on whether per-brief makers run as background jobs or sequential
   dispatch-with-a-lock. *Lean:* v1 fleet runs makers **sequentially behind the
   merge-lock** (one brief fully ships before the next starts) — correct, fully
   no-LLM-gateable, and it matches work-decomposition.md's own v1 stance
   (sequential gated dispatch; parallel fan-out deferred). True parallel makers
   are a #3-follow-on, gated on `project_execution_modes_gate_deciders` /
   `task-fs-sandbox.md`. Flagged, not invented.

2. **Does the failure-surfacing in #1 need a runtime micro-slice?** Today
   git-ops binds `last_op_ok: ok` but its views don't always render
   `last_error`, and `|| true` hides non-zero exits before the bind even sees
   them (`.context/gitops-ux-review.md` #3). Dropping `|| true` and binding the
   real exit is a *story* change; if surfacing `last_error` needs an engine
   render seam it becomes a runtime slice. *Lean:* it's story-only (the bind +
   view already exist; the fix is to stop discarding the exit code) — but #1
   must verify this with a mutation flow before we close it.

## Non-goals

- **Authoring the briefs.** That's the existing design + work-decomposition
  pipeline (`docs/proposals/work-decomposition.md`). The delivery loop starts
  from an *accepted* `decomposition.yaml`.
- **A new runtime "iterate over a list" primitive.** fleet models the brief list
  as a cyclic graph over `fleet_briefs`, exactly as the `decompose` board does
  over `decomp_briefs`.
- **Real-LLM integration tests.** Every gate and flow in this epic is
  deterministic and no-LLM by construction (CLAUDE.md); the maker is mocked via
  cassette in the end-to-end fixture.
- **Cross-machine / remote orchestration and any `git push`.** The loop
  integrates to *local* main only.

## Dogfood note

ship-it/fleet, once built, **is the engine that would have run this very
session's delegations** — the manual orchestrator glue (scope a brief → spawn a
background maker in `.worktrees/<branch>` → run the deterministic gate →
ff-merge or rebase-onto-current-main → independently re-verify the merged commit
→ clean up, fanned out behind a merge-lock) was performed by hand to *produce
this proposal*. The proposal's acceptance test is therefore self-referential:
fleet driving these three slices' own briefs to `shipped` would reproduce the
hand-run that authored it. That closure — kitsoki building kitsoki's own
delivery loop with kitsoki — is the moat (`stories/dev-story/AGENTS.md`).
