# Idea to Implementation

Autonomous intake story for a free-form bug or feature idea.

The story performs one LLM classification step, then hands off to an existing
operation-backed implementation story:

- bug ideas enter `stories/bugfix` through its free-form complaint path and use
  the imported `bf__bugfix_full` operation.
- feature ideas enter `stories/ship-it` with a scoped brief and deterministic
  gate, then use the imported `ship__ship_it` operation.

There are no manual accept/approve/continue gates in this parent story. The
selected child operation declares `run_in_background: true`, so normal live
surfaces auto-drive it after launch. The classifier may stop at `needs_human`
only when it returns necessary clarification questions, and downstream child
operations may still stop on declared safety conditions such as host errors,
conflicts, weak/already-green gates, failed gates, or operator-ask.

Run standalone:

```sh
kitsoki run stories/idea-implementation/app.yaml
```

For a seeded run, use any flow/warp-basis file with `initial_world.idea` set
and pass it with `--warp`.
