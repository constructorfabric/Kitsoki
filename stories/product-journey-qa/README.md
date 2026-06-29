# Product Journey QA Story

This story wraps the deterministic product-journey runner so operators can drive
the standard persona/scenario evidence workflow inside Kitsoki.

It is intentionally no-LLM:

- `validate_corpus` calls `tools/product-journey/run.py --validate-corpus
  --json-output` to preflight personas, scenarios, quality gates, evidence
  hints, and the 10-repo GitHub target catalog before a sweep.
- `refresh_targets` calls `tools/product-journey/run.py
  --refresh-github-targets --json-output` to write a GitHub target-proof
  artifact for the 100-open-bug and popularity matrix contract.
- `load` calls `tools/product-journey/run.py --summarize-run --json-output` to
  load an existing run bundle into the story. This is the MCP-only driver entry
  point when the reusable driver receives a `run_dir` and needs to attach
  evidence through Kitsoki instead of reading files directly. After `load`, the
  story world `last_result` contains `driver_scenarios`,
  `missing_proof_evidence`, and `driver_final_gates` for the driver to inspect
  through MCP.
- `matrix` calls `tools/product-journey/run.py --emit-matrix --json-output` to
  create the 10-repo GitHub assignment plan. Pass `target_proof_file=...` after
  `refresh_targets` when the matrix should embed current GitHub proof.
- `dogfood_smoke` calls `tools/product-journey/run.py --dogfood-smoke
  --json-output` to prove the no-LLM artifact loop from matrix creation through
  assignment run, seeded evidence, review, validation, rollup, and Slidey decks.
- `driver_replay_smoke` calls `tools/product-journey/run.py
  --driver-replay-smoke --json-output` to prove one reusable-driver scenario
  with cassette-backed proof evidence, linked driver journal refs, media
  manifest coverage, review, validation, and a compact Slidey smoke deck. Pass
  `scenario=project-onboarding`, `scenario=prd-design`, or another scenario id
  to exercise a specific journey path.
- `driver_replay_sweep` calls `tools/product-journey/run.py
  --driver-replay-sweep --json-output` to run the same replay proof for every
  product-journey scenario and summarize playback/validation coverage.
- `rollup` calls `tools/product-journey/run.py --rollup-matrix --json-output`
  to create or refresh the matrix-level Slidey deck from reviewed run bundles.
- `validate_matrix` calls `tools/product-journey/run.py --validate-matrix
  --json-output` to check the matrix and rollup artifact contract without
  rewriting files.
- `start` calls `tools/product-journey/run.py --emit-run --json-output`.
- `attach` calls `tools/product-journey/run.py --attach-evidence --json-output`;
  include `source` when the driver knows whether the artifact is `retained`,
  `external`, `local`, or `cassette`.
- `record` calls `tools/product-journey/run.py --record-finding --json-output`
  for strengths, weaknesses, issues, and fixes.
- `blocker` calls `tools/product-journey/run.py --record-blocker --json-output`
  when a scenario was attempted but cannot honestly proceed without live
  authorization, a missing cassette, unavailable repo state, or another
  external prerequisite.
- `driver_event` calls `tools/product-journey/run.py --record-driver-event
  --json-output` to append what the reusable driver actually attempted to
  `driver-journal.md/json`.
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
Loaded runs expose `last_result.next_driver_capture`,
`last_result.next_driver_attach_command`, and
`last_result.next_driver_blocker_command` so the reusable driver can begin with
the first missing proof slot directly from story state and can record an honest
blocker instead of faking evidence.
Use `driver-journal.md` to review the driver's attempted actions, MCP tools,
evidence references, blockers, and per-scenario status before judging whether a
run reflects natural product usage.
Captured media is indexed in `media-manifest.json` so the generated Slidey deck
can expose playback-ready videos and screenshots without scraping prose.
Scenario-level evidence and finding summaries are written to
`scenario-outcomes.md` for review and matrix rollups.
Run `review` before `validate`; review refreshes derived artifacts and only
marks a run ready when every scenario has evidence or an explicit blocker, while
validation deliberately catches stale or inconsistent bundles. The story view
surfaces review pass/total/fail/warn counts so operators can tell whether a run
is progressing against the full review gate set without opening `review.json`.
Validation views also show a compact issue summary, so the operator can see the
first failing or warning check IDs before opening the raw validator output.
`accept` is validation-gated in run, matrix, and dogfood rooms; if validation has
not reported `valid`, the story stays put and sets `status=accept_needs_validation`.
