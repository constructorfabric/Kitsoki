# Graph grouping taxonomy — areas and initiatives

How the project object graph groups features into product/functional areas
and bundles cross-cutting work into initiatives. Landed 2026-07-05 on the
`proposal/project-object-graph` line; the remaining projections are proposed
in [`../proposals/graph-grouping-projections.md`](../proposals/graph-grouping-projections.md).

Companions: [`../proposals/project-object-graph.md`](../proposals/project-object-graph.md)
(the epic), `docs/proposals/project-object-graph/seed-objects.yaml` (the seed
catalog carrying the type registry and data), `internal/graph/lint.go` (the
invariants).

## The two axes

"Grouping" conflates two orthogonal things, and the taxonomy models them as
separate types rather than forcing one to serve both (the Jira failure mode:
epics as both grouping and work):

| Axis | Nature | Type | Lifecycle |
|---|---|---|---|
| **Structural** | stable, near-hierarchical, ownable | `area` | changes rarely; survives every project |
| **Work** | dynamic, cross-cutting, time-bounded | `initiative` | born, executed, closed |

An *area* is where features live and what a team/lead owns. An *initiative*
(a project) is not a place features live; it is a bundle of changes that
**crosses** areas. Every persona view — portfolio rollup, project status,
"what's touching my area", QA coverage — is a computed projection over these
two anchors plus the existing typed edges; nothing is stored per-view,
consistent with the epic's "roadmap is computed, never authored" principle.

This preserves the epic's anti-containment stance (flat typed DAG, no
single-parent trees): membership is a many-edge, hierarchy is a lint-checked
acyclic edge, and nothing is a container.

## Types and edges

- **`area`** (derives from core-node): `part_of → area` (cardinality one,
  `acyclic: true` — cycles hard-fail the generic cycle lint) and
  `owned_by → actor` (many). `product` is deliberately not a type: with one
  product, the root area (`area-kitsoki`) is the product; a second product
  is just another root area.
- **`initiative`** (derives from core-node): `targets → area` (many; a
  derived-but-cached edge kept honest by lint), `includes → change` (many),
  `proposals → proposal` (many), `owned_by → actor` (one). Scalars
  (`horizon`, `priority`) live in per-type fields. This adopts the
  `initiative` level from `docs/proposals/roadmap-portfolio-work.md`;
  `goal`/`wall`/`epic` remain unmodeled until a consumer needs them.
- **`feature.in_area → area`** (many). The **first entry is the primary
  area** for tree-shaped rollups — a convention, not a distinct edge. A
  feature that genuinely spans areas lists all of them (the spanning feature
  is also how an initiative's derived target set stays honest).
- **`actor.owns_area → area`** (many). Group-level ownership. The legacy
  per-feature `actor.owns → feature` remains valid; it was not retargeted
  because existing data references features and the registry's target-type
  check would break.

## Invariants (`internal/graph/lint.go`)

- **orphan-feature** (hard error): a `public` feature with zero `in_area`
  targets fails lint — including via `graph apply`, so a changeset cannot
  introduce an unhomed public feature. Scoped to catalogs whose registry
  declares `feature.in_area`, so taxonomy-free catalogs are exempt.
  Internal features are never flagged (migration stays incremental).
- **area-cycle** (hard error): `part_of` is acyclic-marked; the generic
  cycle lint covers it.
- **initiative-scope** (opt-in warning via `LintOptions.InitiativeScope`):
  `initiative.targets` must equal the derived set
  `includes → change.implements → feature.in_area`. The seed-corpus test
  runs with it enabled.

## Decisions (settled 2026-07-05)

1. **Primary area = first `in_area` entry** — convention over a dedicated
   `primary_area` edge; zero extra machinery.
2. **`component` deferred** — the implementation side of the catalog is too
   thin to justify it yet; the type name is reserved.
3. **Six-area seed cut** under root `area-kitsoki`: `agent-execution`,
   `observability`, `media-and-demos`, `web-experience`, `dev-workflow`,
   `platform-install`.
4. **No team type** — a team, when needed, is an actor
   (`actor_kind: team`) with a members edge, not a new node type.

## Surfaces

The `kitsoki web` object-graph viewer's full-graph overlay has a data-driven
**Group by: area** mode (`tools/runstatus/src/components/objectgraph/`,
`buildAreaGroupResolver`): features bucket under their primary area, areas
under their `part_of` parent, other node types walk one obvious hop to a
feature, else land in "unassigned". The toggle only appears when the catalog
contains area nodes; the default type-layer grouping is unchanged.

The first real initiative node is `initiative-project-object-graph` — the
epic's own work, dogfooding the model it introduced.
