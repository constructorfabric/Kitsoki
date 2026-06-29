# Delivery Loop Stories

The delivery loop is the shipped story stack for turning a scoped brief into
verified work on the integration branch:

```
brief -> maker worktree -> deterministic gate -> lost-work-safe integrate
      -> re-verify on the merged commit -> cleanup -> shipped | needs-human
```

The stack is intentionally split:

- `stories/ship-it/` delivers one brief. It imports the maker loop, integrates
  the result safely, re-runs the same gate on the merged commit, and exits with
  either `shipped` or `needs-human`.
- `stories/fleet/` fans `ship-it` over a brief list and keeps integration
  serialized behind its board state.
- `stories/deliver/` is the epic front door: decompose an accepted epic into a
  validated brief manifest, lint it, then hand the manifest to `fleet`.
- `stories/git-ops/` remains the standalone operator story for normal git
  workflow and the shared design reference for lost-work-safe integration.

## `ship-it`

`ship-it` is the single-brief delivery story. Its contract is:

1. Capture `brief` and `gate_command`.
2. Run the maker in an isolated worktree.
3. Integrate the produced branch onto the current integration branch.
4. Re-run `gate_command` on the merged commit, not only in the maker worktree.
5. Clean up the worktree and branch.

Every failure path carries `last_error` and exits `needs-human`; it must not
report a shipped result from a swallowed host failure.

The story's operator reference lives in
[`stories/ship-it/README.md`](../../stories/ship-it/README.md).

## `fleet`

`fleet` loads a decomposition manifest into a board and dispatches each pending
brief through `ship-it`. A brief that exits `needs-human` is parked with its
error while the board can continue with other work. The v1 board is deliberately
deterministic and fixture-friendly; it does not require a real LLM to validate
the fan-out mechanics.

## `deliver`

`deliver` is the author-facing entry point above `fleet`. It takes an epic,
asks for or creates a decomposition manifest, runs the deterministic linter, and
routes into `fleet.load` when the manifest is valid. This is the practical,
shipped path that covers the earlier work-decomposition proposal's simpler
validated-manifest lane.

## Validation

Use no-LLM flow fixtures when changing these stories:

```sh
go run ./cmd/kitsoki test flows stories/ship-it/app.yaml
go run ./cmd/kitsoki test flows stories/fleet/app.yaml
go run ./cmd/kitsoki test flows stories/deliver/app.yaml
```

The gate command inside a real brief should also be deterministic: `go test`,
`pnpm test`, `kitsoki test flows`, or another scriptable check. Do not use a
live LLM as the proof that a delivery is done.
