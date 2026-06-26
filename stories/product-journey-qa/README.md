# Product Journey QA Story

This story wraps the deterministic product-journey runner so operators can drive
the standard persona/scenario evidence workflow inside Kitsoki.

It is intentionally no-LLM:

- `refresh_targets` calls `tools/product-journey/run.py
  --refresh-github-targets --json-output` to write a GitHub target-proof
  artifact for the 100-open-bug and popularity matrix contract.
- `matrix` calls `tools/product-journey/run.py --emit-matrix --json-output` to
  create the 10-repo GitHub assignment plan. Pass `target_proof_file=...` after
  `refresh_targets` when the matrix should embed current GitHub proof.
- `dogfood_smoke` calls `tools/product-journey/run.py --dogfood-smoke
  --json-output` to prove the no-LLM artifact loop from matrix creation through
  assignment run, seeded evidence, review, validation, rollup, and Slidey decks.
- `rollup` calls `tools/product-journey/run.py --rollup-matrix --json-output`
  to create or refresh the matrix-level Slidey deck from reviewed run bundles.
- `validate_matrix` calls `tools/product-journey/run.py --validate-matrix
  --json-output` to check the matrix and rollup artifact contract without
  rewriting files.
- `start` calls `tools/product-journey/run.py --emit-run --json-output`.
- `attach` calls `tools/product-journey/run.py --attach-evidence --json-output`.
- `record` calls `tools/product-journey/run.py --record-finding --json-output`
  for strengths, weaknesses, issues, and fixes.
- `blocker` calls `tools/product-journey/run.py --record-blocker --json-output`
  when a scenario was attempted but cannot honestly proceed without live
  authorization, a missing cassette, unavailable repo state, or another
  external prerequisite.
- `seed_demo` calls `tools/product-journey/run.py --seed-demo-evidence
  --json-output` to populate a no-LLM review bundle.
- `handoff` calls `tools/product-journey/run.py --driver-handoff --json-output`
  to refresh `driver-handoff.md/json` for the reusable QA driver without
  launching live LLM work.
- `review` calls `tools/product-journey/run.py --review-run --json-output` to
  write `review.json` and score whether the bundle is ready for human review.
- `validate` calls `tools/product-journey/run.py --validate-run --json-output`
  to check required files, metrics freshness, media coverage, scenario
  outcomes, review statuses, and Slidey scenes without rewriting files.
- Flow fixtures stub `host.run`, so automated tests never call a live model or
  external service.

The generated run bundle under `.artifacts/product-journey/<run-id>/` is the
handoff point for visual MCP, Studio MCP, oracle results, and Slidey review.
Use `agent-brief.md` inside that bundle to drive the live persona session, then
use `driver-plan.md` for the scenario harness/visual-surface/action contract and
`execution-plan.md` to copy the generated `--attach-evidence` and
`--record-blocker` commands.
Use `driver-handoff.md` when handing the bundle to
`.agents/agents/product-journey-qa-driver.md`; it names the run directory,
driver inputs, dispatch modes, missing evidence, and final review/validation
commands without spending live model calls.
Captured media is indexed in `media-manifest.json` so the generated Slidey deck
can expose playback-ready videos and screenshots without scraping prose.
Scenario-level evidence and finding summaries are written to
`scenario-outcomes.md` for review and matrix rollups.
Run `review` before `validate`; review refreshes derived artifacts and only
marks a run ready when every scenario has evidence or an explicit blocker, while
validation deliberately catches stale or inconsistent bundles.
