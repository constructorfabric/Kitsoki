# External-project bug-fix benchmark — "should I use kitsoki for MY project?"

The parent [`tools/bugfix-bakeoff`](../README.md) compares kitsoki's `bugfix`
pipeline against a naive single-prompt agent on **kitsoki's own** bugs. This
`external/` subtree generalises that into a **repo-agnostic benchmarking tool**:
point it at any open-source repo, onboard it, let a model fix real filed-issue
bugs through the kitsoki pipeline, and grade each fix **deterministically**
against the regression test the real PR shipped.

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
  GEARS_RUST_REPO=~/code/gears-rust make gears-bakeoff
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

## Run cost-bearing LLM cells (operator-only)

A whole cell — prepare the baseline worktree, drive the kitsoki bugfix pipeline
live under a candidate model, grade it, extract cost — is **one command**:

```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
    --project query-string --bug qs1 --candidate gpt-5.5 --score
#   --no-drive  prepares the worktree + prints the prompt only (free, for review)
```

`drive_cell.sh` reads the manifest (`bench.py meta`) + [`candidates.yaml`](candidates.yaml)
(the model/profile axis), runs the same `bench.py preflight` readiness gate the
`repo-bakeoff` story uses, clones the repo once (reusing `node_modules`), bakes
in every load-bearing `initial_world` knob (the recipe below), and delegates the
live drive to [`tools/mcp-drive/drive.sh`](../../mcp-drive/README.md) (raw
`claude -p` with the studio MCP attached). The **worker** model is chosen by
`session.new {profile, harness:"live"}` — `codex-native` → GPT-5.5,
`synthetic-claude` → GLM-5.2; the orchestrator (cheap sonnet) only advances the
pipeline. The generic instance it drives is
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

For private or heavy `local_only` projects, pass `--repo-dir <checkout>` or set
`<PROJECT>_REPO` (for example `GEARS_RUST_REPO`). The harness creates disposable
per-cell worktrees under `.artifacts/external-bakeoff/cells/` and leaves the
source checkout untouched.

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
