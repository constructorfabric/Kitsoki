# Product Journey QA Story

This story wraps the deterministic product-journey runner so operators can drive
the standard persona/scenario evidence workflow inside Kitsoki.

It is intentionally no-LLM:

- `start` calls `tools/product-journey/run.py --emit-run --json-output`.
- `attach` calls `tools/product-journey/run.py --attach-evidence --json-output`.
- Flow fixtures stub `host.run`, so automated tests never call a live model or
  external service.

The generated run bundle under `.artifacts/product-journey/<run-id>/` is the
handoff point for visual MCP, Studio MCP, oracle results, and Slidey review.
