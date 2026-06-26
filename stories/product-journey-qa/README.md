# Product Journey QA Story

This story wraps the deterministic product-journey runner so operators can drive
the standard persona/scenario evidence workflow inside Kitsoki.

It is intentionally no-LLM:

- `matrix` calls `tools/product-journey/run.py --emit-matrix --json-output` to
  create the 10-repo GitHub assignment plan.
- `rollup` calls `tools/product-journey/run.py --rollup-matrix --json-output`
  to create or refresh the matrix-level Slidey deck from reviewed run bundles.
- `start` calls `tools/product-journey/run.py --emit-run --json-output`.
- `attach` calls `tools/product-journey/run.py --attach-evidence --json-output`.
- `record` calls `tools/product-journey/run.py --record-finding --json-output`
  for strengths, weaknesses, issues, and fixes.
- `seed_demo` calls `tools/product-journey/run.py --seed-demo-evidence
  --json-output` to populate a no-LLM review bundle.
- `review` calls `tools/product-journey/run.py --review-run --json-output` to
  write `review.json` and score whether the bundle is ready for human review.
- Flow fixtures stub `host.run`, so automated tests never call a live model or
  external service.

The generated run bundle under `.artifacts/product-journey/<run-id>/` is the
handoff point for visual MCP, Studio MCP, oracle results, and Slidey review.
Use `agent-brief.md` inside that bundle to drive the live persona session, then
use `execution-plan.md` to copy the generated `--attach-evidence` commands.
