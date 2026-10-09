# Fix a bug workflow

Take a filed ticket to a shipped, verified fix. The pipeline is
[`stories/bugfix/`](../../stories/bugfix/README.md) — the authoritative
reference for its rooms, exits, judge modes, cycle budgets, and the
RED→GREEN regression-gate discipline (the fix's own regression test must
fail before the fix and pass after — never a characterization test dressed
up as a regression test). This page covers only how you *start and drive*
it per surface.

## The three ways to run bugfix

- **Full pipeline** (`start` / `full_pipeline`): every room —
  `idle → reproducing → proposing → implementing → testing → reviewing →
  validating → done`.
- **Triage-only** (`triage` intent, or `bugfix_mode=triage`): a read-only
  verdict on whether the bug still reproduces in the current tree —
  **ALREADY-FIXED** / **STILL-LIVE** / **PARTIAL** / **UNCLEAR**, with
  concrete code evidence (file:line / function / regression test), and no
  managed workspace, no fix attempt. See
  [`stories/bugfix/rooms/triaging.yaml`](../../stories/bugfix/rooms/triaging.yaml).

Full and quick autostarts also run that triage as a built-in **pre-flight**
(`world.auto_triage`, default `true`): before any reproducer/judge/maker
budget is spent, the triager assesses the freshly-cut managed workspace, and an
**ALREADY-FIXED** verdict short-circuits to `@exit:triaged` — no fix run on
a bug that no longer exists. Any other verdict continues into `reproducing`
with the verdict posted as a checkpoint. Callers driving a known-live bug
(e.g. bench cells pinned to a pre-fix baseline) seed `auto_triage: false`
to skip it; when composed under dev-story, set the PARENT-level
`auto_triage` key (the import wrapper re-seeds `bf__auto_triage` on entry).
The pre-flight applies only to the idle **autostart** path — an operator
explicitly typing `start` / `full_pipeline` / `quick_fix` has already
chosen to run the pipeline and goes straight in.
- **Quick fix** (`quick_fix` intent): skips the reviewing + validating
  checkpoints for a trivial fix. See
  [`stories/bugfix/README.md#mode-shortcuts`](../../stories/bugfix/README.md#mode-shortcuts).

The pipeline ends one of two ways (the **`bugfix_exit`** world key):
**`direct-ship`** (default self-hosting loop — composes the shared
[`ship-it`](../../stories/ship-it/README.md) tail: integrate → verify →
cleanup, landing on local main) or **`open-PR`** (hand the close-out
artifact to [`pr-refinement`](../../stories/pr-refinement/README.md)).
An opt-in **`open-PR-merge`** exit walks `open-PR` all the way through
`pr-refinement`'s CI-watch and merge, reaching a real `@exit:merged` with no
parent hub required — see
[`stories/bugfix/README.md#the-exit-slot--direct-ship-vs-open-pr-delivery-loop-slice-4`](../../stories/bugfix/README.md#the-exit-slot--direct-ship-vs-open-pr-delivery-loop-slice-4).
`bugfix_exit` is a plain world var (default `direct-ship`) — a standalone
run selects it via `initial_world: {bugfix_exit: open-PR}` (or
`open-PR-merge`); dev-story instead derives it from its own
`bugfix_destination` choice.

## Prerequisite

Your repo must be onboarded (see [`../getting-started.md`](../getting-started.md))
so a ticket source (local `.artifacts/issues/bugs/`, committed
`issues/bugs/`, or a bound `host.gh.ticket`
GitHub repo) exists. The `repro_command`/`gate_command` convention itself
(a ticket's frontmatter `repro_command` seeds `world.gate_command`) is
documented in
[`stories/bugfix/README.md`](../../stories/bugfix/README.md).

## TUI

The primary surface. From dev-story, choose oversight before launch with
`oversight_gated`, `oversight_llm_review`, or `oversight_no_gate`. The default
gated posture maps to `judge_mode=human`, so each room's proposal pauses for an
explicit `bf__accept` instead of auto-cascading under LLM review. Use
`oversight_llm_review` when the LLM should decide checkpoints with human
fallback, and `oversight_no_gate` when the operation driver should accept
checkpoints without LLM review.

With a live agent backend
configured (or a recorded cassette/flow — see
[`../getting-started.md`](../getting-started.md) for the agent-backend
prerequisite; the "Verify" commands below run the same walk mocked, no
backend needed):

```
kitsoki run
> tickets                  # landing → ticket_search
> search_tickets open
> pick_ticket TKT-100
> go_bugfix                # forces bf regardless of ticket_type; `drive` also
                            # routes here when ticket_type is "bug"
> bf__start                # → reproducing_executing
> bf__proceed                # → reproducing_awaiting_reply
> bf__accept                # → proposing_executing
> bf__proceed                # → proposing_awaiting_reply
> bf__accept                # → implementing_executing
> bf__proceed                # → testing_executing
> bf__proceed                # → testing_awaiting_reply
> bf__accept                # → reviewing_executing
> bf__proceed                # → validating_executing
> bf__proceed                # → validating_awaiting_reply
> bf__accept                # → done_executing
> bf__proceed                # → done_awaiting_reply
> bf__accept                # bf @exit:done → pr.open_pr (if bugfix_exit=open-PR)
```

For a triage-only read: after `go_bugfix` lands you at `bf.idle`, type
`bf__triage` instead of `bf__start` — lands directly at a read-only verdict,
no workspace created. See
[`stories/dev-story/README.md#manual-tui-walkthrough`](../../stories/dev-story/README.md#manual-tui-walkthrough)
for the full walkthrough this is drawn from, including the feature-ticket
(`drive` → `impl`) branch.

Standalone (bugfix on its own, no dev-story hub):

```
kitsoki run stories/bugfix/app.yaml
```

**Verify the commands above yourself** (no LLM):

```
go run ./cmd/kitsoki test flows stories/bugfix/app.yaml --flows 'stories/bugfix/flows/triage_verdict_autonomous.yaml'
go run ./cmd/kitsoki test flows stories/bugfix/app.yaml --flows 'stories/bugfix/flows/done_opens_pr_and_merges.yaml'
```

(The full flow suite covers the pipeline end-to-end — run it with
`--flows 'stories/bugfix/flows/*.yaml'`; see
[`stories/bugfix/README.md`](../../stories/bugfix/README.md) for the index.
If a run stalls silently mid-flow, the
[`kitsoki-debugging`](../../.agents/skills/kitsoki-debugging/SKILL.md) skill
diagnoses the same state machine against your on-disk repo state without a
live session.)

## Web

Drivable — `kitsoki web` runs the same bugfix orchestrator over HTTP.
**Current caveat:** staged-gate and pacing issues have historically shown
up here; treat a full web-driven bugfix run as reliability-suspect
(proof-thin) rather than as solid as the TUI path.

## VS Code

Drivable via the same webview embed as [`file-a-bug.md`](file-a-bug.md).
A dedicated extension e2e spec
(`tools/vscode-kitsoki/tests/vscode-bugfix-walk.e2e.spec.ts`, real VS Code
via `kitsoki.flow`) drives the full pipeline `idle → … → done → @exit:done`
end-to-end from inside the editor — one happy-path proof, the same real
socket capture that pinned `host.ide.get_diagnostics`' wire shape (a
still-open follow-up item; no standalone IDE-integration architecture doc
exists yet).

## gh-agent

**Partially supported.** A `bug`-labeled issue mention routes to a **real**
dispatch of `stories/bugfix` — not a stub — inside a per-job managed clone
workspace (`.capsules/workspaces/gh-job-<id>`), replaying by default
(`KITSOKI_GHAGENT_HARNESS=live` for an explicit live opt-in). This is the
one story with a registered `realDispatchPlan` today. See
[`../architecture/github-agent.md#real-dispatch-per-job-managed-workspace`](../architecture/github-agent.md#real-dispatch-per-job-managed-workspace)
for the full mechanics and its honesty contract (a run may only report
"Done" once real `host.*` calls actually executed).

**Not yet supported:** there is no triage-only route exposed via a label —
a mention always attempts the full pipeline route table in
[`internal/ghagent/router.go`](../../internal/ghagent/router.go).
`KITSOKI_APP_DIR` is still a process-global env var, but the actually-racy
span (loading each job's `app.yaml`) is serialized behind a package mutex
so concurrent jobs no longer cross-contaminate — see
[`../architecture/github-agent.md#kitsoki_app_dir-concurrency`](../architecture/github-agent.md#kitsoki_app_dir-concurrency)
for the precise scope of what is and isn't serialized.

## Standing proof status

See the `fix-bug` row of
[`../testing/dev-workflow-matrix.md`](../testing/dev-workflow-matrix.md).
