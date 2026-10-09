# Story Upgrade Testing

Story upgrades have two gates:

- CI-safe checks that never call a real LLM.
- Operator-approved live smoke runs that prove upgraded project stories still
  continue through realistic work.

## Deterministic Gate

Use capsules for reusable old-project shapes. The first fixture is
`capsules/old-dev-story-project/capsule.yaml`: it represents an older onboarded
project with a project profile and stale materialized dev-story instance.

Run:

```sh
go test ./cmd/kitsoki -run TestProjectToolsUpgrade -count=1
go run ./cmd/kitsoki capsule verify old-dev-story-project
```

The upgrade check is read-only unless `--apply` is passed:

```sh
kitsoki project-tools upgrade --target /path/to/project
kitsoki project-tools upgrade --target /path/to/project --apply
```

`--apply` refreshes only the embedded project toolkit (`.agents`, `.claude`,
`.mcp.json`). It does not overwrite `.kitsoki/stories/*/app.yaml`; story
regeneration needs an explicit reviewed flow so local customizations are not
dropped. A normal dev-story project does not need regeneration: its thin,
project-owned wrapper imports `@kitsoki/dev-story`, so the selected binary or
source override supplies the new base story.

## Test the three update channels separately

An onboarded project has three independent update channels. Testing one is not
evidence that the other two work:

| Channel | Dry run / selection | Apply / clear | Must preserve |
|---|---|---|---|
| Toolkit and MCP registration | `kitsoki project-tools upgrade --target /path/to/project` | `kitsoki project-tools upgrade --target /path/to/project --apply` | Project profile and materialized story wrapper. |
| Project-profile discovery | `kitsoki project-profile refresh --target /path/to/project` (add `--json` for the full candidate) | `kitsoki project-profile refresh --target /path/to/project --apply` | `source: operator` fields, unrelated project policy, and the wrapper. |
| Base `@kitsoki/dev-story` | Install a new binary, or select a source route below. | Clear the selected override to return to the next resolver tier. | The project-owned wrapper and profile. |

Profile refresh owns only fields whose `onboarding.resolutions` source is
`discovered` or `default`. It must retain an explicit `source: operator` value.
It also treats a legacy/manual field value that differs from its last recorded
managed value as operator-owned and promotes its resolution accordingly. An
unchanged second `--apply` must report `changed: false` and write nothing.

Exercise both the installed-binary and folder-based story routes. The normal
installed-binary route has no override and resolves the embedded story. The
repo-wide staging route is:

```sh
cd /path/to/project
kitsoki --kitsoki-repo /Users/brad/code/Kitsoki/.capsules/staging/local run
```

`--kitsoki-repo` resolves every `@kitsoki/<name>` from the checkout's
`stories/<name>/app.yaml`. For only `dev-story`, point directly at the directory
containing its `app.yaml`:

```sh
cd /path/to/project
KITSOKI_KIT_DEV_DEV_STORY=/Users/brad/code/Kitsoki/.capsules/staging/local/stories/dev-story \
  kitsoki run

kitsoki kit dev dev-story \
  --path /Users/brad/code/Kitsoki/.capsules/staging/local/stories/dev-story
kitsoki kit dev dev-story --clear
```

Per-story environment and persisted kit-dev overrides win over
`--kitsoki-repo` / `KITSOKI_REPO`; repo-wide overrides win over the embedded
library. Clear or unset higher-precedence routes before claiming a newly
installed binary's embedded base story passed.

Add a capsule whenever an upgrade bug is fixed. Prefer the smallest project
state that reproduces the old shape: config/profile, stale generated story,
ticket fixture, PR fixture, or previously solved bug.

## Kit Lifecycle Upgrades (S7)

For a kit a project imports but does not own, the two-gate doctrine now runs
through the staged lifecycle (`docs/proposals/kit-lifecycle.md`):

```sh
kitsoki kit update <name>   # stage a semver-gated candidate; kits.lock untouched
kitsoki kit trial <name>    # judge it; failures become the migration worklist
kitsoki kit accept <name>   # promote fail-closed on the trial receipt
```

- **Deterministic gate:** `kit trial`'s contract, frozen-replay, and
  onboarding gates ARE the CI-safe check — kit conformance plus every
  consumer flow fixture replayed against both the locked and staged trees
  under `KITSOKI_CASSETTE_STRICT=1` (a cassette miss fails closed; the
  receipt reports measured per-gate spend, so "zero LLM" is observed, not
  assumed). The same tools ride the studio MCP as `kit.status` /
  `kit.update` / `kit.trial` / `kit.accept` / `kit.reject`, and
  `story.test` / `story.validate` take `staged: true` for a per-call
  staged resolution.
- **Live smoke:** operator-approved live runs map to the trial's
  `baseline_live` lane. No-cost oracles validate by replay immediately;
  live oracles (red_green, agent_eval) queue as `pending_approval` and are
  only ever run at an operator's explicit request, with the outcome
  recorded in the validation ledger (`.kitsoki/qa/validation-ledger.yaml`)
  so an already-validated case is SKIPPED on every re-trial instead of
  re-spending.

The reusable fixture is `capsules/kit-update-pair/capsule.yaml`: a consumer
project shaped to lock widget v1 while upstream ships v2 with a renamed
intent (compat hint) and a new required host. Run:

```sh
go test ./cmd/kitsoki -run TestKitLifecycle -count=1
go run ./cmd/kitsoki capsule verify kit-update-pair
```

then follow the capsule's README to exercise update → trial → accept by
hand.

## Deterministic upgrade case checklist

For each deterministic upgrade case, assert:

- the project wrapper loads through its existing `@kitsoki/dev-story` import;
- two identical profile refresh applies leave byte-identical project policy;
- default/discovered fields can move when repository evidence changes, while
  operator-owned fields do not;
- missing discoveries produce a visible default notice with an exact profile
  field to update;
- onboarding readiness can be rerun and replaces, rather than accumulates, its
  current report;
- applicable test/dev-server, branch, ticket, and PR postconditions have named
  verification results.

## Stable-corpus upgrade gate

Use **repo-history capsules** for the stable historical bug cases; “oracle
capsules” is only a compatibility alias. Prove each hidden oracle RED at the
recorded baseline and GREEN with the maintainer fix, then admit the canonical
`corpus-case.v1` objects through Corpus Forge and freeze a durable
`corpus-receipt.v1`.

Keep two non-overlapping receipts in the same durable registry:

- `calibration` is the repeatable corpus used to diagnose and improve the
  story, prompts, and gates;
- `heldout` is promotion-only evidence. Do not inspect it or tune on it, even
  when a calibration case resembles it.

The upgrade gate is complete only when onboarding prerequisites are explicit,
all calibration cases have independent green evidence, and a developer can pick
a configured bug ticket, work it on a policy-compliant branch, and open a PR
using the recorded destination/base/template policy.

This is not yet a single executable pipeline. `repo-bakeoff` prepares and
scores historical cells, Corpus Forge freezes receipts, `dogfood-marathon`
drives and independently checks queues, and `goal-seeker` supplies an outer
self-healing/integration loop, but no shipped command consumes a receipt and
runs those surfaces until the whole calibration corpus is green. Record the
handoffs explicitly. The current repo-bakeoff harness also lives under
`tools/bugfix-bakeoff/external`, so its preparation/drive commands still require
a Kitsoki source checkout rather than only the installed binary.

See [dev-story onboarding](../stories/dev-story-onboarding.md),
[repo-history capsules](../recipes/repo-history-capsules.md), and
[Corpus Forge](../../stories/corpus-forge/README.md) for the source contracts.

## Live LLM Smoke

Live story upgrade smoke runs are manual and must be explicitly requested by an
operator. Do not wire them into normal tests.

Recommended scenario set:

- Bug fixing: open an old-project capsule with a known failing test and drive
  the bugfix path through the current dev-story, ending with the configured PR
  path rather than only a local patch.
- PR refinement: create a capsule with an existing branch and review feedback,
  then drive the PR refinement path to a patch plus validation evidence.
- PRD-from-ideas: use a captured/mined idea input and verify the current story
  can continue from discovery into a durable PRD/design artifact.

Capture outputs under `.artifacts/story-upgrade/<date-or-run-id>/`:

- the capsule name and commit/digest,
- the exact Kitsoki command and harness profile,
- trace/session IDs,
- model/provider used,
- validation commands and their output,
- whether the story continued without manual repair.

Keep any synthesis notes in `.context/` and promote only durable narrative
updates into docs.
