# Epic: Kits — versioned, reusable story collections

**Status:** Draft v1. Nothing implemented yet. Slices sketched below;
child proposals not yet cut (cut them after the shared decisions are
reviewed).
**Kind:**   epic
**Slices:** 5 (0/5 shipped)

## Why

kitsoki already knows how to *extend* a story without forking it
(`imports:` + `overrides:`, [`docs/stories/imports.md`](../stories/imports.md)),
*compose* stories (dev-story folds eight sub-stories,
`stories/dev-story/app.yaml:1497-1877`; bugfix nests delivery-tail,
`stories/bugfix/app.yaml:656-686`), and *parameterize* them
(`world_in:`, world-default profiles, prompt overlays, `host_bindings:`).
What it cannot do is name, version, and distribute the result. Reuse
today stops at "a thin instance importing `@kitsoki/dev-story` by
name": there is no unit that bundles a set of stories **plus their
standardized systemic aspects** — interface contracts, onboarding,
data-management schemas, conformance fixtures — into one thing another
party can depend on.

The gap becomes structural the moment a *domain pack* is the product.
Think an ISO 9001 quality kit and an ISO 14001 environmental kit, both
extending an abstract management-system kit (document control, audits,
nonconformity/corrective action, management review); an organization
extends and composes those into an org kit; a site or team binds
parameters to get a running implementation. Every layer except the leaf
is itself reusable. Today each layer would be an ad-hoc folder of YAML
with:

- **No versioning.** `ImportDef.Version` is parsed and stored "for
  traceability only" (`internal/app/types.go:548-552`); `imports.md`
  explicitly defers registry/lockfile/semver enforcement.
- **No distribution.** `@kitsoki/<name>` resolves only to a local
  checkout or the compiled-in embedded library — "embed + local
  override, never a fetcher" (`internal/basestories/basestories.go:31-39`).
- **No conformance.** `host_interfaces` operation shapes "parse and
  propagate but aren't cross-checked against the registered handler"
  (`imports.md` §limitations), and nothing verifies that an extension
  still satisfies its base's contract.
- **No evolution path.** Standards rev — ISO revises clauses, an org
  revises conventions — so the relationships can't be static, and they
  can't be hand-migrated at every downstream layer either. Continuous
  improvement has to flow *through* the extension / composition /
  parameterization boundaries.

## What changes

A **kit** is a named, semver-versioned, distributable bundle of stories
with a standardized shape, declared by a `kit.yaml` manifest and
compiled onto the machinery that already exists (the import fold,
interface bindings, profiles, prompt overlays — see the table below).
Once every slice ships:

1. **Manifest** — `kit.yaml` (pinned `kit/v1` schema) declares
   identity, version, what the kit `extends:`/`composes:` (with version
   constraints), the parameter spec (its specialization surface), the
   stories/interfaces/schemas it provides, its onboarding entry, and
   its conformance suite.
2. **Versioned resolution + lockfile** — `@<namespace>/<kit>@<constraint>`
   resolves through tiers (embedded library → local path → git source),
   is pinned by a lockfile, and `version:` finally *means* something at
   load time.
3. **Conformance** — load-time contract checks (params, exits
   `requires:`, interface resolution) plus the base kit's no-LLM flow
   suite re-run against downstream extensions: "does your kit still
   satisfy the contract it extends."
4. **Rev-absorption lifecycle** — `kitsoki kit update` is semver-gated;
   because downstream specialization is declarative (never copy-and-
   edit), an upstream rev never produces file-level merge conflicts —
   breaks surface as a **migration worklist** of failed contract checks
   and conformance flows, with `compat:` deprecation hints
   (renames) applied mechanically and a continuous-improvement operator
   story walking the rest.
5. **Standardized systemic aspects** — onboarding (generalizing
   dev-story init + `internal/baseskills` install), data management
   (kit-declared artifact schemas via the shared schema registry), and
   the per-story README/flows/scenarios contract, all declared in the
   manifest so "fits the kit shape" is checkable, not conventional.

Sketch of the manifest (illustrative, not final):

```yaml
# kit.yaml            — schema: kit/v1 (pinned, registry-validated)
kit: iso-9001
namespace: constructorfabric
version: 1.2.0
extends:
  - "@constructorfabric/iso-ms@^2"     # abstract management-system base
provides:
  stories: [qms, audit, capa]          # each standalone-runnable
  schemas: [nonconformity/v1, audit-finding/v1]
  onboarding: qms.onboard              # entry intent for install/setup
parameters:                            # the whole specialization surface
  registrar: {type: string, required: true}
  doc_repo:  {type: string, default: "docs/qms"}
conformance:
  flows: [flows/ms-contract/*.yaml]    # base contract, re-run downstream
compat:
  renamed: {exits: {closed: resolved}} # hints for absorbers
```

**Vocabulary** (a shared decision, see below): a kit **extends** a kit,
**composes** kits, and a leaf binding of parameters/hosts is an
**implementation**. The word *instance* is deliberately not used — it
already means a running session (`process-design.md` §"Story vs
Session") and a per-run artifact workspace
([`artifact-driven-stories.md`](artifact-driven-stories.md)).

## Where this fits (existing state)

The composition half of kits is **shipped**; kits names, versions, and
certifies it rather than re-inventing it.

| Existing piece | Role here | Status |
|---|---|---|
| Import fold (`internal/app/imports.go:388-749`; [`imports.md`](../stories/imports.md)) | extension + composition substrate: aliased compound state, private world, `world_in`/`exits` projection, `overrides:` | shipped |
| `exits:` `requires:` + `exports.intents` (`stories/bugfix/app.yaml:611-639`) | the statically-checked story-side contract a manifest aggregates | shipped |
| `host_interfaces:` + cascading `host_bindings:` (`internal/app/imports_interfaces.go:42-102`; `stories/bugfix/app.yaml:141-196`) | capability contracts with swappable glue providers (cf. [hosts.md → host.gh.ticket](../architecture/hosts.md#hostghticket--github-issues-backed-tracker)) | shipped (shapes unchecked) |
| `@kitsoki/<name>` resolution + embedded library (`internal/app/imports.go:273-333`; `internal/basestories/materialize.go:34-145`) | name-only resolution + content-addressed cache the versioned resolver generalizes | shipped |
| Thin instances + doc profile (`.kitsoki/stories/kitsoki-dev/app.yaml:121-192`; `stories/bench-bugfix/app.yaml:1-13`; `stories/dev-story/README.md:90-149`) | the leaf-implementation pattern; README states the layering rule verbatim: community patterns → org profile → local world defaults | shipped |
| Config-synthesized root (`internal/app/synthesis.go`) | manifest→instance compilation precedent: fail-fast allow-lists + verified `Synthesize ≡ Load(emit)` round-trip; hard-codes dev-story as the only base (`synthesis.go:14`) | shipped |
| Prompt overlays (`docs/stories/prompts.md`) | block-level prompt extension across layers (`{% extends %}` + `spec_` blocks) | shipped |
| Onboarding installer (`internal/baseskills/install.go:41-79`; `internal/projectprofile/schema.json`; `docs/project-onboarding.md`) | the standardized-onboarding aspect + a versioned specialization schema (`project-profile/v1`) to generalize | shipped |
| [`artifact-format.md`](artifact-format.md) | the pinned-schema registry `kit/v1` and kit data schemas validate against | draft |
| [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md) | sibling on versioned schemas, `composed_of` DAGs, coverage lint — share the schema-pin convention and lint machinery, don't fork them | draft |
| [`artifact-driven-stories.md`](artifact-driven-stories.md) | terminology neighbor ("artifact job" = durable run plus shareable artifacts; "instance" = per-run artifact workspace); reuse its get-or-create identity + disposition patterns for registry state | draft |

Distinct from: [`roadmap-portfolio-work.md`](roadmap-portfolio-work.md)
(classifies *work items*, not story bundles) and
[`session-launcher.md`](session-launcher.md) (scopes Claude Code
sessions).

## Prior art: cypilot kits

The concept and the name come from cypilot / constructor-studio. The
shipped reference is the `cpt` kit toolchain (source
`github:constructorfabric/studio-kit-sdlc`, vendored locally under
`~/code/gears-rust/.cypilot/` for reviewers): a kit is "a file package
that provides domain-specific artifact and codebase definitions" —
templates, rules, checklists, examples, `constraints.toml` (structural
conformance incl. cross-artifact coverage), `SKILL.md`/`AGENTS.md`
onboarding, a `manifest.toml` install manifest, semver registration in
`core.toml`, and a version-gated `cpt kit update`.

What kits takes from it: the manifest + semver + git-source model, the
version-gated update, conformance validation as a first-class kit
member, and per-version "what's new" surfacing. What kits deliberately
does **differently**:

- **Declarative layers instead of copy-and-diff.** cypilot installs
  kit files into the project where users edit them in place, then
  preserves those edits across updates via per-file interactive diffs.
  kitsoki's specialization is structured (`overrides:`, params, prompt
  block-extends, `host_bindings:`) — downstream never edits upstream
  files, so an upstream rev cannot collide at the file level; breaks
  surface as contract/conformance failures with exact locations.
- **Kit-to-kit inheritance.** cypilot's manifest has no
  `extends:`/parent field (its "extension protocol" is deferred). The
  extension/composition DAG — ISO 9001 over a base management-system
  kit, org kits over both — is the generalization this epic adds.

## Impact

- **Spans:** runtime (manifest, resolution/lockfile, conformance),
  story (lifecycle story, reference kits), docs; tracing touched
  lightly (kit name/version as import provenance on trace events —
  today the alias chain is only recoverable from state paths).
- **Net surface:** a `kit.yaml` loader beside `imports.go`/`synthesis.go`,
  a resolver/lockfile generalizing `resolveImportSource` +
  `basestories.Materialize`, `kitsoki kit` CLI verbs
  (`add`/`update`/`verify`), one operator story, and out-of-tree
  reference kits.
- **Docs on ship:** `docs/stories/kits.md` (authoring + lifecycle),
  updates to `docs/stories/imports.md` (version enforcement) and
  `docs/project-onboarding.md` (kit-declared onboarding).

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | kit-manifest | runtime | `kit.yaml` (`kit/v1` pinned schema): identity, version, `extends:`/`composes:`, parameter spec, provided stories/schemas/onboarding — compiled onto the existing import fold, SynthesizeRoot-style (allow-lists + verified round-trip) | — | not cut | — |
| 2 | kit-resolution | runtime | versioned `@ns/kit@constraint` resolution tiers (embed → path → git) + lockfile; make `ImportDef.Version` enforced | 1 | not cut | — |
| 3 | kit-conformance | runtime | handler schema registration so `host_interfaces` op shapes are checked; base kit's no-LLM flow suite re-run against downstream kits; `kitsoki kit verify` | 1 | not cut | — |
| 4 | kit-lifecycle | story + runtime | `kitsoki kit update` (semver-gated), migration worklist from failed checks/flows, `compat:` rename hints, and the continuous-improvement operator story that walks an upstream rev to green | 2, 3 | not cut | — |
| 5 | reference-kits | story | the management-system proof: abstract `iso-ms` base + `iso-9001` + `iso-14001` + a demo org kit + one leaf implementation, exercising extend/compose/parameterize and one staged upstream rev end-to-end | 4 | not cut | — |

## Sequencing

```
#1 (manifest) ──▶ #2 (resolution) ──▶ #4 (lifecycle) ──▶ #5 (reference kits)
       └────────▶ #3 (conformance) ──▶─┘
```

#2 and #3 are parallel once the manifest exists. #5 is last on purpose:
it is the acceptance test for the whole epic (a staged upstream rev of
`iso-ms` must be absorbable by `iso-9001`/`iso-14001` and the org kit
through re-extension alone).

## Shared decisions

1. **Specialization stays declarative — never copy-and-edit.** Every
   downstream delta lives in a structured layer (imports + `overrides:`,
   params/world defaults, prompt block-extends, `host_bindings:`). This
   is the load-bearing difference from cypilot and the reason
   rev-absorption is tractable: upstream evolution arrives by re-load +
   re-validate, not by merging edited copies.
2. **A kit is a bundle around existing machinery, not a new engine.**
   `extends:`/`composes:`/`parameters:` compile to `imports:` /
   `overrides:` / `world_in:` / `host_bindings:` exactly the way
   `SynthesizeRoot` compiles `.kitsoki.yaml` today, including the
   verified manifest ≡ emitted-YAML round-trip. If a slice needs new
   fold semantics, that's a red flag to re-review.
3. **Vocabulary:** kit / extension / composition / **implementation**
   (leaf). Never "instance."
4. **One schema registry.** `kit/v1` and kit-declared data schemas pin
   into the same registry [`artifact-format.md`](artifact-format.md)
   builds and [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md) uses —
   coordinate the convention there rather than inventing a parallel one.
5. **Conformance is deterministic.** Load-time contract checks + the
   kit's no-LLM flow suite. Never a live LLM in the conformance path
   (repo testing rule).
6. **No self-migration.** Absorbing an upstream rev never rewrites
   downstream YAML in place beyond mechanical `compat:` renames the
   operator approves; the worklist + the lifecycle story are the path.
   The relationships evolve; the files stay owned by their layer.

## Cross-cutting open questions

1. **Distribution tiers for v1** — embed + local path + git tag, or is
   git deferrable? A hosted registry is out (non-goal), but whether v1
   must fetch from git (cypilot does) or can require vendored checkouts
   affects slice 2's size. *Lean: embed + path + git-tag fetch with the
   lockfile pinning a commit; no index service.*
2. **Is a kit-interface first-class?** "Fits an interface" could mean
   `implements: [management-system/v2]` as a separate contract object,
   or simply "extends the base kit, whose exports/exits/params/flows
   *are* the contract." *Lean: v1 the base kit is the contract; split
   interface-only kits later if substitutability across unrelated bases
   demands it.*
3. **Where do the reference ISO kits live?** *Lean: a separate
   constructorfabric repo as the flagship third-party-distribution
   proof; in-tree only a minimal synthetic test kit (aligned with the
   gears decoupling: kitsoki carries no domain packs).*
4. **How much may onboarding do?** Kit-declared onboarding that runs
   installers (`baseskills.Install`-style toolkit + `.mcp.json`) is
   powerful and a supply-chain surface for third-party kits. *Lean:
   v1 onboarding = a declared entry story only; installer actions gated
   behind the same review the toolkit installer gets today.*
5. **Does kit resolution subsume the blessed root?**
   `SynthesizeRoot` hard-codes dev-story as the only importable base
   (`internal/app/synthesis.go:14`) and widening it is an acknowledged
   open question there. *Lean: yes — "any installed kit can be a root"
   replaces the allow-list, and dev-story becomes just the default kit.*
6. **Params vs profiles.** The parameter spec overlaps the world-var
   "doc profile" pattern. Do profiles become *named parameter presets*
   in the manifest, or stay free-form world defaults? *Lean: presets —
   it makes the specialization surface enumerable and checkable.*

## Non-goals

- **No hosted registry / package server.** Distribution is embed,
  path, and git; an index service is someone else's product.
- **No new composition engine.** The import fold, interface resolution,
  and prompt overlay semantics are not redefined here.
- **No automated rewriting of downstream kits.** Rev-absorption is
  guided re-extension, not codemods over YAML you don't own.
- **Not artifact jobs.** Per-run output workspaces and durable run artifacts stay in
  [`artifact-driven-stories.md`](artifact-driven-stories.md); kits
  version *definitions*, not run outputs.
- **No runtime marketplace/discovery UI.** A `kitsoki kit list` CLI is
  enough until the concept proves out.
