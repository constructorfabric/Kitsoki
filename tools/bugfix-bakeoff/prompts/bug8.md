# Bugfix task — staged, in-scope

You are fixing a single bug in the kitsoki repository (Go + TypeScript). You are
working in a hermetic worktree checked out at the commit BEFORE the fix, so the
bug is present. Follow the stages below in order. Do not edit unrelated code.

## Bug context

**Component:** runtime · **Severity:** P1

**Symptom:** When the LLM behind a `host.agent.decide` call answers in prose (or
returns an empty `{}`) WITHOUT calling the `submit` tool, the engine transitions
to the SUCCESS arc carrying an **empty artifact** instead of routing to a
failure state. An un-submitted / empty decide silently succeeds with no payload,
undermining the deterministic-verdict guarantee. Where the artifact is templated
from world, this can render a literal `<map[...]Value>` into the file, which then
cascades into a downstream `on_error` bounce.

**Expected:** an un-submitted / empty decide is treated as a FAILURE — it routes
to a dedicated failure state rather than the success arc; the artifact body is
validated non-empty before the success arc fires. (When the LLM answers in prose
without submitting, a friendly clarification is preferable to a raw harness
error, but the load-bearing fix is: empty/no-submit must not take the success
arc.)

**Actual:** silent success, empty artifact, downstream failure.

**Investigation hints:**
- `internal/host/` — the `agent.decide` result handling and how a no-submit /
  empty `{}` result is classified.
- `internal/orchestrator/` — gate routing on an empty/absent submit (success arc
  vs failure arc).

## Stages — do these IN ORDER

1. **REPRODUCE (RED first).** Write a focused failing test that reproduces the
   bug: drive a `host.agent.decide` whose backing agent returns prose / an empty
   `{}` with NO `submit` call, and assert the result is a FAILURE (does not take
   the success arc with an empty artifact). Run it and CONFIRM IT IS RED. Do not
   proceed until you have a red test that fails for the right reason.
2. **IMPLEMENT (minimal fix).** Make the smallest change so a no-submit / empty
   decide is treated as a failure rather than an empty-artifact success. Stay in
   scope.
3. **VERIFY (GREEN + no regressions).** Re-run your test and confirm it is GREEN.
   Then run the surrounding suite and confirm nothing regressed.

## Build / test commands

```
go build ./...
go test ./internal/host/... ./internal/mcp/...
go test ./internal/host/ -run <YourTestName>   # your repro test
```

## Rules

- Write your OWN reproduction test; do not look for or rely on any pre-existing
  hidden regression test. Mock the agent — do NOT make a real LLM call.
- Keep the change minimal and in-scope; do not refactor or touch unrelated code.
- Honor the stage order: reproduce (red) → implement → verify (green).
