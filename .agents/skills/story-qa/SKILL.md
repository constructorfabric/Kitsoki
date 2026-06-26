---
name: story-qa
description: Drive a Kitsoki story as a skeptical practitioner and validate the exploratory QA path against local project corpora. Use when asked to inspect a product flow, run a skeptical walkthrough, or validate an exploratory QA pass against one or more open source projects with reproducible evidence and a written report.
---

# Story QA

`story-qa` is the exploratory QA surface for Kitsoki itself. It is the consumer
of the `mcp-studio` session/render tools: open a session, read the exact frame
the operator would see, decide the next input as a skeptical persona, and write
down what is confusing, missing, or broken.

This repository currently uses the skill in a local, deterministic way to keep
the process honest:

- `tools/product-journey/run.py` validates the checked-in project catalog.
- `tools/story-qa/run.py` wraps those checks into a concise QA report.
- `tools/bugfix-bakeoff/external/bench.py verify --repo-dir <temp-clone>` is the
  deterministic oracle for the `gears-rust` corpus.

## Operating contract

Use the skill as an exploratory reviewer, not a happy-path demo bot.

1. Start from a local product site or story surface.
2. Read the current frame, not the prior intent.
3. Try the natural skeptical phrasing a real engineer would type.
4. If a bug is found, write the concrete reproduction, the relevant state, and
   the source file or project it implicates.
5. Keep the report grounded in artifacts: commands, traces, screenshots, and
   deterministic verifier output.

## Local runbook

```sh
python3 tools/story-qa/run.py
python3 tools/story-qa/run.py --project gears-rust
python3 tools/story-qa/run.py --project all
```

Those commands are summary-only. To run deterministic local oracle checks, opt in
explicitly:

```sh
python3 tools/story-qa/run.py --project postgresql --check
python3 tools/story-qa/run.py --project kubernetes --check --timeout 900
```

For the local product site/docs surface, run the web app from the workspace:

```sh
GOCACHE=$(mktemp -d) go run ./cmd/kitsoki web --addr 127.0.0.1:7777
```

For `gears-rust`, the deterministic verify path clones the local checkout into a
temporary no-local mirror and arms the baked fixtures against that copy. The
other project lanes remain placeholders until a local corpus is available.

## Evidence

The skill writes a transient report under `.context/` and prints the exact status
per project. For repeatable product-journey review artifacts and a Slidey deck,
use:

```sh
python3 tools/product-journey/run.py --emit-run --project gears-rust --persona core-maintainer --seed demo
```

For a 10-repo GitHub natural-usage sweep, start with the deterministic matrix:

```sh
python3 tools/product-journey/run.py --refresh-github-targets --seed demo
python3 tools/product-journey/run.py --emit-matrix --seed demo
python3 tools/product-journey/run.py --emit-matrix --seed demo \
  --target-proof-file .artifacts/product-journey/target-proofs/<proof-id>
```

Before spending live operator time, run the no-LLM dogfood smoke:

```sh
python3 tools/product-journey/run.py --dogfood-smoke --seed demo
```

Inside `stories/product-journey-qa/app.yaml`, submit
`dogfood_smoke seed=demo` for the same proof with story-visible artifact paths.

That single command composes the normal pieces: a 10-repo matrix, one concrete
assignment run, representative demo evidence, review, run validation, matrix
rollup, matrix validation, and a smoke-level Slidey deck under
`.artifacts/product-journey/dogfood/<dogfood-id>/`. Treat it as an artifact-loop
proof only; it does not replace live visual MCP or cassette evidence.

Use `--matrix-personas all` when every persona should run against every target.
The matrix is a no-LLM assignment plan; before a live scored sweep, refresh each
target's current open bug count from its `bug_query` with
`--refresh-github-targets` and feed the proof into `--emit-matrix`. Validation
warns when proof is missing and fails when proof shows a target below the
100-open-bug floor.
Launch an assignment run with the target `id` and persona from that matrix:

```sh
python3 tools/product-journey/run.py --emit-run --project vscode --persona docs-minded-contributor --seed demo-01
```

After assignment runs are reviewed, create the matrix rollup deck:

```sh
python3 tools/product-journey/run.py --rollup-matrix \
  --matrix-dir .artifacts/product-journey/matrices/<matrix-id> \
  --rollup-run-dir .artifacts/product-journey/<run-id>
```

That writes `.artifacts/product-journey/<run-id>/`, including
`deck.slidey.json`, without calling a live LLM. Add `--publish-deck` when you
want to update `docs/decks/product-journey-eval.slidey.json`.
The rollup aggregates `scenario-outcomes.json` across runs so repeated weak
scenarios stay visible at matrix-review time.
Validate generated matrices before using them as the shared sweep contract:

```sh
python3 tools/product-journey/run.py --validate-matrix \
  --matrix-dir .artifacts/product-journey/matrices/<matrix-id>
```

The bundle's `agent-brief.md`, `scenarios.json`, and `evidence.json` are the
contract for live or cassette-backed MCP runs: each scenario names the story
surface, required Kitsoki/visual MCP tools, expected evidence, and success
criteria, while the brief states the persona and operating rules. Use
`execution-plan.md` in the same bundle to follow the concrete MCP capture
sequence and copy ready-to-fill `--attach-evidence` commands.
For a live/cassette dogfood pass, delegate the bundle to
`.agents/agents/product-journey-qa-driver.md`; that agent is scoped to consume
the brief, drive Kitsoki Studio MCP and visual MCP, attach evidence, record
findings or blockers, then run review and validation gates.
`driver-plan.md` is the driver's machine-readable companion rendered for human
review: it lists each scenario's harness, visual surface, action sequence,
evidence slots, attach commands, finding command, blocker command, and final
gates.
Captured screenshots, videos, traces, and documents are indexed in
`media-manifest.json`; the generated Slidey deck uses that manifest for
playback-ready media entries.
Evidence paths should be real run-relative files, absolute paths, repo-root
paths, URLs, or retained MCP references such as `retained://...` and
`image://...`. The review and validation gates warn when captured local paths do
not resolve, so placeholder media cannot silently pass as playback proof.
Use `scenario-outcomes.md` to review evidence coverage and findings per
scenario before treating a run as representative natural-usage proof.

After capturing evidence, attach it back to the bundle:

```sh
python3 tools/product-journey/run.py --attach-evidence \
  --run-dir .artifacts/product-journey/<run-id> \
  --scenario bugfix \
  --evidence-kind key_interaction_video \
  --evidence-path media/bugfix.mp4
```

Use `--record-finding` on the same runner to summarize strengths, weaknesses,
issues found, and fixes for the Slidey review deck.

If a scenario was attempted but cannot honestly capture evidence without live
authorization, a missing cassette, or unavailable repo state, record a blocker
instead of faking a pass:

```sh
python3 tools/product-journey/run.py --record-blocker \
  --run-dir .artifacts/product-journey/<run-id> \
  --scenario prd-design \
  --title "Design path needs a cassette" \
  --summary "No deterministic cassette exists and automated tests must stay no-LLM."
```

The review gate only treats the run as ready when each scenario has captured
evidence or an explicit blocker.

Use `--seed-demo-evidence` only for deterministic no-LLM deck-shape dogfood
before a live visual MCP run.

Before presenting a bundle as review-ready, run the readiness gate:

```sh
python3 tools/product-journey/run.py --review-run \
  --run-dir .artifacts/product-journey/<run-id>
```

The gate writes `review.json`, updates `metrics.json`, and adds a Slidey scene
with hard failures and softer evidence-quality warnings.
Then run the read-only bundle validator so stale derived files or schema drift
fail deterministically before review:

```sh
python3 tools/product-journey/run.py --validate-run \
  --run-dir .artifacts/product-journey/<run-id>
```

The `tools/story-qa/run.py` runner also writes a transient pointer report under
`.context/` and a durable review bundle under `.artifacts/story-qa/<run>/`
containing `report.md`, `summary.json`, and `deck.slidey.json`. The deck is
generated deterministically from structured target/verification rows, so planned
or blocked lanes stay visible without pretending they are validated.
