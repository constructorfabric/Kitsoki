# Feature catalog

One YAML file per kitsoki feature. **This directory is GENERATED** —
`kitsoki graph render-features` (internal/graph/featuresadapter) writes it
from each feature's `sitepage-feature-<id>` node in the project object graph
(`docs/proposals/project-object-graph/seed-objects.yaml`, W3.1). The graph is
the source of truth for feature content: title/tagline/summary, narrative,
related, sections, qa, tour steps, and the demo binding (derived from the
site-page's `has_media` evidence node). Don't hand-edit `features/<id>.yaml`
directly — edits belong on the graph node; `make features` regenerates this
directory from it.

## Editing workflow

1. Edit the `sitepage-feature-<id>` node (and its `feature`/`evidence`
   nodes, if the demo binding or requirements changed) in
   `docs/proposals/project-object-graph/seed-objects.yaml`.
2. Run `make features` — renders `features/<id>.yaml` from the graph
   (`kitsoki graph render-features`), then regenerates the committed tour
   manifests under `tools/runstatus/src/tour/generated/` (banner-marked
   `DO NOT EDIT`). `make features-check` fails if `features/*.yaml` has
   drifted from the graph (`--check`) or any generated file is stale.
3. Validate fast, no LLM, no dwells:
   `cd tools/runstatus && WEB_CHAT_PACE=0 pnpm exec playwright test <specName> --project=chromium`

`make features-check` (run inside `make build` and `make test`) fails on any
schema violation, stale generated file, or spec↔feature mismatch — a stale
manifest can never reach the embedded SPA.

## Completeness invariants

Beyond shape validation, `features-check` enforces that each cataloged feature
has the pieces it needs to render correctly on the site (all deterministic,
file-based — see `schema.ts`):

- **promo ⇒ demo** — a feature with `promo:` must bind a `demo:`, or its grid
  card ships empty.
- **capturable tour demo ⇒ posterStep** — a non-external demo that has a `spec`
  (and is not a stitched product-tour) *and* a `tour:` must name a
  `demo.posterStep`, so the card/hero has a deterministic poster frame instead
  of a black first frame. Tourless demos and stitched tours are exempt.
- **in-site docs ⇒ allowlisted** — every `docs:` link under `docs/` must be
  published by the site allowlist (`tools/site/docs-manifest.json`); otherwise
  it silently degrades to an external GitHub blob link. Links outside `docs/`
  (e.g. `stories/<name>/app.yaml`) are deliberate source links and exempt. Do
  not link transient `docs/proposals/*` from a shipped feature — proposals are
  deleted on ship.

## Field notes

- `kind`: `feature` (product capability — promo grid + docs page),
  `product-tour` (cross-feature walkthrough), `story-demo` (showcase of one
  authored story).
- `promo`: present ⇒ the feature appears on the promo landing page, sorted by
  `order` (lower first); `highlight: true` marks featured slots.
- `demo`: binds the feature to its deterministic capture spec. Product-site
  catalog demos should use `demo.format: rrweb`; that produces
  `<videoBase>.rrweb.json` plus a self-contained `<videoBase>.html` viewer.
  Most tours can reuse an existing `*-video.spec.ts` in rrweb mode because the
  shared helpers disable Playwright `recordVideo` when `KITSOKI_RRWEB_OUT` is
  set. Use `demo.rrwebSpec` when the real capture spec differs from the
  catalog/bijection anchor `demo.spec`. MP4 is the legacy fallback for surfaces
  rrweb cannot reconstruct (`<canvas>`, `<video>`, WebGL) or an explicitly
  requested video export; it should not be catalog media by default. For those
  exports, `artifactDir` / `videoBase` must match the spec's `ARTIFACT_DIR` /
  `saveVideoAsMp4` values.
  `external: true` marks demos that depend on paths outside this repo — they
  are excluded from capture and path validation.
- `demo.embed`: presents a rrweb-native story-demo (its video spec is a
  permanent stub) as an embedded Slidey deck clip instead of an mp4 — set
  `{ deck, rrweb }` to the source deck JSON and the clip's `rrweb` path as
  written in that deck; codegen derives the scene index. The deck's committed
  self-contained bundle (`docs/decks/bundled/<deck-id>.html`) must exist.
  Full contract: `docs/media/README.md` § Deck embeds on the product site.
- `tour.export`: the generated const name. Specs import it from
  `src/tour/generated/<id>.js`; renaming it breaks the import (the bijection
  check will tell you).
- `tour.steps`: exactly the `TourStep` fields (`src/tour/types.ts`). Rules
  enforced by the schema: `explain` steps advance with `next`; `route-match`
  needs `advanceRoute`; `click-target` needs a `target`; step ids unique.
- `qa.scenarios`: observable claims for the **gated** vision-QA pipeline
  (`make feature-qa FEATURE=<id>` — drives the real `claude` CLI, never run
  automatically). Emitted to `.artifacts/features/qa/` by `make features-index`.

## ⚠ Step ids are hook keys

Playwright specs key **pre-step hooks** on step ids (e.g. the agent-actions
spec opens the drawer when it reaches step `aa-affordance`). Renaming a step id
in YAML silently skips that hook — the spec then fails loudly on its
`waitForTarget` / title assertion, but you'll save time by grepping the spec
for the old id before renaming.

## Onboarding tour

`features/onboarding-tour.yaml` generates `src/tour/generated/onboarding-tour.ts`,
re-exported by the compatibility shim `src/tour/manifest.ts` (the import surface
for the tour store, overlay, and unit tests). It must stay story-agnostic —
anchor only to testids present on every story (see `src/tour/types.ts` rules).
