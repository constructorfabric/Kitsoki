# Decomposition Graph - The Two-Graph Model

This document describes the shape and semantics of Kitsoki work
decomposition, which powers goal tracking, proposal planning, and iterative
delivery. The pinned contract is
[`docs/schemas/decomposition-graph.schema.json`](../schemas/decomposition-graph.schema.json),
with deterministic cross-node checks in
[`tools/validate-decomposition-graph.py`](../../tools/validate-decomposition-graph.py).

## Overview

Decomposition in Kitsoki is a two-graph model:

- an **acyclic `depends_on` DAG** that describes work ordering
- a **cyclic process graph** that describes how the runtime drives work through
  states such as evaluate, dispatch, review, integrate, park, and report

A third edge type, `goal_ref`, links work nodes back to declared goal criteria
such as `G1` or `G6`. It is traceability, not execution order.

This split gives us deterministic sequencing, deterministic validation,
traceability from work back to goals, and one reusable node contract for
goal-seeker, deliver, and work-decomposition.

## Shared Node Shape

A decomposition node is one unit of work. Different surfaces call it a change,
brief, or task, but the shared contract is the same:

```yaml
id: WM.6
wall: WM
goal_ref: [G5, G6]
title: Decomposition graph as a shared artifact
scope: ["docs/schemas/decomposition-graph.schema.json"]
depends_on: ["WM.0"]
acceptance:
  - "The schema validates the goal graph and deliver manifest fixture."
gate:
  class: G-LINT
  red_now: "No shared schema exists."
  green_when: "The validator accepts good fixtures and rejects a cycle fixture."
  cmd: "python3 tools/validate-decomposition-graph.py ..."
agent_brief: >
  A self-contained implementation brief for the worker.
```

Required shared fields:

- `id`: stable node identifier
- `wall`: roadmap or workstream bucket
- `goal_ref`: one or more goal criteria, for example `G1`
- `scope`: file or directory globs owned by the node
- `depends_on`: node IDs that must complete first
- `acceptance`: observable done conditions
- `gate`: deterministic RED-first proof with `class`, `red_now`,
  `green_when`, and either `cmd` or `replay_cmd`
- `agent_brief`: the self-contained implementation brief

Tool-specific fields are allowed. Goal-seeker nodes usually include
`pipeline`, `critical`, and `demo_video`. Deliver/work-decomposition manifests
may include `brief`, `gate_command`, `deps`, `kind`, `test_plan`, and `risk`.

## Surface Shapes

Goal-seeker stores nodes under top-level `changes:`:

```yaml
changes:
  - id: W1.1
    wall: W1
    goal_ref: [G1]
    scope: ["docs/getting-started.md"]
    depends_on: []
    acceptance: ["The getting-started path is runnable from a fresh clone."]
    gate:
      class: G-LINT
      red_now: "The doc references missing setup steps."
      green_when: "The setup lint exits 0."
      cmd: "python3 tools/lint-setup-doc.py"
    agent_brief: "Update the getting-started doc and its deterministic lint."
```

Deliver/work-decomposition manifests store compatible nodes under `briefs:` and
can carry deliver aliases for the same graph data:

```yaml
briefs:
  - id: health-handler
    wall: WD
    goal_ref: [G1]
    brief: "Add GET /health."
    gate_command: "go test ./internal/handler/..."
    deps: [health-model]
    depends_on: [health-model]
    scope: ["internal/handler/**"]
    acceptance: ["The handler returns a JSON HealthStatus payload."]
    gate:
      class: G-GOTEST
      red_now: "No health handler exists."
      green_when: "The handler tests pass."
      cmd: "go test ./internal/handler/..."
    agent_brief: "Wire the health handler using the model brief."
```

For deliver compatibility fixtures, `deps` must match `depends_on` and
`gate_command` must match `gate.cmd`; the validator checks this so the alias
fields cannot drift.

## Graph 1: Dependency DAG

The dependency DAG defines hard sequencing: if B depends on A, A must be done
before B can be ready. The DAG is:

- **acyclic**, rejected when a cycle exists
- **closed**, rejected when a dependency names an unknown ID
- **scope-aware**, because each node declares its write boundary

Goal-seeker uses this graph to compute the ready set. Deliver uses it to hand
briefs to fleet in dependency order. Work-decomposition uses it to prove an
agent-generated manifest is buildable.

## Graph 2: Cyclic Process Graph

The process graph is the runtime state machine around a node. It can cycle
because real work can be retried, parked, reopened, or re-reviewed:

```text
blocked -> ready -> assigned -> in_flight -> reviewing -> verified -> integrated
                                              \                         /
                                               -------- parked <--------
```

State transitions belong to the story/runtime ledger, not the decomposition
schema. The schema tells the runtime what work exists and how it depends; the
process graph records what happened while driving it.

Do not collapse these graphs. A cyclic process graph is healthy; a cyclic
dependency DAG is invalid.

## Goal-Ref Traceability

`goal_ref` edges connect work nodes to goal criteria from the goal's `GOAL.md`.
Reports and decks can group progress by these edges without changing execution
order:

```text
G1 <- [0.2, W1.0, W1.1]
G6 <- [WM.0, WM.3, WM.6]
```

The graph validator checks shape. Goal-specific tooling may additionally check
that each `goal_ref` resolves to a declared criterion.

## Validation

Validation has two layers:

1. JSON Schema validates the pinned shape in
   `docs/schemas/decomposition-graph.schema.json`.
2. `tools/validate-decomposition-graph.py` checks deterministic graph
   invariants:
   - unique IDs
   - no dangling dependencies
   - acyclic dependencies
   - known `gate.class`
   - RED-first gate text and `cmd`/`replay_cmd`
   - repo-bounded scope globs
   - deliver alias consistency for `deps`/`gate_command`

The WM.6 gate validates the generalized-usage goal graph, validates a deliver
brief fixture, and rejects a deliberate cycle fixture:

```sh
python3 tools/validate-decomposition-graph.py \
  docs/goals/generalized-usage/decomposition.yaml \
  stories/deliver/testdata/two_briefs.yaml

! python3 tools/validate-decomposition-graph.py \
  stories/deliver/testdata/two_briefs_cycle.yaml
```

## Explicit Deferrals

This schema names the shared shape only. It does not build a new graph runtime.
The following are intentionally deferred to named follow-on proposals:

- **Impact analysis and dirty edges**: edge types such as `supersedes` or
  `dirtied_by`, plus traversal when a completed dependency regresses.
- **Artifact versioning and provenance**: mapping generated code, docs, demos,
  and reports back to the node that produced them.
- **In-flight graph-update transactions**: versioned plan deltas, provenance,
  no-lost-work guarantees, and adversarial review for changes to the graph
  itself.
- **Runtime unification**: merging goal-seeker, deliver, story-forge, and
  dynamic-workflow driving into one shared engine.

Those systems should reuse this node contract; they are not part of this
document's scope.

## See Also

- [`docs/schemas/decomposition-graph.schema.json`](../schemas/decomposition-graph.schema.json)
- [`tools/validate-decomposition-graph.py`](../../tools/validate-decomposition-graph.py)
- [`docs/goals/generalized-usage/decomposition.yaml`](../goals/generalized-usage/decomposition.yaml)
- [`stories/deliver/schemas/decomposition.json`](../../stories/deliver/schemas/decomposition.json)
- [`docs/stories/deliver.md`](../stories/deliver.md)
