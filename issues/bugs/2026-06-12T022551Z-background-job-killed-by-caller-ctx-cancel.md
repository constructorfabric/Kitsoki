---
id: 2026-06-12T022551Z-background-job-killed-by-caller-ctx-cancel
title: "Background jobs inherit the submitting turn's context and die with \"context canceled\" the instant the turn returns (web mode)"
target: kitsoki
filed_at: 2026-06-12T02:25:51Z
status: resolved
severity: P1
component: runtime
kitsoki_rev: 0c9a0ff
resolved_at: 2026-06-12T02:25:51Z
resolved_in_commit: af995fc
trace_ref: "~/.kitsoki/sessions/bugfix/94c6daa4-web-0391e58b-f236-4261-a74b-ca107674f5aa.jsonl"
external: {}
assignee: ""
url: "issues/bugs/2026-06-12T022551Z-background-job-killed-by-caller-ctx-cancel.md"
---

## Body

A background job (`background: true` host call) is meant to **outlive** the
turn that submits it — the submitting room transitions to a `…executing`
state and the view auto-refreshes when the job completes. But the scheduler
derived the job's context from the **caller's** context:

```go
// internal/jobs/jobs.go, Submit()
jobCtx, cancel := context.WithCancel(ctx)   // ctx = the submitting turn's context
```

In `kitsoki web`, the caller's `ctx` is the **per-turn HTTP request context**,
which is cancelled the instant the turn handler returns. So the request
returning killed the in-flight background handler (here a `host.oracle.decide`
→ `claude` exec) with `context canceled`, ~immediately. The room sat forever
on its `…executing` view because the job it was waiting on never ran.

This only bites in web mode. In the TUI the dispatch context lives as long as
the process, so the same job survives.

Adjacent to `2026-06-03T121407Z-oracle-decide-silent-abandon-empty-artifact.md`
(also a decide-fails-invisibly shape) but a distinct root cause: there the LLM
returned no submit; here the exec never got to run at all.

### Steps to reproduce

1. `kitsoki run stories/bugfix/app.yaml` via the **web** transport (`kitsoki web`).
2. Set up a ticket so the `ticket_setup` room dispatches the
   `phase_minus_1` context-extraction as a background `host.oracle.decide`
   and transitions to `phase_minus_1_executing`.
3. Observe the room render *"running — this takes ~30 seconds; the screen will
   refresh automatically…"* and then never refresh.

### Expected vs actual

- **Expected:** the background oracle call runs to completion (~30s) and the
  room advances when the artifact is bound.
- **Actual:** the job dies `~96ms` after start with
  `host.oracle.decide: claude exec failed: context canceled`; the UI sits on
  the stale "running…" view (the `background_completion` turn's failure
  `say` / `last_error` never surfaced over the web transport, so it "looks
  hung").

### Trace evidence

From `94c6daa4-web-…jsonl`, turn 4 → turn 5:

```
turn 4  ticket_setup  -> phase_minus_1_executing  (view: "running — ~30s…")
        scheduler.submitted job 01KTWS4D2RE8QX1PBPK61VYSC4 host.oracle.decide
        turn.end @ 02:03:47.220
turn 5  background_completion @ 02:03:47.273   (53ms later)
        oracle.call.error duration_ms=96
          error="host.oracle.decide: claude exec failed: context canceled"
        scheduler.completed status=failed
```

`duration_ms: 96` is far too fast for `claude` to have actually run — the
context was cancelled out from under it as the turn returned.

## Root cause

`internal/jobs/jobs.go` `Submit()` wrapped the caller's `ctx` directly with
`context.WithCancel`, binding the background job's lifetime to the submitting
turn's (request-scoped, in web mode) context.

## Fix

Detach from the caller's cancellation while preserving its values (trace
sink, oracle-call ctx), then wrap in the scheduler's own cancel so explicit
`Cancel(id)` remains the sole abort path:

```go
jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
```

Safe because the scheduler already owns explicit cancellation (the `cancels`
map / `Cancel(id)`) and shutdown goes through `SweepStaleJobs` on a background
context — nothing relied on the parent ctx to stop jobs.

Regression test `TestSubmit_SurvivesCallerContextCancel` in
`internal/jobs/jobs_test.go`: submits under a caller ctx, cancels it, asserts
the job keeps running, then confirms explicit `Cancel(id)` still terminates it.
Mutation-verified — reverting the one-liner makes the test fail
(`job terminated after caller ctx cancel (got cancelled)`).

## Follow-up (not in this fix)

Even on a genuine background-job failure, the web UI did not surface the
`background_completion` turn's `last_error` / failure `say` — which is why it
"looked hung" rather than showing the error. That web-transport rendering gap
is a separate issue worth filing.
