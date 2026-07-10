# Story QA Runner

This runner is the local, reproducible front door for the exploratory QA agent
surface.

It does two things:

1. summarizes the project lanes that the exploratory QA pass cares about;
2. runs the deterministic verification path for `gears-rust` when a local
   checkout is available.

## Commands

```sh
python3 tools/story-qa/run.py
python3 tools/story-qa/run.py --project gears-rust
python3 tools/story-qa/run.py --project all
```

These commands are summary-only. They do not run heavyweight local project
oracles unless you opt in:

```sh
python3 tools/story-qa/run.py --project postgresql --check
python3 tools/story-qa/run.py --project kubernetes --check --timeout 900
```

The runner writes a transient pointer report to `.context/story-qa-run.md` and a
reviewable artifact bundle under `.artifacts/story-qa/<run>/`:

- `report.md`
- `summary.json`
- `deck.slidey.json`

## Local product site

Stage the Kitsoki product site as a local production build so the QA agent
always points at a deterministic, host-local surface:

```sh
make web
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" go run ./cmd/kitsoki web --addr 127.0.0.1:7777
```

To emit a deterministic product-journey artifact bundle and Slidey deck without
live LLM usage:

```sh
python3 tools/product-journey/run.py --emit-run --project gears-rust --persona core-maintainer --seed demo
```

To plan the broader 10-repo GitHub sweep requested for natural usage:

```sh
python3 tools/product-journey/run.py --emit-matrix --seed demo
```

Use `--matrix-personas all` when every persona should be assigned to every
target. The matrix is a planning artifact; refresh current GitHub bug counts
from each target's `bug_query` before a live scored run.
Then launch an assignment run with the target `id` and persona from the matrix:

```sh
python3 tools/product-journey/run.py --emit-run --project vscode --persona docs-minded-contributor --seed demo-01
```

After assignment runs are reviewed, produce the matrix rollup deck:

```sh
python3 tools/product-journey/run.py --rollup-matrix \
  --matrix-dir .artifacts/product-journey/matrices/<matrix-id> \
  --rollup-run-dir .artifacts/product-journey/<run-id>
```

Add `--publish-deck` when the generated deck should replace
`docs/decks/product-journey-eval.slidey.json`.

The generated bundle includes `scenarios.json` and `evidence.json`. Those files
are the handoff contract for live or cassette-backed MCP runs: each scenario
names the story surface, required Kitsoki/visual MCP tools, expected evidence,
and success criteria.
It also includes `execution-plan.md`, which orders the scenarios, lists concrete
MCP capture steps, and provides ready-to-fill `--attach-evidence` commands.

After a run captures evidence, attach references back to the bundle with
`tools/product-journey/run.py --attach-evidence`. The command regenerates the
journey summary, metrics, and Slidey deck.

Use `tools/product-journey/run.py --record-finding` to summarize strengths,
weaknesses, issues found, and fixes for the final deck.

Use `tools/product-journey/run.py --seed-demo-evidence` only for deterministic
no-LLM deck-shape dogfood before a live visual MCP run.

Before treating a bundle as review-ready, run:

```sh
python3 tools/product-journey/run.py --review-run \
  --run-dir .artifacts/product-journey/<run-id>
```

That writes `review.json`, updates deck metrics, and distinguishes hard missing
evidence from softer quality warnings.
