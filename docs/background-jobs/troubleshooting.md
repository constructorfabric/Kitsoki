# Troubleshooting Background Jobs

Common pitfalls and how to fix them.

---

## "My `on_complete` never fires."

**Check 1: scheduler not wired.**

`on_complete:` requires the orchestrator to have a scheduler. If
`WithScheduler` is not passed to `orchestrator.New(...)`, background effects
are dispatched synchronously and `on_complete:` is silently skipped.

```go
// Production: wire scheduler and job store
orch := orchestrator.New(def, m, st, h,
    orchestrator.WithScheduler(sched),
    orchestrator.WithJobStore(js),
)

// Tests: the flow runner does this automatically when host_handlers: is declared
```

**Check 2: `WaitListenerIdle` not called in tests.**

The session listener goroutine processes events asynchronously. If your test
calls `WaitIdle` on the scheduler but not `WaitListenerIdle` on the
orchestrator, `on_complete:` may not have run yet when you assert on world.

```go
// Correct sequence:
if err := sched.WaitIdle(ctx); err != nil { t.Fatal(err) }
time.Sleep(2 * time.Millisecond)  // yield so channel delivers event
if err := orch.WaitListenerIdle(ctx, sid); err != nil { t.Fatal(err) }
```

In flow fixtures, use `advance_clock:` — the runner calls this sequence for you.

**Check 3: loader rejected `on_complete:` at app load.**

If `on_complete:` contains `background: true` at any depth, the loader rejects
it and logs an error. Check the `app.Load(...)` error return. The error message
includes the location: `state "X" on_enter[0] on_complete[1]: background: true
is not allowed inside on_complete:`.

**Check 4: `on_complete:` is empty but you declared `background: true` on a
`set:` effect.**

```yaml
- set: { x: 1 }
  background: true   # ← loader error: requires invoke:
```

This causes a load error. The loader requires `invoke:` when `background: true`
is set.

---

## "`world.last_job_result` is empty in my `on_enter:`."

This is the same-turn race. `last_job_result` is only injected during the
`on_complete:` synthetic turn. It is not available in the same `on_enter:` or
`effects:` block that fired the background job.

```yaml
# WRONG — last_job_result is empty here (same turn as background dispatch)
on_enter:
  - invoke: host.run
    background: true
  - set: { result: "{{ world.last_job_result.stdout }}" }  # empty!

# RIGHT — use on_complete: instead
on_enter:
  - invoke: host.run
    background: true
    on_complete:
      - set: { result: "{{ world.last_job_result.stdout }}" }  # available here
```

---

## "Test passes locally but races on CI."

Races in background-job tests are almost always caused by one of:

1. **Missing `WaitIdle` / `WaitListenerIdle` calls.** Without these, assertions
   run before the scheduler or listener have finished. Use the correct sequence
   in that order with a real-time deadline via `context.WithTimeout`.

2. **Clock not advanced before asserting.** If the stub handler has `delay: "1s"`
   but no `advance_clock:` is set on the turn, the handler goroutine is still
   blocked on `Sleep` when assertions run. Add `advance_clock: "2s"` (or more
   than the max handler delay) to the turn.

3. **Goroutine leak detected.** Check `runtime.NumGoroutine()` before and after
   the test. Leaked goroutines are usually blocked in `RequestClarification`'s
   poll loop (the handler goroutine is waiting for an answer that never comes)
   or in a session listener whose context was never cancelled. Cancel the
   orchestrator context and call the scheduler's job cancel for all running jobs.

4. **Event dropped from session channel.** The session channel has capacity 16.
   If more than 16 terminal events fire before the listener goroutine processes
   them (unlikely in a test but possible in a heavily parallel suite), events are
   dropped silently. Increase channel capacity or serialise test execution.

---

## "Inbox panel doesn't refresh."

The TUI's inbox panel uses `tea.Tick` (a real-time ticker) to poll the
`jobs.JobStore` for new notifications. In flow tests this ticker is not running
— the flow runner uses the job store directly to assert inbox counts via
`expect_inbox:`.

In live TUI tests that spin up a full `bubbletea` program, the ticker fires on
real wall time. There is currently no mechanism to inject a fake clock into the
TUI's ticker. If you need to assert inbox state in a TUI integration test, poll
with a small timeout rather than relying on the ticker period.

---

## "Clarification handler hangs forever."

`host.RequestClarification` blocks the handler goroutine in a poll loop reading
`AnswerClarificationRaw` every 200 ms. It will block until:
- An answer is written via `AnswerClarification` (by `host.jobs.answer_clarification`), or
- The context is cancelled.

Common causes of a hang:

1. **The user never fires `answer_clarification`.** The flow has no turn that
   calls `host.jobs.answer_clarification` with the correct `job_id` and `answer`.

2. **The fake clock is not advanced past the poll interval.** The poll loop uses
   `host.ClockFromContext(ctx).After(200ms)`. With a fake clock, this waits until
   `clk.Advance(≥ 200ms)` is called. The flow runner's `advance_clock:` handles
   this automatically.

3. **`host.jobs.answer_clarification` not in `hosts:` allow-list.** The loader
   will reject the effect at load time with an "invoke not in hosts list" error.
   Add `- host.jobs.answer_clarification` to your `hosts:` section.

4. **`ClarificationAnswerer` not injected.** The orchestrator must call
   `host.WithClarificationAnswerer(ctx, jobStore)` before dispatching foreground
   effects. This happens automatically when `WithJobStore` is passed to the
   orchestrator. In custom orchestrator setups, ensure this option is set.

---

## "No session listener goroutine started."

`startSessionListener` is called only when a scheduler is wired. If your
orchestrator was built without `WithScheduler`, there is no listener and
`WaitListenerIdle` returns immediately (it treats the missing listener as
already-idle). Background jobs will still run (if dispatched synchronously) but
`on_complete:` effects will never fire.

---

## See also

- [`README.md`](README.md) — entry point and glossary.
- [`testing.md`](testing.md) — `advance_clock:`, `WaitIdle`, `WaitListenerIdle`.
- [`runtime.md`](runtime.md) — goroutine lifecycle and idle detection.
- [`authoring.md`](authoring.md) — forbidden patterns the loader rejects.
