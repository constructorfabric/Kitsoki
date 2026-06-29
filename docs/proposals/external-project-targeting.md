# Epic: Target an external project with dev-story

**Status:** v3. Slices **#1 (profile substrate)**, **#3 (prd → design chain)**,
and **#4 (gears-rust instance)** have **shipped** and are migrated to narrative
docs (the [dev-story README](../../stories/dev-story/README.md) and the
[gears-rust README](https://github.com/constructorfabric/gears-rust/blob/docs/kitsoki-integration/stories/gears-rust/README.md)); their proposal files
are deleted. Only **#2 (gh ticket adapter)** remains **deferred** — the PRD →
Design walk does not pick up a ticket, so it was not needed; GitHub integration
comes later. This epic stays open to track #2.
**Kind:**   epic
**Slices:** 4 (3/4 shipped; #2 deferred)

> **Rename note (shipped with #3):** dev-story's in-story "proposal-authoring"
> pipeline was renamed to the **design pipeline** — rooms `design` /
> `design_search` / `design_materialize` / `design_refine` / `design_draft` /
> `design_done`, world keys `design_*`, personas `design_*`, scripts
> `design_workspace.py` / `publish_design.py`. The repo's `docs/proposals/`
> output directory, the `docs/proposals/templates/`, and the
> `proposal-authoring` skill/discipline keep their names (the pipeline still
> emits proposal-shaped design docs into `docs/proposals/`). The
> doc-shape retargeting (gears-sdlc templates, `cpt-` IDs) remains slice #1/#4.

> **Simplification note (shipped with #1/#4):** per-gear placement is expressed
> simply as `publish_durable_path: gears/<gear>/docs` (a plain relative dir) plus
> a `doc_filename` override that publishes a FIXED filename — **not** the
> `doc_placement` enum + `doc_scope` key the child proposals sketched. The
> shipped world keys are documented in the
> [dev-story README doc-profile section](../../stories/dev-story/README.md#doc-profile--targeting-an-external-project).

## Why

`dev-story` is already provider-neutral — it declares abstract
`host_interfaces` (`ticket` / `vcs` / `ci` / `workspace` / `transport`) and
binds none of them (`stories/dev-story/app.yaml:174`). The only worked
*instance* is `.kitsoki/stories/kitsoki-dev/app.yaml`, which targets **this repo**:
local-file tickets (`issues/bugs/*.md`), kitsoki's own proposal templates,
and a flat `docs/proposals/` publish home. Pointing the same hub at a
**foreign repo** — a user asked for `constructorfabric/gears-rust` — surfaces
everything that is still hardcoded to kitsoki: the ticket source, the
PRD/proposal *document shape*, where docs land, and the commit discipline.

The goal is a **generic** seam: a target project is described by a small
**profile** (ticket adapter + doc-template set + placement rule + commit/CI
discipline), and any instance app fills it. gears-rust is the first worked
profile, not a special case — its conventions (GitHub-issue tickets, the
`gears-sdlc` PRD→DESIGN→ADR templates with `cpt-` IDs, per-gear placement,
DCO + conventional commits, `make check`) are exactly the axes the profile
must expose.

Two things become possible along the way, both requested: **fold `prd` into
`dev-story`** (today it is a standalone story, `stories/prd/`) and **chain
the finished PRD into the proposal/authoring step** so PRD→DESIGN is one
continuous walk rather than two disconnected stories.

## What changes

Once every slice ships:

- A target project is a **profile** — world keys + host bindings + a
  templates dir — that an instance app fills. No engine change is needed to
  retarget; the ticket source, doc templates, placement, and id-scheme are
  all configuration.
- A reusable **`gh`-backed ticket adapter** implements the `ticket`
  interface against GitHub issues, for any GitHub repo (glue, not a new Go
  host).
- `dev-story` imports `prd`; `main` routes to it; the published PRD path
  flows into the proposal intake as its seed — so PRD→proposal-authoring is
  one in-story pipeline.
- `stories/gears-rust/` is the worked example: gh tickets, `gears-sdlc`
  templates + per-gear placement, DCO/conventional-commit `vcs`, `make
  check` as `ci`. A second target needs only a new profile, no code.

## Impact

- **Spans:** runtime (profile substrate + ticket adapter), story (prd fold,
  gears-rust instance).
- **Net surface:** new world keys on `dev-story` for the doc profile;
  parameterized publish glue; one new ticket adapter script; one import edge
  + one intent in `dev-story`; one new instance dir.
- **Docs on ship:** a "targeting an external project" guide in
  `docs/stories/` (or `docs/architecture/`), the dev-story README's
  instance section, and a `stories/gears-rust/README.md`. The shipped
  pieces of each slice migrate per that child's own plan.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Target-project profile | runtime | Parameterize doc shape / placement + `repo_root` passthrough so retargeting is config, not code | — | ✅ Shipped | [dev-story README → doc profile](../../stories/dev-story/README.md#doc-profile--targeting-an-external-project) |
| 2 | gh ticket adapter | runtime | A `gh`-backed glue provider satisfying the `ticket` interface against GitHub issues | 1 | Deferred | [`gh-ticket-adapter.md`](gh-ticket-adapter.md) |
| 3 | prd into dev-story | story | Import `prd`, add `go_prd` from `main`, chain its `@exit:done` into the **design** intake | — | ✅ Shipped | [dev-story README → PRD → Design walk](../../stories/dev-story/README.md#prd--design-walk) |
| 4 | gears-rust instance | story | The worked example: fill the profile (gears-sdlc docs under `gears/<gear>/docs/`, the copy-me template) | 1, 3 | ✅ Shipped | [gears-rust README (in the gears repo)](https://github.com/constructorfabric/gears-rust/blob/docs/kitsoki-integration/stories/gears-rust/README.md) |

## Sequencing

```
#1 (profile substrate) ──▶ #2 (gh ticket adapter) ──┐
                                                     ├──▶ #4 (gears-rust instance)
#3 (prd into dev-story, parallel) ───────────────────┘
```

#1 is the substrate (the publish/template seam every target uses). #2
reuses #1's `repo_root` passthrough. #3 is independent of #1/#2 — pure
story restructuring — and can land in parallel. #4 is the integration: it
fills the profile from #1, binds the adapter from #2, and exercises the
prd→proposal chain from #3.

## Shared decisions

1. **Profile lives at the instance + world layer, not a new file format.**
   A target is described by `host_bindings` (already the seam) plus a small
   set of `dev-story` world keys (`template_dir`, `publish_durable_path`,
   `doc_kind` placement) and a templates dir — not a bespoke `profile.yaml`.
   Principle of least surprise: reuse the import/world mechanism that
   `kitsoki-dev` already demonstrates.
2. **Adapters are glue, not Go hosts, until proven otherwise.** The gh
   ticket provider is a `host.run`/Starlark script (see the `starlark`
   skill), keeping the engine untouched and the adapter forkable per
   project. Promote to a Go host only if a real perf/contract need appears.
3. **The pipeline carries a doc *path*, not doc *content*, between steps.**
   prd publishes to a durable path and returns it; proposal intake seeds
   from that path. The same baton (`world.*_file`) is how PRD→DESIGN, and
   any future step→step, hand off.

## Cross-cutting open questions

1. **Where does an instance for a foreign repo physically live** — in the
   kitsoki repo (`stories/gears-rust/`, run with `--warp` pointing `workdir`
   at the checkout) or owned by the target repo? *Resolved
   and **shipped** by [`kitsoki-as-dependency.md`](kitsoki-as-dependency.md)
   slice #3: the target repo owns its instance and imports the
   base via `@kitsoki/dev-story`, resolved from a binary-embedded story library
   (no kitsoki checkout). See "External targets live in their own repo" below.*

### External targets live in their own repo

External targets no longer live in the kitsoki repo. A target repo carries its
own `stories/<name>/` instance (`app.yaml`, `templates/`, `flows/`,
`scenarios/`, the `drive:`-enabled tour manifest + `features/<id>.yaml`). No
config file is needed: `kitsoki web` walks the default `./stories` dir (see
`internal/webconfig` `defaultStoryDirs`), so a target repo with its instance
under `stories/` is discovered zero-config. The instance imports the kitsoki
base via `import: { source: "@kitsoki/dev-story" }`, which resolves from the
binary's embedded story library — so the target repo runs `kitsoki web` /
`kitsoki tour` with only the binary present, no kitsoki checkout. The kitsoki
repo keeps only self-targeting (dogfood) stories.

`gears-rust` is the worked example — the gears team's own instance at
[`constructorfabric/gears-rust` → `stories/gears-rust/`](https://github.com/constructorfabric/gears-rust/tree/docs/kitsoki-integration/stories/gears-rust).
The only instance edit on the move was the import source
(`../dev-story` → `"@kitsoki/dev-story"`) and the `world.workdir`/`world.repo_root`
defaults (`../gears-rust` → `.`, the gears checkout root). The full migration
mechanics are in [`kitsoki-as-dependency.md`](kitsoki-as-dependency.md) slice 3.
2. **Does `prd` stay runnable standalone after #3 folds it into
   `dev-story`?** *Lean: yes — import is additive; `stories/prd/app.yaml`
   remains a valid root, `dev-story` just imports it under an alias. Decided
   per #3.*

## Non-goals

- A GUI/config wizard for authoring profiles — a profile is hand-written
  YAML, same as every other instance.
- Replacing gears-rust's Cypilot SDLC or its `gears-sdlc` templates — the
  adapter **conforms to** them (emits their doc shape + `cpt-` IDs), it does
  not supersede them.
- A generic ticket abstraction beyond GitHub issues (Jira/Linear/etc.) —
  only the `gh` adapter is in scope; the interface seam leaves room for more.
- Real-LLM tests — every slice is exercised with mock agents / flows
  (CLAUDE.md).
