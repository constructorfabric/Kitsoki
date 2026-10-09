# Arena Cost Corpus

This directory is reserved for the frozen cost-efficiency corpus from
`docs/goals/generalized-usage/decomposition.yaml` (`WB.1`).

The real corpus must not be hand-curated to flatter Kitsoki. It is created only
after the evidence-based archetype mining step described in
`docs/research/cost-efficiency-benchmark.md`, then frozen as:

- `archetypes.yaml`
- `cost-bench.manifest.yaml`
- `sources.yaml`

Before any live benchmark spend, validate the frozen manifest shape with:

```bash
python3 tools/arena/tests/validate_corpus.py tools/arena/corpus/cost-bench.manifest.yaml
```

That command is intentionally red until the corpus exists and every task records
its deterministic RED/GREEN oracle proof and train/held-out split.

## Reusable Sources

`sources.yaml` records corpus families separately from any one frozen manifest.
The current active source is the pre-registered OSS oracle corpus in
`cost-bench.manifest.yaml`. BugSwarm is also registered as an adapter-ready
source: exported BugSwarm artifact metadata can be converted into arena-shaped
tasks with:

```bash
python3 tools/arena/scripts/bugswarm_to_arena.py \
  --in .artifacts/bugswarm/artifacts.json \
  --out .artifacts/bugswarm/arena-source.yaml
```

The committed `bugswarm.seed-artifacts.json` / `bugswarm.seed.yaml` pair is a
metadata-only seed that keeps the GLM-5.2 report wired to a real BugSwarm
artifact before any Docker pulls are approved. Regenerate it with:

```bash
python3 tools/arena/scripts/bugswarm_to_arena.py \
  --in tools/arena/corpus/bugswarm.seed-artifacts.json \
  --out tools/arena/corpus/bugswarm.seed.yaml
```

The converter is offline and does not pull Docker images. The generated
BugSwarm tasks start with `verified_red: false` and `verified_green: false`;
those flags become true only after an explicit Docker verification proves the
failed job still fails and the passed job still passes inside the artifact.

Plan verification without pulling images:

```bash
python3 tools/arena/scripts/bugswarm_verify_source.py \
  --source .artifacts/bugswarm/arena-source.yaml \
  --out .artifacts/bugswarm/verification.json \
  --dry-run
```

Execute verification only when Docker pulls and long CI jobs are acceptable:

```bash
python3 tools/arena/scripts/bugswarm_verify_source.py \
  --source .artifacts/bugswarm/arena-source.yaml \
  --out .artifacts/bugswarm/verification.json \
  --execute
```

On a capacity-constrained Docker host, verify a bounded batch and apply it to
the exact source revision before moving to the next batch.  `--task-id` may be
repeated; the receipt pins the source bytes, and the applier rejects a receipt
for a different source revision.  Re-run the verifier against the newly
written source for the next batch, preserving prior task evidence.

```sh
python3 tools/arena/scripts/bugswarm_verify_source.py \
  --source .artifacts/bugswarm/arena-source.yaml \
  --out .artifacts/bugswarm/verification.okio.json \
  --execute --task-id bugswarm-square-okio-140452393
python3 tools/arena/scripts/bugswarm_apply_verification.py \
  --source .artifacts/bugswarm/arena-source.yaml \
  --verification .artifacts/bugswarm/verification.okio.json \
  --out .artifacts/bugswarm/arena-source.batch-1.yaml
```

The applier creates content-addressed source and receipt snapshots beside its
output. In a managed capsule, supply `--evidence-dir` outside that capsule
(normally the primary checkout's `.artifacts/bugswarm/evidence`); capsule
teardown otherwise deletes ignored evidence and the lock will reject it.

Do not hand-copy image digests or commit identifiers into a source.  The corpus
locker remains blocked until each selected task has execute evidence, a pinned
image digest, both commit SHAs, and a receipt hash.

The verifier follows BugSwarm's own artifact contract: `run_failed.sh` must
exit non-zero and `run_passed.sh` must exit zero, each in a fresh container so
the failed script cannot pollute the passed run.

After an `--execute` verification pass, write a benchmark-ready source without
hand-editing YAML:

```bash
python3 tools/arena/scripts/bugswarm_apply_verification.py \
  --source .artifacts/bugswarm/arena-source.yaml \
  --verification .artifacts/bugswarm/verification.json \
  --out .artifacts/bugswarm/arena-source.verified.yaml
```

Dry-run reports are rejected by default because they do not prove RED/GREEN.
Pass `--allow-dry-run` only when you want to carry command-plan metadata forward
without setting `verified_red` or `verified_green`.

## GLM-5.2 + BugSwarm Report

The interim research report is generated, not hand-maintained:

```bash
python3 tools/arena/scripts/glm52_bugswarm_report.py \
  --generated-at 2026-07-06T00:00:00Z \
  --json-out docs/case-studies/bugswarm-glm52-bugfix-report.data.json \
  --markdown-out docs/case-studies/bugswarm-glm52-bugfix-report.md
```

Pass `--bugswarm-source .artifacts/bugswarm/arena-source.verified.yaml` after
applying an execute-mode verification report, and
`--bugswarm-verification .artifacts/bugswarm/verification.json` so the report
records the verification evidence. Pass `--oss-arena-rollup <rollup.json>` and
`--bugswarm-arena-rollup <rollup.json>` when GLM-5.2 paired-task arena runs have
landed for those corpora; the generator folds those cells into the headline
matrix while leaving the default Codex-native round-1 rollup as supporting
evidence only. The generator keeps unavailable GLM-5.2 cells as `pending`, so
missing raw-prompt or BugSwarm results cannot accidentally become zero-cost
failures.

To turn the report's pending headline cells into an operator run packet without
spending, run:

```bash
python3 tools/arena/scripts/glm52_gap_plan.py \
  --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json \
  --json-out .artifacts/arena/glm52-gap-plan.json \
  --markdown-out .artifacts/arena/glm52-gap-plan.md
```

Add `--oss-spec`, `--bugswarm-spec`, or `--bugswarm-source` to have the packet
include the exact `arena.py plan`, no-LLM `arena.py run`, and explicit
`ARENA_PAIRED_TASK_ENABLE_CODEX=1 ... --live` commands for live-ready missing
cells. The planner audits supplied specs before emitting paid commands: the
Kitsoki GLM-5.2 arm is live-ready through paired-task's Kitsoki profile mapping,
and the raw-prompt GLM-5.2 arm is live-ready only when its variant uses
`backend: claude` so the runner can use the `synthetic-claude` profile. When
given an execute-verified `--bugswarm-source`, the packet generates the
BugSwarm spec with `--kitsoki-backend codex --raw-backend claude`. If the
source is only the committed metadata seed, the packet emits the Docker
verification and verification-application commands instead of live model
commands. The standalone spec generator still defaults to `backend: synthetic`
so ad hoc generated specs stay no-spend until an operator explicitly opts in.
At live run time, paired-task copies the failing checkout from the BugSwarm
artifact image and scores the modified candidate in a fresh artifact container
with `./run_failed.sh`, so Docker image pulls and long CI jobs remain an
operator-controlled step.
