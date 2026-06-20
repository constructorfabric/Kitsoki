# cherny-loop

Facilitate a **Cherny loop** (loop engineering, after Boris Cherny): an agent
iterates toward a goal until a **gate** proves the goal is *actually met* — or a
**budget** ceiling is hit. **The loop runs itself** — the operator kicks it off
once and watches it converge; every iteration streams a breadcrumb and is
persisted as a numbered artifact, so the run is watchable, restartable, and
shareable.

## The loop

```
configuring → baseline ─ RED (auto) ─→ iterating → gating ──┐ (auto-loop)
     ▲            │                        ▲                 │
     └─ reconfig ─┤ GREEN                  └── loop_again ───┤ goal met   → @exit:achieved
                  └─ accept                                  ├ budget hit → @exit:exhausted
                     (already met → @exit:achieved)          └ abort      → @exit:abandoned
```

`configuring` is the root — the operator lands where they act, with no `idle`/
`begin` pass-through turn. **After `launch`, no further prodding is needed**: the
loop drives itself maker → gate → repeat until a gate passes or a budget stops it.

- **baseline** — before any maker spend, `launch` runs the gate **once on the
  unchanged artifact** to prove it can fail. A **RED** baseline (gate fails)
  **auto-proceeds** into the loop. A **GREEN** baseline (gate passes) means the
  gate proves nothing — the goal is already met, or the gate is too weak — so the
  loop *refuses to spend* and rests for the operator (`reconfigure` or `accept`).
  This is the red-before-green discipline: never spend budget on a gate that can't
  fail. The RED proof becomes the first maker feedback.
- **iterating** — the **maker** (`host.agent.task`) makes the smallest change
  toward the goal, fed the *previous gate's failure reason* as feedback (the
  ralph-style reset: anchors + one failure reason, not a growing transcript),
  then **auto-emits `check`** into gating.
- **gating** — the **checker** runs and routes automatically: a pass exits
  (`@exit:achieved`), a budget overrun exits (`@exit:exhausted`), otherwise
  `loop_again` re-enters `iterating` with the failure captured — the autonomous
  step.

## Autonomy & the depth cap

The loop is autonomous **in-story**: `iterating` auto-emits `check` → `gating`
auto-emits `loop_again` → `iterating`. Each iteration is two `emit_intent` hops,
and the engine caps emit chains at **8 per turn** (`EmitIntentMaxDepth`), so an
autonomous-from-`launch` run completes in one turn for **iteration budgets up to
~3**. This is a real engine constraint the story deliberately exposes, not papers
over — larger budgets want a **background-job runner** (`background: true` +
`on_complete`; see `docs/stories/background-jobs/`), which runs the loop off the
turn loop with no depth limit. That runner is the documented next step for
unbounded autonomy.

## Two gate modes (`world.gate_mode`)

| Mode | Checker | Use for |
|---|---|---|
| `script` (default) | `host.run` the command in `gate_command`; pass iff exit 0 | mechanically checkable goals — tests pass, type-checks, lint clean. Deterministic, free, incorruptible. |
| `agent` | `host.agent.decide` adversarially reviews the artifact; pass iff verdict `pass` | goals no test can encode — prose quality, design soundness. |

The script gate is the strongest maker/checker split (code can't be talked into
passing) and costs nothing, so it is the default. Reach for the agent gate only
when the goal is subjective.

## Termination — goal met OR budget hit

Evaluated every turn after a failed gate, in priority order:

1. **goal met** → `@exit:achieved` (always wins)
2. **cost ceiling** → `@exit:exhausted` — `world.session_cost_usd` (the reserved,
   engine-maintained $ spend; see `docs/stories/state-machine.md` §6) ≥
   `cost_budget_usd`, when that budget is > 0
3. **iteration ceiling** → `@exit:exhausted` — `iteration` ≥ `iteration_budget`

## Configure & run

```
configure goal="Get go test ./internal/ratelimit green" artifact="internal/ratelimit/limiter.go" gate_command="go test ./internal/ratelimit/" iteration_budget=3
launch     # proves the gate is RED, then runs the loop to completion on its own
```

That's it — `launch` runs the whole loop. No per-iteration prodding.

## Tracking / restart / share

Each iteration writes `iteration-<n>` via `host.artifacts_dir` under
`.artifacts/`, recording the maker's summary and the gate failure it acted on —
the run trail that makes a loop auditable and resumable.

## Tests

Deterministic, no-LLM flow fixtures under `flows/` cover: achieved (script +
agent gates), iteration-budget exhaustion, cost-budget exhaustion, the
feedback-into-next-iteration edge, and a full multi-iteration run to the ceiling.

```
kitsoki test flows stories/cherny-loop/app.yaml
```

## Implementation note (engine discipline)

The gate result is bound by a host call in `gating.on_enter`; routing is done by
**guarded `emit_intent`s that compare `gate_ok` to a concrete bool**. `gate_ok`
defaults to `""` (tri-state) and is reset before each check, so every routing
guard is false in the pre-bind emit pass and the routing defers to the post-bind
pass once the checker has run — the bugfix decision-emit discipline. See
`rooms/gating.yaml`.

The autonomous self-loop relies on `loop_again` targeting `iterating` by its
**explicit state name** (not `.`): an `emit_intent` only re-runs a target's
`on_enter` when the target differs from the current state (the maker room and
checker room alternate, so each hop is a real state change). Per-iteration host
responses are addressable via a templated invoke `id:` (`maker-{{iteration}}` /
`gate-{{iteration}}`) threaded into `args.call` — `by_call:` / cassettes select
on it.
