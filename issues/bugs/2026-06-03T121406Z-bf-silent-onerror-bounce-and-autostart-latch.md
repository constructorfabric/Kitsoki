---
# triage-marathon: ALREADY-FIXED in main — 65f8f218 — on_error redirect failures surfaced in view
id: 2026-06-03T121406Z-bf-silent-onerror-bounce-and-autostart-latch
title: "Bugfix pipeline silently bounces to idle on workspace/git failure; autostart latch permanently disables recovery"
target: kitsoki
filed_at: 2026-06-03T12:14:06Z
status: fixed
severity: P1
component: orchestrator
kitsoki_rev: 153c3d6
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-03T121406Z-bf-silent-onerror-bounce-and-autostart-latch.md"
---

## Body

Filed from a chat-history mining pass (see
`.context/kitsoki-dev-ideas-from-chats.md`, theme #1 — recurred across 3
sessions, repeatedly called "extremely frustrating" / "fundamentally
broken"). This is the single most acute dogfood pain and it has no owning
proposal — the `kitsoki-debugging` skill *diagnoses* this class but nothing
*fixes* it.

Typing `continue`/`accept` at `proposing` transitions to `implementing`,
whose `on_enter` workspace-sync / git step fails, and the room's
`on_error: idle` redirect **silently** bounces the operator back to `idle`
with no trace signal — the operator sees only a re-rendered idle room and
no indication anything failed. Compounding root causes observed:

- worktree reuse (a `.worktrees/bf-<ticket>` left over from a prior run)
- `git pull --ff-only` run against a freshly-created branch (no upstream)
- a benign `nothing to commit` treated as a hard failure
- relative-vs-absolute `workdir` path comparison mismatches
- empty `workspace_id` / `workdir` passed to imported workspace/vcs invokes
- a `bf_autostart_attempted` latch that, once set, **permanently** disables
  workspace recreation, so the session can never recover within its lifetime

### Steps to reproduce

1. Pick a ticket and enter the bugfix pipeline; reach `proposing`.
2. Arrange any `implementing.on_enter` workspace/git failure (e.g. leave a
   stale `.worktrees/bf-<ticket>`, or start from a branch with no upstream).
3. Type `continue`. Observe the room bounce back to `idle` with no error.
4. Retry: the `bf_autostart_attempted` latch is set, so the workspace is
   never recreated and the pipeline is wedged for the rest of the session.

### Expected vs actual

**Expected:** every `on_error` redirect emits an observable trace/slog
event; the operator sees *why* it bounced; workspace setup is idempotent and
self-healing on retry.

**Actual:** silent `view_bytes: 0` re-render of `idle`, no diagnostic, and a
one-way latch that disables recovery.

### Proposed fix sketch

- **Observable redirects:** emit a typed trace event (and slog line) at every
  `on_error:` redirect site; surface `render_after_bind_failed` instead of an
  empty `view_bytes: 0` frame.
- **Idempotent recreate:** pair every `*_attempted` latch with an idempotent
  recreate path — reuse-or-recreate the worktree, don't refuse because the
  flag is set.
- **Guard empty ids:** refuse (or auto-derive `bf-<ticket>` / `fix/<ticket>`)
  when `workspace_id`/`workdir` is empty, rather than invoking host calls with
  a blank coordinate.
- **Git hygiene:** skip `--ff-only` pull on branches with no upstream; treat
  `nothing to commit` as success; normalize paths to absolute before compare.
- **Recursion cap:** cap `on_error` redirect recursion and break the
  `idle` → autostart → fail → `idle` cycle with a sentinel.
- `RunInitialOnEnter` so the home room's `on_enter` (`ticket.list_mine`) runs
  at session start.

### Severity rationale

P1 — blocks the flagship bugfix arc end-to-end in dogfood, with no operator-
visible cause and no in-session recovery. Not P0 only because a fresh session
(clean worktree) can sometimes proceed.

### Files involved

- `internal/orchestrator/orchestrator.go` — `on_error` redirect sites,
  `runOnEnter` ordering, recursion handling.
- `internal/machine/machine.go` — gate/redirect bookkeeping.
- `stories/dev-story/rooms/` + `stories/bugfix/rooms/implementing.yaml` —
  the autostart latch and workspace-sync on_enter chain.
- `docs/stories/state-machine.md` — document `on_error` observability +
  workspace-as-implicit-room-context.
