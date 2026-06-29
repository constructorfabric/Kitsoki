---
id: 2026-06-25T103757Z-codex-backend-cost-not-surfaced-into-world
title: "codex backend never surfaces agent cost — world.session_cost_usd / turn_cost_usd stay 0 under the codex-native profile, so cost-budget guards and cost reporting are blind"
target: kitsoki
filed_at: 2026-06-25T10:37:57Z
status: open
severity: P2
component: host
kitsoki_rev: f174615f
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-25T103757Z-codex-backend-cost-not-surfaced-into-world.md"
---

## Body

The orchestrator surfaces agent spend into world after every cost-bearing turn:
`internal/orchestrator/host_dispatch.go` sums each call's `total_cost_usd` into
`world.turn_cost_usd` (this batch) and `world.session_cost_usd` (cumulative), so
stories can guard `when: "world.session_cost_usd >= world.cost_budget"` and
operators can read the running cost. This works for the Claude backend.

**Under the `codex` backend (the `codex-native` profile, gpt-5.5) both stay 0.**
A live gpt-5.5 bugfix dogfood reported `session_cost_usd`/`turn_cost_usd` = 0 for
the whole run even though the agents clearly spent tokens — the per-call token
usage was present in the `agent.call.complete` trace event's `meta.usage`, but no
`total_cost_usd` was emitted, so the orchestrator summed 0. Result: cost-budget
guards never fire and any cost read off world is wrong for codex/gpt-5.5 sessions.

### Root cause (where to look)

The codex backend (`internal/host/agent_backend_codex.go` / `agent_stream_codex.go`)
does not populate a per-call `total_cost_usd` on its agent-call result the way the
Claude backend does, so `host_dispatch.go`'s `batchCost` sum is 0. The codex CLI
reports token usage (input/output tokens) but not a dollar cost; the cost must be
COMPUTED from the codex model's token usage × the model's price (the price table
the cost layer already uses for other backends) and attached as `total_cost_usd`
so the existing `turn_cost_usd`/`session_cost_usd` plumbing picks it up unchanged.

### Steps to reproduce

1. Drive any LLM-bearing story on the `codex-native` profile (e.g.
   `session.new {story_path: stories/bugfix/app.yaml, harness: live, profile:
   codex-native, initial_world: {...}}`), run at least one agent turn.
2. Read `world.session_cost_usd` (e.g. `session.world {handle, key:
   "session_cost_usd"}`).
3. It is `0` despite real token spend (visible in the `agent.call.complete`
   trace events' `meta.usage`).

### Expected vs actual

**Expected:** after a cost-bearing turn on codex-native, `world.turn_cost_usd` >
0 and `world.session_cost_usd` accumulates — same contract as the Claude backend,
so cost-budget guards and cost reporting work regardless of provider.

**Actual:** both stay `0` for codex/gpt-5.5 — token usage is recorded in the
trace but never converted to a cost or summed into world.

### Severity rationale

P2: no incorrect state-machine behaviour, but cost-budget guards silently never
fire under codex (a budget overrun risk on autonomous runs) and any cost
accounting on a gpt-5.5 session is wrong. Provider-parity gap.

### Files involved

- `internal/host/agent_backend_codex.go` / `agent_stream_codex.go` — the codex
  agent-call result; needs a computed `total_cost_usd` from token usage × price.
- the price/cost table the Claude path already uses (so codex reuses it).
- `internal/orchestrator/host_dispatch.go` — `batchCost` →
  `turn_cost_usd`/`session_cost_usd` (consumer; unchanged once codex emits cost).
</content>
