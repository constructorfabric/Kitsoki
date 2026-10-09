# Bug-fix evaluation adapter — not Capsule CI

> **Status: retained evaluation substrate, deprecated lifecycle/runtime.** This
> directory keeps real-issue manifests, hidden-oracle RED/GREEN arming,
> deterministic grading, cost accounting, and offline reports. It is not a
> second CI engine, workspace manager, or general coding-agent surface. Native
> pinned Capsule workspaces own source materialization, lifecycle, hygiene, and
> trace/receipt custody; normal project validation should start with Capsule CI.
> Arena is the preferred comparison/orchestration surface as its remaining
> consolidation lands.

A single, repo-agnostic harness that answers, for **any** project:

> *How well can kitsoki fix real bugs in this repo, and what is the **cheapest
> model / effort** that does it?*

This is the project-onboarding capability loop: **onboard → run N real bugs →
grade each fix deterministically against the regression test the real fix shipped
→ escalate cheap→expensive model/effort only when it buys an outcome → harden the
project config / prompts.** It works on kitsoki's own bugs and on third-party
OSS repos with the same contract.

> **History:** this used to be two harnesses — a `bakeoff.yaml`/`run_cell.sh`/
> `score.py` special-case for kitsoki's OWN bugs, and `external/` for third-party
> repos. They are now **one**: `external/` is the harness, and kitsoki-self is
> just another project at [`external/projects/kitsoki`](external/projects/kitsoki).
> See [`.context/bugfix-bakeoff-consolidation.md`](../../.context/bugfix-bakeoff-consolidation.md).

## Where everything lives

```
tools/bugfix-bakeoff/
  external/
    bench.py                 # deterministic grader + fixture verifier + cost (no LLM)
    drive_cell.sh            # run ONE cell live (COST): prepare native Capsule → drive → score
    escalate.sh              # run a project's bugs up a cheap→expensive ladder (COST)
    candidates.yaml          # the model/effort axis + named escalation ladders
    projects/<name>/
      manifest.yaml          # repo + bugs + oracle-injection contract
      oracles/<bug>.<ext>    # the regression test the real fix shipped, isolated
  aggregate.py               # roll cells → results/summary.json (deck header source)
  deck/                      # deterministic slidey deck (docs/decks/bugfix-bakeoff.slidey.json)
  results/                   # cells/ + summary.json (durable, deck regenerates from these)
```

Shipped projects: [`query-string`](external/projects/query-string) (small mature
JS OSS), [`gears-rust`](external/projects/gears-rust) (large private Rust
monorepo), [`kitsoki`](external/projects/kitsoki) (kitsoki's own go+ts bugs).

## The story / agent / skill that encapsulate it

- **Story:** [`stories/bench-bugfix`](../../stories/bench-bugfix) — the generic,
  provider-neutral `bugfix` pipeline. It bakes nothing; every knob (ticket,
  workdir, test command, judge mode) is seeded per session via
  `session.new {initial_world}`. `drive_cell.sh` drives THIS story.
- **Agent:** the live drive is delegated to the **`kitsoki-mcp-driver`** agent via
  [`tools/mcp-drive/drive.sh`](../mcp-drive) (raw `claude -p` + studio MCP). The
  worker model is whatever the candidate's profile selects; the cheap orchestrator
  only advances the pipeline.
- **Skills:** [`external-repo-bakeoff`](../../.agents/skills/external-repo-bakeoff)
  (onboard a third-party repo end-to-end) and
  [`matrix-task-comparison`](../../.agents/skills/matrix-task-comparison) (the
  kitsoki-vs-X matrix + report/deck). `dogfood-marathon` is distinct (drives
  kitsoki's OWN bugs live with no deterministic third-party oracle).

## Onboard a new project (add a repo = drop a manifest)

```yaml
# external/projects/<name>/manifest.yaml
project:
  id: <name>
  repo: https://github.com/owner/repo.git   # or "." + local_only: true
  install: <install cmd>
  onboard_app: "@kitsoki/dev-story"
  oracle: { inject: append|write, target: <path>, run: '<test cmd with {match}>' }
bugs:
  - id: <id>
    baseline_sha: <fix_sha^>      # bug present
    fix_sha: <fix_sha>
    fix_source: <dir or ".">      # what the GREEN verify leg overlays
    oracle_test: oracles/<id>.<ext>   # the real PR's regression test, isolated
    oracle_match: <runner filter>
    ticket: |                     # the bug as the pipeline sees it — NO fix/oracle leak
      ...
```

Then **arm every fixture before spending a cent** (RED@baseline, GREEN@real-fix):

```sh
python3 external/bench.py verify --project <name>           # remote repo
# local/private repo: verify against a disposable, read-only native Capsule
# workspace or other explicitly managed checkout; never mutate a primary HEAD.
python3 external/bench.py verify --project <name> --repo-dir <managed-capsule-path>
```

## Run one cell (COST — operator-only, never CI)

```sh
external/drive_cell.sh --project <name> --bug <id> --candidate glm-5.2 --score
#   --no-drive   prepare native Capsule + print prompt only (free, for review)
```

## Escalate cheap→expensive (the onboarding answer)

`escalate.sh` runs each bug up an ordered candidate **ladder** (model AND effort),
stopping at the first rung that reaches `solved`:

```sh
external/escalate.sh --project query-string --ladder default          # COST
external/escalate.sh --project query-string --ladder default --dry-run # free: print the plan
external/escalate.sh --project query-string --rungs gpt-5.3-spark,glm-5.2,opus-4.8
```

Effort is a property of the harness **profile** (`session.new` has no effort
param), so a ladder rung is just a candidate row pointing at a profile with that
(model, effort). See the header of [`external/candidates.yaml`](external/candidates.yaml).
The result is `.artifacts/qs-bakeoff/results/escalation-<project>.tsv`:
the cheapest solving rung per bug.

> **`local_only` projects (kitsoki-self, gears-rust) drive too.** They run from
> an artifact-local, pinned native Capsule under
> `.capsules/projects/external-bakeoff/`, never a raw worktree or clone. The
> cell source is disposable after deterministic grading; prompts, traces,
> scores, and close receipts remain under `.artifacts`. You can still grade
> without a live drive through `bench.py verify`/`score` against an explicitly
> managed Capsule checkout.

## Report + deterministic slidey deck (offline, zero re-spend)

```sh
python3 aggregate.py
python3 aggregate.py --generated-at 2026-06-24T00:00:00Z --emit-agenteval \
  --deck ../../.artifacts/bugfix-bakeoff/2026-06-24t00-00-00z/deck.slidey.json \
  --markdown ../../.artifacts/bugfix-bakeoff/2026-06-24t00-00-00z/report.md
```

`aggregate.py` reads the kitsoki project manifest + `candidates.yaml` for headers
and `results/cells/*.json` for data. The Markdown report and Slidey deck
regenerate under `.artifacts/bugfix-bakeoff/<run>/` with **no LLM calls**.

## Cost discipline

`drive_cell.sh` and `escalate.sh` are the only cost-bearing pieces — real LLMs,
operator-run, never in CI. `bench.py` (verify/score/cost/summarize), `aggregate.py`,
and the deck build make no LLM calls and are free. The gated scaffold check
`make qs-bakeoff` (and `make gears-bakeoff`) proves onboarding + every armed
fixture **before** any spend.
