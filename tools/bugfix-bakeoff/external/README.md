# Manifest-driven bug-fix harness — "should I use kitsoki for MY project?"

[`tools/bugfix-bakeoff`](../README.md) is the package: live cell driving,
deterministic grading, aggregation, deck/report generation, durable results, and
compatibility entry points. This `external/` subtree is the package's
manifest-driven harness implementation. It handles kitsoki's own bugs and
third-party repos through the same project manifest contract: point it at a repo,
onboard it, let a model fix real filed-issue bugs through the kitsoki pipeline,
and grade each fix **deterministically** against the regression test the real PR
shipped.

Use it to evaluate kitsoki's prompts/patterns on real-world code, and to answer
the prospective-user question: *if I onboard my repo and let kitsoki fix a bug,
do I get a good fix — and at what cost compared to the real one?*

## Layout (add a repo = drop a manifest)

```
external/
  bench.py                       # generic grader + fixture verifier (manifest-driven)
  bench_test.go                  # gated reproducible check (make qs-bakeoff): onboard + arm oracles
  candidates.yaml                # the model/effort axis + named escalation ladders
  drive_cell.sh                  # run ONE cell live (COST)
  prepare_handoffs.sh            # prepare/audit selected no-drive cell handoffs (FREE)
  escalate.sh                    # run a project's bugs up a cheap→expensive ladder (COST)
  projects/<name>/
    manifest.yaml                # repo + bugs + oracle-injection contract
    oracles/<bug>.<ext>          # the regression test the real PR shipped, isolated
```

To benchmark a **new** repo, add `projects/<name>/manifest.yaml` (repo URL,
install/test commands, the per-bug `baseline_sha`/`fix_sha`/`oracle_test`/
`oracle_match`, and a one-line `oracle.run` for the test runner) plus the
isolated oracle files. No code changes — `bench.py` and `bench_test.go` discover
it. The shipped reference projects are
[`projects/query-string`](projects/query-string) (sindresorhus/query-string —
small/simple, one ~558-LOC parser, yet mature: 274 commits, 90 releases) and
[`projects/gears-rust`](projects/gears-rust) (a large, mature, **private** Rust
monorepo — captured from the 2026-06 gears-rust dogfood marathon + hard-case run),
and [`projects/kitsoki`](projects/kitsoki) (**kitsoki's own** go+ts dogfood bugs —
`local_only`, folded in from the retired parent harness; the 3 armed fixtures are
bug9/bug12/bug14, all proven RED@baseline→GREEN@fix via a throwaway local mirror).

The promoted bug rows are exposed as **repo-history capsules** in the shared
Kitsoki capsule catalog. They currently use this harness as their materializer
because core `kitsoki capsule open` is still limited to local synthetic
fixtures:

```sh
go run ./cmd/kitsoki capsule list --kind repo-history
python3 tools/bugfix-bakeoff/external/bench.py capsules --markdown
make repo-history-capsules
```

The current promoted set is 10 capsules: 3 `query-string`, 4 `gears-rust`, and
3 `kitsoki`. Local-only manifests can declare `BUGFIX_BAKEOFF_REPO` and
`BUGFIX_BAKEOFF_META_REPO` plus `repo_subdir`, so the same manifest can run
against a standalone checkout or an initialized meta checkout whose submodule
history contains the benchmark baseline/fix commits.

### Polyglot repos (a JS package not at the repo root)

`bench.py` runs one project-level `install` and links a root `node_modules`. For a
repo whose test target lives in a sub-package (kitsoki's `tools/runstatus` vitest
bug), set a per-bug `oracle.setup` — a command run in the scratch tree BEFORE the
oracle (e.g. `cd tools/runstatus && pnpm install --prefer-offline --silent`). A
warm global pnpm/cargo store keeps it fast; keeps the harness runner-agnostic.

### Escalate cheap→expensive — `escalate.sh`

The onboarding question ("cheapest model/effort that fixes my bugs?") answered as
one command. `escalate.sh --project <name> --ladder default` runs each bug up an
ordered candidate ladder, stopping at the first rung that reaches `solved`
(`--dry-run` prints the plan free). Effort is a **profile** property — a rung is a
candidate row pointing at a profile with that (model, effort); see the
`candidates.yaml` header + the `ladders:` section. The loop is **resumable**: an
already-`solved` cell short-circuits the (cost-bearing) drive, so re-running a
partial ladder never re-spends a solved rung.

`solved` = the hidden oracle is GREEN (and, where a secondary `test_cmd` suite
runs, that too). For a `suite: false` project (kitsoki/gears-rust) the oracle is
the only signal, so a correct fix reaches `solved` on the oracle alone — the rule
lives in `bench.decide_quality`, guarded by the free `bench_grade_test.py` (run by
`make qs-bakeoff`). Each `bench.py score` cell carries `metrics` (worker
cost/tokens from the trace) + `model`/`effort`/`provider`, so it feeds
`aggregate.py` + the deterministic deck directly.

### Heterogeneous / heavy / private repos (gears-rust)

`gears-rust` exercises the parts of the contract a uniform JS repo doesn't:
- **per-bug `oracle:`** — each fixture pins its own crate + cargo invocation
  (and `--features`), overriding the project default; `bench.py` merges per-bug
  over project.
- **`inject: write`** — the oracle is a STANDALONE `tests/oracle_<bug>.rs` calling
  the crate's public API, written (not appended) into the candidate tree.
- **`suite: false`** — skip the (multi-minute) whole-workspace `cargo test`
  secondary signal; the hidden oracle is the only signal.
- **`local_only: true`** — heavy + private, so it is NOT cloned by the
  `qsbakeoff` loop (`TestExternalBakeoff` skips it). Arm it against a LOCAL
  checkout via the gated `gearsbakeoff` test, which clones a throwaway
  `--local --no-checkout` mirror (so the grader's fix-source checkout never
  dirties your working tree) and shares a `CARGO_TARGET_DIR`:

  ```sh
  BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-bakeoff
  ```

  The cost-bearing one-cell path uses the same local checkout explicitly:

  ```sh
  tools/bugfix-bakeoff/external/drive_cell.sh \
    --project gears-rust --bug bug1 --candidate gpt-5.3-spark \
    --repo-dir ~/code/gears-rust --score
  ```

The four armable fixtures (bug1/4/5/9) prove RED@baseline → GREEN@fix; the rest
of the marathon + hard-case corpus is captured under `reference_only:` in the
manifest (copy-in / overlay oracles, to be auto-armed once `bench.py` grows an
`inject: insert-at-marker` mode). Provenance, the full corpus table, and the
H1/H4 findings are in [`projects/gears-rust/README.md`](projects/gears-rust/README.md).

### Pilot automation: repo provisioning + dockerized repo-runtime

To keep VM runs reproducible and reduce host drift, provision target repos and
their checker image before spending. By default, scored cells now run inside the
repo-runtime image for repo isolation (`--no-docker-score` disables this):

For a fresh or stale bake-off VM, start by provisioning the harness checkout.
The script is idempotent: it creates or refreshes `/opt/bakeoff/repos/kitsoki`
from `https://github.com/bsacrobatix/kitsoki.git` on `main`, installs the
`kitsoki` binary, and verifies the codex/claude worker prerequisites.

```sh
ssh root@<vm> 'bash -s' < tools/bugfix-bakeoff/external/provision_vm.sh
```

Use `KITSOKI_DIR`, `KITSOKI_REMOTE_URL`, and `KITSOKI_BRANCH` only when testing a
nonstandard checkout. The default VM path should just pull from
`bsacrobatix/kitsoki/main`.

```sh
cd tools/bugfix-bakeoff/external

# Build or refresh local checkouts for all manifests, and build the shared runtime
# image used for deterministic preflight/verify/score workflows.
./provision_repos.sh

# Provision and build for just the selected projects.
./provision_repos.sh --project query-string --project gears-rust

# For private/local-only projects, point to a local checkout:
BUGFIX_BAKEOFF_REPO=/path/to/checkout ./provision_repos.sh --project gears-rust

# Run a no-cost check in the repo runtime image.
./run_repo_docker.sh \
  --project query-string \
  --repo-dir ../../.artifacts/external-bakeoff/repos/query-string \
  -- \
  /workspace/kitsoki/tools/bugfix-bakeoff/external/bench.py preflight \
    --project query-string --bug qs1 --candidate gpt-5.5
```
`run_repo_docker.sh` always mounts the checkout at `/workspace/repo`; for
local-only projects, pass `--repo-dir /workspace/repo` inside the containered
`bench.py` command. `drive_cell.sh` already handles this via its host-side
`--repo-dir` wiring. When arena calls `drive_cell.sh`, it passes
`--completion-state <path>` through to the final `bench.py score` call so live
and no-LLM verification cells use the same result contract.

The image is built from:
`tools/bugfix-bakeoff/external/docker/Dockerfile.repo-runtime` and includes
`git`, `make`, `go`, `node`, `pnpm`, and `rust` toolchains plus shared cache
mount points under `/workspace/.cache`.  

`provision_repos.sh` inherits the same retry policy used by `drive.sh`:
`MCP_DRIVE_MAX_ATTEMPTS` (default 12), exponential backoff from
`MCP_DRIVE_BACKOFF_BASE` (default 10), and cap at
`MCP_DRIVE_BACKOFF_MAX` (default 600s). This keeps quota/rate/network
friction from failing the loop on first spike, while non-obvious exceptions retry
by default.

## The deterministic good/bad detector — `bench.py`

```sh
# grade a candidate fix (a worktree carrying the model's source edit):
python3 bench.py score --project query-string --bug qs1 --tree <worktree> \
    --out results/cells/qs1-<cand>-<treat>.json
#   exit 0 ⇔ oracle GREEN (good fix) · exit 1 ⇔ RED (bug remains)

# verify every fixture is armed (RED@baseline, GREEN@real-fix):
python3 bench.py verify --project query-string

# preflight repo/candidate readiness before spending:
python3 bench.py preflight --project query-string --candidate gpt-5.5
```

`score` copies the candidate tree (never mutates it), links a prebuilt
`node_modules` (`QS_NODE_MODULES=…` to skip re-install), appends the **hidden
oracle** into the manifest's `oracle.target`, runs `oracle.run` → GREEN/RED, and
also runs the full `test_cmd` suite as a secondary signal. Cells follow the
shared [`results/SCHEMA.md`](../results/SCHEMA.md).

> **Oracle is primary, suite is secondary — by design.** For some bugs a
> *correct* behavioral fix legitimately flips one PRE-EXISTING test's expectation
> (the real PR edited it too), so a source-only fix scores `partial` (oracle
> GREEN, suite RED) until the candidate also updates that test. That gap is the
> quality signal the benchmark surfaces: kitsoki's pipeline runs the full suite
> and updates the affected test (→ `solved`); a careless single-prompt may not
> (→ `partial`). Verified on query-string: qs1/qs2 source-only fix → `partial`,
> qs3 → `solved`.

## Run the gated scaffold check (deterministic, free)

```sh
make qs-bakeoff   # per project: clone + onboard via embedded dev-story + arm every oracle
```

Excluded from `make test` (the `qsbakeoff` build tag). Needs network, git,
node/npm, python3+pyyaml, and an installed `kitsoki`. It proves onboarding works
and every fixture is armed **before** any LLM is spent.

For a repo owner preparing a specific live matrix, use the generic product-path
smoke. It is also deterministic and free: it runs the harness unit checks,
preflights the selected project/bug/candidate matrix, verifies the selected
oracles RED@baseline/GREEN@fix, renders exact `drive_cell.sh --score` commands,
prepares selected cells with `drive_cell.sh --no-drive`, and validates the
`repo-bakeoff` story flows.

```sh
make history-smoke \
  HISTORY_PROJECT=query-string \
  HISTORY_BUGS=qs1 \
  HISTORY_CANDIDATES=gpt-5.5
```

For a private or local-only repo, include the checkout path:

```sh
make history-smoke \
  HISTORY_PROJECT=gears-rust \
  HISTORY_REPO_DIR=~/code/gears-rust \
  HISTORY_BUGS=bug1 \
  HISTORY_CANDIDATES=opus-4.8
```

`make gears-history-smoke` is the preconfigured gears-rust shortcut. If the
generic smoke fails, fix that blocker before running cost-bearing cells.
It also writes a review artifact under
`.artifacts/external-bakeoff/readiness/<project>.md` and the same readiness
index as JSON under `.artifacts/external-bakeoff/readiness/<project>.json`.
It also writes the explicit completion verdict under
`.artifacts/external-bakeoff/readiness/<project>-completion.md` and
`.artifacts/external-bakeoff/readiness/<project>-completion.json`. The
completion report distinguishes four states: no-cost setup ready, ready to
drive live, result evidence complete with pending cells, and fully live-scored
capability evidence.
The free preparation step writes baseline worktrees under
`.artifacts/external-bakeoff/cells/` and the delegated Studio MCP prompt under
`.artifacts/external-bakeoff/drive-prompts/`. It also writes
`.artifacts/external-bakeoff/prepared/<project>-<bug>-<candidate>.json`, a
machine-readable handoff with the worktree, branch, trace, prompt, preflight,
and future score-result paths. By default, `history-smoke` prepares the first
selected cell; set `HISTORY_PREPARE_ALL_CELLS=1` to prepare the full selected
matrix, or `HISTORY_PREPARE_FIRST_CELL=0` to skip preparation.
Prepared handoffs are audited in the same smoke. The audit writes
`.artifacts/external-bakeoff/readiness/<project>-handoffs.md` and JSON next to
the readiness report, then fails if metadata points at missing files, the MCP
prompt is missing required worktree/profile/bug context, or the prompt leaks
hidden oracle paths/content or real-fix commit/source hints.
The story path uses the same no-cost wrapper directly:

```sh
tools/bugfix-bakeoff/external/prepare_handoffs.sh \
  --project gears-rust \
  --bug bug1,bug4 \
  --candidate opus-4.8 \
  --repo-dir ~/code/gears-rust \
  --markdown .artifacts/external-bakeoff/readiness/gears-rust-handoffs.md
```

The readiness report separates missing scored results from handoff prep:
`Missing cells` still need `drive_cell.sh --score` or an honest `pending`
record. `Stale result cells` are selected result artifacts whose recorded
baseline does not match the current manifest, so they are not counted as scored.
`Unprepared cells` need `drive_cell.sh --no-drive` if you want their
prompt/worktree/trace metadata reviewed before spend. `Stale prepared cells`
have metadata already, but it points at missing prompt/worktree/preflight paths;
rerun the listed `--no-drive` command before trusting that handoff.

For the full gears-rust reference corpus, run:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-history-full-smoke
```

That uses the same generic smoke over the four armable fixtures
`bug1,bug4,bug5,bug9`. Because it runs with
`HISTORY_PREPARE_ALL_CELLS=1`, the smoke also asserts from the readiness JSON
that every selected cell has fresh prepared metadata and that stale/unprepared
handoffs are zero. It then runs `history-pending-smoke`, which validates the
deterministic Markdown + Slidey JSON report path from a pre-attempt pending
result without touching the normal live results directory. That pending smoke
also runs `bench.py completion` against the temp result and asserts that pending
completes result evidence without becoming a live scored capability result.

To regenerate that readiness report without rerunning RED/GREEN arming, call the
harness directly. This is useful after adding scored or pending cell JSON:

```sh
python3 tools/bugfix-bakeoff/external/bench.py readiness \
  --project gears-rust \
  --repo-dir ~/code/gears-rust \
  --bug bug1 \
  --candidate opus-4.8 \
  --armed \
  --markdown .artifacts/external-bakeoff/readiness/gears-rust.md
```

To regenerate the completion verdict from the same current artifacts:

```sh
python3 tools/bugfix-bakeoff/external/bench.py completion \
  --project gears-rust \
  --repo-dir ~/code/gears-rust \
  --bug bug1 \
  --candidate opus-4.8 \
  --armed \
  --markdown .artifacts/external-bakeoff/readiness/gears-rust-completion.md
```

For CI or a publish gate, make the verdict enforceable:

```sh
# Accept scored cells and honest pre-attempt pending cells.
python3 tools/bugfix-bakeoff/external/bench.py completion ... --require-result-evidence

# Require every selected cell to be a non-pending scored result.
python3 tools/bugfix-bakeoff/external/bench.py completion ... --require-live-scored
```

Use `--require-result-evidence` before publishing an accounting/completion
report. Use `--require-live-scored` before claiming model capability.

The readiness report is an audit index, not a substitute for arming or scoring:
it shows preflight errors/warnings, exact drive commands, existing scored cells,
missing cells, pending-cell command templates for blocked providers, and the
next action.
Pass `--armed` only when the selected fixtures were just verified, for example
by `make history-smoke` or `bench.py verify`.
To regenerate only the prepared-handoff audit after a `--no-drive` prep:

```sh
python3 tools/bugfix-bakeoff/external/bench.py audit-handoffs \
  --project gears-rust \
  --bug bug1 \
  --candidate opus-4.8 \
  --markdown .artifacts/external-bakeoff/readiness/gears-rust-handoffs.md
```

To prove the blocked-provider path without modifying the normal live results
directory, run the pending smoke. It writes a pending cell to a temporary results
directory, summarizes it, and renders Markdown + Slidey JSON from that pending
result:

```sh
make history-pending-smoke \
  HISTORY_PROJECT=gears-rust \
  HISTORY_BUGS=bug1 \
  HISTORY_CANDIDATES=opus-4.8 \
  HISTORY_PENDING_REASON="profile not configured on this machine"
```

Use this only to validate reporting behavior or to rehearse the blocked-provider
workflow. A real candidate worktree must still be scored with `drive_cell.sh
--score`.

## Run cost-bearing LLM cells (operator-only)

A whole cell — prepare the baseline worktree, drive the kitsoki bugfix pipeline
live under a candidate model, grade it, extract cost — is **one command**:

```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
    --project query-string --bug qs1 --candidate gpt-5.5 --score
#   --no-drive  prepares the worktree + writes prompt/metadata only (free)
```

For a matrix, print the exact commands first:

```sh
python3 tools/bugfix-bakeoff/external/bench.py drive-plan \
  --project gears-rust \
  --bug bug1,bug4 \
  --candidate opus-4.8,gpt-5.3-spark \
  --repo-dir ~/code/gears-rust
```

`repo-bakeoff` renders the same plan in its `running` room after preflight and
oracle arming, so the operator can run copy-ready cell commands instead of
translating placeholders by hand.

`drive_cell.sh` reads the manifest (`bench.py meta`) + [`candidates.yaml`](candidates.yaml)
(the model/profile axis), runs the same `bench.py preflight` readiness gate the
`repo-bakeoff` story uses, clones the repo once (reusing `node_modules`), bakes
in every load-bearing `initial_world` knob (the recipe below), and delegates the
live drive to [`tools/mcp-drive/drive.sh`](../../mcp-drive/README.md) (raw
`claude -p` with the studio MCP attached). The **worker** model is chosen by
`session.new {profile, harness:"live"}` — `codex-native` → GPT-5.5,
`synthetic-claude` → GLM-5.2; the orchestrator defaults to GPT-5.5 via
`MCP_DRIVE_MODEL=gpt-5.5` and only advances the pipeline.
The generic instance it drives is
[`stories/bench-bugfix`](../../../stories/bench-bugfix).

`--score` grades the worktree (`bench.py score`) and extracts the worker cost
(`bench.py cost --trace …` → `cost_usd` for metered providers, token usage for
subscription auth). Pipeline thread files are written under
`.artifacts/external-bakeoff/threads/`, alongside preflight JSON under
`.artifacts/external-bakeoff/preflight/` and the other per-cell cache/log output.
Results land in `.artifacts/external-bakeoff/results/cells/`. The
`repo-bakeoff` story's default `results_dir` points at that artifact directory,
so its deterministic scoring/reporting rooms summarize live-driver output
without copying generated files into the repo. Live runs do not create bare
`bug*` files in the project root. Per-cell branch names include a stable hash of
the artifact cell path, so repeated dry runs from different cache roots do not
collide on the target repo's global worktree branch namespace. The
load-bearing knobs `drive_cell.sh` sets (each learned from a failure) are tabulated in the
[`external-repo-bakeoff` skill](../../../.agents/skills/external-repo-bakeoff/SKILL.md);
the key one is `workspace_id:""` so the implementer edits the prepared worktree
directly instead of creating one against the wrong repo root.

If a provider/profile is blocked before a capability result exists (for example
rate limits, missing API access, or a deliberately unavailable profile), record a
pending cell instead of leaving a silent hole or marking the model failed:

```sh
python3 tools/bugfix-bakeoff/external/bench.py pending \
  --project gears-rust \
  --bug bug1 \
  --candidate gpt-5.3-spark \
  --reason "codex-spark profile not configured on this machine" \
  --out .artifacts/external-bakeoff/results/cells/gears-rust-bug1-gpt-5.3-spark-kitsoki.json
```

Pending cells are included in reports as `pending`, counted separately from
`failed`, and excluded from solve-rate denominator. Use them only when the oracle
never ran; once a model produced a candidate worktree, grade it with `score`.

For private or heavy `local_only` projects, pass `--repo-dir <checkout>` or set
`BUGFIX_BAKEOFF_REPO`. For a meta checkout, set `BUGFIX_BAKEOFF_META_REPO` and
declare `project.repo_subdir`. The harness creates disposable per-cell worktrees
under `.artifacts/external-bakeoff/cells/` and leaves the source checkout
untouched.

Before any arm/drive step, run the free preflight. It reports all setup blockers
as JSON: manifest/oracle files, local checkout presence, baseline/fix commits,
candidate key, and whether the candidate's Kitsoki profile is configured.

```sh
python3 tools/bugfix-bakeoff/external/bench.py preflight \
  --project gears-rust \
  --bug bug1,bug4,bug5,bug9 \
  --repo-dir ~/code/gears-rust \
  --candidate opus-4.8
```

Repeat `--candidate` or pass a comma-separated list to check the full matrix:

```sh
python3 tools/bugfix-bakeoff/external/bench.py preflight \
  --project gears-rust \
  --bug bug1,bug4,bug5,bug9 \
  --repo-dir ~/code/gears-rust \
  --candidate opus-4.8,gpt-5.3-spark
```

`ok: true` means the repo is ready for deterministic arming and a cost-bearing
cell. Pass the same `--bug` list as the matrix you plan to run; `repo-bakeoff`
does this automatically from `world.bugs`, so a one-bug smoke does not verify the
whole manifest. Missing profiles or commits fail here, before `drive_cell.sh`
prepares a worktree or invokes Studio MCP.

See [`docs/case-studies/query-string-bakeoff.md`](../../../docs/case-studies/query-string-bakeoff.md)
for the worked GPT-5.5-vs-GLM-5.2 study.

## Regenerate the report deck

After scoring cells, regenerate the deterministic Slidey spec from the external
summary. This is free and does not call an LLM:

```sh
python3 bench.py summarize --project query-string \
  --deck ../../../.artifacts/query-string-bakeoff/2026-06-26t00-00-00z/deck.slidey.json \
  --markdown ../../../.artifacts/query-string-bakeoff/2026-06-26t00-00-00z/report.md
```

The external summarizer fails by default when `results/cells/` has no scored cell
JSON. That keeps `repo-bakeoff` from producing a misleading "0/0 solved" report;
the story routes back to `running` until at least one `drive_cell.sh --score`
artifact exists. Use `--allow-empty` only for explicit empty-report rendering
tests. The summarizer writes a compact deterministic Slidey spec and Markdown
report directly from `results/summary.json`. Generated deck specs, HTML/MP4
renders, and review artifacts should stay under `.artifacts/<job>/<run>/` so
reruns do not clobber older reports.
