# arena — generalized comparison-job runner

One tool to run any large comparison/sweep — **bug-fixing, onboarding, persona
QA, …** — where every cell executes **in a Docker container**, and containers are
placed on **either the local Docker host or a remote VM** (a VM is just another
Docker host). It unifies `tools/bugfix-bakeoff/` (the cell executor) and the
matrix half of `tools/product-journey/` (the planner/rollup brain) behind a
single `JobSpec` and a **job-type plugin** interface.

Design + rationale: [`docs/proposals/arena-comparison-runner.md`](../../docs/proposals/arena-comparison-runner.md).

## The model

```
JobSpec ──enumerate──▶ Cell[] ──execute(container)──▶ CellResult[] ──▶ rollup
   │                      │                                │
 job_type            target × variant ×                job-type-agnostic
 targets[]           axis (e.g. bug)                   verdict + health + metrics
 variants[]          = the unit of work
 axes{}
 placement{hosts,…}
```

- **Cell** = `target × variant × axis-coords` for one `job_type`. The bakeoff
  `candidate` and the product-journey `persona/scenario` both collapse into a
  cell's **variant + axis**.
- **Two orthogonal axes:** *containerization* is always on (the cell's isolation
  unit); *placement* is just which Docker host the container lands on — `local`
  (active daemon) or `vm-N` (a remote context). The VM **executes the container**;
  there is no separate VM code path.
- **Job-type plugin** is the only thing that knows what a comparison *means*:
  `image(cell)` · `drive_command(cell, live)` · `score(cell, …) → CellResult`.

## The shared completion-state contract

`bugfix.py`'s `score()` no longer regexes the container's stdout/stderr. Whatever
runs inside the container (`bench.py verify`/`score`, including live
`drive_cell.sh --score`) writes one **versioned completion-state JSON** —
[`schemas/completion-state.schema.json`](../../schemas/completion-state.schema.json)
(`verdict` / `health` / `metrics` / `evidence_refs`) — that this plugin reads back
from the shared repo mount (`KITSOKI_MNT` inside the container == `REPO_ROOT`
outside it, for `local` placement) instead of parsing text. The same contract
backs the product-journey side: `tools/persona_qa/completion.py` builds a
schema-conformant completion-state from a `review.json`/`scenario-outcomes.json`
run bundle, so persona-qa arena cells score from the identical shape a bugfix
cell does. Shared helpers in `tools/completion_state.py` own deterministic JSON
writing and validation; `arena/artifact_adapters.py` normalizes producer
artifacts (`completion-state`, `swarm-results`, `ui-qa-verdict`,
`ui-review-verdict`, `product-journey-review`) into that contract before rollup.
A missing or malformed file is reported as an explicit
`infra:*` health (`infra:missing-completion-state`,
`infra:completion-state-malformed`) — stdout/stderr infra-signal regexing (e.g.
`"connection refused"`) survives ONLY as a fallback for when the file is absent.

## The check-type contract (WS-G G1)

A matrix/arena cell declares a **check suite**, not one verdict shape. Every
check emits one completion-state verdict tagged with the schema's optional
`check_type` discriminator; the rollup aggregates **one verdict per cell per
check_type**, so a cell can be mechanically green (`replay`) while its
experience checks are still pending or red.

Check types (`arena/model.py CHECK_TYPES` == the schema enum):

| check_type | Proof class | Status |
|---|---|---|
| `replay` | mechanical no-LLM proof (flow/cassette/oracle replay, per-commit) — the plugin/container path below | **implemented** (the default) |
| `docs-fidelity` | a persona attempts the workflow using ONLY the published docs; doc claims scored truthful/stale/missing | declared, not implemented |
| `ux-heuristic` | heuristic-catalog UX critique over captured frames/transcripts (web, TUI `render_tui_png`, VS Code webview, gh-agent comment/deck quality) | **implemented** (WS-G G6, file adapter — see below) |
| `journey-verdict` | the product-journey review gate (evidence completeness, observed-vs-seeded finding floor) | **implemented** (WS-G G6, file adapter — see below) |

`journey-verdict`/`ux-heuristic` are graded by a **file adapter**
(`arena/checks.py`'s `run_ui_verdict_check`), not a container: they read an
already-written `kitsoki-ui-qa` (`journey-verdict`) or `kitsoki-ui-review`
(`ux-heuristic`) `verdict.json` off disk — path from the check's
`options.verdict_path` — and adapt it into a completion-state via
`tools/persona_qa/ui_verdict.py` (`from_ui_qa_verdict`/`from_ui_review_verdict`).
No container spawn, no LLM call of their own: the judging skill already ran;
this only folds its verdict back into the arena rollup. Honest `pending` (never
a fake green) when no `verdict_path` is configured, or the configured path
doesn't exist yet:

```yaml
checks:
  - replay
  - check_type: journey-verdict
    options:
      verdict_path: .artifacts/ui-qa/onboarding-tour/verdict.json
  - check_type: ux-heuristic
    options:
      verdict_path: .artifacts/ui-review/onboarding-tour/verdict.json
```

Contract rules:

- **`check_type` is optional in the schema; absent means `replay`.** Every
  pre-check-suite producer (bench.py, persona_qa/completion.py) and consumer
  stays valid unchanged. `CellResult.to_dict()` likewise omits the default, so
  existing rollup output is byte-identical for a `[replay]`-only suite.
- **A spec declares its suite** with a top-level `checks:` list — bare type
  strings or mappings with check-type-specific options:

  ```yaml
  checks:
    - replay
    - check_type: docs-fidelity
      docs: docs/getting-started.md   # extra keys fold into CheckSpec.options
  ```

  Omitting `checks:` means `[replay]` — exactly the pre-suite behavior.
  Unknown or duplicated check types are rejected at spec load.
- **Declared-but-unimplemented types (`docs-fidelity`) report honest
  `pending`** (`health: incomplete`, a "not implemented yet" note) at
  execution time — never a fake green, never a container run, never an INFRA
  retry (`arena/checks.py`). `journey-verdict`/`ux-heuristic` are implemented
  as a file adapter (above) and can *also* report honest `pending` — with a
  different note ("no verdict.json found"/"no verdict_path configured") — when
  their input artifact genuinely doesn't exist yet.
- Per-cell result files under the rollup's `cells/` keep the historical
  `<cell_id>.json` name for `replay` and add a `--check-<type>` suffix for the
  others.

## Layout

| File | Role |
|---|---|
| `arena/model.py` | `JobSpec`, `Cell`, `CellResult`, `CheckSpec`, enumeration |
| `arena/artifact_adapters.py` | artifact adapter registry: completion-state/swarm/UI/product-journey artifacts → completion-state payloads |
| `arena/completion_state.py` | arena-specific copy from validated completion-state payloads into `CellResult` |
| `arena/checks.py` | check-suite runner (WS-G G1): replay delegates to the container path; journey-verdict/ux-heuristic delegate to the verdict.json file adapter (WS-G G6); unimplemented types → honest `pending` |
| `arena/plugins/base.py` | `JobTypePlugin` protocol + registry |
| `arena/plugins/bugfix.py` | bugfix plugin — wraps `bench.py` oracle (verify / drive), scores from the completion-state file |
| `arena/plugins/paired_task.py` | paired-task plugin — one task through multiple treatments with shared oracle JSON |
| `arena/treatments/` | reusable treatment library: registry/catalog, action-surface drivers, CodeAct capability presets |
| `arena/executor.py` | `CellExecutor` + `ContainerBackend` seam (`DockerBackend` \| `FakeBackend`) |
| `arena/placement.py` | sweep scheduler (concurrency, INFRA-vs-MODEL retry) |
| `arena/rollup.py` | job-agnostic leaderboard → `rollup.json` + `rollup.md` |
| `arena.py` | CLI: `plan` · `validate` · `doctor` · `treatments` · `run` · `plugins` |
| `specs/*.yaml` | example job specs |
| `tests/test_*.py` | no-LLM, no-docker end-to-end (FakeBackend) |

## Corpora: targets and personas from product-journey

`tools/product-journey/` owns two reusable corpora: `github-targets.json` (the
vetted OSS repo list, with a `selection_contract` and optional refreshed
`target-proof.json` metadata) and `personas.json` (the persona lens catalog).
Rather than hand-copying ids into a spec, a `JobSpec` can load them directly —
read-only; product-journey stays the owner and arena never writes to either
file.

```yaml
job_type: persona-qa

# Materializes Target[] from the corpus instead of hand-inlining `targets:`.
# Path is resolved relative to the repo root.
targets_from: tools/product-journey/github-targets.json

# Optional: merge a refreshed target-proof.json's per-target checks into each
# Target's meta (meta.target_proof.status/open_bug_count/…). Accepts the proof
# file or its containing proof dir. Targets absent from the proof are untouched.
target_proof_from: .artifacts/product-journey/target-proofs/<proof-id>

variants:
  - id: kitsoki-gpt-5.5
    backend: codex
    model: gpt-5.5

axes:
  scenario: [fix_bug, file_product_issue]

# Loads axes.persona = [persona ids...] from personas.json — only fills the
# axis in when the spec doesn't already hand-inline `axes.persona` (that
# always wins, so existing specs are unaffected).
persona_axis_from: tools/product-journey/personas.json
```

Hand-inlined `targets:` / `variants:` / `axes:` keep working completely
unchanged — `targets_from` only applies when `targets:` is absent, and
`persona_axis_from` only fills in the `persona` axis when it isn't already
hand-specified. See `tools/arena/tests/test_corpus_loading.py` for the
parsing contract (pure file parsing, no docker/LLM).

## Usage

```bash
# inspect reusable treatment surfaces
python3 tools/arena/arena.py treatments --aliases

# validate a spec without Docker or LLM calls
python3 tools/arena/arena.py validate --spec tools/arena/specs/bugfix-query-string.yaml

# validate the spec and local Docker readiness
python3 tools/arena/arena.py doctor --spec tools/arena/specs/bugfix-query-string.yaml

# enumerate cells (no execution)
python3 tools/arena/arena.py plan --spec tools/arena/specs/bugfix-query-string.yaml

# run the sweep — DEFAULTS to the no-LLM arming path (oracle RED→GREEN verify)
python3 tools/arena/arena.py run --spec tools/arena/specs/bugfix-query-string.yaml \
    --out .artifacts/arena/qs-skeleton

# the paid path (explicit opt-in to spend on real agent drives)
ARENA_PAIRED_TASK_ENABLE_CODEX=1 python3 tools/arena/arena.py run --spec … --out … --live
```

`doctor` is intentionally stricter than `validate`: it checks Docker daemon
reachability and the container API because arena cells run in containers even on
the no-LLM arming path. Docker startup, context, sign-in, or admin-policy
failures roll up as `blocked` / `infra:harness`, not as a model loss.

WB.2 paired-task gate:

```bash
python3 tools/arena/tests/run_no_llm.py
```

## Paired-Task CodeAct Treatments

Arena treatments are documented in `docs/research/arena-treatments.md` and
implemented as a reusable library under `arena/treatments/`. `paired-task`
treatments are explicit driver names. Existing specs keep working: `kitsoki` is
an alias for `kitsoki-mcp`, and `single-briefed` / `single-naive` are aliases
for the raw one-shot prompt driver. CodeAct specs can now compare the same
frozen task through four action surfaces:

| treatment | Driver surface |
|---|---|
| `raw-codex` | Raw `codex exec` with the current baseline permissions. |
| `codex-codeact` | `kitsoki-codeact-driver` launched through `kitsoki agent launch --mode codeact`; Codex shell/apps disabled; only `mcp__kitsoki-codeact__codeact_eval` exposed. |
| `kitsoki-mcp` | `kitsoki-mcp-driver` drives the Studio MCP and the normal bench-bugfix worker path. |
| `kitsoki-mcp-codeact` | Studio MCP drives the workflow, while the bugfix implementing room uses `host.agent.codeact` for the mutating edit step. |

Direct CodeAct variants must declare `agent: kitsoki-codeact-driver` and a
known `capability_preset`. The default `repo_patch` preset grants repository
read/write through `ctx.fs` plus read-only git probes:

```yaml
options:
  live_gate_env: ARENA_PAIRED_TASK_ENABLE_CODEX
  capability_presets:
    repo_patch:
      fs:
        read: ["**"]
        write: ["**"]
        max_bytes: 1048576
      vcs: read
```

The runner always performs a dry-run `kitsoki agent launch` before a live
`codex-codeact` cell. It persists the launch plan, asserts the expected tool
surface, records the capability hash, and marks the cell `blocked`
(`infra:harness`) if the permission proof fails. Live execution still requires
both `arena run --live` and the configured gate environment variable.

Operator runbook for the CodeAct-vs-Codex action-surface matrix:

```bash
make arena-showdown-plan
make arena-showdown-run       # no-LLM arming run, containerized
```

The live spend path is separate and still double-gated:

```bash
ARENA_PAIRED_TASK_ENABLE_CODEX=1 make arena-showdown-live
```

Every run writes its durable evidence into `ARENA_SHOWDOWN_OUT` (default
`.artifacts/arena/codeact-showdown`). Treat `summary.json`, `report.md`, and
the per-cell JSON files as the source of truth. Do not produce a narrative video
until a real no-LLM or live arena run has completed; demos should be generated
from those run artifacts or from a recorded trace, not from a handcrafted
showdown page.

New treatments should be added to `arena/treatments/registry.py`, documented in
the package README and `docs/research/arena-treatments.md`, and covered by a
no-LLM test that imports `arena.treatments` directly.

Every arena run now writes an offline review bundle in the output directory:

```text
run.yaml
summary.json
report.md
deck.slidey.json
rollup.json
rollup.md
cells/*.json
```

`summary.json`, `report.md`, and `deck.slidey.json` are regenerated from cell
results only; they do not call a model.

## BugSwarm source pipeline

BugSwarm is a reusable external source alongside the built-in OSS oracle corpus.
The arena adapter stays offline until verification is explicitly requested:

```bash
# Convert an exported BugSwarm artifact list into arena source YAML.
python3 tools/arena/scripts/bugswarm_to_arena.py \
    --in .artifacts/bugswarm/artifacts.json \
    --out .artifacts/bugswarm/source.yaml

# Optional dry-run plan (no Docker execution).
python3 tools/arena/scripts/bugswarm_verify_source.py \
    --source .artifacts/bugswarm/source.yaml \
    --out .artifacts/bugswarm/verify-plan.json

# Explicit Docker verification: fresh container for failed and passed scripts.
python3 tools/arena/scripts/bugswarm_verify_source.py \
    --source .artifacts/bugswarm/source.yaml \
    --out .artifacts/bugswarm/verify.json \
    --execute

# Apply execute-mode RED/GREEN evidence to the source.
python3 tools/arena/scripts/bugswarm_apply_verification.py \
    --source .artifacts/bugswarm/source.yaml \
    --verification .artifacts/bugswarm/verify.json \
    --out .artifacts/bugswarm/verified-source.yaml

# Generate a schedulable kitsoki-vs-raw-prompt paired-task spec.
# The default backend is synthetic, keeping the generated spec no-spend.
python3 tools/arena/scripts/bugswarm_to_arena_spec.py \
    --source .artifacts/bugswarm/verified-source.yaml \
    --out .artifacts/bugswarm/bugswarm-glm52.yaml

# No-spend arming path through arena.
python3 tools/arena/arena.py run \
    --spec .artifacts/bugswarm/bugswarm-glm52.yaml \
    --out .artifacts/arena/bugswarm-glm52
```

The generated spec includes only tasks with `verified_red: true` and
`verified_green: true` by default. To prepare a live GLM spec, use
`--kitsoki-backend codex --raw-backend claude` so the raw-prompt arm runs through
the Claude-compatible `synthetic-claude` profile instead of `codex exec`.
Live BugSwarm paired-task cells materialize the failing checkout by copying
`/home/travis/build/<owner>/<repo>` from the artifact image, then score the
candidate by mounting the modified tree back into a fresh artifact container and
running `./run_failed.sh`. Override the checkout path with
`meta.bugswarm_source_dir` when an artifact uses a different layout.

## Cost discipline

`run` defaults to the **no-LLM** path: for bugfix that is `bench.py verify`
(prove the oracle is armed: RED@baseline → GREEN@fix) executed inside the
container — exercising enumerate → container → score → rollup with **zero spend**.
`--live` is the only way to spend and is always explicit. The pipeline is fully
unit-tested with `FakeBackend` (no docker, no LLM): `tools/arena/tests/`.

For `paired-task`, the no-LLM path uses fixture oracle JSON for every treatment
arm and still exercises the same enumerate -> drive -> score -> aggregate ->
report path. A live run only happens with `arena run --live`.

## Image selection

Every job-type plugin implements `image(cell) -> str` (see
`arena/plugins/base.py`) — that's the only place a cell's container image is
decided. The bugfix plugin's convention (`arena/plugins/bugfix.py`):

```python
def image(self, cell: Cell) -> str:
    return cell.target.meta.get("image") or f"kitsoki-arena-repo/{cell.target.id}:latest"
```

i.e. a target can pin an explicit image via `target.meta.image` in the spec;
otherwise the plugin falls back to a per-project default tag. Follow this same
pattern for any plugin that needs the browser-capable image: either hard-code
the browser tag in `image()` for job types that always need a browser (e.g. a
future swarm plugin), or read it from `cell.target.meta["image"]` /
`cell.variant.meta["image"]` when a spec should be able to opt in per-target.

### Browser-capable image (`Dockerfile.repo-runtime-browser`)

`tools/arena/Dockerfile.repo-runtime-browser` layers Chromium + Playwright
(pinned to `tools/runstatus/package.json`'s `@playwright/test` version) on top
of the existing repo-runtime image, for cells that need to drive a real
browser (persona-qa web surfaces today; the swarm job type later). The base
repo-runtime image is untouched — plain bugfix cells never pay the extra
Chromium weight; only a plugin that explicitly requests this image does.

Build it (two steps — base first, then the browser layer):

```bash
docker build -f tools/bugfix-bakeoff/external/docker/Dockerfile.repo-runtime \
    -t kitsoki-arena-repo-runtime:latest tools/bugfix-bakeoff/external/docker

docker build -f tools/arena/Dockerfile.repo-runtime-browser \
    --build-arg BASE_IMAGE=kitsoki-arena-repo-runtime:latest \
    -t kitsoki-arena-repo-runtime-browser:latest tools/arena
```

or run the one-shot build+smoke script (docker-gated, **not** part of the
standing no-docker CI check — run it by hand when the Dockerfile or the
pinned Playwright version changes):

```bash
tools/arena/scripts/smoke-browser-image.sh
```

It builds both images and proves `npx playwright --version` and a real
headless Chromium page-render inside a container run of the image. This
change does **not** wire any plugin to the browser image — that's
`swarm-arena-job` (and any future persona-qa-via-arena plugin), which should
set `image()` to return `kitsoki-arena-repo-runtime-browser:latest` (or a
project-tagged variant of it) once they need one.

## Status — P0 (walking skeleton)

Done: model + enumeration, plugin interface + bugfix plugin, container executor
with the DI backend seam, local placement scheduler, rollup, CLI, no-LLM test.

**Proven end-to-end** on `query-string` (2026-06-29): the 6-cell no-LLM arming
sweep ran in real containers (`kitsoki-arena-repo/query-string:latest`, the
repo-runtime image) and scored **6/6 armed · win-rate 1.0 · 0 infra failures** —
each cell git-cloned the OSS repo, `npm install`ed, and proved the oracle
RED@baseline → GREEN@fix inside its own container. Build the image once with
`docker build -f tools/bugfix-bakeoff/external/docker/Dockerfile.repo-runtime \
-t kitsoki-arena-repo/query-string:latest tools/bugfix-bakeoff/external/docker`.

**VM placement proven** (2026-06-30): the same bugfix sweep ran on a remote
DigitalOcean droplet via a docker context over SSH (`vm-1`) — 3/3 armed,
win-rate 1.0, 0 infra failures — and the persona-QA product-journey smokes ran
in containers on that same VM (corpus valid; both smokes `passed`). The only new
code was placement-aware mounts: `placement.host_repo[host]` declares the
checkout path on each host's daemon (a remote `-v` source resolves on the VM, not
locally). Spec: `specs/bugfix-query-string-vm.yaml`.

Next (see design doc): **P1** a *pool* of VM hosts + completion-state polling
(scheduler already round-robins `placement.hosts`); **P3** retire
`escalate.sh`/matrix `emit_run`, single front door.

## Status — P2 (persona-qa plugin landed)

`persona-qa` is now a first-class job type alongside `bugfix` (`arena plugins`
lists both). One cell = one `(target, persona, scenario)` triple:

- **Non-live** drives the existing deterministic
  `tools/product-journey/run.py --gate driver-replay` path — cassette-backed
  evidence, zero LLM spend, real run bundle on disk (`review.json`,
  `scenario-outcomes.json`, `driver-handoff.json`, decks).
- **Live** (gated behind `--live`, cost-bearing, never run in CI) instead emits
  a fresh run bundle with `--emit-run` and dispatches the
  `product-journey-qa-driver` agent headlessly against it
  (`.agents/agents/product-journey-qa-driver.md`) before reviewing it.
- **score()** never regexes stdout for a verdict. It only reads the `run_dir`
  pointer out of the container's structured JSON output, then hands that
  directory to `tools.persona_qa.load_product_journey_run` — the same
  completion-state bridge `bugfix.py` reads its `--completion-state` file
  through — which derives verdict/health/metrics from the run bundle's real
  `review.json` on disk. Stdout/stderr text is only an INFRA fallback for a
  harness crash before any JSON was ever printed.
- **unify-corpora** wiring: `tools/arena/specs/persona-qa-onboarding.yaml`
  loads its target from the inline shape and its persona axis from
  `persona_axis_from: tools/product-journey/personas.json`, enumerating one
  cell per persona and completing a no-LLM sweep with `placement.concurrency: 2`.
- Tests: `tools/arena/tests/test_persona_qa_plugin.py` proves registration,
  argv shape for both drive paths, scoring against a real on-disk review.json
  bundle (partial + solved + missing-run_dir + infra-crash cases), and a
  2-concurrent `FakeBackend` sweep across 3 personas with axis coords carried
  through to `CellResult`/rollup.

Not yet wired: the browser cell image (`arena-browser-image`) and the swarm job
type build on this plugin but are separate changes.

**P2.10 — scenario-qa can now drive arena in parallel.** `stories/scenario-qa`
(the operator-facing Persona QA product surface) previously had no way to run
its transport legs concurrently — it either drove them one at a time
(`pause=each-leg`) or auto-drained them serially within one turn
(`pause=auto`, the default). `check ... parallel=true` now hands the resolved
legs to a small, arena-adjacent concurrent runner
(`tools/arena/scripts/run_scenario_qa_legs_parallel.py`) that reuses this
plugin's own `CompletionState` contract (`tools/persona_qa/completion.py`) to
score each leg and folds the results back through the story's own
`record_leg_result.star`/`build_report.star`, so `report.md`/`deck.slidey.json`
are identical in shape to a serial run. It is a lightweight local
`ThreadPoolExecutor` sweep over `tools/persona_qa/kit.py replay-smoke` today,
not a containerized `arena run` sweep of this plugin — see that script's own
docstring for why (no transport axis on this plugin's cell shape yet, and a
story-level opt-in shouldn't force a Docker dependency). Promoting it to real
containerized cells (one per transport leg, sharing the browser-capable image
so per-transport VISUAL evidence can be captured too) is a natural follow-up;
see `docs/persona-qa.md`'s "Parallel legs" section for the operator-facing
contract.

## Status — swarm job type landed (swarm-arena-job)

`swarm` is a third job type alongside `bugfix`/`persona-qa` (`arena plugins`
lists all three). Unlike persona-qa's "one cell per persona", **one swarm cell
IS the whole tier-1 swarm run** — the shared `kitsoki web` server plus its
N scripted Playwright users, from `tools/swarm/` +
`tools/runstatus/tests/playwright/swarm-replay-users.spec.ts` (`swarm-tier1`).
The swarm itself is arena's unit of placement; the users inside it are not
separate cells, so scaling the swarm out just means placing more swarm cells
(more targets/variants/axis combos, or more hosts), the same way any other
job type scales.

- **image()** always requests the browser-capable image
  (`kitsoki-arena-repo-runtime-browser:latest`, from `arena-browser-image`,
  the first job type to actually use it) unless a spec pins
  `target.meta.image`/`variant.meta.image` to an override.
- **No separate `--live` path.** The tier-1 harness never calls an LLM — every
  user is a scripted replay driven from `tools/product-journey/personas.json`
  lenses over a flow fixture — so `drive_command` runs the identical
  `cd tools/runstatus && npx playwright test tests/playwright/swarm-replay-users.spec.ts`
  command whether or not `--live` is passed. `live` is accepted only for
  interface parity with `bugfix`/`persona-qa`.
- **Axis-driven knobs:** `axis.users` maps onto the harness's own `SWARM_USERS`
  env var and `axis.interactive_concurrency` onto
  `SWARM_INTERACTIVE_CONCURRENCY` (both already read by
  `swarm-replay-users.spec.ts`). `axis.persona_mix` / `axis.fixture` are
  threaded onto the command line as `SWARM_PERSONA_MIX`/`SWARM_FIXTURE` for
  forward-compat, but the standing harness does not read them yet (it always
  rotates every persona in `personas.json` and always drives the hardcoded
  `happy_path.yaml` fixture) — wiring the harness itself to honor those two
  knobs is a follow-up to `swarm-tier1`, out of this change's scope
  (`tools/arena/arena/plugins/swarm.py` + `tools/arena/tests/test_swarm_plugin.py`
  only; it never touches `tools/swarm/**` or the spec file).
- **score()** never regexes stdout for a verdict. The ONE thing read from
  stdout is the harness's own `[swarm] wrote <path> (...)` line
  (`swarm-replay-users.spec.ts`'s final `console.log`, right after
  `tools/swarm/results.ts`'s `writeResults` returns) — a path, not a verdict —
  mirroring `persona_qa.py`'s `_extract_run_dir` convention. That path is
  mapped from its container form (`KITSOKI_MNT/...`) back to the host path
  (`REPO_ROOT/...`, for `local` placement) and the real `SwarmResults` JSON is
  read off disk. The **aggregate rule**: `all_completed` AND `all_isolated`
  AND `all_console_clean` AND `all_audit_clean` AND (the negative control,
  if it ran, actually detected the seeded cross-talk fault) => `solved`;
  zero completions, or a negative control that ran but failed to detect the
  fault (the isolation gate itself is broken), => `failed`; anything else
  partially clean => `partial`. A results file that was never written at all
  (harness crashed before `writeResults` ran) is reported as an explicit
  `infra:*` health (`infra:missing-results-path`, `infra:harness`,
  `infra:results-malformed`), never guessed as a model verdict.
- Tests: `tools/arena/tests/test_swarm_plugin.py` proves registration,
  image selection (default + override), full argv/env composition (users /
  interactive_concurrency / persona_mix / fixture axes all reaching the
  command line, defaults when axes are absent), scoring against real on-disk
  `SwarmResults` JSON (solved / partial / zero-completions-failed /
  negative-control-failed / negative-control-unexercised / infra-missing /
  infra-crash), the container<->host results-path mapping, and a
  2-concurrent `FakeBackend` sweep across a `users` axis with axis coords
  carried through to `CellResult`/rollup — all with zero docker, zero LLM
  spend. A real docker-gated run (the actual swarm inside the browser image)
  is manual acceptance only, out of this test gate's scope.

## Status — usable-kitsoki-gate job type shipped (S6 of usable-kitsoki.md, Tasks 1-5 landed)

`usable-kitsoki-gate` is a fourth job type alongside `bugfix`/`persona-qa`/
`swarm` (`arena plugins` lists all four). See
`docs/tracing/usable-kitsoki-gate.md` for the narrative doc; the
`usable-kitsoki-release-gate.md` proposal that specified it has been
migrated and deleted per the repo's proposal lifecycle. One cell now drives one
scenario x surface combination (Task 3.1 — enumeration is wired to S4's real
scenario-foundry IR corpus, no bespoke logic in the plugin): a spec's
`targets_from` points at a *directory* of scenario IR documents (default
`tools/session-mining/calibration/`, S4's committed 18-scenario calibration
set — `specs/usable-kitsoki-gate-calibration.yaml`), and
`arena.model.load_targets_from_corpus`'s directory branch (generic, not
scenario-specific) turns each `scn-*.json` document into one `Target` whose
`id` is the scenario id and whose `meta` carries `persona`/`goal`/
`expected_effects`/`abandoned`/`provenance` verbatim. Crossed with
`axes.surface`, that's 18 x 3 = 54 cells against the calibration set — `arena
plan --spec tools/arena/specs/usable-kitsoki-gate-calibration.yaml` prints
`cells=54`. `persona` is deliberately NOT a separate axis to cross-multiply
(a mined scenario's persona is a fixed property of that scenario); `_coords()`
falls back to `target.meta["persona"]` when no explicit persona axis/meta
override is given.

- **image()** dispatches on `axis.surface`: `web`/`tui` request the
  browser-capable image (`kitsoki-arena-repo-runtime-browser:latest`); `mcp`
  is a headless stdio surface and gets the plain
  `kitsoki-arena-repo-runtime:latest`. `target.meta.image`/`variant.meta.image`
  override either default, same escape hatch as `swarm.py`/`persona_qa.py`.
- **drive_command()** dispatches on `axis.surface` too: `web` reuses the
  swarm-style convention (`cd tools/runstatus && npx playwright test ...`);
  `tui`/`mcp` shell into their own stub runner script under
  `tools/usable-kitsoki-gate/`. `GATE_SURFACE`/`GATE_SCENARIO_ID`/
  `GATE_PERSONA`/`GATE_SCENARIO_CORPUS`/`GATE_RUN_ID`/`GATE_RESULTS_PATH` env
  vars carry the cell's coords through either path. Two of the three
  no-LLM harness entry points now exist (`tools/usable-kitsoki-gate/
  run_tui_gate.py`, `tools/usable-kitsoki-gate/run_mcp_gate.py`); the real
  browser-driven `tests/playwright/usable-kitsoki-gate-web.spec.ts` remains
  separately gated, larger, browser-specific work. S1 (workbench producer
  contract, `internal/orchestrator/workbench_gate_signal.go`) and S4
  (scenario foundry, `tools/session-mining/scenario_compiler.py` +
  `calibration/`) have both landed. A real `--live` path now exists too:
  `drive_command(cell, live=True)` dispatches into
  `tools/usable-kitsoki-gate/run_live_gate.py --live-gate`, which drives a
  real agent against `stories/dev-story`'s real `workbench:` room —
  double-gated (`arena run --live` plus that script's own `--live-gate`
  argv flag, mirroring `tools/swarm/tiers/liveExplorerCli.ts`), never run
  in any test or CI job (see `.github/workflows/usable-kitsoki-gate.yml`).
- An empty scenario corpus (no targets/variants/axes) enumerates to **zero
  cells**, not an error — `JobSpec.cells()` already returns `[]` for empty
  `targets`/`variants`/any axis with an empty value list; no special-casing
  needed in the plugin (`arena plan --spec <empty-corpus-spec>` prints
  `cells=0` and exits 0).
- **score()** never regexes stdout for a verdict. The ONE thing read from
  stdout is the harness's own `[usable-kitsoki-gate] wrote <path> (...)`
  line, mirroring `swarm.py`'s `[swarm] wrote <path>` convention. The bundle
  at that path accepts either of two shapes (Task 3.2): an already-built
  `{"run_id", "records": [...]}` bundle (unchanged — what the golden
  fixtures and any harness that does its own join would write), or a raw S1
  signal bundle (`{"turn_signals": [...]}` / `{"trace_events": [...]}`) —
  the turn-level `usable_kitsoki_gate` payload(s) off a real session trace,
  with NO pre-built parity record at all. For the raw-signal shape,
  `score()` performs the actual S1 (candidate) x S4 (source) join itself
  (`build_parity_record` / `extract_turn_signals`): `source_completed` is
  read off `cell.target.meta["abandoned"]` — the exact IR document
  `targets_from` loaded THIS cell from, never re-derived, never re-judged by
  an LLM — `candidate_completed`/`silent_bounce`/`misroute_adjacent` are
  reduced across the turn signals, and every honestly-incomplete field (full
  `expected_effects` coverage; `misroute_adjacent`, hard-false from S1
  today) is called out in the record's own `notes` rather than fabricated.
  Either way, every record is validated against
  `usable_kitsoki_gate_schema.json` — a record that fails validation blocks
  the cell (`infra:results-malformed`) rather than silently scoring past it.
  Clean records are reduced into the three `GATE_CONDITIONS`
  (`usable_kitsoki_gate_constants.py`): zero `silent_bounce`, zero
  `misroute_adjacent`, and a **worst-surface** `parity_percent(...) >=
  PARITY_THRESHOLD_PERCENT` — all three passing is `solved`, any one failing
  is `failed`. This reduction is **per cell**, i.e. per `(persona, surface)`
  in production (one cell drives exactly one surface, so grouping by surface
  is normally a no-op), but `score()` groups records by their own `surface`
  field and gates on the MINIMUM per-surface parity rather than the flat
  aggregate regardless — `usable_kitsoki_gate_constants.WORST_SURFACE_GATING`
  is a fixed design decision (never average across surfaces), so a
  hand-written bundle spanning more than one surface (e.g. a golden
  regression fixture) is reduced correctly too. True cross-**cell**
  worst-surface gating (comparing separately-run cells against each other,
  e.g. across a real S1/S4 sweep) is still a higher rollup layer (Task 3,
  gated on S1/S4 landing — explicitly out of scope here).
- Tests: `tools/arena/tests/test_usable_kitsoki_gate_plugin.py` proves
  registration alongside the other three job types, image selection per
  surface (+ target/variant meta overrides), full argv/env composition for
  all three surfaces (web/tui/mcp, including the default-surface and
  no-persona-axis fallbacks and the explicit `scenario_corpus`/`run_id`
  threading), the empty-scenario-corpus zero-cells behavior, and scoring
  against a real on-disk parity-records bundle (solved / silent-bounce-fails
  / misroute-fails / below-threshold-fails / at-threshold-boundary-solves /
  empty-records-solves / schema-violation-blocks / missing-pointer-infra /
  crash-infra), plus the container<->host results-path mapping — all with
  zero docker, zero LLM spend. `tools/arena/tests/
  test_usable_kitsoki_gate_corpus.py` (Task 3.1/3.2) proves cell enumeration
  off the real calibration set (18 x 3 = 54 cells, persona/scenario_id
  fallback threading), a configured non-default corpus directory loading
  identically, `extract_turn_signals` pulling the one workbench turn's
  signal out of a synthetic raw trace, `build_parity_record`'s join
  (source_completed from `abandoned`, candidate_completed/silent_bounce
  reduced across turns, honesty notes, and a hard rejection of empty
  `evidence_refs`), and `score()` performing that join end-to-end from a raw
  `trace_events`/`turn_signals` bundle through to a rolled-up `CellResult` —
  zero docker, zero LLM spend.
- **Golden regression fixtures** (Task 4.1 — offline proof the gate has
  teeth): `tools/arena/tests/fixtures/usable-kitsoki-gate/clean-pass.json`
  is a hand-written 30-record bundle (web/tui/mcp × 10 scenarios) with zero
  violations and 100% parity everywhere. `scripted-silent-bounce.json`,
  `scripted-misroute-adjacent.json`, and `scripted-parity-miss.json` are
  each that exact same bundle with **exactly one** scripted violation added,
  so each isolates one `GATE_CONDITIONS` entry. `scripted-parity-miss.json`
  is specifically shaped so the flat aggregate across all 30 records sits AT
  the 90% threshold (27/30) while mcp alone is at 70% — proving the rollup
  gates on the worst surface, not the average. `tools/arena/tests/
  test_usable_kitsoki_gate_golden_fixtures.py` scores each fixture through
  the plugin's real `score()` entry point and proves `clean-pass.json`
  solves while each of the other three independently flips the rollup from
  `solved` to `failed` — zero docker, zero LLM spend, no S1/S4 dependency.
- **No-LLM gate harness + calibration run** (Task 3.3's no-LLM half + Task
  4.2): `tools/usable-kitsoki-gate/flow_gate_runner.py` drives one
  (scenario, surface) cell through a REAL `kitsoki test flows --trace-out`
  replay of that scenario's S4-compiled flow fixture and joins the resulting
  trace via this plugin's own `extract_turn_signals`/`build_parity_record`;
  `run_tui_gate.py`/`run_mcp_gate.py` are the two (of three) harness entry
  points this plugin's `drive_command()` already dispatches to, now landed
  against the real `GATE_*` env contract (the web surface's real
  Playwright-driven spec remains separately gated). `run_calibration_gate.py`
  sweeps every scenario x surface at bounded concurrency (mirroring
  `tools/swarm/tiers/tier2.ts`'s bounded-pool shape, no docker needed for
  this substrate) and rolls the swept records up through the same
  `_rollup_from_records` reduction a single cell's bundle goes through.
  `tools/arena/tests/test_usable_kitsoki_gate_calibration.py` regenerates
  the 18-scenario calibration set's 54-cell sweep from scratch and diffs it
  byte-for-byte against the checked-in
  `tests/fixtures/usable-kitsoki-gate/calibration-report.json` — the one
  test in this suite that is not instant (~10-20s: 54 real `go run`
  invocations), still zero docker/LLM spend. `run_calibration_gate.py` now
  sweeps all THREE real `workbench:` targets (`dev-story`/`pets-dev`/
  `slidey-dev`, S6 "no-llm-parity") rather than the original non-workbench
  harness stub; the checked-in run measures `worst_surface_parity_percent =
  100.0%` against the 90% placeholder — see `usable_kitsoki_gate_constants
  .py`'s calibration-contact note for the full caveat on what that 100.0%
  does and does not prove (round 1's original `0.0%` measurement, against
  the non-workbench stub, is preserved there as history, not silently
  dropped).
- **Live gate harness** (Task 3.3's live half):
  `tools/usable-kitsoki-gate/run_live_gate.py` is the gated, cost-bearing
  counterpart to `flow_gate_runner.py` — a real spawned orchestrator agent
  drives the kitsoki studio MCP's `session.new`/`session.drive` against any
  of the three real `workbench:` rooms (`dev-story`/`pets-dev`/`slidey-dev`,
  selected via the same `GATE_TARGET` env var the no-LLM path uses) turn by
  turn, via `tools/mcp-drive/drive.sh` (this repo's headless kitsoki-MCP
  delegation primitive), and the resulting on-disk session trace (at an
  explicit path this script chose, not a directory-mtime guess) is joined
  via the SAME `extract_turn_signals`/`build_parity_record` functions the
  no-LLM path uses (never a second join implementation).
  `run_live_calibration.py` sweeps a small, explicitly-bounded set of
  (scenario, target) cells through that same gated entry point and rolls
  them up the same way `run_calibration_gate.py` does for the no-LLM sweep.
  Structurally gated exactly like `tools/swarm/tiers/liveExplorerCli.ts`'s
  tier 3: a literal `--live-gate` argv flag with no env fallback, checked
  BEFORE any env is read or any agent spawned. `tools/arena/tests/
  test_usable_kitsoki_gate_live_gate.py` and
  `test_usable_kitsoki_gate_live_calibration.py` prove the refusal —
  including monkeypatching `subprocess.run` to raise if ever called —
  without spawning a real agent, mirroring `swarm-cassette-users.spec.ts`'s
  "stubbed live-explorer dispatch contract" test shape. A real live run
  over dev-story + its two thin inheritors (epic-finalization,
  docs/proposals/usable-kitsoki.md) is recorded in
  `tools/arena/tests/fixtures/usable-kitsoki-gate/live-run-summary.md`.
- **CI workflow** (Task 5.1 + 5.2): `.github/workflows/
  usable-kitsoki-gate.yml` has two jobs. `no-llm-gate` runs on every PR
  whose diff touches the S1/S2/S4/S5 code paths (path-filtered), executing
  `make usable-kitsoki-gate-check` (the schema/plugin/corpus/golden/
  live-gate-refusal/calibration suite above) — cassette/flow-replay only,
  zero LLM spend. `release-candidate-live-gate` only fires on an `rc-*` tag
  push or an explicit `workflow_dispatch` with `confirm_live: yes` typed in
  (never `pull_request`, never a plain `push: main`) — its trigger routing
  is real and `actionlint`-clean, but the job itself is an honest,
  loudly-failing placeholder until a real provider-credential secret and
  the arena browser-capable container images are wired in (deliberate
  operator follow-up, not fabricated here).
- **Docs** (Task 5.3): `docs/tracing/usable-kitsoki-gate.md` is the
  narrative home for the parity verdict schema, gate conditions, producer
  contract, and no-LLM/live determinism split; `docs/proposals/
  usable-kitsoki-release-gate.md`, the proposal that specified all of the
  above, has been trimmed to its historical Why/design rationale and
  deleted per this repo's proposal lifecycle (its remaining content moved
  here and into the narrative doc). One remainder is deliberately still
  gated and out of this slice's scope: Task 3.3's real browser-driven
  web-surface harness (`tests/playwright/usable-kitsoki-gate-web.spec.ts`)
  does not exist yet. Also out of scope here, and NOT claimed as done: a
  LIVE, green run of this gate over `stories/dev-story` plus at least one
  other real workbench-bearing story — that is the `usable-kitsoki.md`
  epic's own definition of done, executed once at epic finalization, not a
  claim this slice or this README makes.
