# Case study: the bugfix bake-off — is structure worth more than a bigger model?

> **TEMPLATE — awaiting live-run data.** This report is structure-only.
> Every `{{...}}` token is a placeholder filled mechanically from
> [`tools/bugfix-bakeoff/results/summary.json`](../../tools/bugfix-bakeoff/results/SCHEMA.md)
> once the live grid runs (LLM spend, gated). Do **not** read the numbers
> below as results — there are none yet. See [How to regenerate](#how-to-regenerate).

The two companion studies establish that the [`bugfix`](bug-fix.md)
pipeline *can* be deterministic and [what determinism costs](git-ops-cost.md).
This one asks the question those leave open: **does the structure actually
make the fixes better, and how does that trade against the model you point
at it?** We hold the bug set fixed and vary two axes — structure (the
kitsoki pipeline vs. a single multi-stage prompt) and model (GLM-5.2, Opus
4.8, Sonnet 4.6, GPT-5.5) — then grade every cell against a hidden oracle.
The headline: **is the kitsoki pipeline worth more than a bigger model?**
If a cheaper model *with* structure matches a frontier model *without* it,
structure is the lever and model choice is the cost knob.

---

## 1. Method

### The 2×4 factorial

For each of 5 bugs we run a **2 (treatment) × 4 (candidate)** grid — up to
**40 cells** — from the manifest
[`tools/bugfix-bakeoff/bakeoff.yaml`](../../tools/bugfix-bakeoff/bakeoff.yaml).

- **Structure axis (treatment).** `kitsoki` drives the
  [`bugfix`](../../stories/bugfix/) story (the seven-room
  reproduce→propose→implement→test→review→validate→done pipeline, with the
  candidate as the maker model); `single` gives the *same* candidate one
  multi-stage prompt plus up to **5 oracle-gated guidance turns**.
- **Model axis (candidate).** The same four candidates run under both
  treatments:

  | key | profile | model | provider | single-prompt invoker |
  |---|---|---|---|---|
  | `glm-5.2` | synthetic-claude | hf:zai-org/GLM-5.2 | synthetic.new | `session` |
  | `opus-4.8` | claude-native | opus | anthropic | `claude -p` |
  | `sonnet-4.6` | claude-native | sonnet | anthropic | `claude -p` |
  | `gpt-5.5` | codex-native | gpt-5.5 | openai-codex | `session` |

### Hermetic baselines, hidden oracle

Each cell runs from a **hermetic baseline = the real fix's parent commit**
(`<fix_sha>^`), so the bug is genuinely present and nothing of the real fix
leaks in. The grader's **hidden oracle is the fix's own regression test** —
RED at baseline, must be GREEN after a real fix. The oracle is *never*
placed in the candidate's tree until scoring; the candidate must reproduce
and fix the bug without seeing it.

### The 5 bugs

From the manifest, spanning three components and severities P1–P3:

| id | severity | component | bug | oracle |
|---|---|---|---|---|
| bug1 | P2 | tui | TUI view templates render BEFORE `on_enter` binds — first frame shows `(pending)` | `render_before_bind_repro_test.go` (go) |
| bug2 | P3 | tui | Prose view blocks don't expand past hand-wrapped width on wide terminals | `repro_glamour_cap_prose_test.go` (go) |
| bug8 | P1 | runtime | `agent.decide` with no submit routes to success with an empty artifact instead of failing | `agent_decide_empty_artifact_repro_test.go` (go) |
| bug12 | P1 | runtime | `host.agent.decide` with `validator.post_cmd` reports "abandoned" when the payload WAS captured | `agent_decide_postcmd_captured_abandon_repro_test.go` (go) |
| bug14 | P2 | web | Web transport doesn't surface a `background_completion` turn's failure (looks hung) | `run-background-completion.test.ts` (vitest) |

### Cost, tokens, compliance, outcome

- **Cost & tokens** reuse the cost-study machinery, not a model: token
  counts come from the recorded transcript via
  [`cost_extract.py`](../../tools/session-mining/cost_extract.py) and are
  priced through the shared table
  [`pricing.py`](../../tools/session-mining/pricing.py) — see
  [git-ops-cost.md §4](git-ops-cost.md#4-method-and-the-synthetic-fallback)
  for the exact-from-recorded-usage method. Each cell's `cost_exact` flag
  marks whether the rate was a published row or an estimate.
- **Outcome & compliance** are scored by `score.py` to the shapes in
  [`results/SCHEMA.md`](../../tools/bugfix-bakeoff/results/SCHEMA.md):
  `quality ∈ {solved, partial, failed}` (solved = oracle+build+suite all
  green) and a five-boolean compliance rate (reproduced-red, added own
  regression test, suite-green, in-scope, stage-order).

### Honesty controls

The grade is **oracle-gated** and **never trusts agent self-report**: a
cell "passes" only when the hidden regression test goes green and the
affected suite stays green (`adherence_bar.min_pass_rate = 1.0`). An agent
claiming "fixed" with a red oracle scores `failed`. Cost is reported, not
gated (`max_avg_cost_usd = 0`, informational).

---

## 2. Results

> Placeholders only — filled from `summary.json` (see [How to regenerate](#how-to-regenerate)).

### Per-bug grid (8 cells per bug: 4 candidates × 2 treatments)

Cell value = `quality` (`solved`/`partial`/`failed`) with guidance turns in
parens for `single`.

| bug | glm-5.2 K | glm-5.2 S | opus-4.8 K | opus-4.8 S | sonnet-4.6 K | sonnet-4.6 S | gpt-5.5 K | gpt-5.5 S |
|---|---|---|---|---|---|---|---|---|
| bug1 | `{{cell.bug1.glm-5.2.kitsoki.quality}}` | `{{cell.bug1.glm-5.2.single.quality}}` | `{{cell.bug1.opus-4.8.kitsoki.quality}}` | `{{cell.bug1.opus-4.8.single.quality}}` | `{{cell.bug1.sonnet-4.6.kitsoki.quality}}` | `{{cell.bug1.sonnet-4.6.single.quality}}` | `{{cell.bug1.gpt-5.5.kitsoki.quality}}` | `{{cell.bug1.gpt-5.5.single.quality}}` |
| bug2 | `{{cell.bug2.glm-5.2.kitsoki.quality}}` | `{{cell.bug2.glm-5.2.single.quality}}` | `{{cell.bug2.opus-4.8.kitsoki.quality}}` | `{{cell.bug2.opus-4.8.single.quality}}` | `{{cell.bug2.sonnet-4.6.kitsoki.quality}}` | `{{cell.bug2.sonnet-4.6.single.quality}}` | `{{cell.bug2.gpt-5.5.kitsoki.quality}}` | `{{cell.bug2.gpt-5.5.single.quality}}` |
| bug8 | `{{cell.bug8.glm-5.2.kitsoki.quality}}` | `{{cell.bug8.glm-5.2.single.quality}}` | `{{cell.bug8.opus-4.8.kitsoki.quality}}` | `{{cell.bug8.opus-4.8.single.quality}}` | `{{cell.bug8.sonnet-4.6.kitsoki.quality}}` | `{{cell.bug8.sonnet-4.6.single.quality}}` | `{{cell.bug8.gpt-5.5.kitsoki.quality}}` | `{{cell.bug8.gpt-5.5.single.quality}}` |
| bug12 | `{{cell.bug12.glm-5.2.kitsoki.quality}}` | `{{cell.bug12.glm-5.2.single.quality}}` | `{{cell.bug12.opus-4.8.kitsoki.quality}}` | `{{cell.bug12.opus-4.8.single.quality}}` | `{{cell.bug12.sonnet-4.6.kitsoki.quality}}` | `{{cell.bug12.sonnet-4.6.single.quality}}` | `{{cell.bug12.gpt-5.5.kitsoki.quality}}` | `{{cell.bug12.gpt-5.5.single.quality}}` |
| bug14 | `{{cell.bug14.glm-5.2.kitsoki.quality}}` | `{{cell.bug14.glm-5.2.single.quality}}` | `{{cell.bug14.opus-4.8.kitsoki.quality}}` | `{{cell.bug14.opus-4.8.single.quality}}` | `{{cell.bug14.sonnet-4.6.kitsoki.quality}}` | `{{cell.bug14.sonnet-4.6.single.quality}}` | `{{cell.bug14.gpt-5.5.kitsoki.quality}}` | `{{cell.bug14.gpt-5.5.single.quality}}` |

### Rollup — by treatment (the structure headline)

| treatment | solve_rate | avg_total_tokens | avg_cost_usd | avg_wall_time_s | avg_guidance_turns | avg_compliance |
|---|---|---|---|---|---|---|
| kitsoki | `{{by_treatment.kitsoki.solve_rate}}` | `{{by_treatment.kitsoki.avg_total_tokens}}` | `{{by_treatment.kitsoki.avg_cost_usd}}` | `{{by_treatment.kitsoki.avg_wall_time_s}}` | `{{by_treatment.kitsoki.avg_guidance_turns}}` | `{{by_treatment.kitsoki.avg_compliance}}` |
| single | `{{by_treatment.single.solve_rate}}` | `{{by_treatment.single.avg_total_tokens}}` | `{{by_treatment.single.avg_cost_usd}}` | `{{by_treatment.single.avg_wall_time_s}}` | `{{by_treatment.single.avg_guidance_turns}}` | `{{by_treatment.single.avg_compliance}}` |

### Rollup — by candidate (the model axis)

| candidate | solve_rate | avg_total_tokens | avg_cost_usd | avg_wall_time_s | avg_guidance_turns | avg_compliance |
|---|---|---|---|---|---|---|
| glm-5.2 | `{{by_candidate.glm-5.2.solve_rate}}` | `{{by_candidate.glm-5.2.avg_total_tokens}}` | `{{by_candidate.glm-5.2.avg_cost_usd}}` | `{{by_candidate.glm-5.2.avg_wall_time_s}}` | `{{by_candidate.glm-5.2.avg_guidance_turns}}` | `{{by_candidate.glm-5.2.avg_compliance}}` |
| opus-4.8 | `{{by_candidate.opus-4.8.solve_rate}}` | `{{by_candidate.opus-4.8.avg_total_tokens}}` | `{{by_candidate.opus-4.8.avg_cost_usd}}` | `{{by_candidate.opus-4.8.avg_wall_time_s}}` | `{{by_candidate.opus-4.8.avg_guidance_turns}}` | `{{by_candidate.opus-4.8.avg_compliance}}` |
| sonnet-4.6 | `{{by_candidate.sonnet-4.6.solve_rate}}` | `{{by_candidate.sonnet-4.6.avg_total_tokens}}` | `{{by_candidate.sonnet-4.6.avg_cost_usd}}` | `{{by_candidate.sonnet-4.6.avg_wall_time_s}}` | `{{by_candidate.sonnet-4.6.avg_guidance_turns}}` | `{{by_candidate.sonnet-4.6.avg_compliance}}` |
| gpt-5.5 | `{{by_candidate.gpt-5.5.solve_rate}}` | `{{by_candidate.gpt-5.5.avg_total_tokens}}` | `{{by_candidate.gpt-5.5.avg_cost_usd}}` | `{{by_candidate.gpt-5.5.avg_wall_time_s}}` | `{{by_candidate.gpt-5.5.avg_guidance_turns}}` | `{{by_candidate.gpt-5.5.avg_compliance}}` |

### Rollup — by cell key (`candidate|treatment`, the interaction)

| cell key | solve_rate | avg_total_tokens | avg_cost_usd | avg_wall_time_s | avg_guidance_turns | avg_compliance |
|---|---|---|---|---|---|---|
| glm-5.2\|kitsoki | `{{by_cell_key.glm-5.2|kitsoki.solve_rate}}` | `{{by_cell_key.glm-5.2|kitsoki.avg_total_tokens}}` | `{{by_cell_key.glm-5.2|kitsoki.avg_cost_usd}}` | `{{by_cell_key.glm-5.2|kitsoki.avg_wall_time_s}}` | `{{by_cell_key.glm-5.2|kitsoki.avg_guidance_turns}}` | `{{by_cell_key.glm-5.2|kitsoki.avg_compliance}}` |
| glm-5.2\|single | `{{by_cell_key.glm-5.2|single.solve_rate}}` | `{{by_cell_key.glm-5.2|single.avg_total_tokens}}` | `{{by_cell_key.glm-5.2|single.avg_cost_usd}}` | `{{by_cell_key.glm-5.2|single.avg_wall_time_s}}` | `{{by_cell_key.glm-5.2|single.avg_guidance_turns}}` | `{{by_cell_key.glm-5.2|single.avg_compliance}}` |
| opus-4.8\|kitsoki | `{{by_cell_key.opus-4.8|kitsoki.solve_rate}}` | `{{by_cell_key.opus-4.8|kitsoki.avg_total_tokens}}` | `{{by_cell_key.opus-4.8|kitsoki.avg_cost_usd}}` | `{{by_cell_key.opus-4.8|kitsoki.avg_wall_time_s}}` | `{{by_cell_key.opus-4.8|kitsoki.avg_guidance_turns}}` | `{{by_cell_key.opus-4.8|kitsoki.avg_compliance}}` |
| opus-4.8\|single | `{{by_cell_key.opus-4.8|single.solve_rate}}` | `{{by_cell_key.opus-4.8|single.avg_total_tokens}}` | `{{by_cell_key.opus-4.8|single.avg_cost_usd}}` | `{{by_cell_key.opus-4.8|single.avg_wall_time_s}}` | `{{by_cell_key.opus-4.8|single.avg_guidance_turns}}` | `{{by_cell_key.opus-4.8|single.avg_compliance}}` |
| sonnet-4.6\|kitsoki | `{{by_cell_key.sonnet-4.6|kitsoki.solve_rate}}` | `{{by_cell_key.sonnet-4.6|kitsoki.avg_total_tokens}}` | `{{by_cell_key.sonnet-4.6|kitsoki.avg_cost_usd}}` | `{{by_cell_key.sonnet-4.6|kitsoki.avg_wall_time_s}}` | `{{by_cell_key.sonnet-4.6|kitsoki.avg_guidance_turns}}` | `{{by_cell_key.sonnet-4.6|kitsoki.avg_compliance}}` |
| sonnet-4.6\|single | `{{by_cell_key.sonnet-4.6|single.solve_rate}}` | `{{by_cell_key.sonnet-4.6|single.avg_total_tokens}}` | `{{by_cell_key.sonnet-4.6|single.avg_cost_usd}}` | `{{by_cell_key.sonnet-4.6|single.avg_wall_time_s}}` | `{{by_cell_key.sonnet-4.6|single.avg_guidance_turns}}` | `{{by_cell_key.sonnet-4.6|single.avg_compliance}}` |
| gpt-5.5\|kitsoki | `{{by_cell_key.gpt-5.5|kitsoki.solve_rate}}` | `{{by_cell_key.gpt-5.5|kitsoki.avg_total_tokens}}` | `{{by_cell_key.gpt-5.5|kitsoki.avg_cost_usd}}` | `{{by_cell_key.gpt-5.5|kitsoki.avg_wall_time_s}}` | `{{by_cell_key.gpt-5.5|kitsoki.avg_guidance_turns}}` | `{{by_cell_key.gpt-5.5|kitsoki.avg_compliance}}` |
| gpt-5.5\|single | `{{by_cell_key.gpt-5.5|single.solve_rate}}` | `{{by_cell_key.gpt-5.5|single.avg_total_tokens}}` | `{{by_cell_key.gpt-5.5|single.avg_cost_usd}}` | `{{by_cell_key.gpt-5.5|single.avg_wall_time_s}}` | `{{by_cell_key.gpt-5.5|single.avg_guidance_turns}}` | `{{by_cell_key.gpt-5.5|single.avg_compliance}}` |

### How to regenerate

Numbers are not transcribed by hand. After the live grid runs, the cells
are aggregated and this report is filled mechanically:

```bash
python3 tools/bugfix-bakeoff/aggregate.py        # cells/*.json -> results/summary.json (+ rollup)
# then the report fill step substitutes every {{...}} token above from summary.json
```

Offline, zero-respend regeneration of the data deck goes through the
agenteval bridge — `aggregate.py --emit-agenteval` writes one
`agenteval.Report` per bug, then
[`eval_pilot_report.py --markdown --deck`](../../tools/session-mining/eval_pilot_report.py)
renders it (see [SCHEMA.md §eval_pilot_report.py bridge](../../tools/bugfix-bakeoff/results/SCHEMA.md)).

---

## 3. Discussion

> Filled from results; each subsection states the claim the data tests.

- **Structure vs. model.** Compare the `by_treatment` solve-rate gap to the
  `by_candidate` spread. If `kitsoki − single` exceeds the best−worst model
  gap, **structure outweighs model choice** — the central thesis. _(TBD)_
- **Where single-prompt failed.** Read `by_cell_key` `avg_guidance_turns`
  for `single`: turns spent are the cost of the missing pipeline (each turn
  is a failed oracle gate the structure would have caught at a room
  boundary — reproduce-red, suite-green, stage-order). _(TBD)_
- **Cost per solved bug.** `avg_cost_usd ÷ solve_rate` per cell key — the
  honest unit. A high solve rate bought with runaway tokens is not free;
  this is where the [reprocessing tax](git-ops-cost.md#1-the-reprocessing-tax-measured)
  shows up in the `single` treatment. _(TBD)_
- **Cheaper-model-with-structure.** The money slide: does
  `glm-5.2|kitsoki` or `sonnet-4.6|kitsoki` match `opus-4.8|single` /
  `gpt-5.5|single` on solve rate at a fraction of the cost? If so, the
  pipeline lets you **buy down the model** — the same lever git-ops-cost
  surfaces (Sonnet-where-Opus-was) measured here on outcomes, not tokens. _(TBD)_

---

## 4. Reproducibility appendix

- **Framework.** Everything lives at
  [`tools/bugfix-bakeoff/`](../../tools/bugfix-bakeoff/) — manifest
  ([`bakeoff.yaml`](../../tools/bugfix-bakeoff/bakeoff.yaml)), scoring
  (`score.py`), aggregation (`aggregate.py`), and the result contract
  ([`results/SCHEMA.md`](../../tools/bugfix-bakeoff/results/SCHEMA.md)). The
  framework's own README documents the run path.
- **Offline report regen.** No live grid needed to re-render from existing
  cells: `aggregate.py --emit-agenteval` →
  [`eval_pilot_report.py`](../../tools/session-mining/eval_pilot_report.py)
  `--markdown --deck` reproduces the data deck with zero re-spend.
- **Discovered en route.** Building the kitsoki-treatment runner surfaced a
  real gap: live MCP sessions don't always leave a discoverable trace, so
  `score.py` can't always find the transcript to extract cost from (the
  `trace_found` flag on each cell records this). Tracked at
  `issues/bugs/2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md`.

## See also

- [bug-fix.md](bug-fix.md) — the pipeline under test (the seven-room story,
  typed artifacts, oracle-as-test). This study does not re-explain it.
- [git-ops-cost.md](git-ops-cost.md) — the cost extractor and reprocessing-tax
  framing reused here for the token/cost columns.
- [Progressive determinism](../architecture/concept.md#4-progressive-determinism)
  — the principle the bake-off puts to an outcome test.
