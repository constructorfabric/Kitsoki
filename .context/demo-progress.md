# Slidey Hybrid Demo — progress log

Plan: `.context/slidey-hybrid-demo-plan.md`. Target: `.context/demo-target.md`.
Working dir: `.artifacts/slidey-hybrid/`. Dogfood: drive kitsoki via studio MCP.

## Status
- [x] Surveyed existing infra (slidey healthy; kitsoki-ui-demo rrweb mode; slidey `video` scene supports `rrweb:`+`chapters:auto`).
- [x] Plan + tasks created (#1–#6).
- [x] **Slice 1 (pipeline proof) — DONE, HTML-native rrweb**. Pivoted to the
  user's direction: HTML embeds + native rrweb (no MP4). Found+fixed a real
  slidey gap: `slidey bundle` (build-single.mjs) inlined gif/image assets but
  left a video scene's `rrweb:` as an external ref → self-contained deck couldn't
  play the tour offline. Fixed: embed rrweb as a `data:application/json` URI +
  guard test `test/build-single-rrweb.test.js`. **Merged to slidey main `faeffcf`.**
  Verified headless: `proof.html` (4.4MB self-contained) plays the real kitsoki
  trace UI natively via RrwebPlayer, no console errors (`proof-scene2.png`).

## Key facts / gotchas
- slidey rrweb captions come from in-band `slidey.chapter` custom events in the
  rrweb stream; absent → no captions (fine for proof).
- Need `make build && cp ./kitsoki bin/kitsoki` before capturing new tours.
- Only one rrweb capture exists so far: `.artifacts/rrweb-eval/_smoke/smoke.rrweb.json`.

## DIRECTION (user, 2026-06-23): whole demo is coherently "dev-story on the SLIDEY repo"
Every phase instance must be bound to the **slidey** repo, not gears-rust. gears-bugfix
is a thin instance of the provider-neutral `stories/bugfix` pre-bound to gears-rust;
we need the equivalent **slidey-bound** instances for each phase (config-only seam +
replayed cassette), so PM-idea / design / bugfix / decomposition / feature-demo / PR
are all about slidey. The gears-bugfix rrweb capture from slice-2's first pass is
SUPERSEDED (kept only as a reference of the capture recipe).

## Slice 2 (bugfix phase) — DONE on slidey, committed `1523c732`
- Real slidey bug: grid-cards narration desync (timing estimate vs renderer
  fallback past cards_item_5). Real one-line fix + regression test, merged to
  **slidey main `19c8798`**.
- `stories/slidey-bugfix/` (app+flow+cassette) + `features/slidey-bugfix.yaml` +
  generated tour + specs. story_test GREEN (independently re-verified via MCP),
  features-check GREEN. rrweb capture = 457 events.
- Clip staged: `.artifacts/slidey-hybrid/clips/slidey-bugfix.rrweb.json`.
- Capture recipe (reusable): fork gears/slidey-bugfix-rrweb-capture.spec.ts —
  port Go tour drive to TS + rrweb hooks (installCapture/dumpCapture/writeEvents),
  1600x900 DSF1, unique ADDR port.
- NOTE: other dirty files in tree (stories/bugfix/app.yaml, delivery-tail,
  conflict-resolve) are a PARALLEL session's WIP — do NOT commit them.

## Deck spine authored (slice 5 skeleton)
`.artifacts/slidey-hybrid/dev-story-hybrid.json` — full narrated spine, 6 phases,
title + narrative slides interleaved with 6 `video` scenes (mode:embedded) that
reference per-phase rrweb clips under `.artifacts/slidey-hybrid/clips/`:
- `clips/pm-idea.rrweb.json`        (phase 1 PRD intake)
- `clips/architect-design.rrweb.json` (phase 2 design)
- `clips/decomposition.rrweb.json`  (phase 3 work plan)
- `clips/slidey-bugfix.rrweb.json`  (phase 4 bug fix)  ← agent acce886 building
- `clips/feature-refine.rrweb.json` (phase 5 spatial refine; slidey-edit story)
- `clips/open-pr.rrweb.json`        (phase 6 ship/PR)
Each clip = an rrweb capture of the corresponding slidey-bound dev-story tour.

## Useful existing scaffolding (slices 3-4)
- `stories/dev-story/flows/init_slidey_dogfood.yaml` — slidey project profile
  (stack/commands) already stubbed; onboarding "applies" `stories/slidey-dev/app.yaml`.
- `stories/dev-story/flows/prd_to_design_full.yaml` — the whole PRD→Design walk
  (kitsoki-on-kitsoki); gears-rust has the external-target variant. Need a
  **slidey-dev** external-target instance to make phases 1-3 slidey-coherent.
- `features/dev-story-prd-design.yaml`, `features/design-walkthrough.yaml` — tour mfsts.

## BLOCKER (2026-06-23): parallel session broke `stories/bugfix` on the shared tree
A parallel session committed `stories/bugfix` (triage-only mode + a `../cr`
conflict-resolve import in the delivery-tail tail) but the wiring lives in
UNTRACKED files (`stories/conflict-resolve/`, `stories/delivery-tail/rooms/resolve.yaml`,
`.../flows/resolve_*.yaml`). Result: ANY flow importing `bugfix` fails to load:
  - `tail.resolve` → target `../cr` (tail.cr) does not exist
  - undeclared world keys resolve_*/checkpoint_*; undeclared intents resolve_*
  - (also) bf `triaging.accept → @exit:triaged` doesn't `set:` required `triage_verdict`
This blocks **slice 3** (slidey-dev PRD→Design imports dev-story→bf) AND re-validating
slice 2. NOT mine to fix (their active WIP, moving target) — shared-tree rule: don't
debug another session's half-built work.

### My correct edits kept (uncommitted, can't verify-green until bugfix loads):
- Mapped bf's new `triaged` child-exit in all importers (loader requires every child
  exit mapped): `stories/dev-story/app.yaml` (→landing, status:triaged),
  `stories/gears-bugfix/app.yaml` (→shipped), `stories/slidey-bugfix/app.yaml` (→shipped).

### Slice 3 status: authoring DONE by agent aa955 (500'd before captures):
- `stories/slidey-dev/` (app + flows pm_idea/architect_design/prd_to_design_demo),
  `features/slidey-dev-prd-design.yaml`, capture specs slidey-pm-idea / slidey-architect-design.
- Captures NOT produced (interrupted + now blocked by the bugfix breakage). Resume
  captures once bugfix loads.

## Pivot: phase 5 (feature-refine via slidey-edit) is UNBLOCKED
`stories/slidey-edit` does NOT import bugfix — story_test 8/8 GREEN. Capturing its
annotate→refine tour as the phase-5 rrweb clip now while bugfix is broken.

## MILESTONE: full assembly pipeline VALIDATED end-to-end with real content
- Phase 5 (slidey-edit feature-refine) captured: 557 events, media+overlay
  confirmed → `clips/feature-refine.rrweb.json`. (Spec: slidey-edit-rrweb-capture.spec.ts.
  Note: agent found slidey-edit-video.spec.ts is now stale vs the static-HTML flow —
  not ours, left untouched.)
- Built `dev-story-hybrid-partial.json` (title + bugfix phase + refine phase + cta)
  → `slidey bundle` → `dev-story-hybrid-partial.html` (5.56MB self-contained, 2 rrweb
  clips inlined via my merged build-single fix). Headless-verified: BOTH video scenes
  play the real kitsoki tour natively (replayer mounted, 142.8s scrubber, embedded
  chrome), no console errors. `scene-4.png` shows the bug-fix tour rendering.
- This proves the deliverable format (narrated slides + native rrweb tour embeds)
  works with real per-phase content.

## Clip status (2 of 6)
- [x] clips/slidey-bugfix.rrweb.json (phase 4)
- [x] clips/feature-refine.rrweb.json (phase 5)
- [ ] clips/pm-idea.rrweb.json (phase 1) — BLOCKED (bugfix tree breakage)
- [ ] clips/architect-design.rrweb.json (phase 2) — BLOCKED
- [ ] clips/decomposition.rrweb.json (phase 3) — BLOCKED (deliver→delivery-tail→../cr)
- [ ] clips/open-pr.rrweb.json (phase 6) — UNBLOCKED (pr-refinement 5/5 GREEN); needs
  from-scratch tour authoring (no existing feature/tour/spec for pr-refinement).

## MILESTONE 2: 3-phase developer-arc deck DONE + 2nd slidey fix
- Phase 6 captured (206 events, real PR #128 / CI / merge UI) → clips/open-pr.rrweb.json.
  Committed `e57bb175` (flow + feature + tour + specs; pr-refinement story_test 6/6 GREEN).
- 2nd slidey usability fix: **embedded rrweb tour scenes now AUTOPLAY on activation**
  (was frozen on frame 0 until manual play). `VideoScene` passes autoplay to RrwebPlayer.
  **Merged to slidey main `0bc6f92`.** Verified: playhead advances 0.6s→4.2s hands-free,
  tour popover ("STEP 1 OF 13") renders.
- `dev-story-arc.json` → `dev-story-arc.html` (6.21MB self-contained, 3 clips inlined).
  Headless-verified: all 3 video scenes (bugfix/refine/PR) autoplay natively, 0 errors.
  This is a coherent SHIPPABLE deliverable: "developer arc — fix a bug, refine a
  feature, open the PR" on the slidey repo.

## Clip status (3 of 6 — the full DEVELOPER half)
- [x] slidey-bugfix (4), feature-refine (5), open-pr (6).
- [ ] pm-idea (1), architect-design (2), decomposition (3) — BLOCKED on parallel
  session's stories/bugfix + stories/delivery-tail (../cr conflict-resolve) breakage.

## Next
- QA the dev-story-arc deck (kitsoki-ui-qa) — the completed-portion proof.
- Resume blocked phase 1-3 captures when the ../cr tree breakage clears, then assemble
  the full 6-phase dev-story-hybrid.json + QA.
- Unblock phases 1-3 when parallel session integrates conflict-resolve (../cr); then
  run the already-authored slidey-pm-idea / slidey-architect-design captures + slice-3
  decomposition; assemble full dev-story-hybrid.json; run kitsoki-ui-qa.
- Author slidey-dev PRD→Design + decomposition instances (slices 3-4), mirroring
  the gears-rust external-target variant but bound to /Users/brad/code/slidey.
- Capture each phase tour → clips/*.rrweb.json → bundle dev-story-hybrid.json to HTML.
