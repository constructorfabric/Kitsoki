# Story: Demo — @kitsoki GitHub loop slidey composite

**Status:** Draft v1. The deliverable (one composited, QA-gated slidey
deck) is not shipped, but substantial capture/render scaffolding already
exists and should not be re-authored from scratch: `tools/runstatus/src/tour/github-demo-manifest.ts`
(the Act 1 storyboard beats over a static GitHub-thread fixture),
`tools/runstatus/tests/playwright/github-demo-issuepr.spec.ts` (Act 1
capture), `github-demo-act2-rrweb-capture.spec.ts` (Act 2, the kitsoki web
viewer side), and `github-demo-composite.spec.ts` (the Act 3 slidey
composite, gated on both rrweb clips existing; SKIPs safely if not — see
that file's doc comment). None of it has been run to a finished, checked-in
deck (outputs are gitignored under `.artifacts/github-demo/`), and the
`features/` tour-manifest registration this proposal's task 4.1 calls for is
still outstanding.
**Kind:**   story
**Epic:**   kitsoki-github-agent.md

> **`Kind: story` is the closest fit, not a literal one.** This slice ships a
> *demo deliverable*, not new rooms or world. It is authored as a `features/`
> tour spec (plus per-act Playwright capture specs) and a `*.slidey.json` deck —
> the "story sections" below are adapted accordingly: **Reuse inventory**, a
> **Tour storyboard** (replacing per-room detail), **Net-new files**, and
> **Flow/tour fixtures**. No `stories/` directory is created.

## Why

Slices #1–#5 build the @kitsoki GitHub loop — inbound mention ingress + comment
substrate (#1), job dispatch (#2), the PR-autopilot story (#3), the trace +
artifact service (#4), and the web viewer + operator-drive surface (#5). None of
it is *visible* until something walks the whole loop end to end. The epic's
deliverable (shared decision #6, "no real GitHub / no real LLM") is a single
shareable video that shows a reviewer both halves of the loop — the GitHub
thread and the kitsoki web UI — without anyone standing up an App, a webhook, or
an LLM. This slice is that video.

## What changes

One **tour-driven, no-LLM demo deck**, built with `kitsoki-ui-demo` and gated by
`kitsoki-ui-qa`, using **slidey itself as the worked case study** — kitsoki
fixing/advancing slidey while slidey narrates. It is captured as **rrweb-backed
acts**, each QA-gated, then **composited into one slidey presentation deck**
(source `*.slidey.json` first; rendered MP4 only as an optional QA/share export) with title/section
slides between acts. The deck source embeds rrweb logs, not pre-rendered MP4
clips; MP4 is only the final rendered review artifact unless an act contains a
surface rrweb cannot reconstruct (`<canvas>`, `<video>`, WebGL).

1. **GitHub side** — a user `@kitsoki`s an issue (bug-label → bugfix, feature-label
   → PRD/design) and a PR (CI-watch/fix, rebase-on-conflict, comment-driven change,
   parent-comment resolve, ask-for-guidance), with kitsoki's ack / rolling-status /
   done comments appearing on the thread and a link to the kitsoki web UI.
2. **kitsoki side** — the web viewer: live trace, artifact gallery / media, and the
   operator driving the conversation directly with acks posting back to GitHub.
3. **Composite** — title + section slides bracketing the two clips into one deck.

The GitHub frames are **deterministic static fixtures** (the `gh-issue-review.html`
pattern, driven over `file://`), never live GitHub; the kitsoki frames come from a
real `kitsoki web` server in the no-LLM replay+tour posture. Media artifacts are
served from `baked/` files intercepted at the Playwright network edge.

## Impact

- **Net-new:** 1 `features/` tour spec, 3 Playwright capture specs (+ a thin
  composite spec), 1 slidey deck JSON named `*.slidey.json`, 1 thin demo instance (baked world), a
  `baked/` artifact set, replay recordings + host/exec cassettes, QA feature +
  scenarios per act.
- **Engine/host changes:** none — composes `kitsoki-ui-demo`, `kitsoki-ui-qa`,
  `host.slidey.render`, the cross-site composite scripts, and the rrweb path. If
  any seam is missing it belongs in its owning slice (#1–#5), not here.
- **Docs on ship:** fold the recipe into `docs/tui/web-ui.md` (the demo section of
  the epic's viewer doc) and register the feature in the `features/` catalogue;
  delete this proposal.

## Reuse inventory

Each demo mechanism maps to an existing, referenced reference — this slice is pure
composition of the demo/QA/slidey toolchain.

| Demo concern | Mechanism | Reference |
|---|---|---|
| No-LLM kitsoki web drive | `kitsoki web --harness replay --recording <rec> --host-cassette <cass>`, Playwright-driven | `.agents/skills/kitsoki-ui-demo/SKILL.md` (the loop); `tools/runstatus/tests/playwright/slidey-pm-idea-video.spec.ts` |
| Whole-video tour narration | feature manifest + `__startTourWithSteps`, four-step home→observer intro | `tools/runstatus/tests/playwright/agent-actions-video.spec.ts` + `tools/runstatus/src/tour/agent-actions-manifest.ts` |
| GitHub "UI" frames, no live GitHub | static `file://` fixture + portable `makeCaption`/`makeSpotlight` | `tools/runstatus/tests/playwright/gh-issue-review-video.spec.ts` + `tools/runstatus/src/tour/gh-issue-review-manifest.ts` + `fixtures/gh-issue-review.html` |
| Baked artifacts served by handle (the `--flow` stub gap) | intercept `/artifact` + `/artifact/<id>/poster` + the semantic RPC at the Playwright network edge, serve `baked/` | memory: "--flow stubs artifacts; demos bridge them" |
| Baked demo world (tour can't drive slots / ignores `initial_world`) | thin instance whose `world:` defaults carry the demo state | memory: "Tour demo needs baked world, not initial_world"; `stories/gears-bugfix` precedent |
| Deterministic, server-free re-render | rrweb capture-once-live → replay-render with chapter-keyed holds | `tools/runstatus/tests/playwright/_helpers/rrweb-replay.ts`; existing `slidey-*-rrweb-capture.spec.ts` |
| Watch-pace + duration gate | `WEB_CHAT_PACE` paced loop, `KITSOKI_MIN_DEMO_SECONDS`, chapter sidecar | `_helpers/server.ts` (`saveVideoAsMp4`, `ChapterRecorder`, `writeChapters`) |
| Per-act QA verdict gate | vision agent vs cited frames + completeness, adversarial re-check | `.agents/skills/kitsoki-ui-qa/SKILL.md` → `scripts/qa.sh` → `verdict.json` |
| Clip → deck composite | `host.slidey.render` (JSON scene spec → narrated MP4 + chapter sidecar) | `docs/architecture/hosts.md#hostslideyrender`; `slidey-authoring` skill |
| Title / section cards | Chromium-rendered title cards + mpegts concat (fallback path) | `scripts/make-title-card.mjs` + `scripts/concat-videos.sh` + `scripts/record-gh-issues-demo.sh` |

## Case study: slidey

The issue/PR worked in the demo is about **slidey itself**, so kitsoki improving
slidey is narrated *by* slidey — the cleanest dogfood. Existing slidey assets to
build on, not duplicate: stories `stories/slidey-bugfix`, `stories/slidey-dev`,
`stories/slidey-edit`; features `features/slidey-bugfix.yaml`,
`features/slidey-dev-prd-design.yaml`, `features/slidey-architect-design.yaml`,
`features/slidey-open-pr.yaml`. The concrete worked ticket reuses the proven
**slidey-128 grid-cards narration-drift** fix (`features/slidey-open-pr.yaml`),
so the PR act has a real, already-validated change to walk.

## Tour storyboard

Ordered scenes/chapters. Each names what it proves and which slice it
demonstrates. Acts 1–2 are rrweb-backed captured acts; the composite interleaves
title slides (T) between them.

**Act 1 — GitHub issue & PR dispatch (the GitHub side).** Surface: the
`gh-issue-review.html`-style fixture, `file://`, portable captions.

| # | Scene | Proves | Slice |
|---|---|---|---|
| T0 | Title slide: "@kitsoki on GitHub" | — | — |
| 1 | A `bug`-labelled slidey issue; user comments `@kitsoki` | mention ingress | #1 |
| 2 | kitsoki ack comment + link to `…/run/<job-id>` appears | comment substrate, one job = one link | #1, #2 |
| 3 | A `feature`-labelled issue → "running PRD/design" status | label→story dispatch | #2 |
| 4 | A slidey PR: CI red → kitsoki status "watching CI / pushing fix" | CI-watch + auto-fix | #3 |
| 5 | Merge-conflict → "rebased onto target" status | rebase-on-conflict | #3 |
| 6 | A reviewer comment → kitsoki "implemented requested change" + resolves the parent comment | comment-driven implement, parent-comment resolve | #3 |
| 7 | A low-confidence point → kitsoki posts a **guidance request** comment | "if in doubt, ask" arc | #1, #3 |
| 8 | Rolling status comment edits to **done** | single-voice status, no flood | #1 |

**Act 2 — kitsoki web viewer & operator drive (the kitsoki side).** Surface: real
`kitsoki web` (replay + host-cassette), tour-narrated home→observer intro.

| # | Scene | Proves | Slice |
|---|---|---|---|
| 9 | Click the run link → live trace streaming in the observer | public trace surface | #4, #5 |
| 10 | Artifact gallery: the rendered slidey deck / screenshots browsable by handle (baked-intercepted) | artifact serving by handle | #4, #5 |
| 11 | Operator types directly in the composer; turn lands (state badge advances) | operator-drive surface | #5 |
| 12 | That drive posts an **ack comment back** onto the GitHub thread (cut to the fixture) | operator action → thread | #5, #1 |

**Act 3 — Composite.** Acts 1 and 2 embedded as rrweb-backed `video` scenes inside
one slidey deck, bracketed by T0 and section slides ("The GitHub side" / "The
kitsoki side" / "One loop"), the whole deck narrated. Acceptance criterion: every
deck act scene has `rrweb` and `chapters:"auto"`; no act scene uses `src:"*.mp4"`
unless it is documenting an explicit rrweb-incompatible surface.

## Net-new files

```
features/
└── kitsoki-github-demo.yaml          # the tour spec (manifest export + acts)

docs/proposals/demo-assets/kitsoki-github/   # transient demo assets (per CLAUDE.md, not story dir)
├── instance/                         # thin baked-world instance (no new rooms)
│   └── world-defaults.yaml           # demo world baked in (slots/initial_world unreachable to tours)
├── deck/
│   └── kitsoki-github.slidey.json    # source slidey scene spec
├── baked/                            # artifacts served at the Playwright network edge
│   ├── slidey-deck.mp4 + .poster.png + .semantic.json
│   └── screenshots/*.png
├── clips/                            # rrweb logs embedded by the deck
├── recordings/                       # replay recording(s) for the kitsoki web drive
├── cassettes/                        # host + gh/git exec cassettes (Act 1 fixtures)
└── fixtures/
    └── gh-thread.html                # the GitHub-thread static fixture (gh-issue-review.html shape)

tools/runstatus/tests/playwright/
├── github-demo-issuepr.spec.ts       # Act 1 capture (fixture + portable captions)
├── github-demo-webviewer.spec.ts     # Act 2 capture (replay+host-cassette web drive)
└── github-demo-composite.spec.ts     # optional: drives host.slidey.render of the deck, asserts the MP4 export

tools/runstatus/src/tour/
└── github-demo-manifest.ts           # generated from the feature (make features)

.context/qa/                          # QA inputs per act (transient)
├── act1-{feature.md,scenarios.yaml}
├── act2-{feature.md,scenarios.yaml}
└── composite-{feature.md,scenarios.yaml}
```

## Flow/tour fixtures

The regression contract is the capture specs + their cassettes/recordings, each
gated by a QA verdict. No real GitHub, no real LLM (CLAUDE.md / shared decision #6).

- **`github-demo-issuepr.spec.ts`** — drives `fixtures/gh-thread.html` over
  `file://` through Act-1 scenes 1–8; `gh`/`git` reads are exec cassettes, comment
  posts are fixture state. Proves the GitHub-side narrative (#1–#3). QA gate:
  `act1` scenarios (ack appears, run link present, rebase/resolve/guidance/done
  statuses visible).
- **`github-demo-webviewer.spec.ts`** — `kitsoki web --harness replay --recording
  recordings/<rec> --host-cassette cassettes/<cass>`; the thin baked-world instance
  supplies the demo state; `/artifact` + `/artifact/<id>/poster` + the semantic RPC
  are intercepted at the network edge and served from `baked/`. Proves the kitsoki
  side (#4, #5). QA gate: `act2` scenarios (trace streams, gallery renders real
  media not a blank, operator turn lands, ack-back visible).
- **`github-demo-composite.spec.ts`** — optionally renders `deck/kitsoki-github.slidey.json` via
  `host.slidey.render` (format `mp4`) and asserts the deck uses rrweb act scenes,
  then asserts the output MP4 + its `.chapters.json` sidecar. QA gate:
  `composite` scenarios (both acts present, section slides between them,
  default-pace).
- **Pace + duration gates apply to every recorded act:** `WEB_CHAT_PACE=0` is
  validation-only and emits `<name>.fast.mp4`; the shippable MP4 must clear
  `KITSOKI_MIN_DEMO_SECONDS` and pass `pacing-scan.sh` against its chapter sidecar
  (no flashes) — guarded by `assertVideoDuration` (memory: "Demo watch-speed vs
  validation pace").
- **Optional deterministic re-render:** Acts that are all-DOM (the web viewer,
  excluding any `<canvas>`/`<video>` media tile) can capture-once-live then
  replay-render server-free via the rrweb path; canvas/video tiles stay on the
  live screen-record path (`rrweb-replay.ts` canvas boundary).

## Tasks

```
## 1. Act 1 — GitHub side
- [ ] 1.1 Author fixtures/gh-thread.html + github-demo-manifest.ts (scenes 1–8) + gh/git exec cassettes
- [ ] 1.2 github-demo-issuepr.spec.ts: fast-validate (WEB_CHAT_PACE=0), then record at watch pace
- [ ] 1.3 QA-gate act1 (qa.sh, --strict): verdict.json overall=pass

## 2. Act 2 — kitsoki side
- [ ] 2.1 Bake the demo world into the thin instance; capture the replay recording + host cassette
- [ ] 2.2 Stage baked/ artifacts; wire the /artifact + poster + semantic RPC network-edge intercepts
- [ ] 2.3 github-demo-webviewer.spec.ts (replay + host-cassette + tour intro); fast-validate then record
- [ ] 2.4 QA-gate act2: gallery shows real media (blank-scan clean), operator turn lands, ack-back visible

## 3. Composite
- [ ] 3.1 Author deck/kitsoki-github.slidey.json (T0 + section slides + rrweb-backed video scenes; no MP4/WebM act sources)
- [ ] 3.2 Optional github-demo-composite.spec.ts: host.slidey.render → MP4 + chapters sidecar when video QA/share export is requested
- [ ] 3.3 QA-gate the composite: both acts + section slides present, default pace (pacing-scan clean)

## 4. Land
- [ ] 4.1 Register features/kitsoki-github-demo.yaml in the catalogue (make features)
- [ ] 4.2 Fold the recipe into docs/tui/web-ui.md; move the deck/specs to their home; trim/delete this proposal
```

## Open questions

1. **rrweb boundary for Act 2.** The artifact gallery may render media tiles
   (`<video>`/`<canvas>`) that the rrweb path cannot reconstruct. The default is
   still rrweb-backed deck acts; if a concrete gallery surface crosses the rrweb
   boundary, isolate that surface as the smallest MP4 `src` exception and document
   it in the deck/spec. Do not convert the whole composite or all acts to MP4.
2. **Composite via `host.slidey.render` deck vs `concat-videos.sh`.** The deck path
   gives one narrated artifact with a unified chapter sidecar; the concat path is
   the proven cross-site composite. *Lean:* slidey deck (the epic asks for "one
   slidey presentation deck"); keep concat as the fallback if a `video` scene type
   is unavailable.
3. **One run link or two (issue act vs PR act).** *Lean:* one job-id per worked
   item, both shown, to honour "one job = one run = one linkable surface" (shared
   decision #3) without implying a single run spans issue + PR.

## Non-goals

- **A real GitHub integration test.** GitHub frames are static fixtures + exec
  cassettes; this never authenticates an App, hits a webhook, or calls the API.
  Real-GitHub coverage belongs to slices #1–#3's own fixtures.
- **A live run.** No real LLM, no live server beyond the deterministic replay
  posture (shared decision #6).
- **Demonstrating self-correction.** refine / restart / interpreter-driven
  self-correction flows can't be driven by a tour (memory: "Tour demo needs baked
  world…"); the demo walks the happy-path narrative only.
- **Re-deriving demo/QA/slidey tooling.** This slice only composes the existing
  `kitsoki-ui-demo`, `kitsoki-ui-qa`, and `host.slidey.render` mechanisms; any
  missing seam is filed against its owning slice.
```
