# Model and harness eval pilot

This pilot combines three Kitsoki loops:

- session mining identifies real user intents and repeated task shapes;
- story-local agent evals define bounded call-site benchmarks;
- offline report aggregation compares harness/model evidence by effectiveness,
  speed, and cost.

The default path is no-LLM and no-cost. Live provider collection is a separate
manual step and must be explicitly requested by an operator.

## Process

1. Mine or choose a candidate task.
   Use session-mining intent reports, story coverage worksheets, or existing
   story intent fixtures to identify a bounded call site worth comparing across
   harnesses.

2. Define a story-local eval dataset.
   Add `stories/<story>/evals/<call>.yaml` with examples, expected structured
   output, a comparator, an adherence bar, and a matrix of planned profiles.

3. Validate the contract offline.

   ```sh
   go run ./cmd/kitsoki eval run stories/<story>/evals/<call>.yaml
   ```

4. Collect model/harness evidence.
   Automated tests must stop at offline validation. When a human explicitly
   approves a cost-bearing run, save the resulting `agent_eval_report` JSON under
   `.artifacts/` for review. Move accepted evidence to
   `stories/<story>/evals/reports/<call>/`.

5. Aggregate the evidence.

   ```sh
   python3 tools/session-mining/eval_pilot_report.py \
     --root stories \
     --intent-root .artifacts/eval-pilot/intent-reports \
     --coverage-root .artifacts/eval-pilot \
     --markdown .context/model-harness-eval-pilot.md \
     --deck .artifacts/eval-pilot/index.html \
     --summary .artifacts/eval-pilot/summary.json
   ```

## What the report means

The report groups candidates by `call`, `profile`, `backend`, `provider`,
`model`, and `effort`. For each group it reports:

- effectiveness: comparator pass rate distribution;
- speed: p95 latency distribution;
- cost: average cost distribution;
- pass observations and failure samples.

The p5/median/p95 values are computed across the loaded report files. If only
one report exists for a candidate, all three values are identical. As more live
matrix reports are imported, the same command starts showing real variability.
Cells with no samples render as `-` so a missing measurement is never confused
with a genuine zero.

Each candidate's measured medians are independently re-checked against the
adherence bar (`min_pass_rate`, `max_p95_latency_ms`, `max_avg_cost_usd`)
declared by its dataset, surfaced in the "Adherence-bar compliance" section. A
row marked as a `divergence` passed the upstream report's own `pass` flag yet
still violates the declared bar — for example a candidate the report accepted
whose average cost exceeds the dataset's cost ceiling. This keeps the report's
verdict tied to the contract the dataset declares rather than to the trust of
each imported report.

When `--intent-root` is provided, the same report also ingests
`kitsoki test intents --json` outputs and shows fixture pass rates, run pass
rates, skipped static inputs, and failed fixtures. When `--coverage-root` is
provided, it ingests session-mining coverage job directories containing
`intents.json`, `analysis.json`, and `coverage.md`, then reports grounding,
deduped command shapes, corrected/satisfaction signals, and determinism split.

The report also scans readiness gaps from `stories/*/intents/` and
`stories/*/mining.profile.yaml`, so a large sweep shows both validated cases and
stories that are prepared for the process but not yet represented by evidence.

## Larger no-cost sweep

Use committed/static evidence to validate the process before spending live model
tokens:

```sh
mkdir -p .artifacts/eval-pilot/intent-reports

GOCACHE="$PWD/.cache/go-build" \
  go run ./cmd/kitsoki test intents stories/oregon-trail/app.yaml \
    --harness static \
    --json .artifacts/eval-pilot/intent-reports/oregon-trail.json

bash tools/session-mining/examples/git-ops/run.sh \
  --keep .artifacts/eval-pilot/git-ops-coverage

python3 tools/session-mining/eval_pilot_report.py \
  --root stories \
  --intent-root .artifacts/eval-pilot/intent-reports \
  --coverage-root .artifacts/eval-pilot \
  --markdown .context/model-harness-eval-pilot-big.md \
  --deck .artifacts/eval-pilot-big/index.html \
  --summary .artifacts/eval-pilot-big/summary.json
```

The Oregon Trail command may exit non-zero when fixtures fail; that is still a
useful validation artifact as long as it wrote the JSON report. Treat those
failures as real routing evidence, not as a failed pilot run.

## Current seed evidence

The initial seed uses:

- committed `pr-refinement` `merge_judge` eval reports;
- the static `oregon-trail` intent suite;
- the committed `git-ops` coverage-mining flagship.

That gives the pilot concrete output across model/harness evals, routing intent
tests, and transcript-derived coverage without making provider calls. Missing
profiles and missing coverage jobs in the generated report are the handoff list
for the next explicit collection run.
