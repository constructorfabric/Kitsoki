/**
 * Agent-action-transcripts feature-spotlight tour manifest.
 *
 * A self-contained step array for the dedicated agent-actions video demo. Like
 * trace-manifest.ts, this tour opens on the home story library, explains the
 * bug-fix pipeline, drives a fresh run via route-match action steps (home →
 * new session → observer), then walks the agent-action-transcripts feature AS
 * IMPLEMENTED:
 * the per-call "Agent actions" drawer (typed rows, collapsible tool I/O), the
 * latency waterfall, the running token/cost accrual, the decide guardrail arc
 * (submit -> rejected -> host nudge -> re-submit -> accepted), the honest
 * cassette-vs-live diff under deterministic replay, and the run-wide session
 * rollup.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives both
 *   1. the agent-actions overlay (started via window.__startTourWithSteps), and
 *   2. the Playwright video spec
 *      (tests/playwright/agent-actions-video.spec.ts).
 * The video asserts each step's `title` against the live popover, so the two
 * cannot silently drift.
 *
 * Like manifest.ts this file MUST stay free of Vue / Pinia / DOM-runtime
 * imports — plain types and data only. Every `target` / `waitForTarget` here is
 * a testid the agent-actions drawer (and the universal observer surface)
 * actually ships; see the drawer components under
 * src/components/oracle/{OracleDetail,AgentActions,AgentActionRow,
 * AgentActionWaterfall,TranscriptDiff,SessionRollup}.vue and ViewModeTabs.vue.
 */

import { type TourStep } from "./manifest.js";

// Re-export so the Playwright spec can import the step type alongside the array
// from this one module (mirrors how the trace spec imports from its manifest).
export type { TourStep };

export const AGENT_ACTIONS_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run (tour-driven navigation) ─────────
  // These home/interactive steps mean the WHOLE video is tour-driven: the
  // intro explains where the feature lives and why before we reach the
  // observer, and the navigation itself (home → new session → observer) is
  // performed by route-match action steps, not silent spec orchestration.
  {
    id: "aa-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins here, in the story library — each card is a deterministic story graph kitsoki runs the same way every time. We'll demonstrate agent-action transcripts on the bug-fix pipeline.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: 5000,
  },
  {
    id: "aa-intro-story",
    route: "home",
    target: "story-card",
    waitForTarget: "story-card",
    title: "The bug-fix pipeline",
    body: "This story hands a ticket to an autofix agent that reads the repo, edits code, and runs the build, then a judge gates the result. Those agent calls run rich native execution streams — exactly what the transcript drawer captures and replays.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "aa-intro-start",
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
    id: "aa-intro-observe",
    route: "interactive",
    target: "observe-link",
    waitForTarget: "observe-link",
    title: "Switch to the observer",
    body: "The agent-action transcripts live in the read-only observer view, where every recorded call can be inspected and replayed. Switch to it to open this run.",
    placement: "bottom",
    kind: "action",
    advance: "route-match",
    advanceRoute: "any",
    dwellMs: 4000,
  },

  // ── Introduction ──────────────────────────────────────────────────────────
  {
    id: "aa-welcome",
    route: "any",
    title: "Agent action transcripts",
    body: "Every oracle call — task, decide, ask, converse — runs a rich native execution stream: tool calls, reasoning, MCP submissions. kitsoki captures that stream byte-verbatim to a per-call sidecar, renders it here, and replays it deterministically from the cassette. This tour walks the full drawer.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Open the task call's detail + spotlight the affordance ─────────────────
  {
    id: "aa-affordance",
    route: "any",
    target: "agent-actions-affordance",
    waitForTarget: "agent-actions-affordance",
    title: "Agent actions, per call",
    body: "Select an oracle call in the trace and its detail pane gains an 'Agent actions (N)' affordance — N is the captured event count. This is the bugfix autofix task: a 12-step Read / Grep / thinking / Edit / Bash arc. Click it to open the drawer.",
    placement: "left",
    kind: "action",
    advance: "click-target",
    dwellMs: 4500,
  },
  {
    id: "aa-drawer",
    route: "any",
    target: "agent-actions-drawer",
    waitForTarget: "agent-actions-drawer",
    title: "The execution timeline",
    body: "The drawer renders the native stream as typed rows: assistant reasoning, and each tool call's full input and output — the file Read, the Grep pattern, the Edit diff, the Bash command and its stdout. Every row collapses on its header, so a long arc stays scannable.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
  {
    id: "aa-row",
    route: "any",
    target: "agent-action-row",
    waitForTarget: "agent-action-row",
    title: "Full tool I/O, collapsible",
    body: "Each step is a typed row anchored on a header you can click to expand or collapse its detail. No 200-rune preview, no names-only rollup — the command that ran, the file that was edited, and the bytes that came back, exactly as captured.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Waterfall ──────────────────────────────────────────────────────────────
  {
    id: "aa-waterfall-toggle",
    route: "any",
    target: "agent-actions-mode-waterfall",
    waitForTarget: "agent-actions-mode-waterfall",
    title: "Switch to the waterfall",
    body: "Toggle the drawer's waterfall mode to see per-step latency. The offsets come from a parallel .timings sidecar stamped at capture — never re-derived from replay timing, so the bars are byte-stable.",
    placement: "left",
    kind: "action",
    advance: "click-target",
    dwellMs: 3500,
  },
  {
    id: "aa-waterfall",
    route: "any",
    target: "agent-action-waterfall",
    waitForTarget: "agent-action-waterfall",
    title: "Where the wall-clock went",
    body: "Each bar's width is that step's duration. The slow Bash or the long model turn stands out at a glance — the same headline view a dedicated agent-observability tool gives you, over data we already hold.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Cost / token accrual ───────────────────────────────────────────────────
  {
    id: "aa-accrual",
    route: "any",
    target: "agent-actions-accrual",
    waitForTarget: "agent-actions-accrual",
    title: "Running token + cost accrual",
    body: "The header accrues input/output tokens and cost across the whole call, not just the terminal total — so 'this call retried twice before it finished' is visible in the running tally, not buried.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 4500,
  },

  // ── The decide guardrail arc (the moat-tying beat) ─────────────────────────
  {
    id: "aa-decide-guardrail",
    route: "any",
    target: "guardrail-row",
    waitForTarget: "guardrail-row",
    title: "The decide guardrail arc",
    body: "Now switch to the decide call. Its arc is the moat made legible: the model submits a verdict via mcp__validator__submit, which is typed as a GUARDRAIL row — accept/reject + confidence, not a generic tool call. Here the first submit is REJECTED on a schema violation.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },
  {
    id: "aa-nudge",
    route: "any",
    target: "nudge-row",
    waitForTarget: "nudge-row",
    title: "The host nudge, made visible",
    body: "On rejection the host injects a coaching nudge as the next turn's stdin — invisible in a raw stream-json tee, because the input prompt is never echoed back. kitsoki writes a synthetic _kitsoki row so the full submit -> reject -> NUDGE -> re-submit -> accept round-trip, with iteration boundaries, reads as one sequence.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 6000,
  },

  // ── Cassette-vs-live diff (the determinism payoff) ─────────────────────────
  {
    id: "aa-diff",
    route: "any",
    target: "transcript-diff-control",
    waitForTarget: "transcript-diff-control",
    title: "Cassette-vs-live drift",
    body: "Because the transcript replays byte-identically from the cassette, the drawer can diff a fresh live run against the recorded one and flag tool-path drift — a capability no external tool has, because none has deterministic replay. Under pure replay there is no live run to compare, and the control says so honestly.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },

  // ── Session rollup (the run-wide view) ─────────────────────────────────────
  {
    id: "aa-rollup-tab",
    route: "any",
    target: "tab-actions",
    waitForTarget: "tab-actions",
    title: "All agent actions for the run",
    body: "The Actions view mode rolls every transcript-bearing call across the whole run up under its turn and room — the kitsoki analog of a session replay. Click to open it.",
    placement: "bottom",
    kind: "action",
    advance: "click-target",
    dwellMs: 3500,
  },
  {
    id: "aa-rollup",
    route: "any",
    target: "agent-actions-rollup",
    waitForTarget: "agent-actions-rollup",
    title: "Every call, one place",
    body: "Each row is one oracle call — its verb, call_id, and action count — and expands into its own full drawer. The whole run's agent activity in a single grouped view, the unit an operator actually reasons about.",
    placement: "top",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Wrap-up ────────────────────────────────────────────────────────────────
  {
    id: "aa-done",
    route: "any",
    title: "That's the full picture",
    body: "You've seen the complete agent-action-transcripts stack: per-call typed rows with full tool I/O, the latency waterfall, running cost accrual, the decide submit -> reject -> nudge -> accept guardrail arc, the honest cassette-vs-live diff, and the run-wide session rollup — all over data captured once and replayed deterministically. Hit '?' anytime to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 5500,
  },
];
