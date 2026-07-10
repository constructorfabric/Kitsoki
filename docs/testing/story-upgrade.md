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
dropped.

Add a capsule whenever an upgrade bug is fixed. Prefer the smallest project
state that reproduces the old shape: config/profile, stale generated story,
ticket fixture, PR fixture, or previously solved bug.

## Live LLM Smoke

Live story upgrade smoke runs are manual and must be explicitly requested by an
operator. Do not wire them into normal tests.

Recommended scenario set:

- Bug fixing: open an old-project capsule with a known failing test and drive
  the bugfix path through the current dev-story.
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
