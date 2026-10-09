# BugSwarm + GLM-5.2 bugfix comparison report

Generated at: `2026-07-06T00:00:00Z`.

This report is generated offline from committed evidence. It does not call
BugSwarm, Docker, or any LLM. Missing cells are reported as `pending`.

## Research Question

Compare the Kitsoki `bugfix` pipeline with raw prompts on GLM-5.2,
using total token usage as the primary cost axis, and prepare the same
success-rate comparison for a BugSwarm corpus alongside the existing
OSS oracle corpus.

The current committed evidence does not yet contain the full GLM-5.2
matrix. This report therefore separates observed results from missing
cells instead of imputing raw-prompt or BugSwarm numbers.

## Method

The headline matrix has one row per `(corpus, treatment)` bucket.
A cell is counted as attempted only when its quality is `solved`,
`partial`, or `failed`; `pending` and `blocked` are excluded from the
model-quality denominator. Token totals are summed only from committed
cell evidence that records real usage.

Inputs:

- Report JSON: `docs/case-studies/bugswarm-glm52-bugfix-report.data.json`.
- Report Markdown: `docs/case-studies/bugswarm-glm52-bugfix-report.md`.
- GLM-5.2 bakeoff cells: `tools/bugfix-bakeoff/results/cells`.
- Arena supporting rollup: `tools/arena/results/round-1/rollup.json`.
- OSS oracle corpus: `tools/arena/corpus/cost-bench.manifest.yaml`.
- Source catalog: `tools/arena/corpus/sources.yaml`.
- OSS arena GLM rollup: `not supplied`.
- BugSwarm source: `tools/arena/corpus/bugswarm.seed.yaml`.
- BugSwarm verification report: `not supplied`.
- BugSwarm arena rollup: `not supplied`.

Primary metrics:

- success rate: `solved / (solved + partial + failed)`.
- partial rate: reported separately because hidden oracles can be
  implementation-coupled.
- total tokens: provider-neutral primary cost measure.
- USD cost: secondary; only shown where committed cell evidence provides
  it.

## Corpus Coverage

| corpus | tasks | repositories | verified/imported status |
|---|---:|---:|---|
| OSS oracle corpus | 26 | 12 | frozen and locally validated |
| BugSwarm | 1 | n/a | adapter-ready; converted verified tasks: 0; verification report: 0/0 (none) |

The OSS oracle corpus remains the active internal benchmark source. It
covers the pre-registered public OSS targets plus existing hidden-oracle
bugfix fixtures. BugSwarm is represented separately in the source
catalog, so its fail/pass CI artifact sampling process does not get
collapsed into the OSS oracle denominator.

BugSwarm source contract:

- import explicit exported artifact metadata with
  `tools/arena/scripts/bugswarm_to_arena.py`.
- require `image_tag`, `repo`, `failed_job_id`, and `passed_job_id`.
- treat the failed job as RED and the passed job as GREEN inside the
  artifact image.
- keep imported tasks unattempted until Docker verification proves both
  sides still reproduce.
- verify with `tools/arena/scripts/bugswarm_verify_source.py`; dry-run
  mode records the Docker commands, while `--execute` runs each side in
  separate fresh containers.

## Source Mix

The OSS oracle source and BugSwarm are kept as separate source families
so the report can show blended overall treatment totals without hiding
which evidence came from deterministic GitHub-content oracles, hidden
bugfix fixtures, or containerized fail/pass CI artifacts.

| source component | tasks | repos | oracle kinds | split | repositories |
|---|---:|---:|---|---|---|
| pre_registered_oss_targets | 20 | 10 | github_content | heldout:4, training:16 | ansible/ansible, grafana/grafana, kubernetes/kubernetes, microsoft/TypeScript, microsoft/vscode, python/cpython, pytorch/pytorch, rust-lang/rust, tensorflow/tensorflow, vercel/next.js |
| armed_bugfix_fixtures | 6 | 2 | external_bakeoff | heldout:2, training:4 | kitsoki, query-string |
| BugSwarm containerized_fail_pass_ci_artifacts | 1 | 1 | fail/pass artifact scripts | verification-gated | square/okio |

Blend policy:

- Keep OSS oracle tasks and BugSwarm artifacts as separate source families in denominators.
- Report overall GLM-5.2 treatment totals only after both Kitsoki and raw-prompt arms have attempted cells.
- Use total tokens as the primary cross-source cost axis; USD remains secondary and evidence-dependent.
- Do not count dry-run BugSwarm verification as RED/GREEN proof.

## Reproducibility Ledger

Status: `reproducible`. Generator: `tools/arena/scripts/glm52_bugswarm_report.py` sha256 `08d7ac0776bf1edcca5c1860061be66745ed3b483f58fc37fb004bae9d494e43`.

Regenerate with:

```bash
python3 tools/arena/scripts/glm52_bugswarm_report.py --generated-at 2026-07-06T00:00:00Z --json-out docs/case-studies/bugswarm-glm52-bugfix-report.data.json --markdown-out docs/case-studies/bugswarm-glm52-bugfix-report.md
```

| artifact | kind | status | bytes | sha256 |
|---|---|---|---:|---|
| `tools/arena/corpus/cost-bench.manifest.yaml` | file | present | 20,768 | `e9ac3482ba56eae8e837b32e5f106565fbc8a03915d9c2535e563f87f69ffb49` |
| `tools/arena/corpus/sources.yaml` | file | present | 3,008 | `93125ed682ff0b829b75642fcefd6a4a8f2a2993f99122629dea51b70590e566` |
| `tools/arena/corpus/bugswarm.seed.yaml` | file | present | 1,185 | `f174df93ace8ad08aa7a5f90033301c09719a7b7230617b6b2c6379c13821a2e` |
| `tools/arena/results/round-1/rollup.json` | file | present | 10,978 | `49f8d3cb25601a32a51d44a44cc940bd24f0261af887cc31fdcbc95443745724` |
| `tools/bugfix-bakeoff/results/cells/*glm-5.2*.json` | directory-glob | 1 match(es) | n/a | n/a |
| `tools/bugfix-bakeoff/results/cells/bug9-glm-5.2-kitsoki.json` | file | present | 1,554 | `ae6b0d8529e1b3fc183a999e93788ffa852b9881dbc8e6a3248aa1fe89839046` |
| `optional-oss-arena-rollup` | missing-optional | missing | n/a | `n/a` |
| `optional-bugswarm-verification` | missing-optional | missing | n/a | `n/a` |
| `optional-bugswarm-arena-rollup` | missing-optional | missing | n/a | `n/a` |

Validation commands:

- `python3 tools/arena/scripts/glm52_report_gate.py --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json`
- `python3 tools/arena/scripts/glm52_report_gate.py --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json --require-publishable`
- `python3 tools/arena/tests/test_glm52_bugswarm_report.py`
- `python3 tools/arena/tests/test_glm52_report_gate.py`
- `python3 -m py_compile tools/arena/scripts/glm52_bugswarm_report.py tools/arena/scripts/glm52_report_gate.py`
- `python3 tools/arena/tests/validate_corpus.py tools/arena/corpus/cost-bench.manifest.yaml`
- `python3 tools/arena/tests/run_no_llm.py`

## GLM-5.2 Headline Matrix

| corpus | treatment | n | attempted | solved | partial | failed | pending | success rate | tokens |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| bugswarm | kitsoki | 1 | 0 | 0 | 0 | 0 | 1 | n/a | n/a |
| bugswarm | raw-prompt | 1 | 0 | 0 | 0 | 0 | 1 | n/a | n/a |
| oss-oracle | kitsoki | 1 | 1 | 0 | 1 | 0 | 0 | 0.000 | 2,890,980 |
| oss-oracle | raw-prompt | 1 | 0 | 0 | 0 | 0 | 1 | n/a | n/a |

## Overall GLM-5.2 Treatment Rollup

| treatment | n | attempted | solved | partial | failed | pending | success rate | tokens |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| kitsoki | 2 | 1 | 0 | 1 | 0 | 1 | 0.000 | 2,890,980 |
| raw-prompt | 2 | 0 | 0 | 0 | 0 | 2 | n/a | n/a |

## Kitsoki vs Raw-Prompt Comparisons

| scope | status | Kitsoki attempted | raw attempted | success delta | token ratio | notes |
|---|---|---:|---:|---:|---:|---|
| bugswarm | pending | 0 | 0 | n/a | n/a | Kitsoki GLM-5.2 arm has no attempted cells.; Raw-prompt GLM-5.2 arm has no attempted cells. |
| oss-oracle | pending | 1 | 0 | n/a | n/a | Raw-prompt GLM-5.2 arm has no attempted cells. |
| overall | pending | 1 | 0 | n/a | n/a | Raw-prompt GLM-5.2 arm has no attempted cells. |

## Research Claim Ledger

Status: `partial` (3 supported, 3 pending).

Publication gate:

```bash
python3 tools/arena/scripts/glm52_report_gate.py \
  --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json \
  --require-publishable
```

| claim | status | finding | missing evidence / caveat |
|---|---|---|---|
| overall-token-usage | `pending` | The claim is not yet answerable from committed evidence | Raw-prompt GLM-5.2 arm has no attempted cells.; No delta or token ratio is published while the comparison is pending |
| overall-success-rate | `pending` | The claim is not yet answerable from committed evidence | Raw-prompt GLM-5.2 arm has no attempted cells.; No delta or token ratio is published while the comparison is pending |
| bugswarm-success-rate | `pending` | The claim is not yet answerable from committed evidence | Kitsoki GLM-5.2 arm has no attempted cells.; Raw-prompt GLM-5.2 arm has no attempted cells.; No delta or token ratio is published while the comparison is pending |
| bugswarm-reusable-source | `supported` | Imported BugSwarm task count: 1 | Execute-mode RED/GREEN verification is still required before live GLM-5.2 cells |
| oss-source-mix | `supported` | 20 tasks over 10 public targets; 6 armed bugfix fixture tasks | GLM-5.2 headline cells currently cover only the committed bugfix fixture row |
| observed-oss-kitsoki-glm52-cell | `supported` | 1 attempted cell(s), 2890980 total tokens | This is not a Kitsoki-vs-raw comparison until the matching raw-prompt arm is attempted |

## Threats To Validity

Status: `blocked` (5 active, 2 high severity).

| threat | category | severity | status | mitigation |
|---|---|---|---|---|
| missing-raw-glm52-arm | internal | `high` | `active` | Commit raw-prompt GLM-5.2 cells for every headline task and regenerate the report |
| bugswarm-unverified-artifact | construct | `high` | `active` | Run bugswarm_verify_source.py --execute, apply the verification report, and regenerate with --bugswarm-verification |
| single-observed-glm52-cell | external | `medium` | `active` | Schedule the remaining GLM-5.2 cells and report denominators by source family |
| partial-is-not-solved | construct | `medium` | `active` | Keep partial rate separate from success rate and adjudicate oracle-coupled failures before publication |
| supporting-round-not-glm52 | external | `low` | `active` | Keep supporting round results out of headline GLM-5.2 denominators |

## Completion Audit

Status: `incomplete` (4/8 requirements proven).

| requirement | status | finding | next |
|---|---|---|---|
| report-artifact | `proven` | The report is generated offline from committed inputs | done |
| oss-source | `proven` | The report references the frozen OSS oracle corpus and keeps it separate from BugSwarm | done |
| bugswarm-source | `proven` | Imported BugSwarm task count: 1 | done |
| bugswarm-execute-verification | `missing` | Verification mode=none; verified=0/0 | Run bugswarm_verify_source.py --execute and apply the verification report |
| oss-kitsoki-glm52 | `proven` | 1 attempted cell(s), 2890980 total tokens | done |
| oss-raw-glm52 | `missing` | No attempted cell is committed. Pending task(s): kitsoki-bug9-bugfix-test-repair | Run the generated gap-plan commands, land the rollup, and regenerate this report |
| bugswarm-kitsoki-glm52 | `missing` | No attempted cell is committed. Pending task(s): bugswarm-square-okio-140452393 | Run the generated gap-plan commands, land the rollup, and regenerate this report |
| bugswarm-raw-glm52 | `missing` | No attempted cell is committed. Pending task(s): bugswarm-square-okio-140452393 | Run the generated gap-plan commands, land the rollup, and regenerate this report |

## Study Protocol

Status: `pending-evidence`. Candidate: `glm-5.2`. Primary cost metric: `total_tokens`.

Success metric: `solved / (solved + partial + failed)`.

| corpus | task | treatment | gate |
|---|---|---|---|
| oss-oracle | kitsoki-bug9-bugfix-test-repair | raw-prompt | `ready-to-plan` |
| bugswarm | bugswarm-square-okio-140452393 | kitsoki | `execute-verify-bugswarm` |
| bugswarm | bugswarm-square-okio-140452393 | raw-prompt | `execute-verify-bugswarm` |

Execution steps:

- `oss-raw-glm52`: `ready`; Schedule missing OSS oracle raw-prompt GLM-5.2 cells with the frozen corpus manifest.
  Report regeneration argument: `--oss-arena-rollup .artifacts/arena/glm52-oss/rollup.json`.
  Commands:
  - `python3 tools/arena/scripts/oss_to_arena_spec.py --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json --corpus tools/arena/corpus/cost-bench.manifest.yaml --out .artifacts/arena/oss-glm52.yaml`
  - `python3 tools/arena/arena.py plan --spec .artifacts/arena/oss-glm52.yaml`
  - `python3 tools/arena/arena.py run --spec .artifacts/arena/oss-glm52.yaml --out .artifacts/arena/glm52-oss`
  - `ARENA_PAIRED_TASK_ENABLE_CODEX=1 python3 tools/arena/arena.py run --spec .artifacts/arena/oss-glm52.yaml --out .artifacts/arena/glm52-oss --live`
- `bugswarm-execute-verification`: `required-before-live`; Prove BugSwarm failed/passed scripts still reproduce in fresh containers.
  Report regeneration argument: `--bugswarm-verification .artifacts/bugswarm/verification.json`.
  Commands:
  - `python3 tools/arena/scripts/bugswarm_verify_source.py --source tools/arena/corpus/bugswarm.seed.yaml --out .artifacts/bugswarm/verification.json --execute`
  - `python3 tools/arena/scripts/bugswarm_apply_verification.py --source tools/arena/corpus/bugswarm.seed.yaml --verification .artifacts/bugswarm/verification.json --out .artifacts/bugswarm/arena-source.verified.yaml`
  - `python3 tools/arena/scripts/glm52_gap_plan.py --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json --json-out .artifacts/arena/glm52-gap-plan.json --markdown-out .artifacts/arena/glm52-gap-plan.md --bugswarm-source .artifacts/bugswarm/arena-source.verified.yaml`

Live controls:

- The report generator, gap planner, and tests are offline and must not run Docker or LLMs.
- The operator must run no-LLM arena.py plan and non-live arena.py run before any --live command.
- Live commands must be explicit and include ARENA_PAIRED_TASK_ENABLE_CODEX=1.
- GLM-5.2 raw-prompt variants must use backend=claude so paired_task_runner dispatches through the synthetic-claude profile.
- BugSwarm live cells require execute-mode RED/GREEN verification before model scheduling.

## Committed GLM-5.2 Cells

| task | treatment | quality | tokens | cost | evidence |
|---|---|---|---:|---:|---|
| bug9 | kitsoki | partial | 2,890,980 | $15.575450 | `tools/bugfix-bakeoff/results/cells/bug9-glm-5.2-kitsoki.json` |

## Committed OSS GLM-5.2 Arena Cells

| task | treatment | quality | tokens | cost | evidence |
|---|---|---|---:|---:|---|
| none | none | pending | n/a | n/a | n/a |

## Committed BugSwarm GLM-5.2 Arena Cells

| task | treatment | quality | tokens | cost | evidence |
|---|---|---|---:|---:|---|
| none | none | pending | n/a | n/a | n/a |

## Evidence Gaps

- No committed raw-prompt GLM-5.2 result exists for the OSS oracle corpus.
- BugSwarm artifacts have been imported but none are verified RED/GREEN yet.
- No BugSwarm verification report is attached to this generated report.
- Some imported BugSwarm tasks are missing committed GLM-5.2 Kitsoki or raw-prompt result cells.

## Evidence Closure Packet

Generate the offline execution packet for the pending headline cells with:

```bash
python3 tools/arena/scripts/glm52_gap_plan.py \
  --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json \
  --json-out .artifacts/arena/glm52-gap-plan.json \
  --markdown-out .artifacts/arena/glm52-gap-plan.md \
  --bugswarm-source tools/arena/corpus/bugswarm.seed.yaml
```

The packet emits no-spend `arena.py plan` / arming commands and, only
after a spec passes audit, explicit `ARENA_PAIRED_TASK_ENABLE_CODEX=1
... --live` commands for operator execution.

| corpus | status | pending | next |
|---|---|---:|---|
| oss-oracle | `ready-to-plan` | 1 | Run glm52_gap_plan.py; it can generate an OSS paired-task spec from the frozen corpus manifest. |
| bugswarm | `needs-execute-verification` | 2 | Run bugswarm_verify_source.py --execute and apply the verification report before scheduling live GLM-5.2 cells. |

## Interpretation

- Committed GLM-5.2 Kitsoki evidence contains 1 attempted OSS oracle cell(s), 2890980 total tokens, and no solved cell yet.
- The GLM-5.2 raw-prompt arm remains pending; the report must not compute a token ratio from missing data.
- BugSwarm is reusable as an imported source with 1 task(s) in the supplied source file.

## Provenance and References

Local evidence:

- `tools/bugfix-bakeoff/results/cells` — committed Kitsoki/raw-prompt bugfix cells and usage evidence.
- `tools/arena/corpus/cost-bench.manifest.yaml` — frozen reusable OSS task source and deterministic oracle metadata.
- `tools/arena/corpus/sources.yaml` — adapter contract, required metadata fields, and verification contract.

Upstream references:

- BugSwarm website: https://www.bugswarm.org/ — dataset and project entry point.
- BugSwarm client: https://github.com/BugSwarm/client — artifact client and image execution interface.
- BugSwarm REST API: https://www.bugswarm.org/docs/toolset/bugswarm-rest-api/ — artifact metadata filtering and retrieval interface.
- BugSwarm paper: https://arxiv.org/abs/1903.06725 — published dataset/infrastructure description.

BugSwarm seed provenance:

- BugSwarm seed artifact square-okio-140452393: https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/ — Official BugSwarm tutorial artifact; the tutorial documents run_failed.sh and run_passed.sh execution in fresh containers.

Bottom line: the committed GLM-5.2 evidence is not yet sufficient to
claim Kitsoki beats or loses to raw prompts. The report is useful now as
a reproducible evidence ledger and corpus scaffold; the headline
comparison still requires raw-prompt GLM-5.2 cells and verified
BugSwarm cells.

## Supporting Codex-Native OSS Round

The existing arena `round-1` results are supporting evidence for the
Kitsoki-vs-raw-prompt harness and token accounting, but they are not
GLM-5.2 cells. They should not be used to answer the GLM headline.

| treatment | n | attempted | solved | failed | success rate | tokens |
|---|---:|---:|---:|---:|---:|---:|
| kitsoki | 4 | 4 | 2 | 2 | 0.500 | 21,459,517 |
| raw-prompt | 4 | 4 | 2 | 2 | 0.500 | 537,743 |
