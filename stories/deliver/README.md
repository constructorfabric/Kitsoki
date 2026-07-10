# deliver — epic front-door of the delivery loop

Hand `deliver` a path to an epic/proposal markdown and it decomposes it into
independently-shippable briefs, lints the manifest deterministically, runs it
past an adversarial reviewer, and fans [`stories/fleet/`](../fleet/) (which
runs [`stories/ship-it/`](../ship-it/) per brief behind a merge lock) over the
result.

Layering is acyclic: **deliver → fleet → ship-it**. deliver is the only entry
above fleet; fleet never imports deliver.

`deliver` is the canonical decomposition story: it has absorbed the
work-decomposition skill's richer manifest schema, the budgeted refine loop +
adversarial review gate, managed re-decompose via the decompose-update
transaction, and `dev-story` reachability (below) — all shipped. This README
is the room-by-room reference; see
[`docs/stories/deliver.md`](../../docs/stories/deliver.md) for the narrative
overview (story graph, dev-story integration, per-surface no-LLM proofs).

## Reachable from dev-story

[`stories/dev-story/`](../dev-story/) imports `deliver` (alias `deliver`,
entry `configure`; `world_in: epic_path` reads the child's `design_file`) and
offers it as the **decompose-vs-direct** sibling to the `impl` import — an
operator choice, never a size heuristic (proposal Open Question 3):

- **`design_done`** (right after a proposal publish): `implement`
  (`go_implementation`) drives the freshly-filed ticket straight into
  `impl`; `decompose` (`go_deliver`) drives the just-published proposal
  straight into `deliver.configure` instead.
- **`landing`** (picking up a proposal published earlier): the `decompose`
  quick action / `go_deliver` intent is offered whenever `design_file` is
  already set.

Both arcs land on `@exit:done` → `landing` (`status="delivered"`,
`delivery_summary` lifted from the child) or `@exit:needs-human` → `landing`
(`status="needs-human"`, `last_error` lifted). See
[`stories/dev-story/flows/design_to_decompose_to_impl.yaml`](../dev-story/flows/design_to_decompose_to_impl.yaml)
(the full publish → decompose → review → fleet fan-out walk) and
[`stories/dev-story/flows/deliver_router_picks_arc.yaml`](../dev-story/flows/deliver_router_picks_arc.yaml)
(both arcs routing correctly from the same published proposal).

## Pipeline

```
configure {epic_path}
  └─ start ─▶ decompose (scripts/detect_prior_decomposition.star — no LLM)
               ├─ fresh ──▶ decomposing (host.agent.task: read epic, write decomposition YAML)
               │              ├─ ok ──▶ lint (host.starlark.run — deterministic, no LLM)
               │              └─ host error ─▶ decompose_error ─▶ @exit:needs-human {last_error}
               ├─ redecompose (a prior decomposition.yaml already exists) ──▶ redecompose
               │      (host.agent.task: author an additive delta, no overwrite)
               │        └─▶ redecompose_apply (host.run: tools/decomposition-update/apply_delta.py
               │             — the decompose-update transaction, --list-key briefs --skip-validate)
               │              ├─ ok ──▶ lint
               │              └─ fail/error ─▶ redecompose_error ─▶ @exit:needs-human {last_error}
               └─ host error ─▶ decompose_error ─▶ @exit:needs-human {last_error}

lint ├─ pass ─▶ review (host.agent.decide — adversarial)
     │           ├─ accept ─▶ fleet import (entry: load)
     │           │             — fan ship-it over every brief
     │           │             └─ @exit:done {delivery_summary ← fleet_summary}
     │           └─ revise ─▶ decompose (budgeted refine)
     └─ fail ─▶ decompose (budgeted refine)

any refine-budget exhaustion (lint-fail or review-revise) ─▶ @exit:needs-human {last_error}
```

Every failure arc exits `@exit:needs-human` with a non-blank `last_error`. The
lint-fail and review-revise refine edges share ONE `refine_cycle` /
`refine_budget` counter — one budget for the whole decompose↔lint↔review ring
(default budget: 3 cycles).

## Rooms

| Room | Does |
|---|---|
| `configure` (root) | Captures `epic_path` (`start epic_path=<path>`, or seeded via `initial_world`); defaults `decomposition_path` to `.artifacts/deliver/decomposition.yaml`. |
| `decompose` | Router (no LLM): `host.starlark.run` [`scripts/detect_prior_decomposition.star`](scripts/detect_prior_decomposition.star) checks whether `decomposition_path` already exists on disk, ONCE per run (`refine_cycle == 0`, latched by `redecompose_checked`). `"fresh"` → `decomposing` (unchanged decomposer invoke). `"redecompose"` → `redecompose` (a prior manifest exists — do not overwrite it). |
| `decomposing` | `host.agent.task` invokes the `decomposer` agent (`once: true` — clear `decomposition_briefs` to force a redo). The agent reads the epic, **writes the decomposition YAML to `decomposition_path`** (the format fleet's `load` parses), and submits a manifest validated against [`schemas/decomposition.json`](schemas/decomposition.json); `bind: decomposition_briefs, coverage_note`. On a re-entry (lint fail or review revise), `refine_feedback` is passed into the decomposer's prompt args so it addresses the specific gap instead of resubmitting blind. |
| `decompose_error` | Host-call failure surface; auto-routes to `@exit:needs-human` (the engine sets `last_error`). |
| `redecompose` | `host.agent.task` invokes the `decomposer` agent again, but with [`prompts/redecompose_delta.md`](prompts/redecompose_delta.md): read the epic + the PRIOR manifest and **write an additive delta document** (`trigger`/`provenance`/`operations`, `add_change` only) to `redecompose_delta_path`, then submit a lightweight confirmation validated against [`schemas/redecompose-delta.json`](schemas/redecompose-delta.json) (`{trigger, added}`). |
| `redecompose_apply` | `host.run` invokes [`tools/decomposition-update/apply_delta.py`](../../tools/decomposition-update/README.md) directly — the SAME decompose-update transaction `stories/decompose-update/` wraps, called here with `--list-key briefs` (a deliver manifest, not the dev-workflow ledger graph) and `--skip-validate` (deliver's own `lint` room, entered next, is the deterministic check for this shape). Binds a tri-state `redecompose_apply_route` (`""`/`"ok"`/`"fail"` from `stdout_json.route`, reset before the invoke) rather than branching on the bool `redecompose_apply_ok` directly — a bool's zero-value would coincide with "failed" and break the flow-test harness's discovery pass. `ok` → `lint`; `fail` → `redecompose_error`. |
| `redecompose_error` | Delta-authoring or apply-transaction failure surface; auto-routes to `@exit:needs-human`, mirroring `decompose_error`. |
| `lint` | `host.starlark.run` [`scripts/lint_decomposition.star`](scripts/lint_decomposition.star) over `decomposition_path` → `{route: ok\|fail, error}`; resets `lint_route` before the invoke so stale values never route (the board.yaml pattern). Fail routes to `decompose` with `refine_feedback` set to the lint error and `refine_cycle` incremented — unless the budget is already spent, which routes straight to `@exit:needs-human`. |
| `review` | `host.agent.decide` invokes the `reviewer` agent (prompt [`prompts/review_adversary.md`](prompts/review_adversary.md), schema [`schemas/review-decision.json`](schemas/review-decision.json)) — attacks per-brief buildability and whether `coverage_note` actually covers the epic. No `once:` guard: re-runs fresh on every entry, including after a refine cycle. `accept` → `fleet`; `revise` → `decompose` with the reviewer's `questions` folded into `refine_feedback` (budgeted, same counter as lint). |
| `review_error` | Reviewer host-call failure surface; auto-routes to `@exit:needs-human`, mirroring `decompose_error`. |

A structural note worth keeping in mind when touching `decompose`/`redecompose`:
the flow-test harness resolves a whole turn in two passes — a discovery walk
using the world AS OF TURN START (to find which invokes need dispatching),
then the real sequential dispatch. An `emit_intent:` guard that would match a
bind-target's UNSET default (e.g. a negated guard, or branching on a bool
whose zero-value is one of the two outcomes) gets committed during the
discovery pass, before the real result exists. Every branch in this story
(`lint_route`, `decompose_route`, `redecompose_apply_route`, `review_verdict`)
is therefore an explicit `== '<value>'` check against values the default
never equals — never a catch-all/negation.

## The manifest contract

Top-level, optional: `coverage_note` — the completeness claim (how the briefs
together fully cover the epic/proposal) that `review` attacks.

`briefs:` — an ordered list; each brief:

- `id` — unique, `^[a-z][a-z0-9-]*$` (**required**)
- `brief` — self-contained task for the maker agent, min 10 chars (**required**)
- `gate_command` — deterministic shell command, exit 0 = success, and
  **RED at baseline** (see [`prompts/decompose.md`](prompts/decompose.md))
  (**required**)
- `deps` — ids that must ship first, acyclic (optional, default `[]`)
- `title`, `kind` (`story\|runtime\|tui\|tracing\|test\|docs`), `scope[]`
  (write-boundary globs), `acceptance[]`, `risk` (`low\|medium\|high`) —
  optional richer fields absorbed from the work-decomposition skill's schema.

The lint (no LLM, [`scripts/lint_decomposition.star`](scripts/lint_decomposition.star))
enforces: at least one brief; ids unique and non-empty; non-empty `brief` and
`gate_command` per brief; every dep references a known id; no dependency
cycle; when present, `acceptance` is non-empty and every `scope` glob is
bounded inside the repo (repo-relative, no `..` escape, parent dir exists). It
also accepts the work-decomposition skill's field aliases (`agent_brief` →
`brief`, `test_plan` → `gate_command`, `depends_on` → `deps`). What the lint
CANNOT check — whether a brief is actually buildable as scoped, and whether
`coverage_note` overclaims — is the `review` room's job.

## World / agents / exits

Key world: `epic_path`, `decomposition_path`, `decomposition_briefs`,
`coverage_note`, `lint_route`, `review_verdict`, `review_reason`,
`review_questions`, `refine_feedback`, `refine_cycle`, `refine_budget`,
`last_error`, `delivery_summary`, plus `base_branch` / `main_worktree_path`
projected into fleet. Managed re-decompose: `decompose_route`
(`""`/`"fresh"`/`"redecompose"`), `redecompose_checked`,
`redecompose_delta_path`, `redecompose_versions_dir`, `redecompose_event_log`,
`redecompose_trigger`, `redecompose_added`, `redecompose_apply_ok`,
`redecompose_apply_route`, `redecompose_apply_stdout`.

Two agents:

- `decomposer` (`claude-opus-4-8`, tools `Read/Edit/Write`, no
  Bash/subagents; gates are designed from static evidence and executed
  downstream).
- `reviewer` (`claude-sonnet-4-6`, tools `Read`; skeptic — attacks
  buildability and completeness, no shell/subagents).

Exits: `done` (requires `delivery_summary`), `needs-human` (requires
`last_error`).

## Flows (no-LLM)

```
go run ./cmd/kitsoki test flows stories/deliver/app.yaml
```

| Flow | Proves |
|---|---|
| [`decompose_happy`](flows/decompose_happy.yaml) | 3-brief manifest → real Starlark lint passes → adversarial review accepts → lands in `fleet.load` (fleet's own flows cover fan-out). |
| [`decompose_error`](flows/decompose_error.yaml) | Decomposer host error → `decompose_error` → `@exit:needs-human` with `last_error`. |
| [`lint_rejects_cycle`](flows/lint_rejects_cycle.yaml) | Dependency cycle → budgeted refine loop back to `decompose` (NOT a hard exit) → the refined manifest breaks the cycle → lint passes → review accepts → `fleet.load`. |
| [`lint_rejects_missing_dep`](flows/lint_rejects_missing_dep.yaml) | Dangling dep id with the refine budget ALREADY spent (`refine_budget: 0` seeded) → immediate `@exit:needs-human` with the wrapped budget-exhausted error. |
| [`lint_fail_refine_loop`](flows/lint_fail_refine_loop.yaml) | The generic refine-loop mechanism: lint fail → `decompose` re-arms with `refine_feedback` (the lint error) and `refine_cycle` incremented → second manifest passes → review accepts → `fleet.load`. |
| [`review_revise_loop`](flows/review_revise_loop.yaml) | Lint passing is necessary but not sufficient: adversarial review returns `revise` → `decompose` re-arms with the reviewer's `questions` as `refine_feedback` → second pass → review `accept`s → `fleet.load`. |
| [`refine_budget_exhausted`](flows/refine_budget_exhausted.yaml) | The reviewer keeps revising past the shared `refine_cycle`/`refine_budget` counter → honest `@exit:needs-human` with a specific `last_error` naming the last review reason, instead of looping forever. |
| [`rich_schema_happy`](flows/rich_schema_happy.yaml) | A manifest carrying `coverage_note` + per-brief `title/kind/scope/acceptance/risk` lints clean and reaches `fleet.load` — proves the absorbed skill schema/lint fields, not just the bare `id/brief/gate_command/deps` contract. |
| [`slidey_decomposition`](flows/slidey_decomposition.yaml) | Tour-shaped happy path (`epic_path` seeded in `initial_world` so `start` needs no slot) for the web/no-LLM demo. |
| [`redecompose_managed_delta`](flows/redecompose_managed_delta.yaml) | A prior `decomposition.yaml` already exists (`exists: true` in the inspect cassette) → `decompose` routes to `redecompose` (additive delta authored) → `redecompose_apply` (`host.run`, STUBBED) applies it via the decompose-update transaction → `lint` re-validates the post-apply manifest → review accepts → `fleet.load`. Never a blind decomposer overwrite. |

Both agents are mocked in every flow (`host_handlers.host.agent.task` /
`.host.agent.decide` `by_call`, or a `host_cassette:` when a flow needs
DIFFERENT canned responses across repeated entries into the same room — e.g.
`review_revise_loop`'s revise-then-accept, or `refine_budget_exhausted`'s
always-revise). Lint and fleet-load run **real Starlark** against the inspect
cassettes in [`cassettes/`](cassettes/); a multi-read cassette's interactions
are consumed IN ORDER, one per `ctx.fs.read` call, which is how the refine-loop
fixtures serve a failing manifest on the first lint and a passing one on the
second. Fixture epics live in [`testdata/`](testdata/); [`agent-bench/`](agent-bench/)
holds a GLM decomposer benchmark script.

## See also

- [`stories/fleet/`](../fleet/) — brief fan-out behind the merge lock.
- [`stories/ship-it/`](../ship-it/) — single-brief maker → integrate →
  re-verify loop.
- [`stories/decompose-update/`](../decompose-update/) — a standalone
  review-then-apply demo of the SAME transaction (`review.yaml` is the
  decide-schema-bind-gate shape this story's `review` room lifts); `redecompose`
  above is the real caller, invoking
  [`tools/decomposition-update/apply_delta.py`](../../tools/decomposition-update/README.md)
  directly rather than importing the demo story.
- [`stories/bugfix/rooms/proposing.yaml`](../bugfix/rooms/proposing.yaml) —
  the budgeted refine-loop pattern (`refine_cycle`/`refine_budget`) this
  story's refine edges follow.
- [`.agents/skills/work-decomposition/`](../../.agents/skills/work-decomposition/SKILL.md)
  — the manual twin of this pipeline for by-hand runs; its schema is a copy of
  this story's `schemas/decomposition.json`, kept identical rather than left
  to drift.
