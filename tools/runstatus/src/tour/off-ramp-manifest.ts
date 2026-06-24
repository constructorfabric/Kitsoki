// Hand-authored tour manifest for the AGENT OFF-RAMP feature-spotlight video.
//
// Unlike the generated manifests under src/tour/generated/ (which are emitted
// from features/*.yaml), this one is hand-authored: the off-ramp demo has no
// feature-catalog entry yet, and the tour is driven only by the off-ramp video
// spec (tests/playwright/off-ramp-video.spec.ts), never the live overlay.
//
// THE FEATURE. In a room that opts into `agent_off_ramp:`, a free-text
// utterance the router can't map to a declared intent is handed to a voiced
// host.agent.converse turn INSTEAD of bouncing back with "I didn't catch
// that". The room stays put — NO state advance, NO world mutation — and the
// same menu is there next turn. The contrast that IS the feature: an off-menu
// QUESTION is answered in place; a menu PICK transitions.
//
// The whole video is tour-narrated (home → new session → chat → the off-ramp
// beat → the contrast), so even the intro is popover-narrated rather than
// silent spec orchestration. The spec performs the real interactions (type the
// off-menu question, click Send, click a menu item) as PRE-STEP hooks so each
// spotlighted surface and state exists before the spotlight lands.

import { type TourStep } from "./types.js";

export type { TourStep };

export const OFF_RAMP_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library ───────────────────────────────────────────────
  {
    id: "or-intro-home",
    route: "home",
    title: "The agent off-ramp",
    body: "Every kitsoki story is a deterministic state machine of declared intents. But what happens when a visitor types something the menu can't answer? This tour walks the off-ramp — the no-match door that answers a free-text question in place, without losing your spot.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5500,
  },
  {
    id: "or-intro-story",
    route: "home",
    target: "story-card",
    title: "The Welcome Desk",
    body: "This story is a single non-conversational menu room — the Welcome Desk. It has a real menu (browse / status / about) AND an implicit free-text composer. It opts into `agent_off_ramp:`, so an off-menu question gets a voiced answer instead of a bounce.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "story-card",
    dwellMs: 5500,
  },
  {
    id: "or-intro-start",
    route: "home",
    target: "new-session-btn",
    title: "Start a session",
    body: "Click New session to open a fresh run of the Welcome Desk.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    waitForTarget: "new-session-btn",
    dwellMs: 3500,
  },

  // ── The desk: menu + free-text invitation ──────────────────────────────────
  {
    id: "or-desk",
    route: "interactive",
    target: "current-state",
    title: "A menu AND free text",
    body: "We're in the `desk` room. The buttons below are the menu — browse, status, about. Beneath them sits a free-text box: the off-menu door. The room view literally says 'Off the menu — ask anything else in plain words.'",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "intent-btn-browse",
    dwellMs: 5500,
  },

  // ── THE OFF-RAMP BEAT — the star, dwell longest ────────────────────────────
  {
    id: "or-offramp",
    route: "interactive",
    target: "offramp-bubble",
    title: "Answered in place — no bounce",
    body: "We typed 'why should I trust an AI with my project?' — a question the menu can't map. Instead of 'I didn't catch that,' the off-ramp handed it to a voiced converse turn. The reply is marked '↪ off path': it answered, and nothing else moved.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "offramp-bubble",
    dwellMs: 7000,
  },
  {
    id: "or-no-advance",
    route: "interactive",
    target: "state-badge",
    title: "Same room, menu intact",
    body: "Look at the state badge — still `desk`. No transition, no world mutation. And the menu is exactly where it was: browse / status / about are all still here. The question was answered without losing your place.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "intent-btn-browse",
    dwellMs: 6000,
  },

  // ── THE CONTRAST — a menu pick transitions ─────────────────────────────────
  {
    id: "or-pick",
    route: "interactive",
    target: "state-badge",
    title: "A menu pick transitions",
    body: "Now the contrast that IS the feature: click a menu item — browse — and the room advances normally to the `catalogue`. Same room, two doors: a menu pick transitions; an off-menu question is answered in place. That's the off-ramp.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 6500,
  },
  {
    id: "or-done",
    route: "interactive",
    title: "That's the off-ramp",
    body: "One room, two behaviours: an off-menu question answered in place via a voiced converse turn (no state advance, menu intact), and a menu pick that transitions normally. The off-ramp turns every resting menu into a place you can also just ask a question.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
