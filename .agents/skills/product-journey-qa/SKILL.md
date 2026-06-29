---
name: product-journey-qa
description: Run or refine the Kitsoki product-journey QA pipeline: generate 10-GitHub-repo natural-use matrices, create persona/scenario run bundles, drive or hand off visual/Kitsoki MCP evidence capture, run no-LLM replay/dogfood gates, review/validate bundles, and produce Slidey decks with playback evidence. Use when asked to dogfood onboarding, bugfix, PRD/design, feature implementation, product-discovery, product-bug, personas, or the reusable product-journey QA agent/story.
---

# Product Journey QA

Use this skill when the task is to evaluate Kitsoki as a product through
repeatable persona journeys, not when merely editing one unrelated story room.
The durable surfaces are:

- Runner: `tools/product-journey/run.py`
- Story wrapper: `stories/product-journey-qa/app.yaml`
- Driver agent: `.agents/agents/product-journey-qa-driver.md`
- Catalogs: `tools/product-journey/personas.json`,
  `tools/product-journey/scenarios.json`,
  `tools/product-journey/github-targets.json`
- Review output: `.artifacts/product-journey/**/deck.slidey.json`,
  `review.json`, `media-manifest.json`, `scenario-outcomes.md`,
  `driver-journal.md`, `driver-handoff.md`

## Operating Rules

- Automated tests and repeatable gates must not call a real LLM. Use flow
  fixtures, cassettes, demo evidence, or replay artifacts.
- Live/model work is only for explicitly authorized exploratory runs. If a
  scenario needs live authorization and none was given, record a blocker instead
  of faking evidence.
- Proof evidence sources are `local`, `retained`, `external`, and `cassette`.
  `demo` evidence exercises aggregation only; it is not product proof.
- Preserve the persona lens. A core maintainer, dependency debugger,
  docs-minded contributor, IDE-first engineer, and hobbyist contributor should
  start from different surfaces, ask different first questions, and weigh
  evidence differently.
- Every scenario attempt needs a driver journal event, even when it ends in a
  blocker.
- A bundle is not discussion-ready until `--review-run` and `--validate-run`
  have run and the deck has playback media or an explicit playback blocker.

## No-LLM Gates

Start with the cheap gates before live capture:

```sh
python3 tools/product-journey/run.py --validate-corpus --json-output
python3 tools/product-journey/run.py --driver-replay-sweep --seed demo --json-output
GOCACHE=/private/tmp/kitsoki-gocache go run ./cmd/kitsoki test flows stories/product-journey-qa/app.yaml
```

Use `--driver-replay-smoke --smoke-scenario <scenario-id>` when narrowing a
single scenario. Use `--dogfood-smoke` when checking matrix-to-rollup artifact
composition.

## Matrix Workflow

For the 10 popular GitHub repositories with at least 100 open bugs:

```sh
python3 tools/product-journey/run.py --refresh-github-targets --seed <seed>
python3 tools/product-journey/run.py --emit-matrix --seed <seed> \
  --target-proof-file .artifacts/product-journey/target-proofs/<proof-id>
python3 tools/product-journey/run.py --validate-matrix \
  --matrix-dir .artifacts/product-journey/matrices/<matrix-id> \
  --strict-target-proof
```

Use `--matrix-personas all` when each target should be paired with every
persona. Use normal `--validate-matrix` for draft matrices; use
`--strict-target-proof` before a live scored sweep so missing refreshed GitHub
bug/star/license proof is an error. The matrix is an assignment plan, not
evidence that Kitsoki worked.

## Run Bundle Workflow

Create one bundle from a matrix assignment or direct target:

```sh
python3 tools/product-journey/run.py --emit-run \
  --project <target-id> --persona <persona-id> --seed <seed>
```

Then hand it to the reusable driver:

1. Read `agent-brief.md`, `driver-plan.md`, and `driver-handoff.md`.
2. Use `.agents/agents/product-journey-qa-driver.md` for live/cassette MCP
   capture.
3. Attach evidence with `--attach-evidence` or the story `attach` intent;
   loaded runs also expose `last_result.next_driver_attach_command`.
4. Record findings with `--record-finding`; record honest blockers with
   `--record-blocker` or `last_result.next_driver_blocker_command`.
5. Record each attempt with `--record-driver-event` or the story `driver_event`
   intent.
6. Run:

```sh
python3 tools/product-journey/run.py --review-run --run-dir <run-dir>
python3 tools/product-journey/run.py --validate-run --run-dir <run-dir>
```

Review `deck.slidey.json` for the narrative, `Playback evidence` scenes,
`Proof gates`, `Persona lens`, and `Driver contract` scenes.

## Story Surface

When driving through Kitsoki itself, open `stories/product-journey-qa/app.yaml`.
Useful intents:

- `validate_corpus`
- `matrix seed=... matrix_personas=primary|all`
- `driver_replay_smoke scenario=... persona=... seed=...`
- `driver_replay_sweep persona=... seed=...`
- `start project=... persona=... seed=...`
- `load run_dir=...`
- `handoff`
- `attach`
- `record`
- `blocker`
- `driver_event`
- `validate_matrix_strict`
- `review`
- `validate`

Prefer the story as the write surface when an operator session is attached.
Use CLI fallback when the story session is unavailable.

## Improvement Loop

When refining the pipeline:

1. Identify the missing proof from `review.json`, `validation` output,
   `driver-handoff.md`, or a failed flow.
2. Patch the smallest durable surface: catalog, runner, story, driver agent, or
   this skill.
3. Add or update a deterministic flow/cassette/replay check.
4. Re-run `--validate-corpus`, `--driver-replay-sweep`, and product-journey
   story flows.
5. Commit only the product-journey slice, leaving unrelated workspace dirt
   untouched.
