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

Validate the reusable natural-use corpus before planning a sweep:

```sh
python3 tools/product-journey/run.py --validate-corpus
```

This checks that personas, scenarios, quality gates, evidence hints, and the
10-repo GitHub target catalog still line up. Inside
`stories/product-journey-qa/app.yaml`, submit `validate_corpus` for the same
no-LLM preflight.

Emit a repeatable 10-repo GitHub planning matrix:

```sh
python3 tools/product-journey/run.py --refresh-github-targets --seed demo
python3 tools/product-journey/run.py --emit-matrix --seed demo
python3 tools/product-journey/run.py --emit-matrix --seed demo \
  --target-proof-file .artifacts/product-journey/target-proofs/<proof-id>
python3 tools/product-journey/run.py --emit-matrix --seed demo --matrix-personas all
```

Prove the no-LLM end-to-end artifact loop in one command:

```sh
python3 tools/product-journey/run.py --dogfood-smoke --seed demo
```

This creates a 10-repo matrix, turns the first deterministic assignment into a
run bundle, seeds representative demo evidence plus one driver journal event per
scenario, reviews and validates the run, rolls it back into the matrix,
validates the matrix, and writes a smoke report under
`.artifacts/product-journey/dogfood/<dogfood-id>/`. It also emits a smoke-level
Slidey deck plus the normal run, matrix, and rollup decks. The demo evidence
proves aggregation, driver-journal wiring, and deck shape only; live visual MCP
or cassette evidence is still required before making product claims. Because
seeded demo artifacts are not proof evidence, the smoke command can pass while
the seeded run review remains `needs_evidence` with an unsatisfied quality gate.

This writes `.artifacts/product-journey/matrices/<matrix-id>/` with
`matrix.json`, `matrix.md`, and `deck.slidey.json`. The source target list lives
in `github-targets.json`; `--refresh-github-targets` writes
`.artifacts/product-journey/target-proofs/<proof-id>/target-proof.json` with
current GitHub API counts for each target's `bug_query` plus repository
popularity and license metadata. Feed that proof into `--emit-matrix` before a
live scored sweep so the matrix records whether every target currently satisfies
the 100-open-bug floor, configured stargazer floor, and open-source license
contract.
Each matrix assignment includes deterministic `scenario_tasks` that specialize
the shared scenarios for the target repository, persona, stack, and bug query;
use those prompts to keep natural-use runs repeatable instead of inventing a new
task shape per run.
Every target `id` from `github-targets.json` is also accepted by `--emit-run`,
so a matrix assignment can become a concrete run bundle:

```sh
python3 tools/product-journey/run.py --emit-run --project vscode --persona docs-minded-contributor --seed demo-01
```

After one or more assignment runs have captured evidence and been reviewed,
roll them back up into the matrix:

```sh
python3 tools/product-journey/run.py --rollup-matrix \
  --matrix-dir .artifacts/product-journey/matrices/<matrix-id> \
  --rollup-run-dir .artifacts/product-journey/<run-id>
```

The rollup writes `rollup.json`, `rollup.md`, and `rollup.slidey.json` into the
matrix directory. Omit `--rollup-run-dir` to auto-discover run bundles whose
project, persona, and seed match matrix assignments. Use the assignment
`emit_run_command` in `matrix.json` or `matrix.md` when you want auto-discovery
to pick up the run without extra flags. The rollup includes per-scenario
outcome totals so repeated onboarding, bugfix, PRD/design, implementation, and
product-bug gaps are visible across runs. It also includes per-persona outcome
rows so cross-run review can compare whether core maintainer, dependency
debugger, docs-minded contributor, and IDE-first lenses produce different
evidence and findings. It also aggregates scenario
`quality_gate` coverage so the matrix deck shows which journeys have enough
proof-source minimum evidence to count as completed, plus a missing-proof
evidence backlog that names the evidence kinds still needing live visual MCP or
cassette-backed capture.
Validate a generated matrix before using it as the sweep contract:

```sh
python3 tools/product-journey/run.py --validate-matrix \
  --matrix-dir .artifacts/product-journey/matrices/<matrix-id>
```

This writes `.artifacts/product-journey/<run-id>/` with `run.json`,
`journey.md`, `metrics.json`, `bugs.json`, `findings.json`,
`scenario-outcomes.json`, `scenario-outcomes.md`, `evidence.json`,
`media-manifest.json`, `scenarios.json`, `execution-plan.json`,
`execution-plan.md`, `driver-plan.json`, `driver-plan.md`,
`driver-journal.json`, `driver-journal.md`, `agent-brief.json`,
`agent-brief.md`, `driver-handoff.json`, `driver-handoff.md`, `review.json`,
and `deck.slidey.json`.
Add `--publish-deck` when the generated deck should replace
`docs/decks/product-journey-eval.slidey.json` for review.

Use `agent-brief.md` as the live-driver handoff: it states the persona,
operating rules, persona lens, scenario order, MCP tools, success criteria, and
missing evidence without implying planned steps are validated. The lens makes
the same scenario run differently for core maintainers, dependency debuggers,
docs-minded contributors, and IDE-first engineers while keeping behavior
repeatable. The brief names
`.agents/agents/product-journey-qa-driver.md` as the reusable live/cassette
driver for Kitsoki Studio MCP and visual MCP runs. Use `driver-plan.md` for the
machine-readable harness, visual-surface, action-sequence, and gate contract,
`driver-journal.md` for the auditable record of what the driver actually tried,
`execution-plan.md` for the detailed evidence slots and ready-to-fill
`--attach-evidence` commands, and `driver-handoff.md` as the operator handoff
that names the driver agent, dispatch modes, missing evidence, and final gates
without launching live LLM work. When demo or partial evidence is already
attached, use the handoff's `Missing Proof Evidence` section as the live or
cassette capture backlog; raw `missing_evidence` can be empty while proof-source
quality gates are still unsatisfied.
Each scenario also carries a `quality_gate` with `minimum_evidence`,
`done_when`, and `block_if` rules. Live/cassette drivers should satisfy that
gate before calling a scenario done, or record a blocker tied to the matching
condition. The generated Slidey deck includes a `Proof gates` scene that rolls
up each scenario's minimum-evidence coverage and current outcome for review.

Attach evidence captured by a live or cassette-backed MCP run:

```sh
python3 tools/product-journey/run.py --attach-evidence \
  --run-dir .artifacts/product-journey/<run-id> \
  --scenario bugfix \
  --evidence-kind key_interaction_video \
  --evidence-path media/bugfix.mp4 \
  --evidence-source local \
  --notes "visual MCP capture from bugfix handoff"
```

After each scenario attempt, append a driver journal event so the run records
what was actually tried:

```sh
python3 tools/product-journey/run.py --record-driver-event \
  --run-dir .artifacts/product-journey/<run-id> \
  --scenario bugfix \
  --dispatch-mode replay \
  --driver-status captured \
  --mcp-tools session.open,render.tui,visual.observe \
  --evidence-refs traces/bugfix.jsonl,media/bugfix.mp4 \
  --summary "Replayed the bugfix story through the oracle gate."
```

Attachment updates `evidence.json`, `media-manifest.json`, `scenarios.json`,
`scenario-outcomes.md`, `metrics.json`, `agent-brief.md`, `journey.md`, and
`deck.slidey.json`.
The manifest classifies captured artifacts as video, image, trace, document, or
artifact and feeds the Slidey playback scene with structured media entries.
Embeddable video, rrweb, and image evidence also produce standalone
`Playback evidence` scenes in `deck.slidey.json`, so review can jump directly to
key interactions instead of scraping paths from prose.
The generated deck also includes a `Persona lens` scene so cross-run review can
compare why a core maintainer, dependency debugger, docs-minded contributor, or
IDE-first engineer started from different surfaces and weighted evidence
differently.
Scenario outcomes summarize evidence coverage and finding counts per scenario
so onboarding, bugfix, PRD/design, feature implementation, and product-bug gaps
stay visible independently of the bundle-level review status.
Use `--evidence-source local`, `retained`, `external`, or `cassette` for proof
evidence. `demo` is reserved for deterministic placeholder evidence, and
captured `unknown` evidence does not count as proof evidence. Use a real local
path relative to the run bundle, an absolute path, a repo-root path, a URL, or a
retained MCP reference such as `retained://...` or `image://...`. Review and
validation warn when captured local paths do not
resolve, so placeholder media cannot silently look like real playback proof.
Each evidence row also carries a `source`: `demo` for seeded placeholders,
`retained` for MCP retained references, `external` for URLs, `local` for
file-backed captures, `cassette` for recorded deterministic replay artifacts,
and `unknown` when a captured source cannot be inferred. Review decks and
readiness checks count proof evidence separately from demo/unknown evidence so a
no-LLM smoke can exercise the artifact loop without passing as live product
proof.

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

If a scenario was attempted but cannot honestly capture evidence under the
current harness, record a blocker instead of leaving it invisible or pretending
it passed:

```sh
python3 tools/product-journey/run.py --record-blocker \
  --run-dir .artifacts/product-journey/<run-id> \
  --scenario prd-design \
  --title "Design scenario requires live model authorization" \
  --summary "No cassette exists for this path, and automated tests must stay no-LLM."
```

The review gate treats a scenario as attempted when it has captured evidence or
an explicit blocker, so missing live paths stay visible in the deck and rollup.

For a no-LLM dogfood/demo bundle with representative evidence and findings:

```sh
python3 tools/product-journey/run.py --seed-demo-evidence \
  --run-dir .artifacts/product-journey/<run-id>
```

This is not a substitute for real visual MCP capture, but it proves the report
aggregation, quality-gate accounting, driver-journal coverage, and Slidey deck
shape before a live run. It marks every required evidence slot captured with
deterministic placeholder paths and records one replay-mode driver event per
scenario, so review gates can exercise the full artifact contract while
validation still warns that those local paths do not resolve and review warns
that evidence is demo-only.

Review whether a bundle is ready for human discussion:

```sh
python3 tools/product-journey/run.py --review-run \
  --run-dir .artifacts/product-journey/<run-id>
```

The review writes `review.json`, updates `metrics.json`, and adds a readiness
scene to `deck.slidey.json`. Hard failures mean the bundle is still skeletal;
warnings identify useful evidence quality improvements, such as missing key
interaction video.

Prepare the reusable driver handoff without spending live model calls:

```sh
python3 tools/product-journey/run.py --driver-handoff \
  --run-dir .artifacts/product-journey/<run-id>
```

Inside `stories/product-journey-qa/app.yaml`, submit `handoff` from a run to
refresh the same `driver-handoff.md/json` artifacts.

After review, run the read-only validator before treating the artifacts as a
stable contract for a live or cassette-backed run:

```sh
python3 tools/product-journey/run.py --validate-run \
  --run-dir .artifacts/product-journey/<run-id>
```

The validator checks required files, JSON shape, scenario/evidence/media
consistency, metrics freshness, review statuses, and Slidey review scenes
without rewriting the bundle. If it fails, run `--review-run` again after fixing
or attaching the missing artifact.

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
