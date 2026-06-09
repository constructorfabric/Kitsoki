/**
 * The in-app onboarding / feature-spotlight tour, as a single data-driven
 * manifest.
 *
 * DESIGN: this is a GENERIC, story-agnostic walkthrough of the web UI — it
 * anchors only to elements that exist on EVERY story and EVERY run, and never
 * waits on a specific story state or an LLM turn to complete. That keeps it
 * robust for any first-time user (any story, real LLM or no-LLM) and makes it
 * the place we highlight NEW features over time: to spotlight a new control,
 * append one `{ id, route, target, title, body }` entry below — no other code
 * changes.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives both
 *   1. the live tour overlay (src/components/tour/TourOverlay.vue, via the tour
 *      store), and
 *   2. the Playwright video demo (tests/playwright/tour-video.spec.ts), which
 *      runs this generic tour against the Oregon Trail story and records it.
 * The video asserts each step's `title` against the live popover, so the two
 * can't silently drift.
 *
 * Because the Node-based Playwright spec imports this module directly, it MUST
 * stay free of any Vue / Pinia / DOM-runtime import — plain types and data only.
 *
 * Robustness rules for new steps:
 *   - Anchor by a `data-testid` that is present on every story (top bar, chat,
 *     trace panels, global buttons) — NOT a story-specific intent/state.
 *   - Prefer `kind: "explain"` (advance on Next). Reserve `action` for cheap,
 *     universal gestures: navigation (`route-match`) or an immediate click
 *     (`click-target`). NEVER gate on `state-match` / `waitForState` — that
 *     couples the tour to LLM-turn latency and can strand it.
 */

/** Which route a step belongs to. Matched against the hash path by the overlay. */
export type TourRoute = "home" | "interactive" | "any";

/** How the current step is dismissed / advanced. */
export type AdvanceTrigger =
  | "next" // explain step: the user clicks the popover's Next
  | "click-target" // action step: the click on the real element is the signal
  | "route-match"; // action step: advance when the route becomes `advanceRoute`

export type Placement = "top" | "bottom" | "left" | "right" | "center";

export interface TourStep {
  /** Stable id; also the Playwright screenshot label. */
  id: string;

  /** Route this step lives on; the overlay holds until we're there. */
  route: TourRoute;

  /**
   * `data-testid` of the element to spotlight, resolved as
   * `[data-testid="<target>"]`. Omit for a centered, anchorless step. Must be a
   * UNIVERSAL testid (present on every story), not a story-specific one.
   */
  target?: string;

  /** Narrows an ambiguous target by visible text (rarely needed; keep generic). */
  targetText?: string;

  title: string;
  body: string;
  placement: Placement;

  /**
   * 'explain' — highlight + popover with Back / Skip / Next (read-only).
   * 'action' — the highlighted element is a REAL control; clicking it advances
   *            both the app and the tour. Keep these cheap & universal.
   */
  kind: "explain" | "action";

  /** When this step is considered done. 'explain' steps always use 'next'. */
  advance: AdvanceTrigger;

  /** Required when advance === 'route-match'. */
  advanceRoute?: TourRoute;

  /**
   * Gate BEFORE showing: wait until this testid exists in the DOM (it appears
   * after a route transition or hydration — both fast, NOT turn-dependent).
   */
  waitForTarget?: string;

  /** ms the video spec dwells on this step. The live UI ignores it. */
  dwellMs?: number;
}

export const TOUR_STEPS: readonly TourStep[] = [
  // ── Home ──────────────────────────────────────────────────────────────────
  {
    id: "home-welcome",
    route: "home",
    title: "Welcome to kitsoki",
    body: "kitsoki runs LLM workflows as deterministic, auditable state machines. This quick tour shows you around the UI. Use Back / Next, or Skip anytime — and replay it later from the “?” button.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 4000,
  },
  {
    id: "home-story-cards",
    route: "home",
    target: "story-card",
    title: "Your stories",
    body: "Each card is a story — a deterministic graph of rooms you can run. Pick any one to start a session.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "story-card",
    dwellMs: 3500,
  },
  {
    id: "home-rescan",
    route: "home",
    target: "rescan-btn",
    title: "Rescan the library",
    body: "Authored a new story on disk? Rescan re-reads the stories directory without restarting the server.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 3000,
  },
  {
    id: "home-start",
    route: "home",
    target: "new-session-btn",
    title: "Start a session",
    body: "Click the highlighted New session to spin up a fresh run. Each session gets its own trace.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 2500,
  },

  // ── A live run (universal controls — no story-specific anchors) ─────────────
  {
    id: "iv-current-state",
    route: "interactive",
    target: "current-state",
    title: "Where the run is",
    body: "The current room (state) of the run shows here, and updates after every turn.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 3500,
  },
  {
    id: "iv-chat",
    route: "interactive",
    target: "chat-section",
    title: "The conversation",
    body: "On the left, the story narrates and you respond — the same loop whether a human or an LLM is driving.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "chat-section",
    dwellMs: 3500,
  },
  {
    id: "iv-input",
    route: "interactive",
    target: "input-bar",
    title: "Drive the story",
    body: "Respond here — the room offers exactly the moves it allows as buttons, plus free-text where appropriate. Nothing invalid is possible. Try clicking an option, then carry on with the tour.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "input-bar",
    dwellMs: 4000,
  },
  {
    id: "iv-trace-diagram",
    route: "interactive",
    target: "trace-diagram",
    title: "The live trace — diagram",
    body: "On the right, the state diagram lights up the room you're in. Click any room to filter the timeline below.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "trace-diagram",
    dwellMs: 4000,
  },
  {
    id: "iv-trace-timeline",
    route: "interactive",
    target: "trace-timeline",
    title: "The live trace — timeline",
    body: "Every transition, decision, and host call lands here as a structured, replayable event. This is the audit record.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 4000,
  },
  {
    id: "iv-state-badge",
    route: "interactive",
    target: "state-badge",
    title: "Live or done",
    body: "This badge flips from “live” to “done” when the run reaches a terminal room.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 3000,
  },
  {
    id: "iv-observe",
    route: "interactive",
    target: "observe-link",
    title: "Observe mode",
    body: "Switch to a read-only observer view — handy for watching an LLM-driven run, or reviewing a finished one.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 3000,
  },
  {
    id: "iv-meta",
    route: "interactive",
    target: "meta-button",
    title: "Meta mode",
    body: "From here you can edit this story's YAML live, ask questions about it, or ask about kitsoki itself — without leaving the run.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-button",
    dwellMs: 3500,
  },
  {
    id: "tour-done",
    route: "interactive",
    title: "That's the tour!",
    body: "You've started a run and seen how to drive it, read the trace, and reach meta mode. Click the “?” button anytime to replay this — we'll use it to point out new features too.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 4000,
  },
];
