/**
 * Trace-introspection feature-spotlight tour manifest.
 *
 * A self-contained step array for the dedicated trace-features video demo. The
 * tour opens on the home story library, explains the bug-fix pipeline, drives a
 * fresh run via route-match action steps (home → new session → observer), then
 * walks every trace-introspection capability: view modes, waterfall, category
 * chips, decision-first detail, confidence bar, alternatives, annotation, replay.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives both
 *   1. the trace-features overlay (started via window.__startTourWithSteps), and
 *   2. the Playwright video spec (tests/playwright/trace-features-video.spec.ts).
 *
 * Like manifest.ts this file MUST stay free of Vue / Pinia / DOM-runtime imports
 * — plain types and data only.
 */

import { type TourStep } from "./manifest.js";

export const TRACE_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
  // These home/interactive steps mean the WHOLE video is tour-driven: the
  // intro explains where the feature lives and why before we ever reach the
  // observer, and the navigation itself (home → new session → observer) is
  // performed by route-match action steps, not silent spec orchestration.
  {
    id: "trace-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins here. A story is a deterministic graph of rooms that kitsoki runs the same way every time, recording each decision and call as it goes. We'll demonstrate trace introspection on the bug-fix pipeline.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5000,
  },
  {
    id: "trace-intro-story",
    route: "home",
    target: "story-card",
    waitForTarget: "story-card",
    title: "The bug-fix pipeline",
    body: "This story triages a ticket, lets an agent patch the code, then judges the result against a confidence gate — looping until it passes. It exercises every kind of decision, oracle call, and host call, which makes it the ideal run to introspect.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "trace-intro-start",
    route: "home",
    target: "new-session-btn",
    waitForTarget: "new-session-btn",
    title: "Spin up a run",
    body: "Click New session to create a fresh, independently-traced run of the pipeline.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 3500,
  },
  {
    id: "trace-intro-observe",
    route: "interactive",
    target: "observe-link",
    waitForTarget: "observe-link",
    title: "Switch to the observer",
    body: "The trace tools live in the read-only observer view — built for inspecting a run while it's live or after it's finished. Switch to it to introspect this run.",
    placement: "bottom",
    kind: "action",
    advance: "route-match",
    advanceRoute: "any",
    dwellMs: 4000,
  },

  // ── Introduction ──────────────────────────────────────────────────────────
  {
    id: "trace-welcome",
    route: "any",
    title: "Trace introspection",
    body: "kitsoki records every decision, oracle call, host call, and world mutation as a structured, immutable event stream. This tour walks the full introspection stack — everything you can see, annotate, and replay from a finished run.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 4500,
  },

  // ── View modes ───────────────────────────────────────────────────────────
  {
    id: "trace-view-modes",
    route: "any",
    target: "view-mode-tabs",
    waitForTarget: "view-mode-tabs",
    title: "Three views, one trace",
    body: "Tree shows the event sequence, Timeline is a latency waterfall, Graph is the state diagram — three co-equal projections of the same immutable event stream. Switch anytime.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "trace-timeline-tab",
    route: "any",
    target: "tab-timeline",
    title: "Switch to the waterfall",
    body: "Click the Timeline tab to see a duration-proportional waterfall. Each bar's width is the call's duration_ms within its turn window.",
    placement: "bottom",
    kind: "action",
    advance: "click-target",
    dwellMs: 3000,
  },
  {
    id: "trace-waterfall",
    route: "any",
    target: "waterfall-bar",
    waitForTarget: "waterfall-bar",
    title: "Latency at a glance",
    body: "Bars are colored by observation kind — purple for decisions, blue for oracle calls, amber for host calls. A bottleneck stands out immediately instead of requiring manual millisecond arithmetic.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "trace-tree-tab",
    route: "any",
    target: "tab-tree",
    title: "Back to the event tree",
    body: "Switch back to Tree view to see each event in sequence with its kind badge.",
    placement: "bottom",
    kind: "action",
    advance: "click-target",
    dwellMs: 3000,
  },

  // ── Category chips ────────────────────────────────────────────────────────
  {
    id: "trace-category-chips",
    route: "any",
    target: "category-filter-chips",
    waitForTarget: "category-filter-chips",
    title: "Observation kind taxonomy",
    body: "Every event has a semantic kind: decision, oracle-call, host-call, narration, world-mutation, routing, or lifecycle. Click a chip to filter down to one category — the same taxonomy drives row colors, the waterfall, and a future graph layout.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },

  // ── Decision detail ───────────────────────────────────────────────────────
  {
    id: "trace-decision-detail",
    route: "any",
    target: "decide-verdict",
    title: "Decision-first detail",
    body: "For a gate or routing event, the pane leads with the verdict — available intents, chosen intent, confidence, reason — not the raw prompt. The prompt is still there as a collapsed evidence drawer.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "trace-confidence-bar",
    route: "any",
    target: "confidence-bar",
    waitForTarget: "confidence-bar",
    title: "Confidence vs threshold",
    body: "The bar fills to the model's confidence and marks the configured threshold with a tick. Green means the engine auto-fired; red means it bailed to human review.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Alternatives ──────────────────────────────────────────────────────────
  {
    id: "trace-alternatives",
    route: "any",
    target: "decide-verdict",
    title: "Runner-up alternatives",
    body: "Below the winning verdict, the pane lists every runner-up intent the model considered — with its own confidence score. Seeing the margin between winner and second-place reveals whether the decision was crisp or borderline.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Annotation ────────────────────────────────────────────────────────────
  {
    id: "trace-annotation",
    route: "any",
    target: "annotate-button",
    waitForTarget: "annotate-button",
    title: "Annotate a decision",
    body: "Score, label, or comment on any gate or oracle call. Annotations are stored in a sidecar stream — the deterministic trace is unchanged, but every scored decision becomes a labeled training datapoint.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },

  // ── Replay ────────────────────────────────────────────────────────────────
  {
    id: "trace-replay",
    route: "any",
    target: "replay-button",
    waitForTarget: "replay-button",
    title: "Replay against a different operator",
    body: "Re-run one recorded oracle call against a different LLM or local model and diff the verdict — without touching the original run. This makes the pluggable-operator moat visible: same recorded input, different operator, auditable output diff.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },

  // ── Wrap-up ───────────────────────────────────────────────────────────────
  {
    id: "trace-done",
    route: "any",
    title: "That's the full picture",
    body: "You've seen the complete trace-introspection stack: semantic kind taxonomy, latency waterfall, decision-first detail, confidence bars, runner-up alternatives, human annotation, and single-call replay. Hit '?' anytime to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
