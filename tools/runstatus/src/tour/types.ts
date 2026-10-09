/**
 * The tour-step vocabulary shared by every tour manifest.
 *
 * Manifests under src/tour/generated/ are CODE-GENERATED from the feature
 * catalog (features/*.yaml at the repo root) — edit the YAML and run
 * `make features`, never the generated files. This module is the one
 * hand-written home of the step types; it MUST stay free of any Vue / Pinia /
 * DOM-runtime import because the Node-based Playwright specs import the
 * generated manifests (and through them, this module) directly.
 *
 * Robustness rules for new steps (enforced by the feature-catalog schema):
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

/**
 * A single self-driving action a tour step performs against the live UI before
 * (or as) it advances. This is the declarative capture of what the Playwright
 * demo `.spec.ts` files do imperatively (typeAndSend / clickIntent /
 * waitForState / revealTurn / dwell), so the binary `kitsoki tour` renderer —
 * which cannot read a `.spec.ts` — can drive any demo from data alone.
 *
 *   - type-and-send: fill the composer with `text` and click send.
 *   - click-intent:  click the `intent-btn-<intent>` button.
 *   - wait-state:    poll the interactive view's current-state until it equals
 *                    `state` (the deterministic, no-LLM settle point).
 *   - reveal-turn:   ease the last turn up to the top of the chat, hold, then
 *                    ease down through the reply — the per-turn reading rhythm.
 *   - dwell-ms:      hold on the current frame for `ms` (pace-scaled).
 *
 * Mirror of internal/tour DriveAction (Go). Keep the two in lockstep.
 */
export type DriveAction =
  | { type: "type-and-send"; text: string }
  | { type: "click-intent"; intent: string }
  | { type: "wait-state"; state: string }
  | { type: "reveal-turn" }
  | { type: "dwell-ms"; ms: number };

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

  /**
   * Optional ordered self-driving actions the `kitsoki tour` renderer executes
   * for this step (composer fills, intent clicks, state waits, per-turn
   * reveals, dwells). The live UI tour overlay IGNORES this — it is render-time
   * driving data only, the data form of the Playwright spec's imperative logic.
   */
  drive?: DriveAction[];

  /**
   * Deprecated compatibility field. Tour overlays never dim the page now: a step
   * renders its popover plus, when anchored, a highlight ring so the UI remains
   * visible in demos. Existing hand-written manifests may still carry dim:false.
   */
  dim?: boolean;

  /**
   * Optional trace-storytelling for this beat: drive the live TraceTimeline to
   * EXPAND the rows that prove this step and PULSE the specific fields that
   * matter, so the trace panel narrates alongside the conversation instead of
   * showing a wall of collapsed rows. Applied via window.__tourTrace by the
   * video spec (and any renderer that opts in); the live operator overlay
   * ignores it. `match` expands every row whose searchable text (msg + attrs +
   * the merged host call's args/return + narration) contains a substring;
   * `exclude` disambiguates (e.g. two identical-looking gate rows across rounds);
   * `highlight` pulses fields inside the expanded bodies that contain a term.
   */
  trace?: {
    match: string | string[];
    exclude?: string;
    highlight?: string[];
  };
}
