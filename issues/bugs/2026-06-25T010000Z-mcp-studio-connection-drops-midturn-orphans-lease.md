---
id: 2026-06-25T010000Z-mcp-studio-connection-drops-midturn-orphans-lease
title: "Studio MCP connection drops mid-turn (-32000 Connection closed) during a live session.* turn — wipes all handles and orphans the worktree lease"
target: kitsoki
filed_at: 2026-06-25T01:00:00Z
status: open
severity: P1
component: studio-mcp
kitsoki_rev: 5a125b8c
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-25T010000Z-mcp-studio-connection-drops-midturn-orphans-lease.md"
---

## Body

While driving `stories/bugfix` live via the studio MCP, a `session.submit`
(advancing past a freshly-verified reproduce phase) returned mid-turn:

```
MCP error -32000: Connection closed
```

The studio process / stdio connection dropped during the turn. Two distinct
failure consequences, both bad:

1. **All handles vanished.** `studio.handles` came back empty after the drop —
   the in-memory session registry was lost with the process, so the still-live
   session could not be re-addressed by handle.

2. **The worktree lease was orphaned.** The dropped session
   (`8795191a-…`) still "owned" its worktree via the `.kitsoki-owner` sentinel
   (the bug9 concurrent-checkout safety net). Because the owner was gone from
   the registry, reopening the same ticket hard-failed at autostart:

   ```
   host.git_worktree.create: workspace.create: "bf-<ticket>" is already
   checked out by session "8795191a-…"; refusing to share
   ```

   The dead owner is unaddressable and (pre-`session.close`) unreleaseable, so
   the ticket's worktree is bricked until an out-of-band
   `git worktree remove --force` + studio restart.

This has occurred **at least twice** in one session, both during live
`session.*` turns that dispatch a real model (the long-running implementer /
submit turns), which points at a timeout/keepalive or crash on the long turn
rather than a one-off.

### Impact

Blocks reliable MCP-driven dogfooding: any multi-phase live drive can lose its
studio mid-pipeline, and the resulting orphaned lease + lost handles strand the
worktree. Combines badly with the (now-fixed) no-`session.close` gap and with
bug9's refuse-to-share sentinel — a crash converts the safety net into a
permanent lock.

## Hypotheses / where to look

- stdio MCP server keepalive / write timeout on long-running `session.*` turns
  (the turn dispatches `claude -p` / the live harness and can run minutes).
- Unhandled panic in the turn handler tearing down the server connection
  (check for a recover boundary around `handleSession*`).
- On a dropped connection, nothing flushes/teardowns live sessions, so leases
  and trace flocks leak (the new `session.close` only helps a *reachable*
  handle, not one lost to a crash).

## Proposed fix directions

- Make long `session.*` turns crash-safe: a recover boundary that returns a
  structured error instead of dropping the connection; heartbeat/keepalive so a
  long model turn doesn't trip an idle timeout.
- On studio startup, reconcile/reclaim orphaned `.kitsoki-owner` leases whose
  owning session is not in the (now-empty) registry — a dead owner should not
  block a fresh checkout.
- Persist/restore the handle registry (or at least surface a reclaim path) so a
  reconnect can re-address or release in-flight sessions.

## Related

- `2026-06-25T000000Z-mcp-no-session-close-trace-lock-squat.md` (no release
  tool — now fixed by `session.close`, but it can't reach a crash-lost handle).
- `2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md` (same
  subsystem).
- The bug9 fix (`67ac5fb1`) `.kitsoki-owner` sentinel is what converts a crashed
  owner into a hard lock — the reclaim path above is the missing complement.
