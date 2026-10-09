# Project object graph viewer

`@kitsoki/object-graph` packages the reusable object graph viewer contract: the
`object-graph` story, the `graph` host interface, a Starlark-backed
presentation operation, and the Vue UI entry for browsing graph catalogs.

The kit is intentionally domain-neutral. Projects provide their own graph
catalog and type registry; the kit contributes the interface and viewer surface
that can load, lint, diff, project, and apply graph changes through the engine's
`host.graph` substrate.

## Provided Components

- Story: `object-graph`
- Host interface: `graph`
- Schemas: `graph/core-node/v0`, `graph/actor/v0`, `graph/feature/v0`,
  `graph/requirement/v0`
- UI: `graph`
- Script: `scripts/presentation.star`

## Conformance

No-LLM conformance flows live under `stories/object-graph/flows/*.yaml` and are
referenced from `kit.yaml`.
