# Repo History Training For A New Repo

Use this recipe to turn a repo's own fixed bugs into a reliable Kitsoki dev
story loop. The output is not a demo transcript; it is a manifest of historical
bugs, deterministic RED/GREEN oracles, a no-cost readiness report, and a set of
operator-approved live cells that can be scored by hidden oracles.

The shipped references are:

- `query-string`: public, small, already reported with live GPT-5.5 results.
- `gears-rust`: private/heavy Rust reference with four armable fixtures and a
  full no-cost smoke.

## 1. Pick Armable Historical Bugs

Start with three to five real fixes that added or can be isolated into a
regression test. For each one, record:

- bug id;
- issue or ticket text;
- `fix_sha`;
- `baseline_sha` (`fix_sha^` unless the real parent is different);
- source path or directory changed by the real fix;
- a standalone oracle test file;
- the exact command that runs only that oracle.

Discard a case if the oracle is already green at the baseline. That case cannot
prove the model fixed anything.

## 2. Add A Manifest

Create:

```text
tools/bugfix-bakeoff/external/projects/<project>/
  manifest.yaml
  oracles/<bug>.<ext>
```

Use the existing manifests as templates:

- `tools/bugfix-bakeoff/external/projects/query-string/manifest.yaml`
- `tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml`

For private or heavy repos, set `project.local_only: true` and pass
`HISTORY_REPO_DIR=/path/to/checkout` in the commands below.

## 3. Prove Setup Without Spending

Run the generic smoke over the exact matrix you intend to drive:

```sh
make history-smoke \
  HISTORY_PROJECT=<project> \
  HISTORY_REPO_DIR=/path/to/local-checkout \
  HISTORY_BUGS=<bug1,bug2,bug3> \
  HISTORY_CANDIDATES=<candidate-key>
```

For public repos that the harness can clone, omit `HISTORY_REPO_DIR`.

This gate is no-cost. It runs harness unit tests, preflight, RED/GREEN oracle
arming, drive-command rendering, first-cell `drive_cell.sh --no-drive`
preparation, readiness report generation, and `repo-bakeoff` flow validation.
The first-cell preparation also writes
`.artifacts/external-bakeoff/prepared/<project>-<bug>-<candidate>.json`, which
records the worktree, branch, trace, prompt, preflight, and future score-result
paths for review or handoff.

The readiness report lands at:

```text
.artifacts/external-bakeoff/readiness/<project>.md
```

It should say:

- `Preflight: ready`
- `Arming: verified`
- selected cells equal the intended matrix size
- missing cells equal the live cells that still need model attempts

## 4. Rehearse Blocked-Provider Reporting

If a provider/profile is blocked before any model attempt, record the cell as
`pending`, not failed. Rehearse the reporting path without touching the normal
live results directory:

```sh
make history-pending-smoke \
  HISTORY_PROJECT=<project> \
  HISTORY_BUGS=<bug-id> \
  HISTORY_CANDIDATES=<candidate-key> \
  HISTORY_PENDING_REASON="profile not configured on this machine"
```

This writes a pending cell to a temp directory, summarizes it, and validates the
generated Markdown + Slidey JSON. Use this only for pre-attempt blockers. If a
model produced a candidate worktree, score that worktree instead.

## 5. Drive Live Cells Only On Approval

When the readiness report is clean and a live operator approves spend, run one
cell at a time:

```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
  --project <project> \
  --bug <bug-id> \
  --candidate <candidate-key> \
  --repo-dir /path/to/local-checkout \
  --score
```

For public cloneable repos, omit `--repo-dir`.

`drive_cell.sh --score` prepares the baseline worktree, drives the Kitsoki
bugfix story through Studio MCP, scores the result with the hidden oracle, and
writes:

```text
.artifacts/external-bakeoff/results/cells/<project>-<bug>-<candidate>-kitsoki.json
```

The companion prepared-cell metadata remains at:

```text
.artifacts/external-bakeoff/prepared/<project>-<bug>-<candidate>.json
```

## 6. Report From Results

Regenerate the deterministic report and deck:

```sh
python3 tools/bugfix-bakeoff/external/bench.py summarize \
  --project <project> \
  --results ../../../.artifacts/external-bakeoff/results \
  --deck ../../../.artifacts/external-bakeoff/report/deck.slidey.json \
  --markdown ../../../.artifacts/external-bakeoff/report/report.md
```

Do not hand-edit the generated report. Fix the manifest, cell JSON, or harness
if the report is wrong.

## Done Criteria

A repo-history training path is ready to cite when:

- the manifest and oracle files are committed;
- `make history-smoke ...` passes for the intended matrix;
- readiness shows clean preflight and verified arming;
- every selected cell is either scored from a live model attempt or honestly
  marked `pending` for a pre-attempt provider/profile blocker;
- `bench.py summarize` produces a deterministic Markdown report and Slidey JSON;
- durable claims live in docs or case studies, while generated artifacts remain
  under `.artifacts/` unless deliberately promoted.
