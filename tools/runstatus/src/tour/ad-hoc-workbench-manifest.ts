// Hand-authored tour manifest for the AD-HOC WORKBENCH epic feature-spotlight
// video. Copied in spirit from the golden agent-actions manifest and the
// hand-authored off-ramp manifest: the WHOLE video is tour-narrated, opening on
// the home story library and navigating home -> new session -> the chat view via
// route-match action steps so even the intro is popover-narrated rather than
// silent spec orchestration.
//
// Unlike the generated manifests under src/tour/generated/ (emitted from
// features/*.yaml), this is hand-authored: the ad-hoc-workbench epic has no
// feature-catalog entry yet, and the tour is driven only by the ad-hoc-workbench
// video spec (tests/playwright/ad-hoc-workbench-video.spec.ts), never the live
// overlay.
//
// THE EPIC, made legible. The ad-hoc workbench turns a free-form dev session into
// a project that mines itself into structure. This tour walks its four headline
// surfaces, every target anchored to a data-testid that ships today:
//
//   1. THE FREE-FORM LANDING ROOM (replaces `main`). A fresh run lands on the
//      REAL dev-story `landing` room (root: landing, stories/dev-story/rooms/
//      landing.yaml): it pairs QUICK-ACTION choice buttons (intent-actions /
//      intent-btn-go_ticket_search / go_bugfix / go_prd / …) with a free-text
//      floor — "pick an action, or just say what you want." This is the actual
//      workbench floor, not a lookalike.
//
//   2. PROTECTED MAIN + SCOPED WRITE OPT-IN. Source work happens in a managed
//      capsule based on staging/local; the primary checkout stays protected.
//      The first mutation still parks and asks. The opt-in is surfaced in the
//      SAME operator-question card the operator already answers
//      (operator-question-modal / oq-option-* / oq-submit): "May I change this?"
//      with accept / refine / dismiss. Answering it grants mutation capability
//      for the scoped capsule work (WriteModeGranted); it does not unlock main.
//
//   3. THE /mine PROPOSALS SURFACE. An ambient miner watches the session and,
//      when a recurring pattern is worth capturing as structure, raises a
//      proposal. It surfaces as a count pill in the chat topbar
//      (proposals-badge / proposals-badge-count); clicking it opens the proposal
//      in the same operator-question card (accept / refine / dismiss).
//
//   4. THE REFINE FLOW. Instead of accepting a proposal as-is, the operator
//      picks Refine — which opens the mined draft in story.edit meta-mode.
//      The operator narrows it in plain words; the agent reworks the draft into
//      a more precise structure (here: per-file incremental render instead of a
//      whole-site `make render`). story.edit applies it and live-reloads.
//
// Because the runtime miner and the write-mode gate need a real agent to PRODUCE
// these frames (which we never invoke in a no-LLM demo), the spec seeds them
// through the deterministic window.__pushOperatorQuestion / __pushProposal seams
// (registered by OperatorQuestionModal + InteractiveView onMounted) — the same
// seams the proposals.spec.ts and operator-ask-video.spec.ts regressions use, so
// the REAL badge + REAL card render with byte-stable content. The refine flow is
// seeded via window.__seedMetaRefine (meta.ts) which opens the meta overlay
// pre-loaded with the reworked transcript — no LLM, byte-stable frames.

import { type TourStep } from "./types.js";

export type { TourStep };

export const AD_HOC_WORKBENCH_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library (home) ─────────────────────────────────────────
  {
    id: "awb-intro-home",
    route: "home",
    title: "The ad-hoc workbench",
    body: "kitsoki normally runs a deterministic story graph. The ad-hoc workbench inverts that: you start in a free-form room, just work, and the session mines ITSELF into reusable structure — proposing intents, rooms, and gates as it learns your patterns. This tour walks the three surfaces that make that loop legible.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 6000,
  },
  {
    id: "awb-intro-story",
    route: "home",
    target: "story-card",
    title: "A free-form session",
    body: "We'll drive dev-story — the engineer's-day hub. Its root is the free-form `landing` room that replaced the old `main` menu: a single resting room offering a menu of quick actions (tickets, bugfix, prd, …) AND an open free-text floor.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "story-card",
    dwellMs: 5500,
  },
  {
    id: "awb-intro-start",
    route: "home",
    target: "new-session-btn",
    title: "Open a session",
    body: "Click New session to start a fresh workbench run. It opens directly in the interactive chat view — where the landing room, protected-main delivery model, write-mode opt-in, and proposals badge all live.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    waitForTarget: "new-session-btn",
    dwellMs: 4000,
  },

  // ── 1. THE FREE-FORM LANDING ROOM ───────────────────────────────────────────
  {
    id: "awb-landing",
    route: "interactive",
    target: "intent-actions",
    title: "The free-form landing room",
    body: "Here's the real dev-story landing room — the workbench floor that replaced the old `main` menu. It pairs QUICK-ACTION choice buttons — tickets, bugfix, implement, prd, code review — with a free-text floor beneath them. The whole point of the workbench: there is always a menu to pick from AND a box to just say what you want. No arrow-key picker, no dead-ends.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "intent-btn-go_ticket_search",
    dwellMs: 6500,
  },

  // ── 2. PROTECTED MAIN + SCOPED WRITE OPT-IN ─────────────────────────────────
  {
    id: "awb-writemode-intro",
    route: "interactive",
    target: "current-state",
    title: "Protected main, managed changes",
    body: "The primary checkout stays protected. Changes happen in a managed capsule based on `staging/local`, and the first mutation still parks for your explicit write-mode grant. Finished, validated work is committed and merged back to `staging/local` — never written straight to `main`.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 6000,
  },
  {
    id: "awb-writemode-card",
    route: "interactive",
    target: "oq-submit",
    title: "Approve a scoped capsule change",
    body: "The opt-in surfaces in the SAME operator-question card the operator already knows. The agent is parked, waiting: accept grants mutation capability for the requested capsule work (and emits WriteModeGranted), refine narrows the ask, and dismiss makes no change. This approval never unlocks the protected primary checkout or `main`.",
    placement: "left",
    kind: "action",
    advance: "click-target",
    waitForTarget: "oq-submit",
    dwellMs: 7000,
  },

  // ── 3. THE /mine PROPOSALS SURFACE ──────────────────────────────────────────
  {
    id: "awb-mine-intro",
    route: "interactive",
    target: "current-state",
    title: "The session mines itself",
    body: "While you work, an ambient miner watches the trace. When it spots a recurring pattern worth capturing as structure — a step you keep repeating, a check that should be a gate — it raises a proposal. Run `/mine` to nudge it, or just let it accrue in the background.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 6000,
  },
  {
    id: "awb-proposals-badge",
    route: "interactive",
    target: "proposals-badge",
    title: "The proposals badge",
    body: "A count pill appears in the chat topbar — the number of pending proposals. A parked write-mode opt-in turns it the attention (orange) colour; low-stakes structure proposals stay neutral. It hides when the queue is empty. Click it to review the head proposal.",
    placement: "bottom",
    kind: "action",
    advance: "click-target",
    waitForTarget: "proposals-badge-count",
    dwellMs: 6500,
  },
  {
    id: "awb-proposal-card",
    route: "interactive",
    target: "operator-question-modal",
    title: "Capture as structure?",
    body: "The proposal opens in the same operator-question card: here, “capture `make render` after every doc edit as a gate?” — a pattern the miner saw recur across sessions. Accept writes the structure (on-disk edit + meta-mode reload), refine adjusts the draft, dismiss drops it. The free-form session just taught the project a new trick.",
    placement: "left",
    kind: "explain",
    advance: "next",
    waitForTarget: "operator-question-modal",
    dwellMs: 7000,
  },

  // ── 4. THE REFINE FLOW ───────────────────────────────────────────────────────
  {
    id: "awb-refine-pick",
    route: "interactive",
    target: "oq-submit",
    title: "Refine — not just accept",
    body: "Accept would write the proposal as-is. But this pattern is too blunt: `make render` rebuilds the whole site on every doc edit. So we pick Refine — which opens the draft in meta-mode to shape it in plain words before it lands.",
    placement: "left",
    kind: "action",
    advance: "click-target",
    waitForTarget: "oq-submit",
    dwellMs: 6000,
  },
  {
    id: "awb-refine-open",
    route: "any",
    target: "meta-transcript",
    waitForTarget: "meta-overlay",
    title: "The draft, opened for editing",
    body: "Refine drops you into story.edit meta-mode with the mined draft preloaded — the same edit-and-reload surface meta-mode already uses. Here's the proposed gate: a single `make render` after every docs change.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "awb-refine-ask",
    route: "any",
    target: "meta-composer-input",
    title: "Say what should change",
    body: "You don't hand-write YAML — you say it: \"Only re-render the docs that changed in this edit, not the whole site.\" The refine instruction becomes the meta-mode turn.",
    placement: "top",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "awb-refine-result",
    route: "any",
    target: "meta-row-agent",
    title: "Beyond the original pattern",
    body: "The agent reworks the one-liner into a real script: diff the changed docs against the base, skip if none, and render only those files. That's structure the free-form session couldn't have guessed up front — earned from one refine.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "awb-refine-applied",
    route: "any",
    target: "meta-reload-note",
    title: "Applied, and flow-gated",
    body: "story.edit writes the refined gate and live-reloads — and the accept is gated on the no-LLM flow suite staying green (23/23). The refined structure is live; if it had regressed a fixture, it would revert and hold.",
    placement: "top",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },

  // ── Wrap ────────────────────────────────────────────────────────────────────
  {
    id: "awb-done",
    route: "interactive",
    title: "That's the ad-hoc workbench",
    body: "Three surfaces, one loop: a free-form landing room you can pick OR type into; protected-main delivery through a managed capsule with an operator-held mutation gate; and a proposals badge where the session's own mined structure accrues for one-gesture accept. You worked free-form, and the project mined itself into shape.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
];
