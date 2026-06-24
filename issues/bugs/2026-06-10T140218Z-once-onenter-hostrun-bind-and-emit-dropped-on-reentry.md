---
id:        2026-06-10T140218Z-once-onenter-hostrun-bind-and-emit-dropped-on-reentry
title:     "once: on_enter host.run drops its bind + post-bind emit_intent on room re-entry"
target:    kitsoki
filed_at:  2026-06-10T14:02:18Z
status:    fixed
severity:  P1
component: runtime
kitsoki_rev: ""
trace_ref: "/home/cloud-user/.kitsoki/sessions/git-ops/c728b3d0-tui-acf67622-44fd-47ea-a92a-a8ab37453e00.jsonl"
external: {}
---

## Body

A `host.run` step in a room's `on_enter` marked `once: true`, whose call
**succeeds**, has its `bind:` (and the subsequent `emit_intent`) silently
dropped when the room is re-entered. World keeps its stale defaults and the
room never routes, leaving the operator stranded on a transient view.

### Where it surfaced

`stories/git-ops/rooms/idle.yaml`. `idle` is a transient router: `on_enter`
runs a JSON-emitting `host.run` (`id: detect_context`, `once: true`) that
binds `current_branch` / `route` / `on_integration` / … from `stdout_json.*`,
then `emit_intent: "{{ world.route }}"` to hand off to `branch_ops` /
`main_ops`.

```yaml
on_enter:
  - invoke: host.run
    once: true
    id: detect_context
    with: { ... }              # git rev-parse … | jq -n {branch, route, …}
    bind:
      current_branch: "stdout_json.branch"
      route:          "stdout_json.route"
      # …
    on_error: branch_ops
  - emit_intent: "{{ world.route }}"
on:
  on_main:   [{ target: main_ops }]
  on_branch: [{ target: branch_ops }]
  look:      [{ target: . }]
```

### Repro

1. Run `stories/git-ops` while checked out on a feature branch (not
   `integration_branch`).
2. Enter `idle` (story root), then send a `look`-matching input — e.g.
   "what branch am i on" (matches the `look` intent example
   "what branch are we on"), which re-enters `idle` via `target: .`.

### Expected

`detect_context` returns successfully → its `bind:` populates
`world.current_branch` (and friends) → `emit_intent: "{{ world.route }}"`
fires `on_branch` → operator lands in `branch_ops`, which displays the branch.

### Actual

The host call ran and returned cleanly, but `bind:` never landed in world and
the post-detect `emit_intent` never fired. The gate ended `bailed_to_human`
back at `idle`, showing `Branch: (detecting…)` forever.

Trace `harness.returned` for the step (exit_code 0, ok):

```json
{ "exit_code": 0, "ok": true,
  "stdout": {
    "branch": "feat/copilot-agent-backend",
    "route": "on_branch",
    "on_integration": false,
    "commits_ahead": 3, "commits_behind": 2, "has_uncommitted": true } }
```

…yet post-turn `world` still has `current_branch: ""` and `route: "look"`
(the schema defaults). So the engine computed the right values and discarded
them.

Trace event order for the turn (`turn 1`, input "what branch am i on"):

```
machine.gate_decided  idle  chosen_intent=look (decider=default)
machine.transition    idle  idle→idle  intent=look  synthetic=true
harness.dispatched    idle  detect_context
harness.returned      idle  ok, stdout has branch+route   ← values present
machine.transition    idle  idle→idle  intent=look
machine.gate_decided  idle  bailed_to_human, chosen_intent=""
```

### Suspected cause

The `once: true` re-entry path dispatches the host call but skips applying its
result — neither the `bind:` mutation nor the chained `emit_intent` is
committed to world on the second visit. Likely the same family as the
"emit chains pause at binding hops" / "idempotent on_enter / `once:`" work:
the once-latch appears to gate *result application*, not just *re-dispatch*.

### Impact

Any room that uses a `once:` on_enter host call to bind state and then route
off itself can strand the operator on re-entry. In `git-ops` it manifests as
"the home view never shows my branch and never routes me to a hub."

### Notes / workaround under evaluation (story side)

A story-level mitigation is to make `idle`'s `look` route via `world.route`
to the hub instead of `target: .`, so the hub's own `on_enter` re-gathers and
displays. That sidesteps the dropped bind but does not fix the underlying
engine defect, which is what this bug tracks.
