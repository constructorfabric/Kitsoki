// Package jobs implements the background-job scheduler that lets a kitsoki
// state machine run a long-running [host.Handler] off the turn loop. It sits
// below the orchestrator: the orchestrator's dispatchBackground submits a
// [JobSpec] here, the handler runs in its own goroutine, and a terminal
// [JobEvent] flows back through a per-session subscription so the orchestrator
// can fire the originating state's on_complete effects. It also owns the
// SQLite-backed [JobStore] for jobs and inbox notifications.
//
// Two layers cooperate:
//
//   - [Scheduler] — the in-memory, goroutine-per-job runtime. Owns liveness:
//     submit, cancel, subscribe, heartbeat, idle tracking, and the
//     awaiting-input pause/resume used by mid-flight clarifications.
//   - [JobStore] — write-through persistence on a *sql.DB shared with the
//     session store. The job row is a current-state materialised view; the
//     event log (written by the orchestrator) remains authoritative for replay.
//
// # Algorithm
//
// [Scheduler.Submit] mints a ULID, records the job as running, and launches one
// goroutine that calls spec.Handler. When the handler returns, the goroutine
// classifies the outcome into exactly one terminal status — [JobCancelled] if
// the job's context was cancelled, [JobFailed] if the handler errored or its
// [host.Result] carried a non-empty Error, otherwise [JobDone] — persists the
// terminal row, and fans the [JobEvent] out to both per-job subscribers
// ([Scheduler.Subscribe]) and per-session subscribers ([Scheduler.SubscribeSession]).
//
// Idle tracking is a single counter, runningCount, guarded by its own mutex and
// condition variable. Submit increments it; the terminal block decrements it;
// [Scheduler.Awaiting] decrements it (a job blocked on the user is not "running
// work") and [Scheduler.Resumed] re-increments it. [Scheduler.WaitIdle] blocks
// on the condition until the counter reaches zero. Because awaiting-input both
// decrements (via Awaiting) and is later followed by a terminal decrement, a
// per-job awaitingCounted flag suppresses the second decrement so the counter
// can never go negative.
//
// Session subscriptions carry their own pending counter, incremented before the
// channel send and decremented by the consumer's ack callback. This closes the
// receive→process race that len(channel) cannot observe:
// [Scheduler.WaitSessionDrained] blocks while pending > 0, so the canonical
// "WaitIdle then WaitSessionDrained" sequence yields race-free quiescence —
// scheduler-side then consumer-side.
//
// # Persistence and write-through
//
// When a [JobStore] is supplied to [NewScheduler], every state transition is
// written through to SQLite: Submit persists the initial row, [Scheduler.Heartbeat]
// debounces progress flushes to at most one per heartbeatFlushInterval (500ms)
// per job, and terminal transitions commit immediately under a bounded
// terminalWriteTimeout. With a nil store the scheduler is pure in-memory
// (identical behaviour to [NewInMemoryScheduler]).
//
// On construction with a store, [NewScheduler] runs a supervisor sweep:
// [JobStore.SweepStaleJobs] marks any row left in running or awaiting_input by a
// prior process as failed with error=[ErrProcessDied]. A fresh process owns no
// goroutine for those rows, so they are by definition orphans; sweeping them
// guarantees clients see a clean terminal state after a restart.
//
// # Worked example
//
// A handler that echoes a value, submitted and awaited to its terminal event:
//
//	sched := jobs.NewInMemoryScheduler()
//	h := func(ctx context.Context, args map[string]any) (host.Result, error) {
//	    return host.Result{Data: map[string]any{"output": "hello"}}, nil
//	}
//	id, _ := sched.Submit(ctx, jobs.JobSpec{Kind: "demo", Handler: h})
//	ch, unsub := sched.Subscribe(id)
//	defer unsub()
//	ev := <-ch
//	// ev.Status == jobs.JobDone
//	// ev.Result.Data["output"] == "hello"
//
// A failing or cancelled run differs only in the terminal status carried on the
// event: a handler returning a non-empty [host.Result].Error yields
// [JobFailed] with that string; a cancelled context yields [JobCancelled]. A
// runnable form of this trace lives in [ExampleScheduler_Submit] and
// [ExampleScheduler_Submit_failure].
//
// # Lifecycle
//
// A job advances through these states:
//
//	Submit ──▶ running ──▶ done | failed | cancelled        (terminal)
//	              │  ▲
//	    Awaiting  │  │ Resumed
//	              ▼  │
//	         awaiting_input
//
//   - running → terminal: the handler returned (or its context was cancelled).
//   - running → awaiting_input: a handler called [host.RequestClarification],
//     which flips the DB row, then signals [Scheduler.Awaiting] to fan out a
//     JobAwaitingInput event. The handler goroutine stays alive, parked in its
//     poll loop, and counts as idle for [Scheduler.WaitIdle].
//   - awaiting_input → running: the user answered; the poll loop reads the
//     answer and calls [Scheduler.Resumed] before returning to the handler.
//
// The clarification flow as a whole: a running handler asks the user a question
// via [host.RequestClarification], which calls
// [JobStore.RequestClarificationAny] to store a typed [ClarificationSchema] and
// flip the row to awaiting_input. The orchestrator's session listener posts an
// action_required inbox notification; the user answers via the
// answer_clarification intent; the handler's poll loop returns the raw JSON
// answer and the job resumes. A second clarification while one is pending is an
// error — see [JobStore.RequestClarification].
//
// # Non-goals
//
//   - No job priorities, scheduling fairness, or SLA enforcement. Jobs start a
//     goroutine immediately on Submit; "background" means "off the turn loop,"
//     not "queued behind a scheduler policy." Concurrency is bounded by the
//     handlers themselves, not by this package.
//   - No retries or backoff. RetryCount exists on [Job] as a recorded field
//     only; the scheduler never re-runs a failed handler. Retry semantics, if
//     any, belong to the handler or the on_complete effect chain so they stay
//     visible in the app's state machine rather than hidden here.
//   - No history or audit trail beyond current-state DB snapshots. The job row
//     is a materialised view that terminal writes overwrite; the durable,
//     ordered record of what happened is the orchestrator's event log, so
//     duplicating it here would risk divergence.
//   - No cross-process coordination. A [Scheduler] owns only the jobs its own
//     goroutines started; rows from other processes are treated as orphans and
//     swept on startup rather than adopted, because no live goroutine can
//     deliver their results.
//   - No durable subscriptions. Both per-job and per-session channels drop
//     events when a consumer falls behind rather than buffering without bound;
//     the authoritative terminal state always survives on the persisted row, so
//     a missed in-flight event is recoverable but never blocks the scheduler.
//
// # Reference
//
// The subsystem narrative — the YAML author surface (background:, bind:,
// on_complete:), the component diagram, the goroutine lifecycle, replay
// determinism, and the host/jobs cycle resolution — lives under
// docs/stories/background-jobs (README.md, runtime.md, authoring.md,
// testing.md). The journal dual-write that [JobStore] emits on job mutations is
// described in docs/tracing.
package jobs
