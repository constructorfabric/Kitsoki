---
id: 2026-06-25T074726Z-session-close-leaks-worktree-owner
title: "studio MCP session.close releases the trace flock but NOT the worktree owner marker — a stale .kitsoki-owner bricks every rerun on the same workspace"
target: kitsoki
filed_at: 2026-06-25T07:47:26Z
status: open
severity: P2
component: mcp
kitsoki_rev: a899d092
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-25T074726Z-session-close-leaks-worktree-owner.md"
---

## Body

The studio MCP `session.close` tool releases a session's exclusive trace-path
flock (so the trace path can be reopened) but does NOT release the session's
**worktree owner identity**. A session that created a worktree stamps it with a
`.kitsoki-owner` marker pinned to that session id; `session.close` leaves the
marker behind, so after the session is closed every later `session.new` that
targets the same workspace bounces at idle with:

    workspace.create: <path> is already checked out by session "<dead-session-id>";
    refusing to share

There is no MCP surface to clear or reassign the marker and no automatic release
on close, so the workspace is **bricked for the rest of the server-process
lifetime** — the same class of stale-lock footgun the trace-flock release
(`session.close`, commit 6f7132ff) was added to fix, but for the worktree owner
identity instead of the trace flock.

This bites any retry loop: an exploratory `session.new` that is immediately
`session.close`d (e.g. a mis-seeded probe) leaves the worktree owned by the dead
session, and the real run can no longer create/attach the workspace.

### Root cause

`internal/mcp/studio/session_tools.go` → `handleSessionClose` calls
`srv.sess.CloseSession(handle)`, which releases the trace flock but does not
release the worktree owner marker. The owner identity should be released on close
**symmetrically with the trace flock** — closing a session must relinquish every
exclusive resource it held (trace path AND worktree owner), so a closed session
can never squat a workspace.

### Steps to reproduce

1. `session.new` against a story that mints a worktree (e.g. a bugfix dogfood);
   it stamps the workspace `.kitsoki-owner` with the session id.
2. `session.close` that session.
3. `session.new` again targeting the same workspace.
4. The second session bounces at idle: `workspace.create: … is already checked
   out by session "<first-session-id>"; refusing to share`.

### Expected vs actual

**Expected:** `session.close` releases the worktree owner identity (as it
releases the trace flock), so a later `session.new` can create/attach the same
workspace cleanly.

**Actual:** the owner marker persists; the workspace is bricked until the MCP
server process restarts.

### Proposed fix sketch

In `CloseSession` (or `handleSessionClose`), release the worktree owner marker
for the session being closed — symmetric with the trace-flock release. Optionally
add a `workspace.release {workspace_id}` MCP tool as an explicit escape hatch.
Whatever the shape, a closed session must never be able to squat a workspace.

### Severity rationale

P2: no data loss and a server restart clears it, but it silently bricks
autonomous retry loops (the operator-less case the studio MCP exists to serve)
and there is no in-band recovery.

### Files involved

- `internal/mcp/studio/session_tools.go` — `handleSessionClose`.
- the session manager's `CloseSession` (where the trace flock is released today)
  — the symmetric owner-marker release belongs here.
- the worktree owner-marker writer/checker (`.kitsoki-owner` + the "already
  checked out by session … refusing to share" guard).
</content>
