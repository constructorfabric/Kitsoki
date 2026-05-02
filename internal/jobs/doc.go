// Package jobs implements the background job scheduler for hally applications.
//
// # Architecture
//
// The central abstraction is the Scheduler interface. Applications submit a
// JobSpec (handler name, payload, session ID, origin state) and receive a
// JobID back synchronously. The handler runs in a goroutine; callers subscribe
// to results via a channel.
//
// # Supervisor scan
//
// TODO(supervisor-scan): On startup, stale rows with status="running" should
// be marked failed with error=ErrProcessDied so clients see a clean terminal
// state after a process restart.  This is not yet implemented.
//
//	sched := jobs.NewScheduler(jobStore) // or NewInMemoryScheduler() for tests
//	id, err := sched.Submit(ctx, jobs.JobSpec{...})
//	ch, unsub := sched.Subscribe(id)
//	defer unsub()
//	ev := <-ch // terminal JobEvent (done/failed/cancelled)
//
// # Persistence (JobStore)
//
// JobStore wraps a *sql.DB (shared with the session store) and provides
// write-through semantics: Submit persists the initial job row, Heartbeat
// debounces flushes to ≤ 2/s per job, and terminal transitions commit
// immediately. Open NewScheduler(js) to get both in one call; use
// NewInMemoryScheduler() for tests that do not need SQLite.
//
// # Session-level listeners (SubscribeSession)
//
// The orchestrator registers one per-session listener goroutine via
// SubscribeSession(sessionID). It receives all terminal JobEvents for every
// job belonging to that session on a single buffered channel (capacity 16).
// Events are dropped (not queued to infinity) when the consumer falls behind.
// The unsubscribe function returned by SubscribeSession must always be called.
//
// # Clarification flow
//
// A running handler can pause mid-flight to ask the user a question:
//
//	rawJSON, err := host.RequestClarification(ctx, jobs.ClarificationSchema{
//	    Prompt: "Which branch?",
//	    Fields: map[string]string{"branch": "string"},
//	})
//
// Internally this calls JobStore.RequestClarificationAny to write the schema
// and flip the job row to JobAwaitingInput, then signals Scheduler.Awaiting
// to fan out a JobEvent{Status: JobAwaitingInput} to per-job and per-session
// subscribers. The orchestrator's listener posts an action_required inbox
// notification; the user answers via the answer_clarification intent; the
// handler's poll loop returns the raw JSON answer and the job resumes.
package jobs
