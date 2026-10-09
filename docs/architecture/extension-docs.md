# Extension Docs

Extension docs are the source-owned documentation layer for reusable Kitsoki
units: kits, standalone stories, host interfaces, scripts, schemas, UI entries,
and agent surfaces. They complement `kit.yaml`: the kit manifest remains the
distribution and conformance contract, while extension docs provide the
presentation index that a product site or local project viewer can consume.

The first shipped slice is deterministic and file-only:

```sh
kitsoki docs index --root . \
  --json-out .artifacts/docs/extensions-index.json \
  --markdown-out .artifacts/docs/extensions-index.md
```

The command discovers in-tree kits under `kits/*/kit.yaml`, standalone stories
under `stories/*/app.yaml`, and optional `docs.yaml` sidecars using schema
`kitsoki.docs/v1`. It loads real `kit.yaml` and `app.yaml` files through the
same validators used by runtime code, then emits:

- package nodes for kits;
- story nodes for provided and standalone stories;
- component docs declared in `docs.yaml`;
- generated inventory for world keys, intents, states, agents, providers,
  toolboxes, host interfaces, imports, exports, exits, prompts, schemas,
  scripts, flows, UI entries, and conformance declarations.

No LLM is used. Flow evidence is indexed from declared files and manifests; the
index command does not run model-backed sessions or infer undocumented claims.

## `docs.yaml`

A docs sidecar belongs beside the source it documents. Rich presentation data
should stay out of runtime `app.yaml` unless it is needed by the engine.

```yaml
schema: kitsoki.docs/v1
owner:
  kind: kit
  id: "@kitsoki/object-graph"
title: Project object graph viewer
summary_path: README.md
audiences: [operator, story-author, integrator]
docs:
  - id: overview
    title: Overview
    path: README.md
    kind: overview
    order: 10
components:
  - kind: story
    id: object-graph
    docs:
      - id: contract
        title: Story contract
        generated_from: stories/object-graph/app.yaml
        kind: reference
```

Each docs entry must set `id` plus either `path` or `generated_from`. Paths must
be package-relative and cannot escape the owner directory. Raw prompt files are
not published by default: entries under `prompts/` default to `publish: summary`;
other docs default to `publish: true`. Explicit values are `true`, `false`,
`summary`, and `full`.

## Publication Policy

Publish the library contract by default: kit metadata, story overview,
generated app contract, exits, exports, host interfaces, schemas, public UI
entries, Starlark sidecar inputs/outputs, and no-LLM conformance summaries.

Sanitize or summarize agent/provider/plugin material. Agent names, effect class,
toolbox, provider class, and call sites are useful for trust and operation, but
environment values and credentials are not documentation.

Do not publish cassettes, transcripts, `.artifacts`, `.context`, proposal notes,
local environment values, or raw prompt text unless a docs sidecar explicitly
opts in.

## Object Graph Proof Package

`kits/object-graph/docs.yaml` is the proof sidecar. It indexes the kit overview,
the `object-graph` story contract, the `graph` host interface, the
`presentation.star` sidecar, and the `graph` UI entry. The package can be
verified with:

```sh
kitsoki docs index --root . --json-out .artifacts/docs/extensions-index.json
kitsoki kit verify kits/object-graph
```

## Future Slices

The index format is intended to feed a later `/library/` site surface and local
composite docs. Those slices should add docs template inheritance, block
specialization provenance, generated reference pages, link rewriting, leak
checks, and local upstream-plus-overlay rendering without changing the runtime
story format.
