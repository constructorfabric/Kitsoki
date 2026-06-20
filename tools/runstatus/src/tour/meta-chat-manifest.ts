/**
 * Meta-chat persistence + launcher-status feature-spotlight tour manifest.
 *
 * HAND-AUTHORED (not code-generated): this manifest narrates the meta-mode chat
 * persistence feature — an in-flight meta conversation now survives close/reopen,
 * and the ✦ Meta launcher shows status badges (a working ⟳ while a turn streams,
 * a ready ● when a reply is waiting).
 *
 * ONE annotation style throughout: every narrated moment is a tour popover. The
 * tour overlay paints at z-index 1500 (popover 1600) while the meta overlay sits
 * at z-index 1000, so a tour popover renders ABOVE the meta overlay and CAN
 * spotlight meta-overlay / meta-row-streaming / meta-button / meta-status-busy /
 * meta-status-ready. There is no z-index blocker — the matching video spec
 * (meta-chat-video.spec.ts) performs each open/close/send action, then syncs the
 * overlay to the corresponding step here (window.__tourGoTo) and asserts its
 * title, so the WHOLE walk is tour-narrated with no banner captions.
 *
 * The four-step home → story → new session → chat intro mirrors the golden
 * agent-actions tour so the opening stays narrated rather than a silent
 * page.goto. The remaining `kind:"explain"` steps are advanced manually by the
 * spec (it drives the choreography, then jumps the overlay to the matching step).
 */
import { type TourStep } from "./types.js";

// Re-export so the Playwright spec imports the step type alongside the array.
export type { TourStep };

export const META_CHAT_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: home → story → new session → chat (tour-narrated navigation) ─────
  {
    id: "mc-intro-home",
    route: "home",
    title: "Meta chat that doesn't lose your place",
    body: "kitsoki's meta chat lets you ask about (or edit) a story without leaving the run. This tour shows two new things: an in-flight meta conversation now PERSISTS across close and reopen, and the ✦ Meta launcher shows status badges so you know when a reply is working or waiting — even with the overlay closed. We'll use the bug-fix pipeline.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5500,
  },
  {
    id: "mc-intro-story",
    route: "home",
    target: "story-card",
    title: "The bug-fix pipeline",
    body: "We'll open a run of this story, then talk to the meta agent ABOUT it — read-only Story Q&A. The meta turn streams the agent's thinking and a tool call, paced so you can watch it work.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "story-card",
    dwellMs: 5000,
  },
  {
    id: "mc-new-session",
    route: "home",
    target: "new-session-btn",
    title: "Spin up a run",
    body: "Click New session to create a fresh run. It opens directly in the chat, where the ✦ Meta launcher lives in the bottom-right corner.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    waitForTarget: "new-session-btn",
    dwellMs: 4000,
  },
  {
    id: "mc-launcher",
    route: "interactive",
    target: "meta-button",
    title: "The ✦ Meta launcher",
    body: "Here it is — bottom-right, always present. In a moment we'll open a Story Q&A chat, start a turn streaming, and close the overlay while it's still working. Watch THIS button: it grows a spinning ⟳ badge while a meta chat is busy, then a green ● when a reply is waiting.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-button",
    dwellMs: 6000,
  },

  // ── Persistence / badge choreography — driven by the spec, narrated here ────
  // From mc-open-mode onward the spec performs each action (open the overlay,
  // send, close, reopen), then jumps the overlay to the matching step and
  // asserts its title. These stay `kind:"explain"` so the overlay never tries to
  // advance them itself — the spec controls the sequencing.
  {
    id: "mc-open-mode",
    route: "interactive",
    target: "meta-overlay",
    title: "Open a Story Q&A chat",
    body: "A read-only conversation with an agent that can inspect the loaded story — opened from the ✦ Meta launcher's menu.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-overlay",
    dwellMs: 3000,
  },
  {
    id: "mc-stream",
    route: "interactive",
    target: "meta-row-streaming",
    title: "The turn streams live",
    body: "Ask the agent and the turn streams in the bubble — 🧠 thinking and a Read tool call arrive, paced by the deterministic stub agent.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-row-streaming",
    dwellMs: 3200,
  },
  {
    // Spotlights the launcher button (which HOSTS the transient ⟳ badge) rather
    // than the badge testid itself — the busy window is short, so anchoring to
    // the always-present button keeps the popover stable while the body names
    // the badge.
    id: "mc-close-busy",
    route: "interactive",
    target: "meta-button",
    title: "Close while it works — ⟳ busy",
    body: "Close the overlay while the turn is still streaming: it keeps running, it does not abort. The launcher grows a spinning ⟳ badge so you know a meta chat is busy even with the overlay shut.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-button",
    dwellMs: 4000,
  },
  {
    id: "mc-reopen",
    route: "interactive",
    target: "meta-overlay",
    title: "Reopen — nothing lost",
    body: "Reopen the overlay and you land right where you left off: the same question, the same in-flight turn, as if it was never closed. Persistence is per (session, mode) scope.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-overlay",
    dwellMs: 3500,
  },
  {
    id: "mc-resolved",
    route: "interactive",
    target: "meta-overlay",
    title: "The turn resolves consistently",
    body: "We watch this turn finish: the streaming bubble dissolves into the agent's reply within the same conversation — the chat never reset.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-overlay",
    dwellMs: 3500,
  },
  {
    // Same as mc-close-busy: anchor to the launcher button that hosts the ●
    // ready badge, not the transient badge testid.
    id: "mc-ready",
    route: "interactive",
    target: "meta-button",
    title: "A reply waiting — ● ready",
    body: "Ask again, then close immediately and let the turn finish while the overlay is shut. The launcher first shows ⟳, then flips to a green ● the moment the answer lands — a reply is waiting for you.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-button",
    dwellMs: 4000,
  },
  {
    id: "mc-ready-cleared",
    route: "interactive",
    target: "meta-overlay",
    title: "Reopen — the badge clears",
    body: "Reopening the chat marks the reply seen, so the ● badge goes away. Viewing a scope clears its waiting flag.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-overlay",
    dwellMs: 3500,
  },
  {
    id: "mc-both-badges",
    route: "interactive",
    target: "meta-button",
    title: "Both at once — one waiting, one working",
    body: "Distinct modes hold distinct state: one scope can show ● a reply waiting while another shows ⟳ a turn still streaming — both badges on the launcher together.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-button",
    dwellMs: 3500,
  },
  {
    id: "mc-done",
    route: "interactive",
    target: "meta-button",
    title: "Your meta chat never loses its place",
    body: "Stream it, close it, come back later — the conversation and its status are always there. Hit '?' anytime to replay this tour.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "meta-button",
    dwellMs: 3500,
  },
];

/**
 * The id of the first choreography step. The intro steps (before this id) are
 * walked through the tour overlay's Next button; from this id onward the spec
 * drives the action and jumps the overlay to the matching step.
 */
export const MC_FIRST_CHOREO_STEP = "mc-open-mode";
