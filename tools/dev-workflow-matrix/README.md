# dev-workflow-matrix

The standing 5-workflow × 4-surface × 2-repo support matrix (WS-F F1 of
`.context/dev-workflows-surface-matrix-plan.md`), as a generated artifact
instead of a hand-maintained table.

- `manifest.yaml` — the hand-edited truth: every `{workflow, surface, repo}`
  cell with a status (`works` / `proof-thin` / `gap` / `out-of-scope`), a
  one-line reason, and optional pointers to standing
  `schemas/completion-state.schema.json` verdict files — one per proof class:
  - **mechanical** (`check_type: replay`) — the no-LLM per-commit proof.
  - **experience** (`check_type: docs-fidelity | ux-heuristic |
    journey-verdict`) — the persona-judged proof (plan WS-G).
- `generate.py` — validates the manifest (full cross-product coverage, no
  duplicates, legal statuses/check_types), reads any verdict files, and
  renders `docs/testing/dev-workflow-matrix.md` with both verdicts per cell
  plus verdict freshness (file mtime date). Cells without a verdict render as
  "no standing verdict" — honest, not an error. Output is deterministic.
- `generate_test.py` — run with `python3 tools/dev-workflow-matrix/generate_test.py`
  (or `make dev-workflow-matrix-check`); also asserts the checked-in matrix
  matches a fresh render of the checked-in manifest.

Regenerate after editing the manifest:

```
make dev-workflow-matrix
```

## The standing gate (WS-F F1 exit)

`run_checks.py` is the check suite: it maps each matrix cell that has a real,
runnable, no-LLM proof today onto a plain invocation (`go run ./cmd/kitsoki
test flows <app.yaml>` for the `prd` / `bugfix` / `dev-story` / `deliver`
story flow suites, plus one `tools/product-journey/run.py
--gate driver-replay` pass as a `journey-verdict` experience-class pilot),
runs it, and writes one `schemas/completion-state.schema.json`-conformant
verdict JSON per cell into a verdicts directory — keyed by `(workflow,
surface, repo, check_type)` via `axis.workflow`/`axis.surface`/`target_id`/
`check_type`, independent of any manifest `verdicts.*.path` pointer. It
deliberately does not go through `tools/arena`'s container executor (none of
these checks need a container), but reuses arena's check-type vocabulary and
completion-state shape.

`generate.py --verdicts-dir DIR` ingests that directory: a verdict found
there always wins over a manifest pointer for the same cell + proof class. A
cell with no verdict in the directory (and no manifest pointer either) keeps
the manifest's static status standing — the honest default. A `failed` /
`blocked` verdict (or an `infra:*` health) downgrades the cell to at least
`gap`; a verdict older than `--stale-days` (default 14) downgrades a `works`
cell to `proof-thin`. `out-of-scope` cells are never downgraded. `--gate`
additionally exits non-zero if any cell the manifest marks `works` has
regressed — the WS-F F1 exit criterion ("a red cell blocks declaring the
workflow supported").

```
make dev-workflow-gate
```

runs the check suite into `.artifacts/dev-workflow-matrix/verdicts/` (never
committed), writes a LIVE verdict-aware report to
`.artifacts/dev-workflow-matrix/dev-workflow-matrix.live.md`, and — only if
that passes — regenerates the checked-in, manifest-only
`docs/testing/dev-workflow-matrix.md`. The checked-in doc never carries
verdict-derived timestamps or a pass/fail from a given run (it is a pure
function of the manifest, same as before this gate existed); freshness and
live pass/fail live only in the uncommitted gate output. `run_checks_test.py`
and the verdict-ingestion cases in `generate_test.py` cover the writer/reader
contract, downgrade-on-fail, stale detection, and the `--gate` exit-code
contract — all offline, zero LLM, zero docker (`run_checks.py`'s tests inject
a fake subprocess runner rather than really invoking `go run`/`python3`).

`go run ./cmd/kitsoki test routing` does not exist yet (checked 2026-07-05) so
the routing-fixture check the WS-F plan names is not wired here — add a
`CheckDef` for it once that CLI lands.

## Experience-check runners (WS-G G1: docs-fidelity + ux-heuristic)

`run_checks.py` only writes the **mechanical** proof class
(`check_type: replay`, plus the one `journey-verdict` pilot). The other two
experience check types the schema's `check_type` discriminator names —
`docs-fidelity` and `ux-heuristic` — have their own runners, siblings of
`run_checks.py` in this directory:

- `docs_fidelity.py` — dispatches a persona agent whose ONLY map is one
  canonical `docs/` narrative (plan G3), collects a per-claim
  truthful/stale/missing score plus an overall pass, and writes a
  `check_type: "docs-fidelity"` verdict. A doc that doesn't exist, or an
  agent that reports any stale/missing claim, is an honest `failed` — never a
  hygiene footnote.
- `ux_heuristic.py` — takes already-captured evidence for a cell (web
  screenshots, `render_tui_png` output, or a VS Code webview capture),
  reuses the **same catalog** as the `kitsoki-ui-review` skill
  (`.agents/skills/kitsoki-ui-review/heuristics.yaml` — no second catalog
  invented), dispatches a vision-critique agent against it, and writes a
  `check_type: "ux-heuristic"` verdict. Like that skill's own gate, this
  runner recomputes pass/fail from finding severities itself rather than
  trusting the agent's self-reported `overall_pass` — an `error`-severity
  finding always fails the check.

**Home + shape (why these are NOT arena plugins):** `tools/arena/arena/checks.py`
already has `docs-fidelity` / `ux-heuristic` / `journey-verdict` check types,
but its `docs-fidelity` is a declared-but-unimplemented placeholder
(`unimplemented_check_result`), and its `ux-heuristic`/`journey-verdict` are
pure **file adapters** — they read an already-produced `verdict.json` off
disk and fold it into an arena `CellResult`; they never dispatch an agent
themselves. `docs_fidelity.py`/`ux_heuristic.py` here ARE that missing
producer for the dev-workflow matrix's own cells, matching how
`run_checks.py` already writes `replay`/`journey-verdict` verdicts directly
for this matrix without going through arena's container executor at all —
same simplicity bias, same writer contract (a completion-state JSON file
`generate.py --verdicts-dir` ingests by `(workflow, surface, repo,
check_type)`).

**Dependency injection:** both runners take an `agent_dispatch.DispatchFn`
(`agent_dispatch.py`, shared): production defaults to
`agent_dispatch.claude_cli_dispatch` (one headless `claude -p ...
--output-format json` turn); their own test suites
(`docs_fidelity_test.py`, `ux_heuristic_test.py`) inject a scripted fake that
returns canned JSON — zero LLM, zero network, zero real subprocess, per
AGENTS.md.

**Enumerate without dispatching an agent:**

```
make dev-workflow-experience-list
```

or directly:

```
python3 tools/dev-workflow-matrix/docs_fidelity.py --list
python3 tools/dev-workflow-matrix/ux_heuristic.py --list
```

Both scripts also support `--dry-run` (prints what would be dispatched —
the doc path or the frame paths — without calling the dispatch function at
all) and `--only <workflow-ids>` to scope a run, matching `run_checks.py`'s
CLI shape.

**Running a REAL (live) check.** Neither script is run live in this slice —
that is a deliberate scope boundary, not an oversight. When a real run is
warranted:

```
python3 tools/dev-workflow-matrix/docs_fidelity.py --only onboard
python3 tools/dev-workflow-matrix/ux_heuristic.py --only fix-bug
```

dispatches a real agent turn per declared check and writes real verdict
files into `.artifacts/dev-workflow-matrix/verdicts/` (never committed). Per
plan G5, every judged run like this is meant to be **cassette-recorded**, the
same way the repo's other live-gated tools work (`kitsoki record` + `kitsoki
test flows` for story replay, `tools/usable-kitsoki-gate/run_live_gate.py`'s
`--live-gate` flag for its live path): capture the real dispatch once, then
replay it no-LLM for dispute/regression-diffing without re-spend. Recording
discipline for these two runners' own dispatch calls (a cassette format for
`agent_dispatch.DispatchFn`, wired the way `--host-cassette` wires host calls
elsewhere) is intentionally NOT built in this slice — it is deferred to
whichever WS-G slice actually schedules continuous/nightly persona runs (plan
G5), so it can be designed once against real recorded traffic instead of
speculatively here. Until then, treat any live invocation as a manual,
budget-aware run (per the "maximize autonomous work, quota pacing is the only
constraint" decision) rather than something CI or `make dev-workflow-gate`
calls automatically — neither Makefile target above touches these two
scripts.
