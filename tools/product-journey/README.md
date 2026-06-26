# Product Journey Evaluator

This directory holds the first runnable harness for the **exploratory product
journey** experiment:

- discover how Kitsoki behaves as a skeptical, practical evaluator,
- keep checks deterministic by default,
- and emit evidence artifacts (log + deck) as execution progresses.

## How to run

The harness is intentionally small and opinionated for the first milestone:

```sh
python3 tools/product-journey/run.py
```

That prints catalog status and perspective checks (including PostgreSQL and
Kubernetes placeholders).

Run a specific project check:

```sh
python3 tools/product-journey/run.py --project gears-rust --mode check
```

Emit a repeatable no-LLM dry-run bundle and Slidey deck:

```sh
python3 tools/product-journey/run.py --emit-run --project gears-rust --persona core-maintainer --seed demo
```

This writes `.artifacts/product-journey/<run-id>/` with `run.json`,
`journey.md`, `metrics.json`, `bugs.json`, `evidence.json`, `scenarios.json`,
and `deck.slidey.json`. Add `--publish-deck` when the generated deck should replace
`docs/decks/product-journey-eval.slidey.json` for review.

Attach evidence captured by a live or cassette-backed MCP run:

```sh
python3 tools/product-journey/run.py --attach-evidence \
  --run-dir .artifacts/product-journey/<run-id> \
  --scenario bugfix \
  --evidence-kind key_interaction_video \
  --evidence-path media/bugfix.mp4 \
  --notes "visual MCP capture from bugfix handoff"
```

Attachment updates `evidence.json`, `scenarios.json`, `metrics.json`,
`journey.md`, and `deck.slidey.json`.

For `gears-rust`, this prints the existing external-bakeoff readiness signal and
the local-only verification command. If you have a local checkout, it also
emits the exact environment-required command for validation:

```sh
GEARS_RUST_REPO=~/code/gears-rust make gears-bakeoff
```

`postgresql` and `kubernetes` use local oracle scripts in
`tools/product-journey/checks/` so the runner can prove the real red@baseline /
green@fix split from the checked-out local repos.

### Local product site for deterministic A/B testing

For all journey runs, use a local production build of the product site so no remote state is shared:

```sh
make web
GOCACHE=$(mktemp -d) go run ./cmd/kitsoki web --addr 127.0.0.1:7777
```

This stages the production bundle locally and then serves it from a reproducible
local endpoint (`http://127.0.0.1:7777`) for every run against docs,
onboarding, and bugfix surfaces.

## Files

- `catalog.json` — first-pass project + perspective registry.
- `personas.json` — reusable personas for deterministic journey assignment.
- `scenarios.json` — reusable scenario/task definitions with required MCP tools,
  expected evidence, and success criteria.
- `schema.json` — current artifact and stage contract.
- `run.py` — entrypoint script used by the journey orchestrator.

## Output discipline

- `.context/product-journey-runlog.md` stores the run log in the worktree root.
- `docs/decks/product-journey-eval.slidey.json` stores the proof-ready
  narrative state of progress.
