---
id: 2026-06-23T092410Z-mcp-no-standalone-gate-runner
title: "Studio MCP has no standalone gate-runner — can't run a story's gate_command / host.run against a worktree outside a live session"
target: kitsoki
filed_at: 2026-06-23T09:24:10Z
status: fixed
severity: P2
component: mcp
kitsoki_rev: 154630be
trace_ref: ""
external: {}
assignee: ""
related:
  - 2026-06-23T092411Z-mcp-live-harness-no-profile-uses-synthetic
url: "issues/bugs/2026-06-23T092410Z-mcp-no-standalone-gate-runner.md"
---

## Body

When driving a delivery/bug-fix live through the studio MCP, there is no
read-only tool to run a story's deterministic `gate_command` (or any
`host.run`) against a worktree **independently of a live session turn**. The
operator/agent can only observe gate results that happen *inside* a session's
room dispatch.

This matters for the exact thing the pipelines are built to do: gate on the
deliverable passing, not on an agent's self-report. During the imports-rewriter
dogfood delivery, the driver could not independently re-confirm the committed
tip was GREEN — it had to fall back to reading source files plus the pipeline's
own in-session CI. `story_validate {dir}` runs the *server's* compiled
`internal/app`, not the worktree's Go, so it cannot exercise a worktree's fix.

## Expected

A read-only studio tool to execute a command against a worktree and return its
exit code + output, e.g.:

- `host_run { dir, cmd, args? } -> { ok, exit_code, stdout, stderr }`, or
- `story_gate { dir, gate_command } -> { ok, exit_code, log }`

so a delegated agent can gate on the real Go test / build passing on the
committed tip, outside any session.

## Actual

No such tool exists in `internal/mcp/studio`. Gate execution is only reachable
through a live session's room `host.run` invocations.

## Impact

Live MCP-driven deliveries cannot independently verify the deliverable; the
human/agent operator must shell out separately (defeating the MCP-first,
no-CLI delegation model) or trust the in-session report.

## Notes

Surfaced during the live imports-rewriter delivery (see the lost-work guard
work). Directly relevant to the `bugfix-repro-gate` proposal, which needs the
same "run gate_command against a worktree" capability for its RED-first check.

## Resolution

Added the studio tool `host.run` (`internal/mcp/studio/host_tools.go`): a thin
wrapper over `host.RunHandler` that runs a command against a named worktree dir
outside any session and returns `{ok, exit_code, stdout}`. The first proposed
shape (`host_run {dir, cmd, args?}`) — a story's `gate_command` is just
`host.run {dir, cmd: gate_command}`. A non-zero exit is returned as DATA
(`ok:false`), never a tool error, so a caller can gate on RED. Omitted on a
read-only server (a command runner is a write surface). Covered by
`host_tools_test.go` (green/red/args-mode/missing-dir/bad-dir). Same execution
semantics as a story's in-session `host.run` because it reuses the same handler.
