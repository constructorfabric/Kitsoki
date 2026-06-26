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

Emit a repeatable 10-repo GitHub planning matrix:

```sh
python3 tools/product-journey/run.py --emit-matrix --seed demo
python3 tools/product-journey/run.py --emit-matrix --seed demo --matrix-personas all
```

This writes `.artifacts/product-journey/matrices/<matrix-id>/` with
`matrix.json`, `matrix.md`, and `deck.slidey.json`. The source target list lives
in `github-targets.json`; refresh each target's `bug_query` before a live scored
sweep so current open bug counts are recorded outside the no-LLM planner.
Every target `id` from `github-targets.json` is also accepted by `--emit-run`,
so a matrix assignment can become a concrete run bundle:

```sh
python3 tools/product-journey/run.py --emit-run --project vscode --persona docs-minded-contributor --seed demo-01
```

This writes `.artifacts/product-journey/<run-id>/` with `run.json`,
`journey.md`, `metrics.json`, `bugs.json`, `findings.json`, `evidence.json`,
`scenarios.json`, `execution-plan.json`, `execution-plan.md`, `review.json`,
and `deck.slidey.json`. Add `--publish-deck` when the generated deck should
replace `docs/decks/product-journey-eval.slidey.json` for review.

Use `execution-plan.md` as the operator handoff: it lists the scenario order,
the MCP tools to use, expected evidence slots, and ready-to-fill
`--attach-evidence` commands.

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

Record a review finding for the deck summary:

```sh
python3 tools/product-journey/run.py --record-finding \
  --run-dir .artifacts/product-journey/<run-id> \
  --finding-kind weakness \
  --scenario project-onboarding \
  --title "Onboarding hid the next command" \
  --summary "The persona could not tell which Kitsoki story to launch after config generation."
```

Finding kinds are `strength`, `weakness`, `issue`, and `fix`.

For a no-LLM dogfood/demo bundle with representative evidence and findings:

```sh
python3 tools/product-journey/run.py --seed-demo-evidence \
  --run-dir .artifacts/product-journey/<run-id>
```

This is not a substitute for real visual MCP capture, but it proves the report
aggregation and Slidey deck shape before a live run.

Review whether a bundle is ready for human discussion:

```sh
python3 tools/product-journey/run.py --review-run \
  --run-dir .artifacts/product-journey/<run-id>
```

The review writes `review.json`, updates `metrics.json`, and adds a readiness
scene to `deck.slidey.json`. Hard failures mean the bundle is still skeletal;
warnings identify useful evidence quality improvements, such as missing key
interaction video.

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
- `github-targets.json` — 10 GitHub candidate targets for natural-usage
  journey sweeps.
- `personas.json` — reusable personas for deterministic journey assignment.
- `scenarios.json` — reusable scenario/task definitions with required MCP tools,
  expected evidence, and success criteria.
- `schema.json` — current artifact and stage contract.
- `run.py` — entrypoint script used by the journey orchestrator.

## Output discipline

- `.context/product-journey-runlog.md` stores the run log in the worktree root.
- `docs/decks/product-journey-eval.slidey.json` stores the hand-refined,
  proof-ready narrative reference. Report generation links to it and does not
  overwrite it.
- `.artifacts/product-journey-eval/<generated-at>/deck.slidey.json` is the
  generated companion deck for a specific structured report run.
