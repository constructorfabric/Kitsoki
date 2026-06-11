/**
 * Mockup Video Studio — full story-walkthrough tour manifest.
 *
 * A self-contained TourStep array for the mockup-video feature demo. Like the
 * golden agent-actions tour, the WHOLE video is tour-driven: it opens on the
 * home story library, frames the mockup-video story card, drives a fresh run
 * (home → new session), then walks the produce→review→refine loop room by room
 * on the interactive route — intake (brief discovery), authoring (the authored
 * deck), rendering (deterministic render), REVIEW (the inline produced video
 * actually plays + the /review feedback pointer), then DONE (the gallery: the
 * final video + the scenarios it covers).
 *
 * Navigation and every advancing story turn are performed by the overlay's own
 * action steps: `route-match` for home → interactive, and `click-target` on the
 * on-camera `intent-btn-<intent>` buttons for each story turn (a real click
 * renders the turn result directly — no RPC-then-reload race). Narration steps
 * are `kind: "explain"` (Next advances) and rest the camera via dwellMs.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives both
 *   1. the live overlay (window.__startTourWithSteps), and
 *   2. the Playwright video spec (tests/playwright/mockup-video-demo.spec.ts),
 * which asserts each step's `title` against the live popover, so the two cannot
 * silently drift.
 *
 * Every `target` / `waitForTarget` is a UNIVERSAL drive/observe testid that the
 * story rooms actually ship via the shared SPA chrome (home-view, story-card,
 * new-session-btn; chat-section, current-state, state-badge, intent-btn-*; and
 * the media <video> the review/done rooms render — data-testid="media-video").
 * Story rooms carry no feature-specific testids, so we anchor to these.
 *
 * Like manifest.ts, this file MUST stay free of Vue / Pinia / DOM-runtime
 * imports — plain types and data only.
 */

import { type TourStep } from "./manifest.js";

// Re-export so the Playwright spec can import the step type alongside the array
// from this one module (mirrors the agent-actions / trace manifests).
export type { TourStep };

export const MOCKUP_VIDEO_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
  {
    id: "mv-intro-home",
    route: "home",
    title: "Mockup Video Studio",
    body: "Mockup Video Studio is a kitsoki story that authors a walkthrough video of a feature, lets you review it, and refines the source behind any moment you flag. It begins here, in the story library — each card is a deterministic story graph kitsoki runs the same way every time.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5500,
  },
  {
    id: "mv-intro-story",
    route: "home",
    target: "story-card",
    targetText: "mockup",
    waitForTarget: "story-card",
    title: "The mockup-video story",
    body: "This story walks the produce → review → refine loop: discover a brief, author the source, render a video deterministically, review it inline, and accept or flag-and-refine. Let's run it.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "mv-intro-start",
    route: "home",
    target: "new-session-btn",
    targetText: "mockup",
    waitForTarget: "new-session-btn",
    title: "Spin up a run",
    body: "Click New session to create a fresh, independently-traced run of the studio.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 3500,
  },

  // ── intake: brief discovery ─────────────────────────────────────────────────
  {
    id: "mv-intake",
    route: "interactive",
    target: "current-state",
    waitForTarget: "current-state",
    title: "Intake — discover the brief",
    body: "The run opens in intake: a conversation, not a form. You describe the feature to mock and the user scenarios to walk; an interviewer agent sharpens it. When the brief is concrete, the next step distils it and advances.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "mv-intake-ready",
    route: "interactive",
    target: "intent-btn-ready",
    waitForTarget: "intent-btn-ready",
    title: "Brief is ready",
    body: "Click ready: the conversation is distilled into a structured brief, a concreteness gate checks it, and the run advances to authoring.",
    placement: "top",
    kind: "action",
    advance: "click-target",
    dwellMs: 4000,
  },

  // ── authoring: the authored source ──────────────────────────────────────────
  {
    id: "mv-authoring",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Authoring the source",
    body: "One author agent turns the brief into the video source — here a slidey deck (a tour medium would produce static HTML mockup pages + a tour manifest). The authored kind, file count, and summary are shown. This is the only interpretive step; everything downstream is deterministic.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "mv-authoring-accept",
    route: "interactive",
    target: "intent-btn-accept",
    waitForTarget: "intent-btn-accept",
    title: "Accept the source",
    body: "Accept the authored source to render it into a walkthrough video. Rendering runs deterministically and auto-advances to review once the video handle is bound.",
    placement: "top",
    kind: "action",
    advance: "click-target",
    dwellMs: 4000,
  },

  // ── review: the produced video plays inline ─────────────────────────────────
  {
    id: "mv-review-state",
    route: "interactive",
    target: "state-badge",
    waitForTarget: "state-badge",
    title: "Review",
    body: "Rendering produced the walkthrough deterministically and routed straight here, to review. The video handle is content-addressed and resolved through the artifact journal — the same seam /artifact and /review serve.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "mv-review-video",
    route: "interactive",
    target: "media-video",
    waitForTarget: "media-video",
    title: "The walkthrough, playing inline",
    body: "Here is the produced video, playing inline in the review room — real rendered bytes served from the resolved handle, not a placeholder. The room also points to /review, where you scrub the video, flag a scene or time range, grab the frame, and dispatch a feedback note that refines the source behind that moment.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 7000,
  },
  {
    id: "mv-review-accept",
    route: "interactive",
    target: "intent-btn-accept",
    waitForTarget: "intent-btn-accept",
    title: "Accept the walkthrough",
    body: "This walkthrough is good — accept it to finish. (Instead you could refine on a flagged moment, which edits the source behind it and re-renders.)",
    placement: "top",
    kind: "action",
    advance: "click-target",
    dwellMs: 4000,
  },

  // ── done: the gallery ───────────────────────────────────────────────────────
  {
    id: "mv-done-video",
    route: "interactive",
    target: "media-video",
    waitForTarget: "media-video",
    title: "Done — the final walkthrough",
    body: "The accepted walkthrough, in the done gallery — the same real video, playing inline.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "mv-done",
    route: "interactive",
    title: "That's the loop",
    body: "You've seen the full Mockup Video Studio loop: discover a brief, author the source, render a video deterministically, review it inline, and accept — with /review feedback to flag-and-refine any moment. The gallery lists the scenarios it covers and the refine passes taken. Hit '?' anytime to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
