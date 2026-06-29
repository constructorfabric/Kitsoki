---
id: 2026-06-25T103758Z-studio-handles-no-worktree-owner-state
title: "studio.handles doesn't report worktree owner/stale state, and there is no workspace.release escape hatch — a driver can't detect or clear a stale .kitsoki-owner without restarting the studio"
target: kitsoki
filed_at: 2026-06-25T10:37:58Z
status: open
severity: P3
component: mcp
kitsoki_rev: f174615f
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-25T103758Z-studio-handles-no-worktree-owner-state.md"
---

## Body

When a kitsoki session mints a worktree (`bf-<ticket>`), it stamps a
`.kitsoki-owner` sentinel naming the owning session. If that session dies / is
closed while the sentinel persists (see the separate session.close owner-leak
bug), a later `session.new` on the same workspace bounces with
`… is already checked out by session "<dead id>"; refusing to share`.

The studio MCP gives a driver **no way to SEE or CLEAR this**: `studio.handles`
reports sessions + the workspace but **not** the worktree owner/stale state, and
there is **no `workspace.release` tool**. So a driver that hits the collision can
only stall and re-diagnose, or give up — there is no in-MCP recovery. (Observed
live: a driver lost several turns to exactly this, with nothing in `studio.handles`
to explain why the bounce kept happening.)

### Expected vs actual

**Expected:** `studio.handles` surfaces, per workspace, the worktree owner session
id and whether it is stale (owner session no longer live) — e.g.
`{workspace: {path, owner_session, stale}}` — AND a `workspace.release {id|path,
force?} → {ok, released, owner_session?}` tool lets a driver clear a provably-dead
owner's marker without restarting the studio process.

**Actual:** `studio.handles` is silent on owner/stale state and no release tool
exists, so a stale-owner collision is undiagnosable and unrecoverable from inside
the MCP surface.

### Steps to reproduce

1. `session.new` a bugfix session that mints `bf-<ticket>` (stamps `.kitsoki-owner`).
2. `session.close` it (the marker is left behind — see the owner-leak bug).
3. `studio.handles` → the output shows the workspace but no owner/stale info.
4. `session.new` on the same workspace → bounces "already checked out by …", with
   no MCP tool to detect or clear the stale owner.

### Severity rationale

P3: a recoverable-by-restart observability/ergonomics gap, but it silently burns
driver turns on autonomous/marathon runs and there is no in-band recovery. Pairs
with the session.close owner-leak bug (that one stops the leak; this one lets a
driver see + clear an existing one).

### Files involved

- `internal/mcp/studio/` — the `studio.handles` handler (add owner/stale to the
  workspace report) and a new `workspace.release` tool.
- the worktree owner-marker reader/writer (`.kitsoki-owner` + the
  "already checked out by session … refusing to share" guard) — the source of
  truth for owner/stale.
</content>
