# repo-bakeoff — optional external evaluation workflow

> **Not project CI.** This story is an operator-facing wrapper around the
> external evaluation adapter. Native pinned Capsules own source lifecycle and
> Capsule CI owns normal project validation. Use this only for hidden-oracle
> model evaluation, never as a general workspace/runtime path.

A drivable kitsoki story that wraps the **`external-repo-bakeoff`** method
([`.agents/skills/external-repo-bakeoff/SKILL.md`](../../.agents/skills/external-repo-bakeoff/SKILL.md))
and its repo-agnostic harness ([`tools/bugfix-bakeoff/external`](../../tools/bugfix-bakeoff/external))
into a workflow that ends in a **baked static-HTML slidey report** — the
"should I use kitsoki for my project?" study.

```
kitsoki run stories/repo-bakeoff/app.yaml
```

Sibling of [`stories/task-bakeoff`](../task-bakeoff/README.md): that one wraps the
**internal** matrix harness (`tools/bugfix-bakeoff`, kitsoki's own bugs); this one
wraps the **external** harness (`bench.py` + `drive_cell.sh`) — onboard a real
third-party repo, fix real filed-issue bugs through the bugfix pipeline, grade
each fix against the PR's own hidden oracle, and deck it.

For a private/heavy repo, seed `repo_dir` with a local checkout path. The story's
`prepare` room passes it to `bench.py preflight --repo-dir`,
`bench.py verify --repo-dir`, and `prepare_handoffs.sh --repo-dir`, so local
checkout, candidate profile, commit, oracle, RED/GREEN arming, no-drive Capsule
prep, and MCP prompt leak-audit gates all run before any model spend. Commands
are scoped to `world.bugs`, so a one-bug smoke does not arm or prepare the whole
manifest.
`gears-rust` is the reference case for this path; see
[`docs/recipes/repo-history-training-gears-rust.md`](../../docs/recipes/repo-history-training-gears-rust.md).

## Rooms

```
idle ─start─▶ configure ─accept─▶ prepare ─accept─▶ running ─accept─▶ scoring ─(auto)─▶
   reporting ─accept─▶ slideshow ─(auto render)─▶ done ─accept─▶ @exit:done
```

| Room | Split | What it does |
|---|---|---|
| `idle` | deterministic | Park; `start` boots the bake-off. |
| `configure` | deterministic | Declare the matrix (bugs × candidates); compute the cell roster; optionally carry `repo_dir` for private/local repos. |
| `prepare` | **deterministic · free · real** | `host.run → bench.py preflight --bug <world.bugs> [--repo-dir ...]` checks setup, `bench.py verify --bug <world.bugs> [--repo-dir ...]` arms the selected hidden oracles (RED@baseline / GREEN@real-fix), then `prepare_handoffs.sh --bug <world.bugs> --candidate <world.candidates>` writes and audits no-drive handoffs. This proves the bake-off is valid and reviewable **before** any LLM is spent. |
| `running` | stub | Tracks the roster, shows the handoff-audit artifact, and renders exact per-cell `drive_cell.sh --score` commands for the selected matrix. The commands are run **manually** — the only cost-bearing step. |
| `scoring` | deterministic | `host.run → bench.py completion --results <artifact-results-dir> --markdown <report-dir>/completion.md` records the evidence verdict, then `bench.py summarize --results <artifact-results-dir> --deck <report-dir>/deck.slidey.json --markdown <report-dir>/report.md` rolls the per-cell verdicts up and writes project-specific report artifacts. If no cell JSON exists, it routes back to `running` instead of producing a misleading 0-cell report. Each scoring pass also writes `.artifacts/external-bakeoff/status/repo-bakeoff-completion-<project>.json` with machine-readable `completed`, `status`, `requires_drive`, and `repairable` fields. |
| `reporting` | deterministic | Present the generated report markdown path and Slidey deck spec. |
| `slideshow` | deterministic | `host.slidey.render` → static-HTML deck + sidecar to `host.artifacts_dir` (exactly slidey-edit's rendering room). |
| `done` | gallery | The rendered report deck + the headline rollup. |

## Honesty (stories/AGENTS.md)

`running` is a thin stub — the cost-bearing cell execution lives in the harness
scripts and is run by hand (AGENTS.md no-LLM rule). The story orchestrates the
**free deterministic** pieces (`prepare` arms the oracles for real, `scoring`
records the completion verdict and summarizes real committed verdicts,
`reporting`/`slideshow` deck it) end-to-end; it never fabricates cell results.
`prepare` is the load-bearing genuine step — it runs `bench.py preflight`,
`bench.py verify`, and `prepare_handoffs.sh` live and proves the setup is ready,
the fixtures are armed, and the MCP handoff prompts are reviewable without
leaking hidden oracles.

## The cost-bearing cells (operator-run)

```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
    --project query-string --bug qs1 --candidate gpt-5.5 --score
```
Writes `results/cells/<bug>-<candidate>-kitsoki.json`, which `scoring` rolls up.
See the [external harness README](../../tools/bugfix-bakeoff/external/README.md).

For `gears-rust` or another local-only repo:

```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
    --project gears-rust --bug bug1 --candidate gpt-5.3-spark \
    --repo-dir /Users/brad/code/gears-rust --score
```

By default `drive_cell.sh` writes generated results under
`.artifacts/external-bakeoff/results/`. The story's `results_dir` defaults to the
same directory, expressed relative to `harness_dir`, so `scoring` summarizes the
actual live-driver output instead of the checked-in reference results. `scoring`
also writes `.artifacts/external-bakeoff/report/completion.md`,
`.artifacts/external-bakeoff/report/report.md`,
`.artifacts/external-bakeoff/report/deck.slidey.json`, and
`.artifacts/external-bakeoff/status/repo-bakeoff-completion-<project>.json`. The completion report
distinguishes `ready-to-drive`, `complete-with-pending`, and fully live-scored
evidence. Accepting `running` before at least one `results/cells/*.json` exists
returns to `running`; run a cell with `drive_cell.sh --score` or explicitly use
`bench.py summarize --allow-empty` outside the story if you are testing
empty-report rendering.
If a provider/profile is blocked before a model actually attempts the bug, write
a pending cell with `bench.py pending` and then score. Pending cells report as
`pending`, not `failed`, and are excluded from the solve-rate denominator.

The `prepare` room first prepares and audits no-drive handoffs with:

```sh
./prepare_handoffs.sh \
  --project gears-rust \
  --bug bug1,bug4 \
  --candidate opus-4.8,gpt-5.3-spark \
  --repo-dir /Users/brad/code/gears-rust \
  --markdown ../../../.artifacts/external-bakeoff/readiness/repo-bakeoff-handoffs.md
```

That writes baseline worktrees/prompts under `.artifacts/external-bakeoff/` and
fails if metadata is stale, required prompt context is missing, or hidden oracle
or real-fix hints leak into the MCP prompt.

The `running` room then computes the copy-ready live commands with:

```sh
host.bakeoff.run --op bench --cwd tools/bugfix-bakeoff/external -- drive-plan \
  --project gears-rust \
  --bug bug1,bug4 \
  --candidate opus-4.8,gpt-5.3-spark \
  --repo-dir /Users/brad/code/gears-rust
```

That command is deterministic and free. It does not call a model; it mirrors the
selected matrix into the exact cost-bearing commands the operator should run
after the handoff audit passes.
