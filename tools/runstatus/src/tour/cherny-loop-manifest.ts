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
 * host_handlers stub the maker (host.oracle.task), the script gate (host.run),
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
  {
    id: "cl-welcome",
    route: "any",
    title: "Reason → act → gate → repeat",
    body: "A Cherny loop replaces prompting an agent with writing the loop that prompts it: define a goal, and the agent iterates until a gate says the goal is actually met — bounded by a budget so it can never run forever.",
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
    body: "A new run lands straight in configuration — no idle/begin step. Our goal: get a flaky token-bucket rate limiter's tests green. The artifact under test is `internal/ratelimit/limiter.go`, and the gate is a script shown verbatim — `go test ./internal/ratelimit/`. The checker is code you can read, and it passes only on exit 0, so it can't be talked into passing. Budget: 4 iterations.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "cl-launch",
    route: "any",
    target: "intent-btn-launch",
    waitForTarget: "intent-btn-launch",
    title: "Launch — but prove the gate fails first",
    body: "Launch does NOT spend on the maker yet. It first runs the gate ONCE on the unchanged artifact — the red-before-green discipline. Why pay an agent to chase a gate that can't even fail? We prove it's RED first.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "cl-baseline",
    route: "any",
    target: "current-state",
    waitForTarget: "current-state",
    title: "RED confirmed — the gate fails today",
    body: "The baseline ran the exact command and it failed (exit 1): `TestAllow_Burst` — a burst of 5 is rejected at request #4. The real go-test output is shown verbatim. The gate works and there's a concrete bug to fix, so it's safe to spend budget. That RED failure becomes the first feedback handed to the maker. Proceed.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "cl-evaluate-1",
    route: "any",
    target: "intent-btn-evaluate",
    waitForTarget: "intent-btn-evaluate",
    title: "Iteration 1 → 2: a different, deeper failure",
    body: "Iteration 1's maker sized the bucket to the burst, acting on the baseline burst failure. Evaluate gates it: the burst test passes now, but a NEW failure surfaces — `TestAllow_Refill`: tokens never refill. The loop captures that reason and hands it to iteration 2. It's converging on real, distinct bugs — not blindly retrying the same one.",
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
    title: "Iteration 2 → 3: from refill to a data race",
    body: "Iteration 2 added time-based refill — so the refill test passes, but now `TestAllow_Concurrent` exposes a data race: 12 of 100 goroutines double-spend a token. Each evaluate gates the current fix and feeds the next failure forward. Iteration 3 will take a lock to close the race.",
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
    title: "Iteration 3 → 4: the lock caused a deadlock",
    body: "Iteration 3's mutex killed the race — but it held the lock across a sleep, so the concurrency test now times out (a deadlock). Iteration 4 moves the sleep outside the lock. The budget is 4, so this is the final attempt before the ceiling stops the loop.",
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
    title: "Budget hit — one assertion short",
    body: "Iteration 4 got it down to a single off-by-one under contention — but that's iteration 4 of 4, so the ceiling stops the loop as exhausted rather than letting it run forever. This is exactly why budgets exist: real, visible progress, stopped one assertion short. Bump the budget (or keep going) to finish. Goal-met OR budget-hit — always one or the other.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "cl-done",
    route: "any",
    title: "That's the Cherny loop",
    body: "A goal, a gate that deterministically proves it, a budget that bounds it, and every iteration shown and recorded — restartable and shareable. Swap the script gate for an adversarial oracle when the goal is prose, not a test. Hit '?' to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
];
