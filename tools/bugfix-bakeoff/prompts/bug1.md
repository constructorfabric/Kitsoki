# Bugfix task — staged, in-scope

You are fixing a single bug in the kitsoki repository (Go + TypeScript). You are
working in a hermetic worktree checked out at the commit BEFORE the fix, so the
bug is present. Follow the stages below in order. Do not edit unrelated code.

## Bug context

**Component:** tui · **Severity:** P2

**Symptom:** When a state's `on_enter:` chain includes a synchronous `invoke:`
with a `bind:` projection (e.g. `iface.vcs.diff` binding to
`world.feature_branch_diff`), the bound world key is populated AFTER the first
TUI view is rendered for that state. The first frame therefore renders against
the PRE-bind world snapshot, so any view template referencing the bound key
shows the schema default (e.g. `""` or `0`) until the next intent turn refreshes
the view.

**Expected:** the TUI either (a) defers rendering until the `on_enter` bind
chain completes so the first frame shows the bound value, or (b) the loader
detects `view:` references to a bind-target that lack a `??` fallback and emits a
warning. The runtime re-render (option a) is the correct fix; the loader lint
(option b) is the cheap mitigation.

**Actual:** silent first-frame stale render, no diagnostic.

**Investigation hints:**
- `internal/orchestrator/orchestrator.go` — `runOnEnter` ordering vs the TUI
  subscription/notification (does the view render fire before or after the
  `on_enter` chain binds complete?).
- `internal/tui/transcript.go` — the view render entry point.

## Stages — do these IN ORDER

1. **REPRODUCE (RED first).** Write a focused failing test that reproduces the
   bug: a state whose `on_enter` binds a world key, whose `view:` references that
   key without a `??` fallback, asserting that the first rendered frame shows the
   BOUND value (it currently shows the default). Run it and CONFIRM IT IS RED.
   Do not proceed until you have a red test that fails for the right reason.
2. **IMPLEMENT (minimal fix).** Make the smallest change that fixes the root
   cause. Stay in scope — touch only what this bug requires.
3. **VERIFY (GREEN + no regressions).** Re-run your test and confirm it is now
   GREEN. Then run the surrounding suite and confirm nothing regressed.

## Build / test commands

```
go build ./...
go test ./internal/orchestrator/... ./internal/app/...
go test ./internal/orchestrator/ -run <YourTestName>   # your repro test
```

## Rules

- Write your OWN reproduction test; do not look for or rely on any pre-existing
  hidden regression test.
- Keep the change minimal and in-scope; do not refactor or touch unrelated code.
- Honor the stage order: reproduce (red) → implement → verify (green).
