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

For `gears-rust`, this prints the existing external-bakeoff readiness signal and
the local-only verification command. If you have a local checkout, it also
emits the exact environment-required command for validation:

```sh
GEARS_RUST_REPO=~/code/gears-rust make gears-bakeoff
```

`postgresql` and `kubernetes` use local oracle scripts in
`tools/product-journey/checks/` so the runner can prove the real red@baseline /
green@fix split from the checked-out local repos.

Generate the deterministic report JSON, companion Slidey deck spec, and Markdown
review index:

```sh
python3 tools/product-journey/run.py \
  --mode report \
  --generated-at 2026-06-26T00:00:00Z
```

By default this writes:

- `.artifacts/product-journey-eval/<generated-at>/report.json`
- `.artifacts/product-journey-eval/<generated-at>/deck.slidey.json`
- `.artifacts/product-journey-eval/<generated-at>/report.md`

Use `--run-checks` only when you want to refresh local oracle evidence while
building the report. The default report uses the catalog's current validated
state and does not run expensive checks.

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
- `run.py` — entrypoint script used by the journey orchestrator.

## Output discipline

- `.context/product-journey-runlog.md` stores the run log in the worktree root.
- `docs/decks/product-journey-eval.slidey.json` stores the hand-refined,
  proof-ready narrative reference. Report generation links to it and does not
  overwrite it.
- `.artifacts/product-journey-eval/<generated-at>/deck.slidey.json` is the
  generated companion deck for a specific structured report run.
