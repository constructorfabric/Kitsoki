/**
 * External-target PRD → Design feature-spotlight tour manifest.
 *
 * A self-contained step array for the gears-rust POC video: the SAME dev-story
 * hub that drives kitsoki's own work, here pointed at a FOREIGN repo
 * (constructorfabric/gears-rust). The tour opens on the home story library,
 * frames the gears-rust instance, drives a fresh run home → new session → the
 * interactive /chat view, then walks the PRD → Design CONVERSATION in the main
 * chat: author a PRD by talking it through — including the multi-round
 * clarification that grounds it — watch it publish into the gears-rust checkout
 * under the fixed gears-sdlc name (gears/notes-service/docs/PRD.md), carry it
 * into the design intake, refine the design brief, and publish the gears-sdlc
 * DESIGN alongside it. One continuous walk that lands conforming docs in an
 * external tree with no engine or story change, only the doc-profile keys.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives both
 *   1. the live overlay (window.__startTourWithSteps), and
 *   2. the Playwright video spec (tests/playwright/gears-prd-design.spec.ts).
 * The video asserts each step's `title` against the live popover, so the two
 * cannot silently drift.
 *
 * The pipeline-advancing interactions run as PRE-STEP HOOKS in the spec, but
 * unlike an RPC-driven demo they are driven THROUGH THE PAGE (composer +
 * intent buttons) so each turn renders into the chat the spotlight then frames
 * — the conversation IS the demo. Every `target` / `waitForTarget` is a testid
 * the chat surface actually ships: home-view, story-card, new-session-btn
 * (home) and chat-transcript (InteractiveView).
 *
 * Like manifest.ts this file MUST stay free of Vue / Pinia / DOM-runtime
 * imports — plain types and data only.
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

export const GEARS_PRD_DESIGN_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ────────
  {
    id: "gr-intro-home",
    route: "home",
    title: "Drive an external project",
    body: "kitsoki's dev-story hub authors kitsoki's own PRDs and designs. The same hub can target a FOREIGN repo — here, the Rust monorepo constructorfabric/gears-rust — and drive its PRD → Design spec chain. Nothing in the engine or the story changes; a handful of doc-profile keys retarget where docs land and what shape they take.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5500,
  },
  {
    id: "gr-intro-story",
    route: "home",
    target: "story-card",
    waitForTarget: "story-card",
    title: "The gears-rust instance",
    body: "This is kitsoki-dev with one thing changed: the profile points dev-story at a gears-rust checkout, so PRDs and designs publish into gears/<gear>/docs/ as gears-sdlc PRD.md / DESIGN.md — not kitsoki's own flat docs tree.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "gr-new-session",
    route: "home",
    target: "new-session-btn",
    waitForTarget: "new-session-btn",
    title: "Spin up a run",
    body: "Click New session to start a fresh run of the hub against gears-rust. It opens in the chat, on the engineer's-day landing.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 3500,
  },

  // ── The PRD: discovery → multi-round clarification → draft ─────────────────
  {
    id: "gr-prd-discovery",
    route: "interactive",
    target: "chat-transcript",
    waitForTarget: "chat-transcript",
    title: "Talk through the PRD",
    body: "`prd` opens a discovery conversation, not a form. We're authoring a concrete gear — notes-service — so the talk stays grounded in what it must do: the interviewer takes the pitch and sharpens it into a crisp problem statement before any questions or drafting begin.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "gr-prd-clarify",
    route: "interactive",
    target: "chat-transcript",
    waitForTarget: "chat-transcript",
    title: "Clarify, in rounds",
    body: "Discovery isn't one-shot. A structured clarification round asks the questions that most change the PRD — actors, the success metric — and the operator answers in the chat. The brief can loop back for ANOTHER round (here, tenant isolation and admin visibility), so each round sharpens the spec before drafting. The accumulated Q&A becomes the PRD's grounding.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "gr-prd-draft",
    route: "interactive",
    target: "chat-transcript",
    waitForTarget: "chat-transcript",
    title: "Review the drafted PRD",
    body: "With the clarification rounds settled and a prior-art scan done, the author writes the PRD into a per-session workspace and surfaces it right here — title, confidence, and the full draft to review. Accept publishes it.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "gr-published",
    route: "interactive",
    target: "chat-transcript",
    waitForTarget: "chat-transcript",
    title: "Published into the gears tree",
    body: "Here is the retarget made concrete: the PRD did not land in kitsoki's docs/prd/ — the chat reports it published to gears/notes-service/docs/PRD.md, the fixed gears-sdlc name, inside the gears-rust checkout. The landing offers `continue` to carry it into the design phase.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },

  // ── The PRD → Design handoff, brief refinement, and the DESIGN ─────────────
  {
    id: "gr-design-intake",
    route: "interactive",
    target: "chat-transcript",
    waitForTarget: "chat-transcript",
    title: "Carry it into design",
    body: "`continue` seeds the design intake with a pointer to the PRD we just published — the chat shows the design conversation opening from exactly where the PRD left off, grounded in the doc, not a blank idea.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "gr-design-refine",
    route: "interactive",
    target: "chat-transcript",
    waitForTarget: "chat-transcript",
    title: "Refine the design brief",
    body: "Design has its own clarification loop: the brief is refined conversationally — the operator folds in notes (thread the cpt-IDs, add the component model) and the refiner reworks it — then a quality check gates it before the author drafts. Same iterative shaping the PRD did with questions.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },
  {
    id: "gr-design-done",
    route: "interactive",
    target: "chat-transcript",
    waitForTarget: "chat-transcript",
    title: "DESIGN, alongside the PRD",
    body: "The design author reads gears-rust's own DESIGN template and threads cpt-IDs, and the chat reports it published to gears/notes-service/docs/DESIGN.md — beside the PRD, no kitsoki feature ticket. PRD → Design is one continuous, grounded walk against the external repo.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 6500,
  },

  // ── Wrap-up ────────────────────────────────────────────────────────────────
  {
    id: "gr-done",
    route: "any",
    title: "Retargeted, not rebuilt",
    body: "That's the proof of concept: dev-story's PRD → Design walk — discovery, multi-round clarification, brief refinement, publish — pointed at a foreign repo by configuration alone, landing conforming gears-sdlc docs in gears/notes-service/docs/. A second target needs only a new profile. Hit '?' anytime to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
