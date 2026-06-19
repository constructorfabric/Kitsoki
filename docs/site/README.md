# The promo site, help docs, and feature catalog

One pipeline turns the feature catalog into every public-facing surface:

```
features/<id>.yaml ──codegen──► tools/runstatus/src/tour/generated/<id>.ts (committed)
   │                               ├─► live tour overlay (web UI)
   │                               └─► Playwright *-video.spec.ts ──► .artifacts/<dir>/
   │                                     demo MP4 + NN-<step>.png + .chapters.json
   ├──codegen──► features-index.json + qa/<id>.{scenarios.yaml,feature.md}
   └──────────► tools/site (VitePress)
                   ├─► GitHub Pages  (base /Kitsoki/, full videos)
                   └─► internal/helpdocs go:embed → `kitsoki web` /help/ (posters only)
```

## The feature catalog (`features/`)

One YAML per feature: title/tagline/summary (promo + docs copy), the tour
steps (drive both the live overlay and the recorded demo), the demo's
recording binding, optional gated ui-qa scenarios, doc links. Authoring guide:
[`features/CLAUDE.md`](../../features/CLAUDE.md). The committed manifests under
`tools/runstatus/src/tour/generated/` are code-generated — `make features`
regenerates, `make features-check` (inside `make build` and `make test`) fails
on stale output, schema violations, or spec↔feature drift. The chain
YAML → generated TS → live popover is closed end-to-end by the existing video
specs' title assertions.

## Demo recording (`make demos`)

`scripts/record-demos.sh` records every recordable demo at watch-speed
(`WEB_CHAT_PACE=1`), deterministically (no LLM — `--flow`/`--host-cassette`).
Incremental by per-demo content stamps (feature YAML + spec + story inputs +
binary) in `.artifacts/<dir>/.stamp`; `make demos-force` ignores them. One
demo: `make demo-feature FEATURE=<id>`. Videos are **never committed**.
Each spec also emits a `<video>.mp4.chapters.json` sidecar (one chapter per
tour step) the site uses for its clickable chapter rail.

## The site (`tools/site`, VitePress)

- **Promo landing** (`src/index.md`) is a thin layer: hero + `<HeroDemo/>` +
  `<FeatureGrid/>` over the same data/components as the docs pages — zero
  duplicated content.
- **Feature pages** are dynamic routes (`src/features/[id].md` +
  `[id].paths.ts`) over the generated `features-index.json`: chaptered video,
  step cards (click → seek), narrative markdown, doc links.
- **Guide docs** are an **allowlist copy** of `docs/` (`docs-manifest.json` +
  `scripts/stage-docs.mjs`): internal trees (proposals, competitive-analysis,
  skills, …) can never leak; links escaping the allowlist are rewritten to
  GitHub URLs; dead links fail the build; `scripts/check-leaks.mjs` re-checks
  the dist.
- Missing media never fails a build — pages degrade to poster + placeholder,
  so docs-only iteration works with an empty `.artifacts/`.

Targets: `make site` (build, base `/Kitsoki/`), `make site-dev` (HMR),
`make site-full` (demos + site), `make site-clean`.

## Publishing

- **GitHub Pages**: `.github/workflows/site.yml` builds the binary, records
  stale demos (two-level cache: `actions/cache` over `.artifacts` + the
  per-demo stamps), builds, deploys. Docs-only pushes deploy in minutes with 0
  recordings; a cold run records ~14 demos (30–50 min). Manual dispatch has a
  `rerecord` input. One-time setup: repo Settings → Pages → Source: GitHub
  Actions.
- **In the binary**: `make site-embed` builds the embedded variant
  (base `/help/`, posters only — never MP4s, ~5MB) into
  `internal/helpdocs/assets/`; the next `make build` embeds it and
  `kitsoki web` serves it at `/help/`. Unstaged help yields an actionable
  placeholder page, never an error (`internal/helpdocs`).

## QA (gated — real LLM, never automatic)

`make feature-qa FEATURE=<id>` records the demo then judges it against the
catalog-generated scenarios + feature spec
(`.artifacts/features/qa/<id>.{scenarios.yaml,feature.md}`) via the
[kitsoki-ui-qa](../../.agents/skills/kitsoki-ui-qa/SKILL.md) pipeline. `make demo-tour-qa`
is the onboarding-tour instance of the same flow.
