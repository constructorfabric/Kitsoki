---
id: 2026-06-25T000000Z-mcp-no-session-close-trace-lock-squat
title: "Studio MCP has no session.close/release — a stale/wrong-cell live session squats its trace-path lock and bricks any rerun on that path"
target: kitsoki
filed_at: 2026-06-25T00:00:00Z
status: fixed
severity: P1
component: studio-mcp
kitsoki_rev: 501cbd28
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-25T000000Z-mcp-no-session-close-trace-lock-squat.md"
---

## Body

The studio MCP binds a keyed live session to its trace path with an **exclusive
file lock** and exposes **no tool to close, release, or re-target a session**.
The `session_*` surface is `new / attach / drive / submit / continue / answer /
inspect / trace / world` — none releases a bound session.

Consequence: a stale or wrong-cell session permanently squats its trace path. A
new `session_new` aimed at the same path fails with
`BAD_REQUEST: trace file is locked by another writer`, and the squatter cannot
be evicted through the MCP. The session itself can be defunct (its worktree
deleted → `bf_autostart_attempted` latched, `workspace.create` errors with
"no such file or directory"), having already spent money, yet still hold the lock.

### Reproduced (bug9 GLM-5.2 bake-off cell, 2026-06-25)

- Target trace path `…/traces/bug9-glm-5.2-kitsoki.jsonl` was held by a
  pre-existing defunct session `s15` (ticket `bug9`, workdir `.worktrees/bf-bug9`
  — a deleted tree, $3.72 already spent, autostart latched).
- Could neither reuse `s15` (unrecoverable for the new worktree) nor create a
  fresh session on that path (`trace file is locked by another writer`).
- Workaround: drove the *separately-keyed* already-bound cell `s19`
  (`…/traces/bug9-glm-run2.jsonl`) instead — which meant the run landed on a
  non-canonical trace filename, off the path the bake-off harness expects.

This is the load-bearing blocker for rerunning any bake-off / dogfood cell on a
fixed canonical trace path — more so than provider quota (the GLM 429s were just
account-level throttle and cleared on their own). Each failed/abandoned attempt
strands a lock that no operator action short of process-kill can clear.

## Proposed fix

Add a `session_close` (a.k.a. `session_release`) MCP tool that flushes + unlocks
the trace and tears down the keyed session. Optionally: make `session_new` on a
locked path detect a **dead** holder (worktree gone / process exited) and reclaim
the lock, or accept an explicit `--force-reclaim`. At minimum surface the holder
(session key + pid) in the `BAD_REQUEST` so an operator can act.

## Related

- `2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md` (the temp-path
  default that makes traces undiscoverable in the first place — same subsystem).
