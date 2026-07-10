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
 * stitch into a single coherent session. Each utterance is typed as FREE TEXT and
 * ROUTED live by the deterministic semantic tier (no LLM) — the spec drives via
 * InteractiveView's __kitsokiSendText hook (session.turn), and the inline routing
 * chip under each bubble shows the tier + match + confidence. The two genuine
 * agent calls (commit message, conflict resolution) replay through a host cassette
 * so they carry real token usage + cost; the spend meter shows ~$0.10 total, moved
 * only on those two turns.
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
 * places the popover to its `right` — over the trace/diagram panel
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
    body: "From a recorded session, a developer typed: “commit the staged fix” — as FREE TEXT, not a button. The semantic router resolved it to the commit intent deterministically (no LLM). The commit room then gathers the staged diff and the agent drafts the message it really landed — fix(auth): handle nil session on expiry. Review, regenerate, or edit; accept runs the real git commit.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: D,
  },

  // ── Routing feedback: how the typed words became an intent ───────────────────
  {
    id: "gitops-routing",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "routing-chip",
    title: "How your words got routed",
    body: "Look under the message: the routing chip shows how the typed words resolved — → commit · SEMANTIC · leading-verb:commit · 0.90. The deterministic router matched the imperative verb in-process (the leading-verb tie-break: “commit” leads, so it wins over “staged”). No model classified this turn — it's pure, traceable computation, and it's free. The LLM is reserved for work only it can do.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 4500,
  },

  // ── ② Real session: rebase onto main, resolve the conflicts ─────────────────
  {
    id: "gitops-rebase",
    route: "interactive",
    target: "chat-section",
    waitForTarget: "chat-section",
    title: "Real session ② — rebase & resolve",
    body: "The next recording: “rebase onto main and resolve the conflicts” — again free text, routed deterministically to rebase (its chip shows leading-verb:rebas). In the real session the rebase conflicted in TWO source files — internal/auth/session.go and internal/auth/token.go. The story routed into the conflict room and the agent resolved both, ran the build check, and continued — back on the hub, rebased and green. Reading two files and editing both is the demo's biggest agent call — watch the spend meter jump on this turn.",
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

  // ── Cost emphasis: spend tracks ONLY the genuine agent work ────────────────
  {
    id: "gitops-cost",
    route: "interactive",
    target: "usage-meter",
    waitForTarget: "usage-meter",
    title: "It only spent where the LLM earned it",
    body: "The spend meter reads about $0.10 — and it moved exactly TWICE across four scenarios: drafting the commit message (~$0.012) and resolving the two-file conflict (~$0.083). Everything else — routing every typed utterance, branch detection, staging, each merge guard, the whole worktree lifecycle — is deterministic transitions and real git commands, and cost nothing. The agent is the only surface that can spend, and the story confines it to the two moments only a model can do. The deterministic engine scales for free; you pay for judgment, not plumbing.",
    placement: "right",
    kind: "explain",
    advance: "next",
    dwellMs: 5000,
  },

  // ── Wrap-up ─────────────────────────────────────────────────────────────────
  {
    id: "gitops-done",
    route: "interactive",
    title: "Four real sessions, replayed",
    body: "Every input here was the verbatim text a developer typed in a real Claude Code session — commit, rebase-with-conflict, merge, and worktree setup — mined with the story-coverage-mining loop. Each was ROUTED live by the deterministic semantic tier (the chips show how), and only the two genuine agent calls spent tokens. Routing replay + traced spend means the whole run is auditable: you can see how every word resolved and exactly where the ~$0.10 went. Not a synthetic tour — the story handled exactly what people actually asked for. Hit '?' to replay this.",
    placement: "center",
    kind: "explain",
    advance: "next",
    dwellMs: 4000,
  },
];
