# Runtime: Dynamic Workflow Draft Packages

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [`dynamic-workflows.md`](dynamic-workflows.md)

## Why

A dynamic workflow is only useful if the generated artifact is a first-class Kitsoki story before it runs. Today MCP can write and validate story files (`docs/architecture/mcp-studio.md:152`), and stories already map workflow concepts onto rooms, transitions, guards, world, host calls, and deciders (`docs/stories/architecture.md:965`). What is missing is a constrained scratch package format for generated workflows, plus invariants that reject unsafe or incomplete drafts before any session starts.

## What changes

Add a dynamic workflow draft package under `.artifacts/dynamic-workflows/<id>/story/` with an ordinary `app.yaml`, optional `rooms/`, `prompts/`, `scripts/`, `flows/`, and `cassettes/`. The package carries provenance metadata and is loaded with the existing story loader. A draft can run only after a new validation gate succeeds:

```text
operator idea -> generated story package -> app.Load + dynamic invariants -> session.new/kitsoki web
```

The generated YAML remains canonical story YAML. Dynamic-specific data is metadata around the package, not a new execution model.

## Impact

- **Code seams:** story loading/validation, Studio MCP story tools, CLI command plumbing, artifact path policy.
- **Vocabulary:** draft package manifest, provenance fields, promotion eligibility state.
- **Stories affected:** none by default; dev-story may consume the draft writer in a later slice.
- **Backward compat:** existing stories and cassettes keep working; this is opt-in.
- **Docs on ship:** `docs/stories/state-machine.md`, `docs/stories/architecture.md`, `docs/architecture/mcp-studio.md`.

## Vocabulary Changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| manifest | `dynamic.workflow.json` | `{id, created_at, source_task, generator_surface, model_profile, draft_path, validation_report_hash, allowed_host_capabilities, trace_path, session_handle, promotion_eligibility, status}` | JSON sidecar outside story YAML, written beside the scratch package. |
| world key | `dynamic_workflow_id` | string | Optional seed for generated sessions so traces and artifacts share one id. |
| validation | `dynamic_draft` | story load plus dynamic invariants | Must pass before launch. |

## The Model

The validator should layer on top of `app.Load`:

1. Load the draft as a normal app.
2. Reject undeclared hosts, targets, world references, malformed views, and schema errors through existing invariants.
3. Apply dynamic-only invariants:
   - every generated host invocation has an `id` suitable for fixture/cassette export;
   - every LLM/agent call declares its schema or output contract;
   - mutating/external calls are visible in metadata and launch blocks them until
     the operator supplies an explicit `allowed_host_capabilities` allow-list;
   - no file path escapes the draft package unless a host contract allows it;
   - promotion metadata is absent until the operator promotes it.
4. Emit a draft validation report used by MCP/CLI/web/TUI alike.

## Decision Recording

Generation is interpretive and must be recorded. The validator itself is deterministic. Trace events should distinguish:

- `dynamic.workflow.generated`: prompt, model/profile, output package path, generated file list, hash summary.
- `dynamic.workflow.validated`: validator version, status, errors/warnings.
- `dynamic.workflow.launch_blocked`: validation failure or unsafe launch posture.

## Engine Seams & Invariants

This should reuse the existing story and MCP seams rather than adding a parallel engine:

- Studio MCP already exposes `story.write`, `story.validate`, and `story.test` as deterministic authoring tools (`docs/architecture/mcp-studio.md:149`).
- A session already records free-text routing as the one interpretive seam (`docs/architecture/mcp-studio.md:159`).
- Stories already model workflow nodes as rooms and edges as transitions (`docs/stories/architecture.md:963`).

## Backward Compatibility / Migration

No existing story migrates. Scratch packages are generated into `.artifacts/` and ignored unless explicitly launched. Promotion copies reviewed artifacts into normal story locations.

## Tasks

```text
## 1. Draft Package
- [ ] 1.1 Define `.artifacts/dynamic-workflows/<id>/` layout and manifest.
- [ ] 1.2 Add a draft writer API with dependency-injected clock/id/fs seams.
- [ ] 1.3 Add path confinement for generated files.

## 2. Validation
- [ ] 2.1 Layer dynamic invariants on top of `app.Load`.
- [ ] 2.2 Return structured errors consumable by MCP, CLI, TUI, and web.
- [ ] 2.3 Emit draft validation trace events.

## 3. Verification
- [ ] 3.1 Unit-test invalid hosts, missing call ids, path escapes, and missing schemas.
- [ ] 3.2 Add a no-LLM fixture that validates a tiny generated workflow package.
- [ ] 3.3 Update docs; trim/delete this proposal when shipped.
```

## Verification

Use only deterministic tests: package writer unit tests, `story.validate` on generated apps, and `story.test` with generated flow fixtures. No live LLM is needed for validator coverage.

## Decisions

1. Dynamic-specific metadata lives in `dynamic.workflow.json`, a JSON sidecar
   beside the scratch story package. Story YAML stays canonical so generated
   drafts continue to load through ordinary story tooling without frontmatter
   rules.
2. Unsafe host calls are reported by validation and blocked at launch. A draft
   may validate with warnings that enumerate mutating/external capabilities, but
   launch requires an explicit allowed-capabilities set recorded in the
   manifest.

## Non-goals

- Designing the generator prompt.
- Promoting drafts into base stories.
- Adding new host capabilities beyond validation and scratch file management.
