/**
 * "Cherney loop" feature-tour manifest.
 *
 * A self-contained step array for the cherney-loop video demo. Like
 * report-bug-manifest.ts, the WHOLE video is tour-driven: it opens on the home
 * story library, frames the cherney-loop story, drives a fresh run (home → new
 * session → drive view) via a route-match action step, then walks the loop in
 * the InteractiveView — configure a goal + gate + budget, launch, and watch the
 * loop iterate (each iteration shown, the gate's failure fed forward) until the
 * iteration budget stops it.
 *
 * The story is driven NO-LLM via `kitsoki web --flow web_tour.yaml`, whose
 * host_handlers stub the maker (host.oracle.task), the script gate (host.run),
 * and the per-iteration artifact write (host.artifacts_dir). The flow's turns:
 * block is the Mode-2 proof of this exact path.
 *
 * SINGLE SOURCE OF TRUTH: this array drives both the live tour overlay
 * (window.__startTourWithSteps) and the Playwright spec
 * (tests/playwright/cherney-loop-video.spec.ts), which asserts each step's
 * `title` against the live popover so the two cannot drift.
 *
 * Targets are testids the home + InteractiveView ship: home-view, story-card,
 * new-session-btn, current-state, intent-btn-begin, intent-btn-launch,
 * intent-btn-evaluate.
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

export const CHERNEY_LOOP_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
  {
    id: "cl-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins here in the story library. We'll demonstrate the Cherney loop — an agent that iterates toward a goal until a gate proves it's met, or a budget stops it.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5000,
  },
  {
    id: "cl-intro-story",
    route: "home",
    target: "story-card",
    waitForTarget: "story-card",
    title: "The cherney-loop story",
    body: "This story runs the loop: a maker makes the smallest change toward your goal, a checker gates it, and a budget guards against runaway cost. Every iteration is shown and recorded.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "cl-intro-start",
    route: "home",
    target: "new-session-btn",
    waitForTarget: "new-session-btn",
    title: "Spin up a run",
    body: "Click New session to create a fresh, independently-traced run of the loop.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 3500,
  },

  // ── The loop, driven in the InteractiveView ─────────────────────────────────
  {
    id: "cl-welcome",
    route: "any",
    title: "Reason → act → gate → repeat",
    body: "A Cherney loop replaces prompting an agent with writing the loop that prompts it: define a goal, and the agent iterates until a gate says the goal is actually met — bounded by a budget so it can never run forever.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "cl-configure",
    route: "any",
    target: "current-state",
    waitForTarget: "current-state",
    title: "Set goal, gate, and budget",
    body: "A new run lands straight in configuration — no idle/begin step. We set a goal (make the unit tests pass), a script gate (a command that passes only on exit 0 — the checker is code, it can't be talked into passing), and a small iteration budget of 4.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "cl-launch",
    route: "any",
    target: "intent-btn-launch",
    waitForTarget: "intent-btn-launch",
    title: "Launch — iteration 1",
    body: "Launch runs the first maker iteration: the agent makes a change toward the goal. The status bar tracks iteration 1 of 4 and the running cost.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "cl-evaluate-1",
    route: "any",
    target: "intent-btn-evaluate",
    waitForTarget: "intent-btn-evaluate",
    title: "Gate it — and feed the failure back",
    body: "Evaluate runs the gate. It fails, so the loop captures the gate's reason and hands it to the next maker iteration as feedback — this is what makes the loop converge instead of blindly retrying. Watch the iteration counter advance to 2.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "cl-evaluate-2",
    route: "any",
    target: "intent-btn-evaluate",
    waitForTarget: "intent-btn-evaluate",
    title: "Iteration 3",
    body: "Each evaluate gates the current attempt and, on failure, runs the next iteration with the latest feedback. Iteration 3.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "cl-evaluate-3",
    route: "any",
    target: "intent-btn-evaluate",
    waitForTarget: "intent-btn-evaluate",
    title: "Iteration 4 — the last allowed",
    body: "Iteration 4. The budget is 4, so this is the final attempt the loop will make before the ceiling stops it.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    // Center (anchorless): this step's evaluate drives the loop to the terminal
    // __exit__exhausted state, which removes intent-btn-evaluate — a step
    // anchored to it would tear the overlay down. A center step has no anchor,
    // so the overlay survives the button vanishing.
    id: "cl-budget",
    route: "any",
    title: "Budget hit — the loop stops itself",
    body: "Iteration 4 fails the gate and the iteration ceiling is reached, so the loop terminates as exhausted rather than running forever. Goal-met OR budget-hit — always one or the other.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "cl-done",
    route: "any",
    title: "That's the Cherney loop",
    body: "A goal, a gate that deterministically proves it, a budget that bounds it, and every iteration shown and recorded — restartable and shareable. Swap the script gate for an adversarial oracle when the goal is prose, not a test. Hit '?' to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
];
