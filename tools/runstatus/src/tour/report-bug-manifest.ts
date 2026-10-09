/**
 * "Report bug" Meta-menu feature-spotlight tour manifest.
 *
 * A self-contained step array for the dedicated report-bug video demo. Like
 * agent-actions-manifest.ts, this tour opens on the home story library,
 * explains the demo story, drives a fresh run via route-match action steps
 * (home -> new session -> observer), then walks the "Report bug" feature AS
 * IMPLEMENTED:
 *
 * Clicking the Meta launcher's "Report bug" item no longer files silently. It
 * CAPTURES (rrweb session replay + console/errors + a browser-observed,
 * server-scrubbed HAR via the runstatus.bug.preview RPC) and opens a REVIEW MODAL. The single
 * visual is the rrweb session replay — an interactive, scrubbable reconstruction
 * of the exact DOM the operator saw. The operator inspects that replay, the
 * scrubbed HAR (summary + expandable raw JSON), and console/error state, edits
 * the prefilled title + optional description, then clicks "File bug" (or
 * Cancel). Only on "File bug" does the server write
 * .artifacts/issues/bugs/<id>.md plus a sibling <id>.artifacts/ folder,
 * surfacing the filed path in a result toast.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives both
 *   1. the live tour overlay (started via window.__startTourWithSteps), and
 *   2. the Playwright video spec
 *      (tests/playwright/report-bug-video.spec.ts).
 * The video asserts each step's `title` against the live popover, so the two
 * cannot silently drift.
 *
 * Like manifest.ts this file MUST stay free of Vue / Pinia / DOM-runtime
 * imports — plain types and data only. Every `target` / `waitForTarget` here is
 * a testid the Meta launcher (MetaButton.vue), the review modal
 * (BugReportModal.vue, a Teleport-to-body overlay), and the universal observer
 * surface actually ship: meta-button, meta-menu, meta-report-bug, bug-modal,
 * bug-modal-replay, bug-modal-har-summary, bug-modal-har-raw-toggle,
 * bug-modal-har-raw, bug-modal-description, bug-modal-submit, bug-report-toast,
 * bug-toast-path.
 */

import { type TourStep } from "./manifest.js";

// Re-export so the Playwright spec can import the step type alongside the array
// from this one module (mirrors how the agent-actions spec imports from its
// manifest).
export type { TourStep };

export const REPORT_BUG_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
  // These home/interactive steps mean the WHOLE video is tour-driven: the
  // intro explains where the feature lives and why before we reach the
  // observer, and the navigation itself (home → new session → observer) is
  // performed by route-match action steps, not silent spec orchestration.
  {
    id: "bug-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins here, in the story library — each card is a deterministic story graph kitsoki runs the same way every time. We'll demonstrate the capture-and-review bug reporter on the bug-fix pipeline.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5000,
  },
  {
    id: "bug-intro-story",
    route: "home",
    target: "story-card",
    waitForTarget: "story-card",
    title: "The bug-fix pipeline",
    body: "This story hands a ticket to an autofix agent that reads the repo, edits code, and runs the build, then a judge gates the result. If a run ever lands somewhere surprising, the bug reporter captures exactly what you were looking at.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "bug-intro-start",
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
    id: "bug-intro-observe",
    route: "interactive",
    target: "observe-link",
    waitForTarget: "observe-link",
    title: "Switch to the observer",
    body: "The bug reporter lives on the global Meta launcher, available everywhere — including the read-only observer view. Switch to it to open this run.",
    placement: "bottom",
    kind: "action",
    advance: "route-match",
    advanceRoute: "any",
    dwellMs: 4000,
  },

  // ── Introduction ──────────────────────────────────────────────────────────
  {
    id: "bug-welcome",
    route: "any",
    title: "Capture, review, then file",
    body: "Hit a surprising state? The Meta launcher's Report bug action captures an interactive session replay of exactly what you saw, the console, and a browser-observed network trace — scrubbed of secrets — then opens a review modal so you can check it all before anything is written. This tour walks the whole flow.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── The Meta launcher ──────────────────────────────────────────────────────
  {
    id: "bug-meta-button",
    route: "any",
    target: "meta-button",
    waitForTarget: "meta-button",
    title: "The Meta launcher",
    body: "The Meta button sits fixed in the bottom-right corner of every live view. Besides story edit and Q&A, it hosts the bug reporter. Click it to open the menu.",
    placement: "left",
    kind: "action",
    advance: "click-target",
    dwellMs: 4000,
  },
  {
    id: "bug-report-item",
    route: "any",
    target: "meta-report-bug",
    waitForTarget: "meta-report-bug",
    title: "Report bug",
    body: "Pinned below the divider — always available, on any route. Clicking it captures the rrweb session replay, the console, and a server-scrubbed HAR, then opens the review modal. Nothing is filed yet. Click it to capture.",
    placement: "left",
    kind: "action",
    advance: "click-target",
    dwellMs: 4500,
  },

  // ── The review modal ───────────────────────────────────────────────────────
  {
    id: "bug-modal",
    route: "any",
    target: "bug-modal",
    waitForTarget: "bug-modal",
    title: "Review before filing",
    body: "Capture done — and instead of filing silently, the reporter opens this review modal. You see everything that would be attached and stay in control: file it, or cancel and nothing is written.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "bug-replay",
    route: "any",
    target: "bug-modal-replay",
    waitForTarget: "bug-modal-replay",
    title: "The session replay",
    body: "The visual evidence is an rrweb session replay — a faithful, scrubbable reconstruction of the exact DOM you saw when you hit the bug. Play it back or scrub to an earlier frame to show precisely what happened, pixel-accurate to your run.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "bug-har",
    route: "any",
    target: "bug-modal-har-raw",
    waitForTarget: "bug-modal-har-summary",
    title: "The scrubbed network trace",
    body: "The browser records the network exchanges it observed, then the server runs the LLM-free harscrub anonymizer over them. The summary lists each request; expand the raw HAR JSON to confirm exactly what — secret-free — would be attached.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "bug-describe",
    route: "any",
    target: "bug-modal-description",
    waitForTarget: "bug-modal-description",
    title: "Add a description",
    body: "The title comes prefilled with 'Bug report'. Add an optional description of what went wrong and what you expected — it's written into the issue alongside the capture.",
    placement: "top",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "bug-submit",
    route: "any",
    target: "bug-modal-submit",
    waitForTarget: "bug-modal-submit",
    title: "File the bug",
    body: "Now click File bug. Only now does the server write the issue and copy the held capture — the scrubbed HAR and the session replay — into its artifacts folder.",
    placement: "top",
    kind: "action",
    advance: "click-target",
    dwellMs: 4500,
  },

  // ── The result toast ───────────────────────────────────────────────────────
  {
    id: "bug-filed",
    route: "any",
    target: "bug-toast-path",
    waitForTarget: "bug-toast-path",
    title: "Filed, with a path",
    body: "Done. The toast shows the filed path — .artifacts/issues/bugs/<id>.md — backed by a sibling <id>.artifacts/ folder holding the scrubbed HAR and the session replay. A reviewed, reproducible, secret-free local artifact bug report.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },

  // ── Wrap-up ────────────────────────────────────────────────────────────────
  {
    id: "bug-done",
    route: "any",
    title: "That's the bug reporter",
    body: "One click off the Meta launcher captured an interactive session replay of your screen, the console, and a scrubbed network trace, let you review every attachment, and filed a markdown issue with its artifacts on your confirmation — everything an engineer needs to reproduce the problem, with nothing sensitive leaked. Hit '?' anytime to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
