# GOAL — reusable measurement-driven story improvement

**Status:** Active target. This goal is the productization path for a Kitsoki
story developer, not a Kitsoki-core-only benchmark script.

**One line:** Given a story, a matched raw-agent baseline, an oracle-backed task
corpus, and a finite spend policy, Kitsoki repeatedly measures, diagnoses,
implements, independently verifies, and promotes improvements until the
Kitsoki treatment beats the raw baseline—or stops with an honest evidence-based
verdict.

## Done-when

- **G1 — Reusable evaluation contract.** A story author can declare a corpus,
  raw baseline, Kitsoki treatments, quality metric, and finite budget policy;
  every cell emits a versioned result with outcome, cost, trace, and diagnosis.
- **G2 — Goal-seeker control loop.** The goal-seeker consumes only bounded
  rollups, selects the next reversible improvement hypothesis, and persists
  its decision, evidence pointers, and stop reason. It never treats a missing
  trace, blocked cell, or self-reported worker result as a win.
- **G3 — Improvement pipeline.** A selected hypothesis is dispatched through
  the relevant Kitsoki implementation/bugfix pipeline, receives an independent
  deterministic gate, and is measured again against the unchanged baseline.
- **G4 — Promotion rule.** A candidate becomes the story's default only after a
  predeclared multi-task comparison beats raw Codex on solved rate with no
  unresolved harness failures; otherwise it is retained as a failed experiment
  with a replayable diagnosis.
- **G5 — Developer product surface.** A non-core story developer can start,
  inspect, pause, resume, and export the loop through a documented story/Studio
  MCP surface, with JSON as source of truth and a Slidey status deck for review.
- **G6 — No-spend regression proof.** All loop logic, report generation,
  hypothesis selection, and promotion guards have cassette/fixture tests. Live
  drives are explicit, budgeted acceptance runs only.

## Initial evidence

The 2026-07-10 GPT-5.4 query-string action-surface run is diagnostic, not a
leaderboard: raw Codex solved; direct CodeAct was blocked by a capability-plan
assertion; both Studio treatments failed with missing trace usage and zero
CodeAct calls. The first loop cycle must repair measurement fidelity before it
optimizes prompts or declares a model ranking.

## Non-goals

- Unbounded autonomous paid runs.
- Tuning a single Kitsoki-core task until it overfits the benchmark.
- Replacing hidden oracles with agent self-evaluation.
