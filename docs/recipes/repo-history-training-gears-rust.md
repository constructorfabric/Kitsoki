# Repo History Training With gears-rust

Use this recipe when the goal is not just "can an agent fix this one bug?", but
"can a repo owner use Kitsoki to turn their own history into a stable dev story
that fixes future bugs reliably?"

`gears-rust` is the reference private/heavy repo for that path. It exercises the
parts a toy project does not: a large Rust workspace, per-bug cargo invocations,
private local checkout access, hidden regression oracles, and no whole-workspace
test suite shortcut.

## What Product-Ready Means

A repo-history training run is product-ready only when each layer has evidence:

1. **Corpus:** historical fixes are captured as manifest rows with baseline,
   real fix, ticket text, and hidden oracle.
2. **Arming:** every promoted fixture proves RED at the historical baseline and
   GREEN after the real fix before any model call.
3. **Driving:** cost-bearing cells run through Kitsoki's bugfix story via Studio
   MCP; the worker model edits a prepared external worktree.
4. **Scoring:** the hidden oracle, not model self-report, decides solved /
   partial / failed.
5. **Learning:** failures become story, prompt, harness, or documentation fixes;
   accepted examples remain deterministic fixtures.

The current gears corpus lives at
`tools/bugfix-bakeoff/external/projects/gears-rust/`.

## One-Time Setup

Start from a local checkout of the target repo:

```sh
GEARS_RUST_REPO=/Users/brad/code/gears-rust
```

For another private repo, use a dedicated manifest under
`tools/bugfix-bakeoff/external/projects/<name>/` and pass
`--repo-dir /path/to/repo` to the commands below.

The target checkout may have unrelated dirty files, but the drive should never
edit that checkout directly. The harness creates per-cell worktrees under
`.artifacts/external-bakeoff/cells/`.

## Free Preflight

Run preflight before arming or driving. It is deterministic and no-cost:

```sh
python3 tools/bugfix-bakeoff/external/bench.py preflight \
  --project gears-rust \
  --bug bug1,bug4,bug5,bug9 \
  --repo-dir /Users/brad/code/gears-rust \
  --candidate opus-4.8
```

The JSON output should say `ok: true`. If it does not, fix every `errors` entry
first: missing local checkout, unknown candidate, unconfigured profile, absent
historical commits, or missing oracle files. `warnings` are audit friction, not
hard blockers; a tracked-dirty target checkout is warned because the harness
uses disposable mirrors/worktrees, but clean source state is easier to review.
Use the same bug list you plan to run in the matrix. A one-bug smoke should pass
`--bug bug1`; the drivable `repo-bakeoff` story does this automatically from
`world.bugs`.

For the full product-path smoke, run the bundled target. It delegates to the
generic `history-smoke` gate and covers the harness unit checks,
candidate/profile preflight, scoped RED/GREEN arming, drive-command rendering,
first-cell worktree/prompt preparation via `drive_cell.sh --no-drive`, and the
`repo-bakeoff` story flows without calling a live model:

```sh
GEARS_RUST_REPO=/Users/brad/code/gears-rust make gears-history-smoke
```

By default this smokes `bug1` with `opus-4.8`. Override the matrix before a live
run so the free proof matches the cell you intend to drive:

```sh
GEARS_RUST_REPO=/Users/brad/code/gears-rust \
GEARS_HISTORY_BUGS=bug1,bug4 \
GEARS_HISTORY_CANDIDATES=opus-4.8,gpt-5.3-spark \
make gears-history-smoke
```

Before calling the reference path product-ready for the current gears-rust
corpus, run the full armable-fixture smoke:

```sh
GEARS_RUST_REPO=/Users/brad/code/gears-rust make gears-history-full-smoke
```

That verifies `bug1,bug4,bug5,bug9` RED@baseline/GREEN@fix, renders the full
live command matrix, prepares the first cell prompt/worktree, writes readiness,
and validates the `repo-bakeoff` story flows. It is still no-LLM, but it runs
more cargo work than the one-bug smoke.

For another repo, use the generic target directly after adding a manifest and
oracles under `tools/bugfix-bakeoff/external/projects/<name>/`:

```sh
make history-smoke \
  HISTORY_PROJECT=<name> \
  HISTORY_REPO_DIR=/path/to/private-or-local-checkout \
  HISTORY_BUGS=<bug-id> \
  HISTORY_CANDIDATES=<candidate-key>
```

If this target fails, do not run live cells. Its failures are setup or story
quality blockers: missing profiles, missing local commits, broken oracles,
stale flow fixtures, or drive commands that no longer match the harness.
When it passes, it also writes a review artifact at
`.artifacts/external-bakeoff/readiness/gears-rust.md` with preflight status,
the selected live-cell commands, existing scored/pending cells, missing cells,
pending-cell command templates for true provider/profile blockers, and the next
action. The first selected cell is prepared under
`.artifacts/external-bakeoff/cells/`, with the delegated MCP prompt under
`.artifacts/external-bakeoff/drive-prompts/`, so the operator can inspect the
exact setup before spending on a live drive. Set `HISTORY_PREPARE_FIRST_CELL=0`
to skip that free preparation step.

Regenerate that readiness report after adding results without rerunning the
cargo-backed RED/GREEN arming step:

```sh
python3 tools/bugfix-bakeoff/external/bench.py readiness \
  --project gears-rust \
  --repo-dir /Users/brad/code/gears-rust \
  --bug bug1 \
  --candidate opus-4.8 \
  --armed \
  --markdown .artifacts/external-bakeoff/readiness/gears-rust.md
```

Use `--armed` only when the selected fixtures were just verified by
`make gears-history-smoke`, `make history-smoke`, or `bench.py verify`.

To rehearse the blocked-provider report path without touching the normal live
results directory, run:

```sh
make history-pending-smoke \
  HISTORY_PROJECT=gears-rust \
  HISTORY_BUGS=bug1 \
  HISTORY_CANDIDATES=opus-4.8 \
  HISTORY_PENDING_REASON="profile not configured on this machine"
```

This proves a pending cell rolls up into Markdown + Slidey JSON as `pending`.
Use it only when no real model attempt happened; score real candidate worktrees
with `drive_cell.sh --score`.

## Deterministic Arming

Before spending on a live model, prove the corpus:

```sh
GEARS_RUST_REPO=/Users/brad/code/gears-rust make gears-bakeoff
```

The same proof can run through the drivable `repo-bakeoff` story by seeding:

```yaml
project: gears-rust
repo_dir: /Users/brad/code/gears-rust
bugs: [bug1, bug4, bug5, bug9]
candidates: [gpt-5.3-spark]
```

The `prepare` room calls:

```sh
python3 bench.py verify --project gears-rust --repo-dir /Users/brad/code/gears-rust
```

That is the full-corpus gate. For a scoped smoke or a smaller matrix, add the
same comma-separated bug list:

```sh
python3 bench.py verify \
  --project gears-rust \
  --bug bug1 \
  --repo-dir /Users/brad/code/gears-rust
```

If any selected fixture is not RED at baseline and GREEN after the real fix, do
not run live cells; fix or demote the fixture first.

## Free Cell Preparation

Inspect the exact Studio MCP drive prompt without spending:

```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
  --project gears-rust \
  --bug bug1 \
  --candidate gpt-5.3-spark \
  --repo-dir /Users/brad/code/gears-rust \
  --no-drive
```

This should prepare a worktree and print an artifact prompt path under
`.artifacts/external-bakeoff/drive-prompts/`. It should not modify the source
checkout.

## Live Drive

Run a single operator-approved cell:

```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
  --project gears-rust \
  --bug bug1 \
  --candidate gpt-5.3-spark \
  --repo-dir /Users/brad/code/gears-rust \
  --score
```

The delegated driver must act through Kitsoki Studio MCP only. The supervisor can
inspect git state, traces, and oracle results afterward.

For a cheapest-viable sweep:

```sh
tools/bugfix-bakeoff/external/escalate.sh \
  --project gears-rust \
  --bugs bug1,bug4,bug5,bug9 \
  --ladder default \
  --repo-dir /Users/brad/code/gears-rust
```

Use `--dry-run` first to review the cost-bearing plan.

## Score And Report

`drive_cell.sh --score` writes cell verdicts to
`.artifacts/external-bakeoff/results/cells/`. The `repo-bakeoff` story's
`results_dir` defaults to that same artifact directory, relative to the external
harness directory:

```yaml
results_dir: ../../../.artifacts/external-bakeoff/results
```

After one or more cells have run, advance the story from `running` to `scoring`.
That calls:

```sh
python3 bench.py summarize \
  --project gears-rust \
  --results ../../../.artifacts/external-bakeoff/results
```

The summary is deterministic and free. It rolls up whatever live-driver cell JSON
exists into solved / partial / failed counts and stores
`.artifacts/external-bakeoff/results/summary.json`. The same scoring step also
writes:

```text
.artifacts/external-bakeoff/report/report.md
.artifacts/external-bakeoff/report/deck.slidey.json
```

Those artifacts are generated from the scored cell JSON. They should not be
edited by hand or committed unless you are deliberately publishing a frozen case
study.

## Outputs

The external harness writes generated artifacts under:

```text
.artifacts/external-bakeoff/
  cells/<project>-<bug>-<candidate>/
  traces/<project>-<bug>-<candidate>.jsonl
  results/cells/<project>-<bug>-<candidate>-kitsoki.json
  report/report.md
  report/deck.slidey.json
  threads/<project>-<bug>-<candidate>.md
```

Do not commit those artifacts. Commit the durable improvements: manifests,
oracles, story hardening, docs, deterministic flow fixtures, and result summaries
only when they are intended to become part of the product evidence.

## Promoting Learning

A run improves the dev story only if the finding changes the reusable system:

- weak reproducer -> prompt or gate hardening;
- stuck maker call -> runtime watchdog or async status handling;
- wrong worktree -> harness/workspace fix;
- unclear repo setup -> onboarding docs or manifest schema improvement;
- repeated successful fix pattern -> fixture, precedent example, or eval case.

The gears corpus currently keeps four armable fixtures plus reference-only cases.
Promote a reference-only case when it has a standalone oracle or the harness gains
the injection mode it needs.
