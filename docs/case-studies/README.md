# Case studies

Worked examples of [progressive determinism](../architecture/concept.md#4-progressive-determinism)
applied to real workflows. Each case study tells the same story from a
different angle:

1. The workflow starts as a prompt-driven agent loop. It mostly works.
2. Reading the trace, the same LLM decisions keep showing up. Some of
   them are recurring interpretive judgements; some of them are things
   the prompt *tries* to control with "YOU MUST" and "ALWAYS" rules,
   and the LLM keeps cutting corners on.
3. One decision at a time, the recurring decisions move out of the
   prompt and into the state machine — as rooms, intents, transitions,
   effects, host calls, or typed artifacts.
4. What survives in the LLM domain is the work that genuinely needs
   interpretation, with maximum context and a focused tool set for
   exactly that sub-task.

The goal isn't to remove the LLM. It's to make every place the LLM is
still needed *named*, *bounded*, and *replayable* — and to stop relying
on prompt incantations to enforce structure.

## Studies

- **[bug-fix.md](bug-fix.md)** — the canonical example. A bug-fixing
  agent loop becomes a seven-room pipeline (`reproducing` → `proposing`
  → `implementing` → `testing` → `reviewing` → `validating` → `done`),
  with typed artifacts at every phase, deterministic boundaries between
  them, and a failing test as the reproduction artifact. The pipeline
  ships as the [`bugfix`](../../stories/bugfix/) story.

- **[git-ops-cost.md](git-ops-cost.md)** — the *how much*. Puts a price
  tag on progressive determinism using **real telemetry** from real
  Claude Code sessions. The four git-ops operations run as the
  [`git-ops`](../../stories/git-ops/) story for a committed $0.0955, flat
  in session length — versus a raw agentic loop where 98–99% of every
  turn's cost is *reprocessing the prior conversation* to reach the next
  action (the demo-building session itself cost $546). Introduces the
  real-cost extractor [`cost_extract.py`](../../tools/session-mining/cost_extract.py)
  (reads recorded `message.usage`, exact) and the reprocessing-tax framing.
  Now generated **per story** by
  [`cost_report.py`](../../tools/session-mining/cost_report.py) (`make
  cost-report`): the deterministic story cost vs the real raw-agentic
  cost of the same operations, with a per-intent distribution.

- **[bugfix-bakeoff.md](bugfix-bakeoff.md)** — the *does it work better*.
  A 2×4 factorial (kitsoki pipeline vs. a naive single multi-stage prompt,
  across GLM-5.2 / Opus 4.8 / Sonnet 4.6 / GPT-5.5) over 5 real bugs, each
  from its hermetic parent-commit baseline and graded against the fix's own
  regression test as a hidden oracle. Asks the headline question: **is
  structure worth more than a bigger model?** Framework at
  [`tools/bugfix-bakeoff/`](../../tools/bugfix-bakeoff/). _(template — awaiting
  live-run data.)_

- **[routing-model-cost-study.md](routing-model-cost-study.md)** — the
  model-selection lever after deterministic routing has already done its
  job. Mines real Kitsoki turns into a routing corpus, compares available
  Haiku / synthetic-small / GPT-mini-style candidates, and argues for
  room-by-room cheap-router promotion with explicit hard-negative tests
  and fallback rather than a global model downgrade.

Future studies (planned, not yet written):

- **PR refinement.** The `pr-refinement` tail: watching CI, resolving
  reviewer comments, deciding when to merge. Shows progressive
  determinism applied to a workflow whose state lives in an external
  system (GitHub / Bitbucket).
- **Triage and intake.** Turning unstructured bug reports and feature
  requests into typed tickets. Shows the interpretation-to-template
  ladder: free-text routing → synonym matches → slot templates → LLM
  fallback for the long tail.
- **Story authoring.** Kitsoki authors itself: a meta-mode workflow
  that proposes YAML edits, validates them against the loader, and
  applies them. Shows the script-producing form of interpretation
  (the LLM emits YAML that the runtime *loads* — the loader is the
  judge, not the LLM).
- **Incident response.** A workflow where the *runtime* picks
  collaborators (oncall, SRE, infra) based on the artifact, and the
  LLM's job is summarisation and timeline reconstruction.

If you only read one case study, read bug-fix. It establishes the
vocabulary the others reuse.
