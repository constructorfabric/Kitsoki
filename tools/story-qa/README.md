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

The runner writes a transient report to `.context/story-qa-run.md`.

## Local product site

Stage the Kitsoki product site as a local production build so the QA agent
always points at a deterministic, host-local surface:

```sh
make web
GOCACHE=$(mktemp -d) go run ./cmd/kitsoki web --addr 127.0.0.1:7777
```

To emit a deterministic product-journey artifact bundle and Slidey deck without
live LLM usage:

```sh
python3 tools/product-journey/run.py --emit-run --project gears-rust --persona core-maintainer --seed demo
```

Add `--publish-deck` when the generated deck should replace
`docs/decks/product-journey-eval.slidey.json`.

The generated bundle includes `scenarios.json` and `evidence.json`. Those files
are the handoff contract for live or cassette-backed MCP runs: each scenario
names the story surface, required Kitsoki/visual MCP tools, expected evidence,
and success criteria.

After a run captures evidence, attach references back to the bundle with
`tools/product-journey/run.py --attach-evidence`. The command regenerates the
journey summary, metrics, and Slidey deck.
