# gears-rust — dev-story targeting an external project

This instance points the **dev-story** hub at a *foreign* repo —
[`constructorfabric/gears-rust`](https://github.com/constructorfabric/gears-rust)
— and drives its **PRD → Design** spec chain. It is
[`kitsoki-dev`](../kitsoki-dev/app.yaml) with **one thing changed**: a handful
of doc-profile world keys retarget *where* docs land and *what shape* they
take. No engine or dev-story room change is needed to retarget — the seam is
configuration. It is the worked example of the
[external-target profile](../dev-story/README.md#doc-profile--targeting-an-external-project).

## What it proves (the POC)

Walking, from `core.main`:

```
prd  →  author a PRD  →  prd_published  →  continue  →  design pipeline  →  publish a DESIGN
```

lands two **gears-sdlc-shaped** docs in the gears-rust checkout:

```
<repo_root>/gears/<gear>/docs/PRD.md      # fixed name, gears-sdlc PRD
<repo_root>/gears/<gear>/docs/DESIGN.md   # fixed name, gears-sdlc DESIGN
```

rather than kitsoki's own flat `docs/prd/<slug>.md` + `docs/proposals/<slug>.md`.
The design author reads the **vendored gears-sdlc templates**
([`templates/`](templates/)) and **no kitsoki feature ticket** is minted —
gears-rust tracks work in GitHub issues (the `gh` ticket adapter is a
separate, deferred epic slice; the PRD → Design walk does not pick up a
ticket, so it is not needed here).

The target gear is **`notes-service`** — a fresh scratch gear that does not
exist in the real tree, so the POC never clobbers a real gear's docs.

## Quickstart

```bash
# clone the target as a sibling of the kitsoki repo (the app.yaml default)
git clone https://github.com/constructorfabric/gears-rust ../gears-rust

kitsoki run stories/gears-rust/app.yaml
# Lands in core.main. Type `prd` to start the PRD → Design walk.
```

Point it at a checkout elsewhere (absolute path) or author for a different
gear via the warp scenario — edit the paths in
[`scenarios/gears-rust.yaml`](scenarios/gears-rust.yaml):

```bash
kitsoki run stories/gears-rust/app.yaml --warp scenarios/gears-rust.yaml
```

## The profile (the only thing that differs from kitsoki-dev)

All set in [`app.yaml`](app.yaml)'s instance `world:` and projected into the
`core` (dev-story) import via `world_in`. Every key has a dev-story default
that reproduces kitsoki's own behaviour — overriding them **is** the profile:

| World key | gears-rust value | Effect |
|---|---|---|
| `workdir` / `repo_root` | `../gears-rust` | the external checkout docs publish into |
| `publish_durable_path` | `gears/notes-service/docs` | PRD home (relative to workdir) |
| `prd_doc_filename` | `PRD` | → `gears/notes-service/docs/PRD.md` (fixed, not slug-named) |
| `design_durable_path` | `gears/notes-service/docs` | DESIGN home |
| `design_doc_filename` | `DESIGN` | → `gears/notes-service/docs/DESIGN.md` |
| `design_template_dir` | `stories/gears-rust/templates` | the gears-sdlc templates the author reads |
| `design_ticket_dir` | `""` | skip the kitsoki feature ticket |

The world keys + the publish-path/`doc_filename` parameterization live in the
hub; see the dev-story README's
[doc-profile section](../dev-story/README.md#doc-profile--targeting-an-external-project).

## Flows (no-LLM validation)

```bash
kitsoki test flows stories/gears-rust/app.yaml   # 2/2
```

- [`flows/prd_to_design.yaml`](flows/prd_to_design.yaml) — the PRD half:
  `main → prd → … → prd_published → continue → design`, asserting the PRD
  publishes to `gears/notes-service/docs/PRD.md` and the path seeds the design
  intake. It also asserts the profile threads instance → core → **prd**
  (`core__prd__publish_durable_path`, `core__prd__prd_doc_filename`).
- [`flows/design_publishes_gears_design.yaml`](flows/design_publishes_gears_design.yaml)
  — the DESIGN half: the seeded design intake walked to publish, asserting
  `gears/notes-service/docs/DESIGN.md` and **no** feature ticket.

They are split because each carries one acceptance shape; a third flow,
[`flows/prd_to_design_full.yaml`](flows/prd_to_design_full.yaml), walks the
**whole** PRD → Design chain in one session (the prd author and design author
are disambiguated by the `id: prd_author` / `id: design_author` task ids) and
is the host-stub source the demo video drives.

## Demo video (no-LLM, tour-driven)

A tour-narrated walkthrough of the full PRD → Design conversation — driven
entirely through the chat UI against the no-LLM flow above — records via the
[`kitsoki-ui-demo`](../../docs/skills/kitsoki-ui-demo/SKILL.md) skill:

- manifest: `tools/runstatus/src/tour/gears-prd-design-manifest.ts`
- spec:     `tools/runstatus/tests/playwright/gears-prd-design.spec.ts`

```bash
make build && cp ./kitsoki bin/kitsoki          # SPA → binary (go:embed)
cd tools/runstatus
WEB_CHAT_PACE=0 pnpm exec playwright test gears-prd-design --project=chromium  # validate
pnpm exec playwright test gears-prd-design --project=chromium                  # record → .artifacts/gears-prd-design/gears-prd-design.mp4
```

## Copy this dir for a NEW external target

This instance **is** the template for a second target — no code change:

1. `cp -r stories/gears-rust stories/<target>`.
2. In `app.yaml`, swap the `host_bindings` if the target's ticket / vcs / ci
   providers differ, and repoint the doc-profile world keys (`workdir`,
   `*_durable_path` = `<scope>/docs`, the fixed filenames, `design_template_dir`).
3. Vendor that target's doc templates into `templates/`.
4. Copy the two flows, adjust the asserted paths.

A laxer target (flat docs, slug-named files, kitsoki templates) needs even
less — just `workdir`.
