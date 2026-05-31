# Testing Background Jobs

How to test the background-job lifecycle deterministically with flow fixtures.

## The orchestrator path (auto-upgrade)

When a flow fixture declares any of the following, the test runner automatically
uses the **orchestrator-backed path** instead of the legacy machine-only path:

- `host_handlers:` — any entry.
- Any turn with `advance_clock:`.
- Any turn with `expect_inbox:`.

You can also set `use_orchestrator: true` explicitly. Setting
`use_orchestrator: false` forces the legacy path regardless.

The orchestrator path wires:
- An in-memory SQLite store.
- A `jobs.JobStore` backed by that store.
- A `jobs.NewScheduler` using a `clock.Fake` at epoch zero.
- A `host.Registry` populated from `host_handlers:`.
- An `orchestrator.Orchestrator` with `WithScheduler` and `WithJobStore`.
- A per-session listener goroutine.

## `host_handlers:` — the `HostStub` fields

```yaml
host_handlers:
  host.run:
    data:
      stdout: "hello"
      exit: 0
    delay: "1s"

  host.slow_and_fails:
    error: "something_went_wrong"    # domain-level error (Result.Error)
    delay: "500ms"

  host.infra_error:
    infra_error: "connection refused" # infrastructure failure (Go error)
```

| Field | Type | Purpose |
|---|---|---|
| `data` | map | Returned as `host.Result.Data` on a successful invocation. |
| `error` | string | Non-empty → sets `host.Result.Error`; job terminates as `failed`. |
| `infra_error` | string | Non-empty → handler returns `(Result{}, error)`; job terminates as `failed`. |
| `delay` | duration | Blocks the stub handler for this **virtual** time using `host.ClockFromContext(ctx).Sleep(d)`. |

`delay` and `error`/`infra_error` are independent. You can combine them:
a slow then failing handler is `delay: "2s"` plus `error: "timeout"`.

Only one of `error` / `infra_error` should be set per stub.

## `advance_clock:` — the per-turn key

```yaml
turns:
  - intent: { name: start }
    advance_clock: "2s"    # move fake clock forward by 2 s
    expect_world:
      result: "hello"      # assertions run AFTER clock advance + drain
```

After `advance_clock:` fires, `advanceAndWait` runs an **event-driven** drain
loop (no real-time sleep):

1. Subscribe to the session's job-event channel (before advancing, to avoid
   missing events).
2. `clk.Advance(d)` moves the fake clock forward, unblocking any timers.
3. `sched.WaitIdle(ctx)` — blocks until all job goroutines are terminal or
   `awaiting_input`.
4. `orch.WaitListenerIdle(ctx, sid)` — blocks until the listener goroutine has
   processed all queued terminal events (runs `handleJobTerminal` and
   `on_complete` effects).
5. Non-blocking drain of the session channel: if cascading `on_complete` effects
   dispatched new jobs, new terminal events arrive here; repeat steps 3–4 for
   each one.
6. After `WaitIdle` + `WaitListenerIdle` both return with the channel empty the
   system is quiescent.

The outer context deadline (typically 5 s) is the hard cap.

All turn assertions (`expect_state`, `expect_world`, `expect_inbox`, etc.) run
after this drain loop, so they see the post-completion world state.

## `expect_inbox:` — the per-turn assertion

```yaml
expect_inbox:
  unread: 2                          # total unread notification count
  needs_attention: 0                 # count of action_required severity
  severities: ["info", "success"]    # sorted list of all unread severities
```

All fields are optional; only the ones present are checked.

A successful background job produces **two** notifications by default:
- `info` — posted at job submission (`dispatchBackground`).
- `success` / `error` / `warn` — posted at job termination
  (`handleJobTerminal`).

So `expect_inbox: { unread: 2, severities: ["info", "success"] }` is the
typical assertion after a successful job.

## Patterns

### Happy path (mirrors `happy_path.yaml`)

```yaml
test_kind: flow
app: ../app.yaml
initial_state: lobby
initial_world:
  result: ""
  last_job_id: ""

host_handlers:
  host.run:
    data: { stdout: "hello", exit: 0 }
    delay: "1s"

turns:
  - intent: { name: enter }
    advance_clock: "2s"
    expect_state: running
    expect_world:
      result: "hello"
    expect_inbox:
      unread: 2
      severities: ["info", "success"]

expect_no_errors: true
```

The `advance_clock: "2s"` moves past the 1 s handler delay; the on_complete
chain fires and sets `world.result`.

### Error path

```yaml
host_handlers:
  host.run:
    error: "process_exited_nonzero"
    delay: "500ms"

turns:
  - intent: { name: enter }
    advance_clock: "1s"
    expect_inbox:
      unread: 2
      severities: ["error", "info"]
```

The `on_complete:` still fires. Use `{{ world.last_job_status }}` inside
`on_complete:` to branch:

```yaml
on_complete:
  - set:
      result: >-
        {{ world.last_job_status == "done"
           ? world.last_job_result.stdout
           : "failed: " + world.last_job_status }}
```

### Verifying clarification round-trip

There is no `request_clarification:` field in `HostStub` — clarification is
implemented inside the handler Go code. To test it deterministically, write a
custom handler in your app's test suite that calls
`host.RequestClarification(ctx, schema)`.

In a flow fixture you can simulate the answer by firing the
`answer_clarification` intent on a subsequent turn:

```yaml
host_handlers:
  host.my_task:
    delay: "500ms"   # stub that blocks waiting for answer (see Go handler)

turns:
  # Turn 1: submit the job; handler immediately calls RequestClarification
  - intent: { name: start }
    advance_clock: "200ms"   # enough for handler to reach RequestClarification poll
    expect_inbox:
      needs_attention: 1     # action_required notification posted

  # Turn 2: answer the clarification
  - intent:
      name: answer_clarification
      slots:
        job_id: "{{ world.last_job_id }}"
        answer: "main"
    advance_clock: "1s"      # job resumes and completes
    expect_inbox:
      unread: 3              # submitted + action_required + success
```

> Note: the `{{ world.last_job_id }}` template in slots is expanded before
> the turn fires, so `job_id` receives the real ULID.

### Verifying notification-driven UX

Use `expect_inbox:` to assert that a given severity appears. You can also chain
assertions across turns to verify read/dismiss behaviour once TUI-level Teleport
testing is supported.

## The `internal/clock` package in unit tests

For unit tests that don't use the flow runner, inject a `*clock.Fake` directly:

```go
clk := clock.NewFake(time.Unix(0, 0))

// Wait until the goroutine under test has parked on clk.After(...)
clk.BlockUntil(1)   // blocks until waitCount >= 1

// Now advance; the goroutine wakes and proceeds
clk.Advance(200 * time.Millisecond)
```

**`clock.Real()`** — use in all production code. Never call `time.Now()` or
`time.After()` directly in a package that must be tested deterministically.

**`clock.NewFake(start)`** — use in tests. Time does not advance on its own;
call `Advance(d)` or `Set(t)`.

**`Fake.BlockUntil(n)`** — blocks until at least `n` goroutines are registered
as waiters (via `After`, `Sleep`, `NewTimer`, `NewTicker`). Use this before
`Advance` to ensure the goroutine you are testing has actually called into the
clock before you advance it.

```go
// Pattern: start background work, wait for it to park, then advance.
go myFunc(ctx, clk)
clk.BlockUntil(1)
clk.Advance(1 * time.Second)
```

**Inject into the scheduler:**

```go
sched := jobs.NewScheduler(js, jobs.WithClock(clk))
```

The scheduler injects `clk` into the handler's context via `host.WithClock`,
so handler code that calls `host.ClockFromContext(ctx).Sleep(d)` uses the same
fake clock.

## See also

- [`README.md`](README.md) — entry point and glossary.
- [`authoring.md`](authoring.md) — YAML reference.
- [`troubleshooting.md`](troubleshooting.md) — common pitfalls.
- [`testdata/apps/background_jobs/flows/happy_path.yaml`](../../testdata/apps/background_jobs/flows/happy_path.yaml) — canonical example.
- [`internal/testrunner/flows.go`](../../internal/testrunner/flows.go) — full flow runner implementation.
- [`internal/clock/clock.go`](../../internal/clock/clock.go) — `Clock`, `Fake`, `BlockUntil`.
