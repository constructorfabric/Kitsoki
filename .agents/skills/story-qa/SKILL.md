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

For the local product site/docs surface, run the web app from the workspace:

```sh
GOCACHE=$(mktemp -d) go run ./cmd/kitsoki web --addr 127.0.0.1:7777
```

For `gears-rust`, the deterministic verify path clones the local checkout into a
temporary no-local mirror and arms the baked fixtures against that copy. The
other project lanes remain placeholders until a local corpus is available.

## Evidence

The skill writes a transient report under `.context/` and prints the exact status
per project. That keeps the exploratory pass reviewable without pretending the
planned lanes are already validated.
