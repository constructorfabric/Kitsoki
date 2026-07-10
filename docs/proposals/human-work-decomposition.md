# Story: Human work in decomposition

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   ../human-action-workflows.md

## Why

Work decomposition currently thinks primarily in agent briefs: produce a
manifest, validate it, then drive implementation through agent-capable stories.
That is too narrow for generalized project management. Real plans contain tasks
that should be explicitly assigned to people: approve a spec, review copy,
confirm requirements, make a product decision, prioritize an epic, refine a
roadmap, run a manual check, coordinate a launch, or unblock a third-party
dependency.

Once `host.human.*` exists, decomposition should emit human briefs beside agent
briefs and coordinate both through the same dependency graph.

## What changes

Extend the decomposition/deliver manifest model with the roadmap taxonomy and an
executor dimension:

```yaml
briefs:
  - id: design-signoff
    title: Approve onboarding copy
    executor: human
    role: product_owner
    kind: decision
    depends_on: [copy-draft]
    acceptance:
      - Product owner approves or requests concrete changes.
    human_task:
      provider: github
      mode: comment
      target: "{{ world.ticket_id }}"
      done_label: kitsoki:done
```

Agent briefs continue through the implementation/fleet path. Human briefs call
`host.human.task` or `host.human.decide`, then the board waits until the human
action completes before unblocking dependents.

Each human brief also exposes the task-scoped assistance room. The assignee can
ask for help from the assigned ticket/comment chain without changing the brief's
status. This is important for roadmap decisions and review tasks: asking
Kitsoki to summarize tradeoffs should not count as approval, rejection, or task
completion unless the completion predicate is explicitly satisfied.

At larger planning levels, the same schema can express roadmap work:

```yaml
items:
  - id: prioritize-observability
    level: decision
    executor: human
    role: product_owner
    horizon: quarter
    parent: initiative-observability
    human_task:
      provider: github
      mode: issue
      title: "Prioritize observability initiative"
  - id: trace-waterfall
    level: change
    executor: agent
    parent: epic-trace-introspection
    depends_on: [prioritize-observability]
```

## Impact

- **Story files:** `stories/deliver/` and/or the surviving
  work-decomposition path; dev-story launch docs
- **Runtime dependency:** requires the human action runtime, GitHub backend,
  tracing slices, roadmap taxonomy slice, and task-scoped assistance room slice
- **Vocabulary:** `level: goal|initiative|wall|epic|change|task|decision|gate`,
  `executor: agent|human|script`, and `human_task` config
- **Docs on ship:** work-decomposition/deliver docs and dev-story README

## Story design

The board should classify ready items by executor:

| Executor | Action |
|---|---|
| `agent` | existing agent/fleet/implementation dispatch |
| `human` | `host.human.task` / `host.human.decide`, await completion |
| `script` | deterministic `host.run` / `host.starlark.run` when supported |

Human briefs are not "failed agent briefs" or generic `needs_human` fallbacks.
They are planned dependencies that can be assigned, linked, waited on, and
completed in the trace.

## Manifest changes

Add these fields to the decomposition schema:

| Field | Type | Notes |
|---|---|---|
| `level` | enum `goal|initiative|wall|epic|change|task|decision|gate` | Defaults to `task` for legacy brief manifests |
| `executor` | enum `agent|human|script` | Defaults to `agent` for backward compatibility |
| `horizon` | enum/string | Optional roadmap horizon such as now/next/later, month, quarter |
| `parent` | string | Optional rollup id for initiative/epic/wall hierarchy |
| `role` | string | Required for `executor: human` |
| `human_task` | object | Provider-specific task config |
| `task_room` | object | Optional assistance-room config or bound result |
| `completion_schema` | object/ref | Optional structured payload expected from human completion |

Deterministic lint should check:

- every human brief has a role
- every role resolves through app `humans:` or the provider config
- every `human_task.provider` is available
- dependencies can mix executors and levels without cycles
- rollups have consistent parent/child links
- roadmap decisions cite the initiative, epic, wall, or change they affect
- task-help turns do not mark a human item done unless the completion predicate
  is also satisfied
- no human brief can be silently routed through an agent path

## Tasks

```
## 1. Schema and lint
- [ ] 1.1 Add `executor`, `role`, `human_task`, and optional completion schema to the decomposition manifest.
- [ ] 1.2 Update deterministic decomposition lint for mixed executor DAGs.
- [ ] 1.3 Keep existing manifests valid by defaulting missing `executor` to `agent`.

## 2. Story adoption
- [ ] 2.1 Add board route for ready human briefs.
- [ ] 2.2 Dispatch human briefs through `host.human.task` / `host.human.decide`.
- [ ] 2.3 Surface the task-scoped assistance room for each pending human brief.
- [ ] 2.4 Resume dependents when the human action completes.

## 3. Verification and docs
- [ ] 3.1 No-LLM flow: agent brief produces artifact, human approval waits, final dependent unblocks.
- [ ] 3.2 No-LLM flow: human rejects/requests changes and the board records blocked/revise status.
- [ ] 3.3 No-LLM flow: assignee asks for help in the ticket, answer posts back, item remains pending.
- [ ] 3.4 Document mixed human/agent decomposition; migrate shipped details and trim/delete this proposal.
```

## Verification

Flow tests should use cassettes for the human action calls. The core proof is a
mixed DAG where the next agent task cannot run until the human approval has
completed.

## Open questions

1. **Where to adopt first:** `stories/deliver` vs. a richer
   work-decomposition story. *Lean: adopt in the shipped/simple path first so
   the contract gets exercised without reopening the full board design.*
2. **Executor taxonomy:** `agent|human|script` now, or only `agent|human`.
   *Lean: include `script` because decomposition already distinguishes
   deterministic checks from interpretive work.*
3. **Planning level scope:** include roadmap rollups in the same manifest or
   keep a separate roadmap manifest that compiles down to decomposition briefs.
   *Lean: support the fields in the schema but allow stories to project a
   roadmap manifest into brief-sized execution items.*

## Non-goals

- Rebuilding the full work-decomposition board before the human-action runtime
  exists.
- Treating every `needs_human` fallback as a planned human brief. Fallbacks can
  be migrated later when they represent a real durable dependency.
