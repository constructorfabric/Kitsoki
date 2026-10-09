/**
 * "Cherny loop" feature-tour manifest.
 *
 * A self-contained step array for the cherny-loop video demo. Like
 * report-bug-manifest.ts, the WHOLE video is tour-driven: it opens on the home
 * story library, frames the cherny-loop story, drives a fresh run (home → new
 * session → drive view) via a route-match action step, then walks the loop in
 * the InteractiveView — configure a goal + gate + budget, launch, and watch the
 * loop iterate (each iteration shown, the gate's failure fed forward) until the
 * iteration budget stops it.
 *
 * The story is driven NO-LLM via `kitsoki web --flow web_tour.yaml`, whose
 * host_handlers stub the maker (host.agent.task), the script gate (host.run),
 * and the per-iteration artifact write (host.artifacts_dir). The flow's turns:
 * block is the Mode-2 proof of this exact path.
 *
 * SINGLE SOURCE OF TRUTH: this array drives both the live tour overlay
 * (window.__startTourWithSteps) and the Playwright spec
 * (tests/playwright/cherny-loop-video.spec.ts), which asserts each step's
 * `title` against the live popover so the two cannot drift.
 *
 * Targets are testids the home + InteractiveView ship: home-view, story-card,
 * new-session-btn, current-state, intent-btn-launch, intent-btn-proceed,
 * intent-btn-evaluate.
 *
 * The loop now proves the gate is RED before any maker spend: launch enters the
 * `baseline` room (runs the gate once on the unchanged artifact), and `proceed`
 * starts the maker loop only once that baseline is confirmed failing.
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

export const CHERNY_LOOP_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
  {
    id: "cl-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins here in the story library. We'll demonstrate the Cherny loop — an agent that iterates toward a goal until a gate proves it's met, or a budget stops it.",
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
    title: "The cherny-loop story",
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
  // Loop narration sits OFF to the right so the conversation/trace stays fully
  // visible and readable while it runs.
  {
    id: "cl-welcome",
    route: "any",
    title: "Write the loop, not the prompt",
    body: "A Cherny loop replaces prompting an agent with writing the loop that prompts it: name a goal, and the agent iterates — make, gate, repeat — until a gate proves the goal is actually met, or a budget stops it. You kick it off once; it runs itself.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "cl-goal",
    route: "any",
    target: "text-floor-input",
    waitForTarget: "text-floor-input",
    title: "Say it in your own words",
    body: "The first message is just what you want, as free text: the command you need to go green. Here, `go test ./internal/ratelimit` — a flaky rate limiter. The story script-gates the loop on exactly that command: the checker is code, it passes only on exit 0.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "cl-launch",
    route: "any",
    target: "intent-btn-launch",
    waitForTarget: "intent-btn-launch",
    title: "One launch — then hands off",
    body: "There's your message — the goal in plain words, now a script-gated loop. Launch is the only button you press: it proves the gate is RED first (never spend on a gate that can't fail), then runs the whole loop on its own — make, gate, feed the failure forward, repeat. No prodding between iterations.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 8500,
  },
  {
    id: "cl-converge",
    route: "any",
    title: "Watch it converge by itself",
    body: "The loop ran three iterations unattended, each fixing the failure the gate named and surfacing the next, deeper one: burst rejected → tokens never refill → a data race. Every step is a tracked turn in the trace — make, gate, loop — with no human in the inner loop.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "cl-budget",
    route: "any",
    title: "Budget hit — it stops itself",
    body: "Three iterations was the budget, so the loop halts as exhausted rather than running forever — mid-convergence, one bug from green. That's the guard: goal-met OR budget-hit, always one or the other. Bump the budget to let it finish.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "cl-done",
    route: "any",
    title: "That's the Cherny loop",
    body: "A goal in plain words, a gate that deterministically proves it, a budget that bounds it — and the loop runs itself, every iteration tracked and recorded. Swap the script gate for an adversarial agent when the goal is prose, not a test. Hit '?' to replay this tour.",
    placement: "right",
    dim: false,
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
];
