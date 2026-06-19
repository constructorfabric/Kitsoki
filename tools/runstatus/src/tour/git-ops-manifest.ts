/**
 * git-ops story-walkthrough tour manifest (mined-from-real-sessions edition).
 *
 * The whole video is tour-driven: it opens on the home story library, frames the
 * git-ops card, drives home → new session → the interactive /chat view, then
 * replays FOUR scenarios mined from REAL Claude Code sessions with the
 * story-coverage-mining loop (tools/session-mining/examples/git-ops/). Every
 * typed user message in the video is the VERBATIM `user_text` a developer
 * actually typed in a recorded session — not a synthetic intent name, and not a
 * mechanical room-by-room tour:
 *
 *   ① "commit the staged fix"                          (sess-commit-happy)
 *   ② "rebase onto main and resolve the conflicts"     (sess-rebase-conflict)
 *   ③ "merge the feature branch into main"             (sess-merge-direct)
 *   ④ "set up a worktree for the new cache feature"    (sess-worktree)
 *
 * These four are one developer's natural feature-branch lifecycle (finish the
 * work → rebase onto main → merge → set up the next feature's worktree), so they
 * stitch into a single coherent session. The deterministic engine replays the
 * resolved intent for each; the real utterance rides as the chat bubble via
 * submitIntent's displayLabel (see InteractiveView's __kitsokiSubmitIntent).
 *
 * SINGLE SOURCE OF TRUTH: the same array drives the live tour overlay
 * (window.__startTourWithSteps) and the video spec (git-ops-video.spec.ts), which
 * asserts each step's `title` against the live popover so the two cannot drift.
 *
 * MUST stay free of Vue / Pinia / DOM-runtime imports — plain types + data. Every
 * `target` / `waitForTarget` is a UNIVERSAL testid (chat section, state badge,
 * story cards), never a git-ops-specific room element, and no step gates on a
 * story state (the spec's pre-step hooks own state advancement).
 *
 * POPOVER PLACEMENT: the chat is what viewers most need to read, so every
 * in-session step anchors the SPOTLIGHT on `chat-section` (left ~46% column) and
 * places the popover to its `right` — parked over the dimmed trace/diagram panel
 * (right ~54%), never covering the chat. (The intro steps are on the home view,
 * which has no chat; the wrap-up is a centered summary card.)
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

const D = 3500; // base dwell — quick pace; viewers pause to inspect

export const GIT_OPS_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run ──────────────────────────────────
  {
    id: "gitops-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins in the story library — each card is a deterministic story graph. We'll walk git-ops: a guided, hub-and-spoke git workflow where every command is a real, traced operation. What follows isn't a scripted tour — it's four real Claude Code sessions, replayed.",
    placement: "center",
    kind: "explain",
    advance: "next",
    waitForTarget: "home-view",
    dwellMs: D,
  },
  {
    id: "gitops-intro-story",
    route: "home",
    target: "story-card",
    waitForTarget: "story-card",
    title: "The git-ops story",
    body: "On entry it detects your branch and routes to the right hub, offering only the operations legal there. We mined how developers actually drive git in recorded sessions (the story-coverage-mining loop) and replay four of those sessions here verbatim.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },
  {
    id: "gitops-intro-start",
    route: "home",
    target: "new-session-btn",
    waitForTarget: "new-session-btn",
    title: "Spin up a session",
    body: "New session starts a fresh, independently-traced git-ops run on a feature branch (feat/auth, one commit ahead of main) — the hub a real developer's session opened on.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 2500,
  },

  // ── ① Real session: commit the staged fix ───────────────────────────────────
  {
    id: "gitops-commit",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Real session ① — commit the fix",
    body: "From a recorded session, a developer typed: “commit the staged fix.” The commit room gathers the staged diff and the oracle drafts the message it really landed — fix(auth): handle nil session on expiry. Review, regenerate, or edit; accept runs the real git commit.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── ② Real session: rebase onto main, resolve the conflicts ─────────────────
  {
    id: "gitops-rebase",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Real session ② — rebase & resolve",
    body: "The next recording: “rebase onto main and resolve the conflicts.” In the real session the rebase conflicted in TWO source files — internal/auth/session.go and internal/auth/token.go. The story routed into the conflict room, the oracle resolved both, ran the build check, and continued — back on the hub, rebased and green.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 4000,
  },

  // ── ③ Real session: merge the feature branch into main ──────────────────────
  {
    id: "gitops-merge",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Real session ③ — merge into main",
    body: "Then: “merge the feature branch into main.” merge_into_main runs every guard in one script — descendant + stale-rebase check, a dirty-tree stash sandwich, the --no-ff merge, a post-merge build check — reports merged, and drops us on the integration hub.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── ④ Real session: set up a worktree for the next feature ──────────────────
  {
    id: "gitops-worktree",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Real session ④ — set up a worktree",
    body: "Last recording: “set up a worktree for the new cache feature.” From the integration hub the story spins up an isolated worktree for the next feature — pinned under .worktrees/ as feat-cache, exactly the command the real session ran.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 4000,
  },

  // ── Wrap-up ─────────────────────────────────────────────────────────────────
  {
    id: "gitops-done",
    route: "interactive",
    title: "Four real sessions, replayed",
    body: "Every input here was the verbatim text a developer typed in a real Claude Code session — commit, rebase-with-conflict, merge, and worktree setup — mined with the story-coverage-mining loop and replayed deterministically, no LLM in the loop. Not a synthetic tour: the story handled exactly what people actually asked for. Hit '?' to replay this.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 4000,
  },
];
