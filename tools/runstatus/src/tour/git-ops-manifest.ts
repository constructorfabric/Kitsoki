/**
 * git-ops story-walkthrough tour manifest (extensive edition).
 *
 * A self-contained step array for the git-ops demo video. Like the other feature
 * tours, the WHOLE video is tour-driven: it opens on the home story library,
 * frames the git-ops card, drives home → new session → the interactive /chat view
 * via a route-match action step, then narrates ONE long feature-branch → integration
 * session that visits many rooms so the demo shows the story's full scope:
 *
 *   hub → stage (suspicious-file gate) → oracle-authored commit (regenerate) →
 *   undo (reset modes) → rebase (auto-resolved conflict) → merge to main →
 *   pull → create a worktree → list worktrees → cleanup.
 *
 * The pace is deliberately quick — enough to see each scene; viewers pause to
 * inspect. Drives are PRE-STEP HOOKS in the spec (multi-story pattern); each step
 * just narrates the resulting state.
 *
 * SINGLE SOURCE OF TRUTH: the same array drives the live tour overlay
 * (window.__startTourWithSteps) and the video spec (git-ops-video.spec.ts), which
 * asserts each step's `title` against the live popover so the two cannot drift.
 *
 * MUST stay free of Vue / Pinia / DOM-runtime imports — plain types + data. Every
 * `target` / `waitForTarget` is a UNIVERSAL testid (chat section, state badge,
 * story cards), never a git-ops-specific room element, and no step gates on a
 * story state (the spec's pre-step hooks own state advancement).
 */

import { type TourStep } from "./manifest.js";

export type { TourStep };

const D = 3000; // base dwell — quick pace; viewers pause to inspect

export const GIT_OPS_TOUR_STEPS: readonly TourStep[] = [
  // ── Intro: the story library → a fresh run ──────────────────────────────────
  {
    id: "gitops-intro-home",
    route: "home",
    title: "Start at the story library",
    body: "Every run begins in the story library — each card is a deterministic story graph. We'll walk git-ops: a guided, hub-and-spoke git workflow where every command is a real, traced operation.",
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
    body: "On entry it detects your branch and routes to the right hub, offering only the operations legal there. The oracle appears in just two places: authoring a commit message and resolving a conflict.",
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
    body: "New session starts a fresh, independently-traced git-ops run on a feature branch.",
    placement: "right",
    kind: "action",
    advance: "route-match",
    advanceRoute: "interactive",
    dwellMs: 2500,
  },

  // ── Feature-branch hub ──────────────────────────────────────────────────────
  {
    id: "gitops-hub",
    route: "interactive",
    target: "state-badge",
    waitForTarget: "state-badge",
    title: "The feature-branch hub",
    body: "We're on branch_ops — the hub for feat/auth, one commit ahead of main. The state badge always names the room. Legal moves: stage, commit, squash, rebase, merge.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Stage + the suspicious-file gate ────────────────────────────────────────
  {
    id: "gitops-stage",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Stage — classify the tree",
    body: "The staging room classifies the working tree: staged, modified, untracked, and a suspicious-file flag — here it caught a .npmrc that looks like a credential.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },
  {
    id: "gitops-gate",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "A decision gate",
    body: "add_all won't silently stage a suspicious file. It stops for an explicit confirmation — a deterministic safety gate the story enforces before the dangerous default.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Oracle-authored commit ──────────────────────────────────────────────────
  {
    id: "gitops-commit",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "An oracle-authored commit",
    body: "The commit room shows the staged diff stat and a message the oracle drafted — fix(auth): guard nil session on expiry. Review, regenerate, or edit; accept runs the real git commit.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Undo (the safety room) ──────────────────────────────────────────────────
  {
    id: "gitops-undo",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Undo, safely",
    body: "The undo room shows the last commit and offers --mixed / --soft / --hard resets — the destructive --hard needs an explicit confirmation. We'll keep our commit and head back.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Rebase with an auto-resolved conflict ───────────────────────────────────
  {
    id: "gitops-rebase",
    route: "interactive",
    target: "state-badge",
    waitForTarget: "state-badge",
    title: "Rebase — conflict auto-resolved",
    body: "Rebasing onto main hit a conflict in internal/auth/session.go. The story routed into the conflict room, the oracle resolved it (the second of its two appearances), ran the build check, and continued — back on the hub, rebased and green.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: 3500,
  },

  // ── Merge into main ─────────────────────────────────────────────────────────
  {
    id: "gitops-merge",
    route: "interactive",
    target: "state-badge",
    waitForTarget: "state-badge",
    title: "Merge into main",
    body: "merge_into_main runs every guard in one script — descendant + stale-rebase check, a dirty-tree stash sandwich, the --no-ff merge, a post-merge build check — and reports merged.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Integration hub: pull ───────────────────────────────────────────────────
  {
    id: "gitops-pull",
    route: "interactive",
    target: "state-badge",
    waitForTarget: "state-badge",
    title: "On the integration hub — pull",
    body: "The merge dropped us on main_ops, the integration-branch hub. Pull checks for an upstream tracking ref first — there's none here, so it reports that rather than failing blindly.",
    placement: "bottom",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Worktree create ─────────────────────────────────────────────────────────
  {
    id: "gitops-worktree",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Create a worktree",
    body: "From the integration hub you can spin up an isolated worktree for the next feature. It's pinned under .worktrees/ — the branch name is derived from a short description (add login → add-login).",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: 3500,
  },

  // ── Worktree list ───────────────────────────────────────────────────────────
  {
    id: "gitops-list",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "List the worktrees",
    body: "The worktree room enumerates every checkout — the main worktree plus the new add-login one — and offers prune / remove actions for housekeeping.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Cleanup ─────────────────────────────────────────────────────────────────
  {
    id: "gitops-cleanup",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Cleanup after merge",
    body: "Once a branch is merged, cleanup removes its worktree and the branch in one step — keeping the repo tidy without leaving stale checkouts behind.",
    placement: "left",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Wrap-up ─────────────────────────────────────────────────────────────────
  {
    id: "gitops-done",
    route: "interactive",
    title: "That's the git-ops story",
    body: "One session walked the full scope — staging with a safety gate, an oracle-authored commit, undo, a rebase with an auto-resolved conflict, a guarded merge, pull, and the worktree lifecycle — hub to hub, every command a real, auditable operation. Hit '?' to replay this tour.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 3500,
  },
];
