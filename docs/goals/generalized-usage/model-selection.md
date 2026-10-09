# Model Selection Policy

Goal-seeker worker dispatches use a cheap-first harness ladder for the live
punch-list maker boundary. The starting rung is synthetic GLM via the codex
backend; fallback rungs move through codex-native, codex-spark, sonnet, and
opus, sweeping effort from `low` to `max` before moving to a stronger model.

The policy is emitted into each generated punch-list manifest as
`defaults.harness_ladder` and passed through punch-list to the `host.agent.task`
maker call as `with.harness_ladder`. This keeps the model pool attached to the
actual live worker dispatch, not just to an operator-global web config.

No automated gate may spend LLM budget. The deterministic proof is split:

- `go test ./internal/host/ -run Ladder ./internal/webconfig/` proves the ladder
  engine and per-call `harness_ladder` fallback behavior with fake runners.
- `kitsoki test flows stories/goal-seeker/app.yaml --flows stories/goal-seeker/flows/worker_pool_quota.yaml`
  proves the goal-seeker emits the ladder policy into the worker manifest and
  reaches the imported punch-list maker dispatch with no live agent call.

Live runs still record the actual model/backend/effort on agent trace metadata,
so later model-selection evaluation can compare change archetype, rung, outcome,
and cost without relying on chat context.
