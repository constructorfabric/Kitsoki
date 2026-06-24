/**
 * Slidey-edit "annotate then refine" feature-tour manifest.
 *
 * A self-contained step array for the dedicated slidey-edit video demo. It
 * spotlights the NEW unified-annotation surfaces shipped on this branch:
 *
 *   - the ArtifactAnnotator mounted on a media element (ViewElement.vue's
 *     "Annotate" affordance, data-testid `media-annotate`),
 *   - the SemanticOverlay markers drawn from the deck's `<name>.semantic.json`
 *     sidecar over a poster backdrop (the `aa-slidey` stage → `aa-slidey-poster`
 *     + `semantic-overlay` with one `so-marker-<ref>` per declared element), and
 *   - the location-tied annotate → refine loop: pointing at a deck element emits
 *     a `semantic_element` anchor, and `refine` edits the exact scene behind it.
 *
 * The whole video is tour-narrated, mirroring the golden agent-actions /
 * dev-story-bugfix manifests: it opens on the home story library, its one
 * `route-match` action step navigates home → the drive (chat) view, then the
 * explain beats narrate the reviewing room → Annotate → overlay → pick → refine
 * walk while the spec drives the matching intents between beats.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives the live overlay
 * (window.__startTourWithSteps) AND the Playwright video spec
 * (tests/playwright/slidey-edit-video.spec.ts), which asserts each step's
 * `title` against the on-screen popover — a drift guard.
 *
 * Like the other hand-written manifests this file MUST stay free of any Vue /
 * Pinia / DOM-runtime import — plain types and data only. Every `target` here is
 * a testid the feature actually ships:
 *   home-view, story-card, new-session-btn,                       (intro)
 *   current-state, media-element, media-annotate,                 (reviewing + affordance)
 *   aa-slidey, aa-slidey-poster, semantic-overlay, so-marker-1/card_0.  (overlay)
 */

import { type TourStep } from "./types.js";

// Re-export so the Playwright spec can import the step type alongside the array.
export type { TourStep };

export const SLIDEY_EDIT_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library ────────────────────────────────────────────────
  {
    id: "se-intro-home",
    route: "home",
    title: "Annotate a rendered deck, then refine it",
    body: "Every run begins here, in the story library. We'll walk the slidey-edit pipeline: author a slide deck, render it to a real MP4, review it, then point at a SPOT on the rendered deck and refine the exact scene you pointed at — all deterministic, no LLM.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5500,
  },
  {
    id: "se-intro-story",
    route: "home",
    target: "story-card",
    title: "The slidey deck editor",
    body: "This story authors a 3-scene explainer deck, renders it with the slidey pipeline (which also emits a semantic sidecar describing every named element and its box), then opens it for a location-tied review.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "story-card",
    dwellMs: 5000,
  },
  {
    id: "se-intro-start",
    route: "home",
    target: "new-session-btn",
    title: "Spin up a run",
    body: "Click New session to create a fresh run of the deck editor on the drive view.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    waitForTarget: "new-session-btn",
    dwellMs: 4000,
  },

  // ── Author: the operator's request (shown typed, on camera) ─────────────────
  {
    id: "se-author",
    route: "any",
    target: "composer-input",
    title: "Author the deck",
    body: "Type the request in the composer — a short explainer deck. The text you see here IS the input the run receives; kitsoki authors a 3-scene slidey spec from it, then we accept it to render.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "composer-input",
    dwellMs: 6000,
  },

  // ── Drafting: review the authored plan BEFORE rendering ─────────────────────
  {
    id: "se-drafting",
    route: "any",
    target: "current-state",
    title: "Review the authored deck",
    body: "kitsoki authored a 3-scene deck spec from your request — the Summary above shows what it built. Review the plan, then accept the `accept → rendering` step to render it to a real MP4. Nothing is approved sight-unseen: you read the plan here, and you'll review the rendered deck next.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 6000,
  },

  // ── Reviewing: the rendered deck ────────────────────────────────────────────
  {
    id: "se-reviewing",
    route: "any",
    target: "media-element",
    title: "The rendered deck, in review",
    body: "We authored and accepted the deck, and now the reviewing room plays the REAL rendered MP4 inline. The deck media is genuine bytes; only the agent/render calls are stubbed for a free, reproducible recording.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "media-element",
    dwellMs: 5500,
  },
  {
    id: "se-annotate",
    route: "any",
    target: "media-annotate",
    title: "Annotate the media",
    body: "Every live media element carries an Annotate affordance — the unified ArtifactAnnotator. Click it and kitsoki probes the artifact's semantic sidecar; because this deck HAS one, it opens the slidey annotation substrate (a poster still with clickable element markers) rather than a generic region picker.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "media-annotate",
    dwellMs: 5500,
  },

  // ── The SemanticOverlay markers ─────────────────────────────────────────────
  {
    id: "se-overlay",
    route: "any",
    target: "aa-slidey",
    title: "Poster-backed semantic overlay",
    body: "A slidey deck is a multi-scene render with a semantic sidecar, so its pixels aren't an addressable still — whether it's rendered to MP4, PDF, or static HTML. Instead the annotator floats a SemanticOverlay over a poster image sized to the producer's natural 1920×1080 frame. Each box comes straight from the deck's .semantic.json sidecar.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "aa-slidey-poster",
    dwellMs: 6000,
  },
  {
    id: "se-markers",
    route: "any",
    target: "semantic-overlay",
    title: "Clickable element markers",
    body: "Each named scene element — the title, the cards, the body copy — is a positioned, clickable marker. Hovering one shows its label; the boxes are placed as a percent of the natural frame, so they track the poster at any CSS scale.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "semantic-overlay",
    dwellMs: 6000,
  },
  {
    id: "se-pick",
    route: "any",
    target: "so-marker-1/card_0",
    title: "Point at a deck element",
    body: "Click a marker — here Scene 1's first card — and the overlay emits a location-tied `semantic_element` anchor: the sidecar ref (verbatim), the plugin, the label, and the box. kitsoki dispatches it as an anchored note, so the refine pass knows EXACTLY which scene element you meant.",
    placement: "right",
    kind: "explain",
    advance: "next",
    waitForTarget: "so-marker-1/card_0",
    dwellMs: 6000,
  },

  // ── Refine from the anchor ──────────────────────────────────────────────────
  {
    id: "se-refine",
    route: "any",
    target: "composer-input",
    title: "Type the refinement",
    body: "Back in review, type what to change — you can read the instruction being composed here. The reviser receives BOTH your words AND the location-tied anchor from the marker you clicked, so it edits the exact scene element you pointed at, never a guess. Dispatch it and the run drops into `refining`, edits the anchored scene, and re-renders.",
    placement: "top",
    kind: "explain",
    advance: "next",
    waitForTarget: "composer-input",
    dwellMs: 7000,
  },
  {
    id: "se-loop-closed",
    route: "any",
    target: "current-state",
    title: "Back in review — the loop closed",
    body: "The refine pass completed: refining → rendering → and the state reads `reviewing` again, with the deck re-rendered and the cycle advanced. The anchor you pointed at is recorded as addressed. Annotate → refine → review, a closed loop you can run again.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    waitForTarget: "current-state",
    dwellMs: 6500,
  },
  {
    id: "se-done",
    route: "any",
    title: "That's unified annotation",
    body: "One Annotate affordance on any media, a poster-backed SemanticOverlay driven by the artifact's own sidecar, a clickable element marker that pins a location-tied anchor, and a refine pass that edits the exact thing you pointed at — all deterministic, no LLM, no cost.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
