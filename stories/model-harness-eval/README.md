# Model Harness Eval Story

This story turns a broad evaluation question into review artifacts:

- a Markdown report;
- a slide-style HTML deck;
- a machine-readable summary JSON.

The operator starts with `start question=...`. The `reporter` agent runs the
offline evidence process documented in `docs/testing/model-harness-eval-pilot.md`
and returns a schema-validated `report_artifact` with exact paths and limits.

Automated flows stub `host.agent.task`; they do not call a live LLM or run paid
provider-backed benchmarks.
