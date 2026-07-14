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

For a repo-agnostic version of this process, start with
[`repo-history-training-new-repo.md`](repo-history-training-new-repo.md). This
gears-rust recipe is the private/heavy Rust reference path.

## One-Time Setup

Start from a local checkout of the target repo:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout
```

Or start from an initialized meta checkout:

```sh
BUGFIX_BAKEOFF_META_REPO=/path/to/meta-checkout
git -C "$BUGFIX_BAKEOFF_META_REPO" submodule update --init src/cyberfabric/gears-rust
```

The manifest declares `repo_subdir: src/cyberfabric/gears-rust`, so every
`--repo-dir /path/to/meta-checkout` command resolves to the submodule
checkout. Passing `/path/to/checkout` still works as the standalone
single-repo form. In both forms, the checkout must have the benchmark
`baseline_sha` and `fix_sha` commits reachable; `bench.py preflight` reports the
missing commits before any model spend.

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
no-drive worktree/prompt preparation via `drive_cell.sh --no-drive`, and the
`repo-bakeoff` story flows without calling a live model:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-history-smoke
```

By default this smokes `bug1` with `opus-4.8`. Override the matrix before a live
run so the free proof matches the cell you intend to drive:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout \
GEARS_HISTORY_BUGS=bug1,bug4 \
GEARS_HISTORY_CANDIDATES=opus-4.8,gpt-5.3-spark \
make gears-history-smoke
```

Before calling the reference path product-ready for the current gears-rust
corpus, run the full armable-fixture smoke:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-history-full-smoke
```

That verifies `bug1,bug4,bug5,bug9` RED@baseline/GREEN@fix, renders the full
live command matrix, prepares every selected prompt/worktree, writes readiness,
asserts every selected cell has fresh prepared metadata with zero
stale/unprepared handoffs, validates the `repo-bakeoff` story flows, and
rehearses deterministic pending report/deck generation. It is still no-LLM, but
it runs more cargo work than the one-bug smoke.

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
`.artifacts/external-bakeoff/readiness/gears-rust.md` and a machine-readable
audit index at `.artifacts/external-bakeoff/readiness/gears-rust.json` with
preflight status, the selected live-cell commands, existing scored/pending
cells, missing cells, stale result artifacts, prepared/stale/unprepared handoff
counts, pending-cell command templates for true provider/profile blockers, and
the next action. It also writes
`.artifacts/external-bakeoff/readiness/gears-rust-completion.md` and
`.artifacts/external-bakeoff/readiness/gears-rust-completion.json`, which give
the explicit product verdict: no-cost ready, ready to drive live, result
evidence complete, and live scored capability result. It also writes
`.artifacts/external-bakeoff/readiness/gears-rust-handoffs.md` and
`.artifacts/external-bakeoff/readiness/gears-rust-handoffs.json`, which audit
the prepared MCP prompts before spend. That audit fails if the prompt lacks the
bug id, profile, worktree, and no-shell drive instructions, or if it leaks hidden
oracle paths/content or real-fix commit/source hints.
`Unprepared cells` means a selected cell does not yet have no-drive handoff
metadata; `Stale prepared cells` means the metadata exists but points at
missing prompt/worktree/preflight paths. Neither means the cell failed. The
prepared cells are under `.artifacts/external-bakeoff/cells/`, with the
delegated MCP prompt under `.artifacts/external-bakeoff/drive-prompts/`. The
same step writes
`.artifacts/external-bakeoff/prepared/<project>-<bug>-<candidate>.json` with the
worktree, branch, trace, prompt, preflight, and score-result paths, so the
operator can inspect or hand off the exact setup before spending on a live
drive. `gears-history-smoke` prepares the first selected cell by default;
`gears-history-full-smoke` prepares all four armable cells. Set
`HISTORY_PREPARE_FIRST_CELL=0` to skip that free preparation step.

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
Regenerate the completion verdict from the same artifacts after live cells
finish or after true pre-attempt blockers are recorded as pending:

```sh
python3 tools/bugfix-bakeoff/external/bench.py completion \
  --project gears-rust \
  --repo-dir /Users/brad/code/gears-rust \
  --bug bug1,bug4,bug5,bug9 \
  --candidate opus-4.8 \
  --armed \
  --markdown .artifacts/external-bakeoff/readiness/gears-rust-completion.md
```

For the current full no-cost reference run, completion should say
`No-cost ready: yes` and `Ready to drive live: yes`, while
`Result evidence complete` and `Live scored capability result` remain `no`
until every selected cell has a current scored or pending result artifact.
Use `--require-result-evidence` to fail until those scored/pending artifacts
exist. Use `--require-live-scored` to fail until every selected cell is a
non-pending scored result; that is the gate for claiming live model capability.

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
It also proves the completion verdict treats the pending result as complete
evidence for accounting while still saying `Live scored capability result: no`.
Use it only when no real model attempt happened; score real candidate worktrees
with `drive_cell.sh --score`.
The full gears smoke runs this rehearsal after the full readiness gate, so one
command proves both the live-cell handoff path and the no-cost reporting
fallback.

## Deterministic Arming

Before spending on a live model, prove the corpus:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-bakeoff
```

Equivalent meta-repo form:

```sh
python3 tools/bugfix-bakeoff/external/bench.py verify \
  --project gears-rust \
  --bug bug1,bug4,bug5,bug9 \
  --repo-dir /path/to/meta-checkout
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

This should prepare a worktree, print an artifact prompt path under
`.artifacts/external-bakeoff/drive-prompts/`, and write prepared-cell metadata
under `.artifacts/external-bakeoff/prepared/`. It should not modify the source
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
