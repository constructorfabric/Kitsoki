# Runtime: idempotent on_enter host calls (`once:`)

**Status:** `once:` shipped. Remaining: `/reload --force` to bypass it during
authoring — Open question 1.
**Kind:**   runtime
**Epic:**   — standalone

The core mechanism shipped: an opt-in `once: true` flag on an `invoke:`
effect makes the engine skip the call when every one of its `bind:` targets
is already set (non-empty), so `/reload`, self-transitions, and `on_error`
re-entry re-render from the cached world instead of recomputing an expensive,
non-idempotent host call. The durable description lives in
[`docs/stories/state-machine.md`](../stories/state-machine.md)
§"`on_enter` must be idempotent" (the `once: true` bullet). The
`stories/dev-story/rooms/proposal_*.yaml` rooms are migrated off their
hand-rolled `when: "<result> == ''"` guards onto `once:`.

Code seams that shipped: `app.Effect.Once` (`internal/app/types.go`); the
skip check + `allBindTargetsSet`/`worldValueSet` helpers and the
`EffectApplied{skipped:"cached"}` record in `internal/machine/machine.go`
(`applyEffectsTraced`); the load-time `once: true requires a non-empty bind:`
invariant in `internal/app/loader.go`. Tests: `TestOnce_*` (machine),
`TestOnceEffectValidation` (app), `TestRerunOnEnter_OnceSkipsCachedInvoke`
(orchestrator), plus `stories/dev-story/flows/proposal_reload_safe.yaml`.

## Remaining: `/reload --force` (Open question 1)

During authoring you `/reload` to test a prompt edit — `once:` now skips the
call, so the edit doesn't take effect without clearing the bind target by
hand, which is awkward mid-authoring. The deferred slice is a `--force` flag
on the reload command that bypasses `once:` for a single turn (re-runs the
on_enter chain ignoring the cache). Options considered:

- **(a) `/reload --force`** — a force flag on the reload command. *Lean.*
  Threads a `force` bool from the slash command through `RerunOnEnter` into
  the effect runner, which ignores `eff.Once` for that one synthetic turn.
- (b) Accept that you clear the world key to re-run (status quo — works, but
  awkward).
- (c) Honor `once:` only outside a "dev/authoring" session mode.

Until this lands, the workaround is (b): clear the bind target (e.g.
`set: { proposal_brief_decision: {} }` via the room's existing re-run intent)
to re-arm the call, then re-enter.
