# Tracing: Dynamic Workflow Promotion and Cassette Export

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   [`dynamic-workflows.md`](dynamic-workflows.md)

## Why

A dynamic workflow run should not end as a disposable trace. If it works, Kitsoki should help turn it into a reusable story and deterministic starting artifacts. The current architecture already treats traces as the path from idea to predictable flow: prove the workflow, inspect repeated decisions, then convert prompt instructions into rooms, intents, transitions, and effects (`docs/architecture/concept.md:191`). Promotion makes that loop explicit for generated workflow drafts.

## What Changes

Add an export path from `.artifacts/dynamic-workflows/<id>/` into a reviewable story package:

- copy the validated story files into project-local `stories/<slug>/` by default;
- allow `internal/basestories/stories/<slug>/` only with explicit operator
  approval, clean validation, and the base-story gate satisfied;
- preserve provenance in docs, not hidden metadata;
- generate starter flow fixtures from the run's successful path;
- generate host cassette starter files for host calls that were recorded or stubbed;
- write an export report listing inferred artifacts, unresolved live decisions, and manual hardening tasks.

Promotion is an operator-reviewed action. It produces a normal working-tree diff
that can be tested, reviewed, and committed separately; export never commits
automatically.

## Impact

- **Trace/cassette code:** trace reader, fixture exporter, host cassette writer.
- **Story files:** promoted package layout and README generation.
- **MCP/CLI/TUI/web:** export command/action and report rendering.
- **Docs on ship:** `docs/stories/testing.md` or equivalent fixture docs, `docs/tracing/trace-format.md`, `docs/stories/dynamic-workflows.md`.

## Export Model

```text
scratch draft + run trace + validation report
  -> export planner
  -> story package diff
  -> starter flows/cassettes
  -> export report
  -> deterministic story.validate/story.test gate
```

The export planner should be conservative. When a trace contains live model behavior that cannot be replayed deterministically, it should write a placeholder fixture step with a clear TODO rather than fabricate a cassette.

## Cassette Starter Policy

| Source evidence | Export behavior |
|---|---|
| Flow fixture drove host handler | Copy fixture step with the same expected handler id. |
| Host cassette episode exists | Copy the episode and preserve matching keys. |
| Trace has host call input/output only | Create a starter cassette entry marked `review_required: true`. |
| Live LLM/agent decision only | Do not cassette silently; write TODO and link the trace turn. |
| Missing host call id | Block export until the draft is fixed. |

## Decision Recording

Promotion and export should emit:

- `dynamic.workflow.export_planned`: candidate files, inferred cassettes, blocked items.
- `dynamic.workflow.exported`: target path, written files, validation status.
- `dynamic.workflow.export_verified`: story validation and flow-test results.

## Backward Compatibility / Migration

No existing cassettes migrate. Exported starter artifacts use the current flow/cassette formats and should pass existing linters once reviewed.

## Tasks

```text
## 1. Export Planner
- [ ] 1.1 Read receipt + trace + validation report into a typed plan.
- [ ] 1.2 Classify calls as copyable, inferable, TODO, or blocked.
- [ ] 1.3 Unit-test classification with small synthetic traces.

## 2. Story Promotion
- [ ] 2.1 Copy reviewed draft files into `stories/<slug>/` by default.
- [ ] 2.2 Gate base-story promotion behind an explicit flag and clean validation.
- [ ] 2.3 Generate README/provenance docs for the promoted story.

## 3. Fixture/Cassette Export
- [ ] 3.1 Generate starter flow fixtures from the successful path.
- [ ] 3.2 Generate starter host cassettes only where evidence supports them.
- [ ] 3.3 Add a report for unresolved decisions and manual hardening.

## 4. Verification
- [ ] 4.1 Run `story.validate` on the promoted story.
- [ ] 4.2 Run generated no-LLM flows.
- [ ] 4.3 Update docs and trim/delete this proposal.
```

## Verification

Use synthetic traces and recorded no-LLM fixtures. Tests must not call a real LLM. The strongest acceptance case is a tiny dynamic workflow that promotes into `stories/<slug>/`, validates, and passes its generated starter flow using mocked host calls.

## Decisions

1. Export does not create commits automatically. It writes a normal working-tree
   diff plus an export report; git operations remain a separate operator or
   git-ops action.
2. Project-local story export is the default. Base-story promotion is explicit
   and gated, and import/doc rewrites happen only after the project-local story
   has clean validation and starter fixture evidence.

## Non-goals

- Claiming exported starter cassettes are complete regression coverage.
- Auto-mining every trace into a polished reusable workflow.
- Changing the existing cassette format in v1.
