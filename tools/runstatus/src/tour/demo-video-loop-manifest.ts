/**
 * "Demo-video loop" feature-tour manifest.
 *
 * A self-contained step array for the demo-video-loop video demo. Like the
 * cherny-loop manifest it specializes, the WHOLE video is tour-driven: it opens
 * on the home story library, frames the demo-video-loop story, drives a fresh run
 * (home → new session → drive view) via a route-match action step, then NARRATES
 * the autonomous run in the InteractiveView.
 *
 * KEY DIFFERENCE from cherny-loop: demo-video-loop's root `generating` CASCADES
 * on session entry — there is no configure/launch step. Creating the session
 * (RunInitialOnEnter) fires the whole loop, and in one-shot mode the emit_intent
 * chain auto-advances generating → qa → … → @exit:achieved before the operator
 * does anything. So beyond the 3-step home → new-session intro (copied verbatim
 * from cherny-loop), the tour is PURE NARRATION: every loop step is
 * kind:"explain", route:"any", dim:false, no target, narrating the resulting
 * InteractiveView trace + conversation.
 *
 * The story is driven NO-LLM via `kitsoki web --host-cassette
 * web_tour.cassette.yaml --mode one-shot` (nil harness — no free-text routing is
 * needed because the loop self-drives). The cassette stubs a compelling
 * fail-then-pass run over TWO iterations: a first cut that the deterministic
 * video gate passes but the kitsoki-ui-qa vision gate FAILS (a scenario the video
 * didn't show), the loop feeds the qa-report.md back to the maker, the maker
 * re-records to close the gap, and the second QA run PASSES → @exit:achieved.
 *
 * SINGLE SOURCE OF TRUTH: this array drives both the live tour overlay
 * (window.__startTourWithSteps) and the Playwright spec
 * (tests/playwright/demo-video-loop-video.spec.ts), which asserts each step's
 * `title` against the live popover so the two cannot drift.
 *
 * Targets are testids the home view ships: home-view, story-card,
 * new-session-btn. The loop steps anchor to nothing (centered-right narration).
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

export const DEMO_VIDEO_LOOP_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
  // Copied verbatim (shape) from cherny-loop's 3 intro steps.
  {
    id: "dvl-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins here in the story library. We'll demonstrate the demo-video loop — an agent that PRODUCES a demo video of a feature, then GATES it with a vision review, looping on failure until the video actually proves the feature.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5000,
  },
  {
    id: "dvl-intro-story",
    route: "home",
    target: "story-card",
    waitForTarget: "story-card",
    title: "The demo-video-loop story",
    body: "This story runs the loop: a maker records a tour demo video and writes its QA inputs, a deterministic gate validates the file, then the kitsoki-ui-qa vision review judges whether the video SHOWS each scenario. A budget guards against runaway cost. Every iteration is shown and recorded.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "dvl-intro-start",
    route: "home",
    target: "new-session-btn",
    waitForTarget: "new-session-btn",
    title: "Spin up a run",
    body: "Click New session to create a fresh, independently-traced run of the loop. The loop step IS the root here — no setup turn — so the whole run cascades the moment the session is created.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 3500,
  },
  {
    id: "dvl-intro-observe",
    route: "interactive",
    target: "observe-link",
    waitForTarget: "observe-link",
    title: "Watch it in the observer",
    body: "Open the observer to watch the run as a trace: the state diagram and the per-turn event timeline. Because this is a no-LLM cascade there's no operator chat — the TRACE is the story, every maker/gate/loop step a tracked turn.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "any",
    dwellMs: 3500,
  },

  // ── The loop, narrated on the observer (RunView) — trace + state diagram ─────
  // Loop narration sits OFF to the right with no dimming backdrop (dim: false),
  // so the conversation/trace stays fully visible and readable. There is NO drive
  // here — the run already completed autonomously on session entry.
  {
    id: "dvl-generating",
    route: "any",
    title: "The maker records — the gate validates",
    body: "In the trace, the first turn is the maker (an agent) recording a deterministic, no-LLM tour demo video and writing the QA feature.md + scenarios.yaml — then a script gate (host.run) checks the FILE itself: canonical name, watch-speed duration, frames present, written this turn. That gate is code: it can't be talked into passing.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "dvl-qa-fail",
    route: "any",
    title: "QA caught a problem",
    body: "The video file is valid, so the run hands it to the kitsoki-ui-qa vision review. Its EXIT CODE is the gate — and round 1 FAILS: a required scenario is `unsupported`, meaning no frame actually SHOWS it. A perfectly good video can still be the wrong evidence; that's exactly what this gate exists to catch.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 7000,
  },
  {
    id: "dvl-loop",
    route: "any",
    title: "The failure feeds the next maker turn",
    body: "On a failing verdict with budget left, the loop reads the qa-report.md and carries it BACK to the maker as feedback — no human in the inner loop. Round 2 is a distinct, tracked turn in the trace: the maker re-records to close the specific gap the report named.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "dvl-qa-pass",
    route: "any",
    title: "Re-recorded — and it passes",
    body: "The second cut adds the beat the report asked for, the video gate passes again, and this time the QA review returns PASS (exit 0) — the verdict surface now shows overall: pass. The loop ends ACHIEVED: the video doesn't just exist, it PROVES the feature.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "dvl-done",
    route: "any",
    title: "That's the demo-video loop",
    body: "A maker that records, a deterministic file gate, a vision gate that demands the video SHOW each scenario, and a budget that bounds it — looping on failure until the demo earns its PASS, every iteration tracked and recorded. Hit '?' to replay this tour.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
];
