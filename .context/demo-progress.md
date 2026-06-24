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

## MILESTONE 3: dev-story-arc deck PASSED kitsoki-ui-qa (5/5) — committed cf2df55f
- kitsoki-ui-qa gate: **✅ PASS, 5/5 scenarios, 0 visual/annotation issues** (8
  advisory blank-scan = legit centered-content letterbox gutters). Report:
  `.artifacts/slidey-hybrid/qa/qa-report.md`.
- Two real bugs found+fixed by QA along the way:
  1. slidey `VideoScene` froze tour embeds on frame 0 → **autoplay fix, slidey main 0bc6f92**.
  2. cta scene rendered blank — my deck-authoring error: cta uses
     `wordmark`/`tagline`/`url` (+ `meta.mode: pitch`), not title/subtitle. Fixed in all decks.
- Deck specs committed (cf2df55f): dev-story-arc.json (the validated 3-phase
  developer arc) + dev-story-hybrid.json (full 6-phase, phases 1-3 pending unblock).

## DELIVERABLE STATE
- ✅ `dev-story-arc.html` — self-contained, autoplaying, QA-passed 3-phase deck
  (fix a bug → refine a feature → open the PR) on the slidey repo.
- 3 slidey main commits: faeffcf (rrweb inline), 19c8798 (real bug fix), 0bc6f92 (autoplay).
- ⛔ Full 6-phase deck blocked: phases 1-3 (PM/architect/decomposition) need the
  parallel session's stories/bugfix + delivery-tail (../cr conflict-resolve) to load.
  Authoring for phases 1-2 already done (stories/slidey-dev); captures resume on unblock.

## Unblock attempt (d7f9db88): committed-layer fixed; untracked pollution remains
- Fixed the committed triaged-exit bugs (triaging-set + importer mappings) →
  committed `d7f9db88`. The block narrowed to ONLY the parallel session's
  UNTRACKED `stories/delivery-tail/rooms/resolve.yaml` (+ resolve_* flows +
  `stories/conflict-resolve/`) which reference `../cr`. Committed delivery-tail is
  clean — the loader reads untracked rooms/*.yaml from disk, so their WIP pollutes
  every bf importer. NOT mine to touch (active WIP). Phases 1-3 unblock the moment
  they integrate or remove those untracked files.

## UNBLOCKED (4ff63d6): conflict-resolve integrated → phases 1-3 achievable
- Agent wired the orphaned conflict-resolve feature into delivery-tail (world keys,
  intents, `cr` import, integrate→resolve budget route) + found/fixed a real
  machine.go set-effect bool-coercion bug. Committed `4ff63d6`.
- Verified fresh-source: slidey-dev 3/3 PASS, delivery-tail 11/11, bugfix 58/58,
  conflict-resolve 2/2, ship-it 1/1, slidey-bugfix 1/1. Tree loads.
- ⚠️ In-session MCP story_test runs a STALE binary (pre-machine.go-fix) — validate
  via `go run ./cmd/kitsoki test flows` OR after `make build`/MCP reload.
- Rebuilt bin/kitsoki (picks up the fix) for the phase 1-3 captures.

## Resuming phases 1-3
- slice-3 capture specs ALREADY authored (slidey-pm-idea / slidey-architect-design
  -rrweb-capture.spec.ts + stories/slidey-dev + features/slidey-dev-prd-design.yaml).
  Run them → clips/pm-idea.rrweb.json, clips/architect-design.rrweb.json.
- phase-3 decomposition clip still to author+capture → clips/decomposition.rrweb.json.
- Then assemble full dev-story-hybrid.html (6 phases) + kitsoki-ui-qa.

## Phases 1-3 blocked by TWO real runtime gaps (diagnosed, to FIX not work around)
Flows are valid no-LLM (slidey-dev 3/3 via `go run test flows`), but the rrweb WEB
tour path diverges from `test flows`:
1. **`kitsoki web` ignores flow `initial_state`/`initial_world`** — web NewSession
   always starts at app root (`cmd/kitsoki/registry.go:244-246` orch.NewSession +
   InitialState); only `record`/`test flows` apply the fixture seed (`record.go:378`).
   → tours can't start at a seeded mid-pipeline state (architect `core.prd_published`,
   deliver seeded `epic_path`). Contained fix, unblocks phases 2-3.
2. **Global `core__work` (prio 45, slot request) intercepts conversational free-text**
   in `core.prd.idle` before the room's `default_intent: discuss` (prio 40) → PM idea
   utterance binds `work` → bounces to `core.landing`. default_intent is a
   deterministic tier AFTER semroute; in nil-harness web there's no interpreter to
   defer the content-bearing slot-bearing match to. Blocks phase 1 (conversational
   intake is the POINT — can't avoid free-text).
Why slidey-bugfix works: root: bf (boots into bf.idle), button-driven (no free-text,
no seed, no teleport). slidey-dev: root: core (→ landing) + conversational intake.

## Runtime fixes DONE → phases 1-3 capturable
- GAP 1 **FIXED** `5ed2ebd1`: `kitsoki web --flow` now honors flow `initial_state`/
  `initial_world` (seedFlowInitialState in registry.go, mirrors testrunner). Phases
  2-3 can seed a mid-pipeline start (architect core.prd_published, deliver epic_path).
- GAP 2 was a MISDIAGNOSIS by the clip agent — does NOT reproduce on main. Conversational
  free-text correctly sinks to default_intent:discuss (protected by 3952199f slot-bearing
  deferral). Pinned with regression test `85ff4917`. Phase 1 conversational intake WORKS;
  the prior clip drive just had a bug (likely a command-like utterance). Drive a genuinely
  conversational idea + assert it stays in/advances core.prd.idle (not bounce to landing).
- Binary rebuilt with both. In-session MCP stale (reload to pick up) — captures use bin/kitsoki.

## ROOT CAUSE for phases 1-3 (the real one): free-text needs the REPLAY harness
- After GAP1 fix + GAP2 (non-issue), the pm-idea WEB tour STILL stalls right after
  new-session (3 bootstrap frames, never reaches the idea utterance/PRD intake).
- Why: phases 1-3 are CONVERSATIONAL — the operator types a free-text idea, and the
  room routes it to `core__prd__discuss` needing the `message` slot EXTRACTED from
  free text. `--flow` (nil harness) cannot extract a slot from typed free text
  (only explicit-intent submission works — which is why `test flows` passes 3/3 but
  the live web tour can't). The button-driven phases (bugfix/refine/PR) work BECAUSE
  they drive choice-selector intents, no free text.
- The ESTABLISHED pattern for no-LLM free-text demos (memory: demo-free-text-needs-
  replay-harness): `kitsoki web --harness replay --recording <rec> --host-cassette`.
  Every clip agent used `--flow` — wrong harness for conversational phases.
- To produce phases 1-3 this way needs a RECORDING per phase (a real-LLM session
  recorded once, OR a hand-authored recording) for the replay harness. Distinct,
  non-trivial workstream (recordings + replay-harness capture specs).

## DELIVERED (this goal): QA-passed 3-phase developer-arc deck + 7 real fixes
- dev-story-arc.html (fix bug → refine → PR), kitsoki-ui-qa 5/5 PASS.
- slidey main: faeffcf (rrweb inline), 19c8798 (grid-cards bug), 0bc6f92 (autoplay).
- kitsoki: 1523c732/aeddb833/e57bb175 (3 phase instances), d7f9db88 (triaged-exit),
  4ff63d6 (conflict-resolve integ + machine.go coercion), 5ed2ebd1 (web flow-seed),
  85ff4917 (routing regression test), cf2df55f (deck+QA).

## Next: decide — invest in replay-harness recordings for phases 1-3, or finalize arc.
- When ../cr breakage clears: verify importer triaged-exit fixes green; run the
  authored slidey-pm-idea / slidey-architect-design captures; build phase-3
  decomposition clip; assemble + QA the full dev-story-hybrid.html.
- Resume blocked phase 1-3 captures when the ../cr tree breakage clears, then assemble
  the full 6-phase dev-story-hybrid.json + QA.
- Unblock phases 1-3 when parallel session integrates conflict-resolve (../cr); then
  run the already-authored slidey-pm-idea / slidey-architect-design captures + slice-3
  decomposition; assemble full dev-story-hybrid.json; run kitsoki-ui-qa.
- Author slidey-dev PRD→Design + decomposition instances (slices 3-4), mirroring
  the gears-rust external-target variant but bound to /Users/brad/code/slidey.
- Capture each phase tour → clips/*.rrweb.json → bundle dev-story-hybrid.json to HTML.
