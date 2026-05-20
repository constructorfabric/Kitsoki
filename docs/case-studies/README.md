# Case studies

Worked examples of [progressive determinism](../concept.md#4-progressive-determinism)
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
