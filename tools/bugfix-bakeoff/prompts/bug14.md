# Bugfix task — staged, in-scope

You are fixing a single bug in the kitsoki repository (Go + TypeScript). You are
working in a hermetic worktree checked out at the commit BEFORE the fix, so the
bug is present. Follow the stages below in order. Do not edit unrelated code.

## Bug context

**Component:** web · **Severity:** P2

**Symptom:** When a background job fails, the engine fires a
`background_completion` turn that sets `last_error` and emits a `machine.say`
describing the failure. In the **web** transport this turn's output does not
reach the browser — the user keeps seeing the stale `…executing` ("running…")
view and the session "looks hung" rather than showing the error. The engine
reports the failure; the web transport drops it.

**Expected:** when a `background_completion` turn lands, the web UI re-renders
the destination view (and any `say` / `last_error`) so the operator sees the
job's terminal status.

**Actual:** the web UI stays on the pre-completion `…executing` view; the failure
`say` and `last_error` are dropped.

**Investigation hints:**
- The web run store `tools/runstatus/src/stores/run.ts`: the SSE subscription
  pushes events and applies the state path but never refreshes `currentView`.
  `currentView` is written only by the RPC-driven turn-result path, the
  once-only initial-view load, and meta-mode rehydrate — none of which a
  scheduler-driven `background_completion` turn triggers. So the operator stays
  on the stale `…executing` view.
- The fix: refetch the destination view in the run store when a `turn.end` with
  `outcome=background_completion` (or a newly-landed state) arrives over SSE,
  mirroring the TUI's re-render-on-completion.

## Stages — do these IN ORDER

1. **REPRODUCE (RED first).** Write a focused failing test (vitest, in
   `tools/runstatus`) that reproduces the bug: deliver a `background_completion`
   `turn.end` over SSE WITHOUT an inbound RPC turn, and assert that the run
   store's `currentView` updates to the destination view (surfacing
   `last_error`). Run it and CONFIRM IT IS RED. Do not proceed until the red test
   fails for the right reason.
2. **IMPLEMENT (minimal fix).** Make the smallest change so a
   background-completion SSE event refreshes the view. Stay in scope.
3. **VERIFY (GREEN + no regressions).** Re-run your test and confirm it is GREEN.
   Then run the surrounding suite and confirm nothing regressed.

## Build / test commands

```
# from repo root (pnpm workspace):
pnpm --filter runstatus test
pnpm --filter runstatus test <your-test-file>   # your repro test
```

## Rules

- Write your OWN reproduction test; do not look for or rely on any pre-existing
  hidden regression test.
- Keep the change minimal and in-scope; do not refactor or touch unrelated code.
- Honor the stage order: reproduce (red) → implement → verify (green).
