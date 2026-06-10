/**
 * Story Editor View feature-spotlight tour manifest.
 *
 * A self-contained step array for the dedicated story-editor video demo. Like
 * agent-actions-manifest.ts, the WHOLE tour is narrated: it opens on the home
 * story library, spotlights the new "Edit story" affordance, drives into the
 * editor via a route-match action step, then walks the editor surface AS
 * IMPLEMENTED — the BFS-ordered room list, the room detail (hook, domain model,
 * read-only typed view, IDE deep-link), the meta-chat column, and the Oracle
 * Workbench (contract cards + cassette browser).
 *
 * SINGLE SOURCE OF TRUTH: the same array drives both
 *   1. the tour overlay (started via window.__startTourWithSteps), and
 *   2. the Playwright video spec (tests/playwright/story-editor-video.spec.ts).
 * The video asserts each step's `title` against the live popover, so the two
 * cannot silently drift.
 *
 * The /editor hash route maps to the overlay's "any" route kind (it is neither
 * "/" nor "*\/chat"), so every editor step is `route: "any"`. Each `target` is a
 * data-testid the editor surface actually ships (see views/EditorPage.vue and
 * components/editor/{HookDetail,DomainModel,OracleWorkbench,CassetteBrowser,
 * StoryViewer}.vue).
 *
 * Like manifest.ts this file MUST stay free of Vue / Pinia / DOM-runtime
 * imports — plain types and data only.
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

export const STORY_EDITOR_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → the editor (tour-driven navigation) ──────────
  {
    id: "se-intro-home",
    route: "home",
    title: "Authoring, without the YAML expedition",
    body: "Every kitsoki story is a deterministic graph of rooms authored in YAML. The Story Editor gives that graph a browseable surface — room order, wiring, and oracle contracts — without leaving the web UI. We'll walk it on the PRD-authoring story.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5000,
  },
  {
    id: "se-intro-card",
    route: "home",
    target: "edit-story-btn",
    waitForTarget: "edit-story-btn",
    title: "Open a story in the editor",
    body: "Every story card now carries an 'Edit story' link. It opens that story's room graph in the editor — no session, no LLM, just a static read of the story as authored.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "se-intro-open",
    route: "home",
    target: "edit-story-btn",
    waitForTarget: "edit-story-btn",
    title: "Into the editor",
    body: "Click 'Edit story' to open the PRD story's graph.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "any",
    dwellMs: 3500,
  },

  // ── The shell ───────────────────────────────────────────────────────────────
  {
    id: "se-shell",
    route: "any",
    target: "editor-room-list",
    waitForTarget: "editor-room-list",
    title: "The story map",
    body: "The editor is a two-column shell. On the right, every room in the story — on the left, a meta-chat scratchpad. The room list is the map you navigate by.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "se-roomlist",
    route: "any",
    target: "editor-room-list",
    waitForTarget: "editor-room-item",
    title: "Ordered by reachability",
    body: "Rooms are sorted by their average BFS distance from the entry point — idle (0) → clarifying (1) → brief (2) → references (3) → drafting (4). Early rooms first, unreachable rooms last. You think in 'how far from the start', not YAML file order.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "se-meta",
    route: "any",
    target: "editor-meta-chat",
    waitForTarget: "editor-meta-chat",
    title: "Meta chat, alongside",
    body: "The left column is the same off-path meta oracle you have on the run surface — ask 'what does this room's prompt do?' without leaving the page. It needs a live session, so until one is attached it shows this placeholder.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Room detail (clarifying is selected by the spec's pre-step hook) ──────────
  {
    id: "se-detail",
    route: "any",
    target: "editor-room-detail",
    waitForTarget: "editor-room-detail",
    title: "A room, fully unpacked",
    body: "Selecting 'clarifying' pins it in the detail pane: its hook, its domain model, its typed view, and an IDE deep-link — everything you'd otherwise reconstruct by reading YAML across files.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },
  {
    id: "se-ide",
    route: "any",
    target: "editor-ide-link",
    waitForTarget: "editor-ide-link",
    title: "Jump to source",
    body: "The header carries a vscode://file deep-link to the exact line of the room's source. Editing stays in your IDE — the editor reads and links, it doesn't write YAML.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 4500,
  },
  {
    id: "se-hook",
    route: "any",
    target: "editor-hook",
    waitForTarget: "editor-hook-effect",
    title: "The hook (on_enter)",
    body: "Each on_enter effect renders as a typed card — the host call, its arguments, and the world bindings it produces. The clarifying room fires a decide oracle to surface the gaps in the brief.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "se-domain",
    route: "any",
    target: "editor-domain-model",
    waitForTarget: "editor-domain-model",
    title: "The domain model",
    body: "Three sections: world keys this room reads and writes (with direction), the intents it defines, and its transitions. Transition targets are links — click one to jump straight to that room.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "se-view",
    route: "any",
    target: "editor-story-viewer",
    waitForTarget: "editor-story-viewer-view",
    title: "The typed view, rendered",
    body: "The room's typed view is rendered read-only by the very same component the run surface uses — no divergence between what you author and what the operator sees.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Oracle workbench ─────────────────────────────────────────────────────────
  {
    id: "se-workbench",
    route: "any",
    target: "editor-oracle-workbench",
    waitForTarget: "editor-oracle-card",
    title: "The Oracle Workbench",
    body: "For any room that calls an oracle, the workbench lists its contracts: the call kind, the prompt template, the declared output schema, and the cassette key that flow-tests match on — the contract an author tunes.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "se-cassette",
    route: "any",
    target: "editor-cassette-list",
    waitForTarget: "editor-cassette-list",
    title: "Browse the cassettes",
    body: "Each contract embeds a cassette browser — pick a recorded episode, preview its output, and replay the call in isolation to see the room render with that response. The PRD story ships no oracle cassettes yet, so the browser honestly shows its empty state.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Wrap-up ──────────────────────────────────────────────────────────────────
  {
    id: "se-done",
    route: "any",
    title: "The whole story, on one surface",
    body: "That's the Story Editor: a reachability-ordered room map, per-room hook / domain-model / typed-view detail, IDE deep-links, a meta-chat scratchpad, and an oracle workbench with a cassette browser — all a static, no-LLM read of the story as authored. Hit '?' anytime to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
