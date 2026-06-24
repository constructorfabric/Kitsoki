/**
 * Spatial-oracle feature-spotlight tour manifest.
 *
 * A self-contained step array for the dedicated spatial-oracle video demo. The
 * spatial surfaces live on the /review feedback page and the chrome-less /point
 * handoff window — NOT on the story-library/observer surface the agent-actions
 * tour walks — so the recording is NOT driven by the kitsoki tour overlay
 * (window.__startTourWithSteps). It is narrated with the PORTABLE caption +
 * spotlight helpers (tests/playwright/_helpers/demo.ts → makeCaption /
 * makeSpotlight), the same posture the gh-issue-review external act uses for a
 * page outside the kitsoki SPA shell.
 *
 * The walkthrough demonstrates, deterministically (no LLM — the offpath oracle
 * is STUBBED via page.route, mirroring spatial-capture.spec.ts), the shipped
 * behaviour:
 *
 *   1. /review opens with a video player + chapter timeline + flag list.
 *   2. Flag a scene → a flag is selected → the SpatialPicker mounts over the
 *      player frame.
 *   3. Click a point on the frame → pins a crosshair (sp-point) and resolves the
 *      DOM element under the click into a {selector, role, text} chip (fd-element).
 *   4. Type a question into the per-flag composer (fd-chat-box) and Ask
 *      (fd-chat-send) → the STUBBED oracle answer renders in the chat alongside
 *      the captured frame thumbnail (fd-still) and the element chip.
 *   5. The chrome-less /point handoff window (PointPage.vue) is the same picker +
 *      composer stripped to a transient single-purpose window for the TUI OSC-8
 *      handoff.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives the Playwright video spec
 * (tests/playwright/spatial-oracle-video.spec.ts), which asserts each step's
 * `title` against the on-screen caption — a drift guard, mirroring how the
 * agent-actions / report-bug specs assert the popover title against the manifest.
 *
 * Like the other manifests this file MUST stay free of Vue / Pinia / DOM-runtime
 * imports — plain types and data only. Every `target` here is a testid the
 * feature actually ships: review-page, rp-frame, rp-replay-frame, rp-player,
 * chapter-timeline, ct-marker-intro, ct-flag-btn, flag-detail, spatial-picker,
 * sp-point, fd-element, fd-still, fd-chat-box, fd-chat-send, fd-chat, point-page,
 * pp-frame, pp-input, pp-send.
 */

import { type TourStep } from "./manifest.js";

// Re-export so the Playwright spec can import the step type alongside the array
// from this one module (mirrors the agent-actions / report-bug manifests).
export type { TourStep };

export const SPATIAL_ORACLE_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro ───────────────────────────────────────────────────────────────────
  {
    id: "so-intro",
    route: "any",
    title: "Point at what you mean",
    body: "kitsoki lets you ask the oracle about a SPOT on a rendered frame — not just in words. Click a video or screenshot, kitsoki resolves the DOM element under your click, and the question rides that {frame, point, element} bundle to a read-only oracle. This tour walks the whole spatial capture flow.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "review-page",
    dwellMs: 5500,
  },
  {
    id: "so-review",
    route: "any",
    target: "rp-frame",
    title: "The review surface",
    body: "The /review page shows a recorded run with a chapter timeline beneath it. When the capture carries a recorded rrweb session, the frame is the REAL reconstructed UI — not an opaque video — so you can point at actual controls. Each marker is a scene you can seek to, select, and flag for discussion.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "rp-frame",
    dwellMs: 5000,
  },
  {
    id: "so-flag",
    route: "any",
    target: "chapter-timeline",
    title: "Flag a scene",
    body: "Pick a chapter marker and hit 'Flag this'. The flag is selected — and selecting a flag mounts the spatial picker as a transparent overlay over the player frame, and eagerly captures the still for that moment.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "chapter-timeline",
    dwellMs: 5000,
  },
  {
    id: "so-picker",
    route: "any",
    target: "spatial-picker",
    title: "The spatial picker",
    body: "This transparent overlay sits over the frame. A click pins a point; a drag draws a box. Over a reconstructed rrweb session the picker resolves the click against the replay iframe's own DOM — so it returns the REAL app element under your cursor (the Start intent button here), not a pixel.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "spatial-picker",
    dwellMs: 5500,
  },
  {
    id: "so-point",
    route: "any",
    target: "sp-point",
    title: "Click pins a crosshair",
    body: "Clicking the frame pins a crosshair at that point — mapped from the overlay's rendered rect into the frame's natural pixels, so it tracks the frame at any CSS scale.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "sp-point",
    dwellMs: 5000,
  },
  {
    id: "so-element",
    route: "any",
    target: "fd-element",
    title: "Resolved to an element chip",
    body: "The reconstructed-DOM element under the click resolves into a typed 'pointing at:' chip — selector (here the Start intent button), role, and truncated text. That structured element, not just an (x,y), is what the oracle receives.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "fd-element",
    dwellMs: 5500,
  },
  {
    id: "so-ask",
    route: "any",
    target: "fd-chat-box",
    title: "Ask about this spot",
    body: "Type a question into the per-flag composer and click Ask. The question dispatches to the read-only off-path oracle carrying the visual bundle — the frame handle, the point, and the resolved element.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "fd-chat-box",
    dwellMs: 5000,
  },
  {
    id: "so-answer",
    route: "any",
    target: "fd-chat",
    title: "The answer, with context",
    body: "The oracle's answer renders inline in the chat — beside the captured frame thumbnail and the element chip, so the spot you asked about and the answer stay together. Deterministic and free: the oracle is stubbed for this tour.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "fd-chat",
    dwellMs: 5500,
  },
  // ── The chrome-less /point handoff window ────────────────────────────────────
  {
    id: "so-point-window",
    route: "any",
    target: "point-page",
    title: "The /point handoff window",
    body: "The same picker + composer, stripped to a transient single-purpose window. When a TUI operator needs to point at something, the terminal prints an OSC-8 link to /point?chromeless=1; this window opens with just the frame, the picker, and a composer, and POSTs the bundle back to resolve the parked turn.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "point-page",
    dwellMs: 5500,
  },
  {
    id: "so-done",
    route: "any",
    title: "That's spatial capture",
    body: "You flagged a scene, pointed at a spot, kitsoki resolved the element under it, and the question carried that {frame, point, element} bundle to a read-only oracle — on the review surface and in the chrome-less TUI handoff window. All deterministic, no LLM, no cost.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
