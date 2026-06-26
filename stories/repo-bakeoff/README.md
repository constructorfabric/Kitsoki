# repo-bakeoff — generate & execute an external-repo bug-fix bake-off

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
`prepare` room passes it to `bench.py preflight --repo-dir` and then
`bench.py verify --repo-dir`, so local checkout, candidate profile, commit, oracle,
and RED/GREEN arming gates all run before any model spend. Both commands are
scoped to `world.bugs`, so a one-bug smoke does not arm the whole manifest.
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
| `prepare` | **deterministic · free · real** | `host.run → bench.py preflight --bug <world.bugs> [--repo-dir ...]` checks setup, then `bench.py verify --bug <world.bugs> [--repo-dir ...]` arms the selected hidden oracles (RED@baseline / GREEN@real-fix) — proves the bake-off is valid **before** any LLM is spent. |
| `running` | stub | Tracks the roster. The cost-bearing per-cell drive (`drive_cell.sh --candidate <m> --score`) is run **manually** — the only cost-bearing step. |
| `scoring` | deterministic | `host.run → bench.py summarize --results <artifact-results-dir> --deck <report-dir>/deck.slidey.json --markdown <report-dir>/report.md` rolls the per-cell verdicts up and writes project-specific report artifacts. If no scored cell JSON exists, it routes back to `running` instead of producing a misleading 0-cell report. |
| `reporting` | deterministic | Present the generated report markdown path and Slidey deck spec. |
| `slideshow` | deterministic | `host.slidey.render` → static-HTML deck + sidecar to `host.artifacts_dir` (exactly slidey-edit's rendering room). |
| `done` | gallery | The rendered report deck + the headline rollup. |

## Honesty (stories/AGENTS.md)

`running` is a thin stub — the cost-bearing cell execution lives in the harness
scripts and is run by hand (AGENTS.md no-LLM rule). The story orchestrates the
**free deterministic** pieces (`prepare` arms the oracles for real, `scoring`
summarizes real committed verdicts, `reporting`/`slideshow` deck it) end-to-end;
it never fabricates cell results. `prepare` is the load-bearing genuine step — it
runs `bench.py preflight` and `bench.py verify` live and proves the setup is
ready and the fixtures are armed.

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
also writes `.artifacts/external-bakeoff/report/report.md` and
`.artifacts/external-bakeoff/report/deck.slidey.json`. Accepting `running`
before at least one `results/cells/*.json` exists returns to `running`; run a
cell with `drive_cell.sh --score` or explicitly use `bench.py summarize
--allow-empty` outside the story if you are testing empty-report rendering.
