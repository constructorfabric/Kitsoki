# Bugfix bake-off — pilot runbook

A reusable harness for comparing **kitsoki's `bugfix` pipeline against a naive
single-prompt agent**, across a model/harness candidate matrix, on real fixed
bugs from this repo. The reusable recipe for future *kitsoki-vs-X* comparisons.

## The grid

For each bug, a **2 (treatment) × 4 (candidate)** grid of cells. Every cell runs
from a hermetic baseline = the real fix's PARENT commit (`baseline_sha`), so the
bug is present and no two cells share a checkout.

|                | glm-5.2 | opus-4.8 | sonnet-4.6 | gpt-5.5 |
|----------------|---------|----------|------------|---------|
| **kitsoki**    | drive `stories/bugfix` under the candidate (maker=model) |
| **single**     | one multi-stage prompt + up to `max_guidance_turns` nudges |

- **Treatments** (`bakeoff.yaml: treatments`): `kitsoki` (the structured
  pipeline) vs `single` (one prompt, the control).
- **Candidates** (`bakeoff.yaml: candidates`): each `{profile, model, effort,
  provider, invoker}`. The `invoker` decides HOW a `single` cell is driven:
  - `claude_p` (opus-4.8, sonnet-4.6) → `claude -p --model <model>` (fully
    scripted by `run_cell.sh`, including the resumable guidance loop).
  - `session` (glm-5.2, gpt-5.5) → studio-MCP `session_new` under the profile.
- **Bugs** (`bakeoff.yaml: bugs`): bug1, bug2, bug8, bug12, bug14 — each with a
  `baseline_sha`, an `oracle_test` (the regression test the real fix added; the
  grader's HIDDEN oracle, kept OUT of the candidate's tree until scoring), and
  `affected_test_pkgs`.

Files: `bakeoff.yaml` (manifest) · `prepare.sh` · `run_cell.sh` (this dir) ·
`prompts/<bug>.md` (the `single`-treatment prompt, one per bug, identical in
structure) · `score.py` + `aggregate.py` (separate; consume the sidecars +
oracle) · `results/SCHEMA.md` (the result contract).

## Cost warning

`run_cell.sh` is the **only cost-bearing piece** of the bake-off — it invokes
real LLMs (`claude -p` or the kitsoki live harness). It is run **manually by the
operator**, never in CI and never automatically. `prepare.sh`, `score.py`, and
`aggregate.py` make no LLM calls and are free.

## Run one cell end-to-end

```bash
cd tools/bugfix-bakeoff
BUG=bug1; CAND=opus-4.8; TREAT=single

# 1. Hermetic, isolated worktree at the bug's baseline (free; idempotent).
./prepare.sh $BUG $CAND $TREAT          # prints the worktree path

# 2. Run the cell (COST). Writes results/sidecars/<bug>-<cand>-<treat>.{env,json}.
./run_cell.sh $BUG $CAND $TREAT

# 3. Score it (free; copies the hidden oracle into a scratch copy of the tree,
#    runs build + oracle + affected suite, writes results/cells/<cell>.json).
python3 score.py --bug $BUG --candidate $CAND --treatment $TREAT
```

### What `run_cell.sh` does per (treatment, invoker)

- **single / claude_p** — fully scripted. Runs `claude -p "$(cat prompts/<bug>.md)"
  --output-format json --model <model>` with the worktree as CWD, captures the
  `session_id` and resolves the Claude Code transcript
  (`~/.claude/projects/<encoded-worktree>/<session>.jsonl`). It then prints an
  **oracle-check** hint and the exact resume command. To drive an operator-decided
  guidance turn (up to `run.max_guidance_turns`):

  ```bash
  ./run_cell.sh bug1 opus-4.8 single "the test is still RED — check runOnEnter ordering"
  ```

  Each guidance turn `claude -p --resume <session_id> "<msg>"` and accumulates
  `guidance_turns` + `wall_time_s` into the sidecar.

- **single / session** (glm-5.2, gpt-5.5) — **no profile-pinned single-turn
  kitsoki CLI exists** (`kitsoki agent task` is headless but takes no
  `--profile`/`--model`; profile is config-driven). So `run_cell.sh` prepares
  the worktree and **prints the exact studio-MCP procedure**: `session_new
  {story_path, harness:"live", profile, trace}` then `session_drive {input: the
  prompt}`, plus where the trace lands. The operator (or the kitsoki-mcp-driver
  agent) executes it, then fills the sidecar.

- **kitsoki** (all candidates) — driving `stories/bugfix` is studio-MCP /
  operator-driven. `run_cell.sh` seeds the bug ticket into the worktree and
  **prints the exact `session_new` args** (profile, model, effort, workdir, and
  an `initial_world` carrying the ticket). After the drive, locate the trace:

  ```bash
  ./run_cell.sh --locate bug1 opus-4.8 kitsoki
  ```

  > **Known open bug:** MCP-driven bugfix sessions may not leave a discoverable
  > local trace. The locate helper reports `trace_found=false` clearly — that
  > absence is itself a study finding, not a script failure.

## Aggregate + regenerate the report/deck (offline, zero re-spend)

```bash
cd tools/bugfix-bakeoff
python3 aggregate.py                       # merges results/cells/*.json -> results/summary.json
python3 aggregate.py --emit-agenteval      # also writes results/agenteval/<bug>/latest.json
python3 ../path/to/eval_pilot_report.py --markdown --deck   # regenerates the deck
```

`summary.json` and the per-bug `agenteval.Report` files are the durable
artifacts; the report/deck regenerate from them with no LLM calls. See
`results/SCHEMA.md` for the exact cell/summary shapes every tool must honor.

## Resumability

- **Per-cell isolation.** Each cell has its OWN detached worktree
  (`.worktrees/bakeoff-<bug>-<candidate>-<treatment>`). A cell can be re-run,
  re-scored, or thrown away without touching any other cell — and never sharing
  a checkout (the concurrent-checkout bug #9 this study covers).
- **`prepare.sh` is idempotent.** A clean worktree at the right baseline is a
  no-op; a dirty/off-baseline one is refused unless you pass `--force`.
- **`run_cell.sh` is re-runnable.** It rewrites the sidecar each call; guidance
  turns and wall time accumulate across resumed `single/claude_p` turns. Scoring
  is fully separate, so you can re-score without re-spending.
