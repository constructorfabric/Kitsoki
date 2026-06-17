# cherney-loop

Facilitate a **Cherney loop** (loop engineering, after Boris Cherny): an agent
iterates toward a goal until a **gate** proves the goal is *actually met* — or a
**budget** ceiling is hit. Each iteration is a visible, tracked turn; every
iteration is persisted as a numbered artifact so the run is restartable and
shareable.

## The loop

```
configuring → iterating ⇄ gating
                  │           ├─ goal met     → @exit:achieved
                  │           ├─ budget hit   → @exit:exhausted
   abort ─────────┴───────────┴─ operator     → @exit:abandoned
```

`configuring` is the root — the operator lands where they act, with no `idle`/
`begin` pass-through turn.

- **iterating** — the **maker** (`host.oracle.task`) makes the smallest change
  toward the goal, fed the *previous gate's failure reason* as feedback (the
  ralph-style reset: anchors + one failure reason, not a growing transcript).
- **gating** — the **checker** runs and routes. `evaluate` gates the current
  iteration; a pass exits, a failure loops back with the reason captured.

## Two gate modes (`world.gate_mode`)

| Mode | Checker | Use for |
|---|---|---|
| `script` (default) | `host.run` the command in `gate_command`; pass iff exit 0 | mechanically checkable goals — tests pass, type-checks, lint clean. Deterministic, free, incorruptible. |
| `oracle` | `host.oracle.decide` adversarially reviews the artifact; pass iff verdict `pass` | goals no test can encode — prose quality, design soundness. |

The script gate is the strongest maker/checker split (code can't be talked into
passing) and costs nothing, so it is the default. Reach for the oracle gate only
when the goal is subjective.

## Termination — goal met OR budget hit

Evaluated every turn after a failed gate, in priority order:

1. **goal met** → `@exit:achieved` (always wins)
2. **cost ceiling** → `@exit:exhausted` — `world.session_cost_usd` (the reserved,
   engine-maintained $ spend; see `docs/stories/state-machine.md` §6) ≥
   `cost_budget_usd`, when that budget is > 0
3. **iteration ceiling** → `@exit:exhausted` — `iteration` ≥ `iteration_budget`

## Configure

```
configure goal="Make the unit tests pass" gate_command="go test ./..." iteration_budget=8 cost_budget=0.50
launch
```

Then `evaluate` each iteration until the loop exits.

## Tracking / restart / share

Each iteration writes `iteration-<n>` via `host.artifacts_dir` under
`.artifacts/`, recording the maker's summary and the gate failure it acted on —
the run trail that makes a loop auditable and resumable.

## Tests

Deterministic, no-LLM flow fixtures under `flows/` cover: achieved (script +
oracle gates), iteration-budget exhaustion, cost-budget exhaustion, the
feedback-into-next-iteration edge, and a full multi-iteration run to the ceiling.

```
kitsoki test flows stories/cherney-loop/app.yaml
```

## Implementation note (engine discipline)

The gate result is bound by a host call in `gating.on_enter`; routing is done by
**guarded `emit_intent`s that compare `gate_ok` to a concrete bool**. `gate_ok`
defaults to `""` (tri-state) and is reset on each `evaluate`, so every routing
guard is false in the pre-bind emit pass and the routing defers to the post-bind
pass once the checker has run — the bugfix decision-emit discipline. See
`rooms/gating.yaml`.
