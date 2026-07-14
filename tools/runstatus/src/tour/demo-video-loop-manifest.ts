/**
 * "Demo-video loop" feature-tour manifest.
 *
 * A self-contained step array for the demo-video-loop video demo. Like the
 * cherny-loop manifest it specializes, the WHOLE video is tour-driven: it opens
 * on the home story library, frames the demo-video-loop story, drives a fresh run
 * (home → new session → the chat/InteractiveView) via a route-match action step,
 * then NARRATES the autonomous run ON THE CONVERSATION SURFACE.
 *
 * KEY DIFFERENCE from cherny-loop: demo-video-loop's root `generating` CASCADES
 * on session entry — there is no configure/launch step. Creating the session
 * (RunInitialOnEnter) fires the whole loop, and in one-shot mode the emit_intent
 * chain auto-advances generating → qa → … → @exit:achieved before the operator
 * does anything. So beyond the 3-step home → new-session intro, the tour is PURE
 * NARRATION: every loop step is kind:"explain", route:"any", dim:false, no
 * target, narrating the run as it reads in the CONVERSATION column.
 *
 * WHY THE CONVERSATION (not the observer/trace): the run self-drives with no
 * operator input, so its progress would otherwise live only in the developer
 * trace. The runtime surfaces each `say:` breadcrumb as a "Loop" conversation
 * bubble (see stores/run.ts chatEntries + ChatTranscript narration role), so the
 * autonomous run reads as a followable conversation — Iteration 1/5 → QA FAIL →
 * Iteration 2/5 → QA PASS → achieved — right beside the live trace. The camera
 * stays on the InteractiveView the whole time; we never leave for the observer.
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
 * new-session-btn. The loop steps anchor to nothing (centered-right narration so
 * the conversation column on the left stays fully visible).
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

export const DEMO_VIDEO_LOOP_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
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
    body: "Click New session to create a fresh, independently-traced run. The loop step IS the root here — no setup turn — so the whole run cascades the moment the session is created, with NO operator input. We watch it unfold in the conversation.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 3500,
  },

  // ── The loop, narrated on the CONVERSATION (the chat transcript) ────────────
  // Narration sits OFF to the right so the left "Conversation" column stays
  // fully visible and readable. There is NO
  // drive here — the run already completed autonomously on session entry, and
  // each step surfaced as a "Loop" conversation bubble.
  {
    id: "dvl-generating",
    route: "any",
    title: "The run narrates itself — no input needed",
    body: "Even with NO operator input, the conversation fills with the run's progress. The first \"Loop\" message reads `Iteration 1/5 · …` — the maker (an agent) recorded a deterministic, no-LLM tour video and wrote the QA feature.md + scenarios.yaml, then a script gate checked the FILE itself. On the right, the trace is expanded to prove it: the maker's submitted artifact and the deterministic video gate returning PASS — that gate is code; it can't be talked into passing.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
    trace: {
      match: ["recorded the demo-video-loop tour", "video gate pass (round 1)"],
      highlight: ["video gate pass (round 1)", "demo-video-loop-demo.mp4"],
    },
  },
  {
    id: "dvl-qa-fail",
    route: "any",
    title: "QA caught a problem",
    body: "The video file is valid, so the run hands it to the kitsoki-ui-qa vision review — its EXIT CODE is the gate. The trace expands the QA gate's return: `exit_code: 1`, a required scenario `unsupported` (no frame actually SHOWED it). A perfectly good video can still be the wrong evidence; that's exactly what this gate exists to catch.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 7000,
    trace: {
      match: "qa fail:",
      highlight: ["exit_code", "unsupported"],
    },
  },
  {
    id: "dvl-loop",
    route: "any",
    title: "The failure feeds the next maker turn",
    body: "On a failing verdict with budget left, the loop carries the qa-report.md BACK to the maker as feedback — no human in the inner loop. The trace expands the report (note its `Fix:` line) and the next maker's submitted summary: it re-recorded to close the specific gap the report named. Two iterations, legible in both the conversation and the trace.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
    trace: {
      match: ["qa report — demo-video-loop", "addressed qa feedback"],
      highlight: ["fix:", "addressed qa feedback"],
    },
  },
  {
    id: "dvl-qa-pass",
    route: "any",
    title: "Re-recorded — and it passes",
    body: "The second cut adds the beat the report asked for, the video gate passes again, and this time the QA review returns `exit_code: 0`. The trace expands the round-2 gate — `QA PASS 4/4 — overall: pass` — and the conversation shows `QA iteration 2 → PASS ✓`. The loop ends ACHIEVED: the video doesn't just exist, it PROVES the feature.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
    trace: {
      match: ["qa pass 4/4", "video gate pass (round 2)"],
      highlight: ["qa pass 4/4", "overall: pass"],
    },
  },
  {
    id: "dvl-done",
    route: "any",
    title: "That's the demo-video loop",
    body: "A maker that records, a deterministic file gate, a vision gate that demands the video SHOW each scenario, and a budget that bounds it — looping on failure until the demo earns its PASS, every iteration narrated in the conversation AND tracked in the trace. Hit '?' to replay this tour.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
];
