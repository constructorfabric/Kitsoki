# Bugfix task — staged, in-scope

You are fixing a single bug in the kitsoki repository (Go + TypeScript). You are
working in a hermetic worktree checked out at the commit BEFORE the fix, so the
bug is present. Follow the stages below in order. Do not edit unrelated code.

## Bug context

**Component:** runtime · **Severity:** P1

**Symptom:** A `host.agent.decide` call that declares a
`validator: { post_cmd: ... }` block **fails the whole job** with
`validator: session abandoned without successful submit after N outer
iteration(s)` — even though the LLM **did** call `submit` and the schema-valid
payload WAS captured to disk. The submit succeeded; it is the post-submission
(semantic) `post_cmd` gate that never passes, and that failure is mis-reported
as a missing submit.

Root cause (`internal/host/agent_decide.go`): when a validator block is present,
the submitted payload is captured schema-only, then the `post_cmd` is run
separately in a read-only sandbox. In the retry loop, the schema-passed branch
requires the sandboxed `post_cmd` to exit 0 before accepting; when the `post_cmd`
**cannot run** (argv0 not on the sandbox allowlist, or `post_cmd_cwd` dropped so
the interpreter can't import its module), `runDecideSandboxValidator` returns a
non-empty rejection. An infrastructure failure (can't exec) is folded into the
SEMANTIC-rejection path — it burns every retry and ends as "abandoned." The
captured payload at the validator output path is never consulted as a fallback,
so a successful capture + failing post_cmd is indistinguishable from "never
submitted," and the terminal message hides the real cause.

**Expected:** a schema-valid submit whose `post_cmd` can't run is either (a)
accepted on the schema capture, or (b) surfaced as a hard, NAMED error
("post_cmd could not execute" / "rejected: <reason>") — NOT silently retried to
"abandoned." Honoring `post_cmd_cwd` in the sandbox path (and distinguishing
post_cmd *infrastructure failure* from *semantic rejection*) is the true fix.

**Actual:** un-runnable post_cmd → permanent semantic rejection → wasted outer
iterations → mis-labeled "abandoned without successful submit."

**Investigation hints:**
- `internal/host/agent_decide.go` — `runDecideWithValidatorRetryLoop`, the
  `mcpOutcomeSuccess` branch, `runDecideSandboxValidator`,
  `RunValidatorSandboxed`, and whether `post_cmd_cwd` is honored.

## Stages — do these IN ORDER

1. **REPRODUCE (RED first).** Write a focused failing test that reproduces the
   bug: a `decide` with a validator `post_cmd` that cannot run cleanly in the
   sandbox, where the agent DOES submit a schema-valid payload, and assert the
   outcome is NOT a silent "abandoned without successful submit" (either accepted
   on the schema capture, or a hard named post_cmd error). Run it and CONFIRM IT
   IS RED. Do not proceed until the red test fails for the right reason.
2. **IMPLEMENT (minimal fix).** Make the smallest change that fixes the root
   cause. Stay in scope.
3. **VERIFY (GREEN + no regressions).** Re-run your test and confirm it is GREEN.
   Then run the surrounding suite and confirm nothing regressed.

## Build / test commands

```
go build ./...
go test ./internal/host/...
go test ./internal/host/ -run <YourTestName>   # your repro test
```

## Rules

- Write your OWN reproduction test; do not look for or rely on any pre-existing
  hidden regression test. Mock the agent — do NOT make a real LLM call.
- Keep the change minimal and in-scope; do not refactor or touch unrelated code.
- Honor the stage order: reproduce (red) → implement → verify (green).
