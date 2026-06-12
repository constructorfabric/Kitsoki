package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/ulid"
)

// JobID is a ULID string uniquely identifying a job.
type JobID = string

// JobStatus is the lifecycle state of a job.
type JobStatus string

const (
	JobRunning       JobStatus = "running"
	JobAwaitingInput JobStatus = "awaiting_input"
	JobDone          JobStatus = "done"
	JobFailed        JobStatus = "failed"
	JobCancelled     JobStatus = "cancelled"
	// ErrProcessDied is the error string written to orphaned running /
	// awaiting_input rows by JobStore.SweepStaleJobs on scheduler startup.
	ErrProcessDied string = "process_died_mid_job"
)

// JobSpec describes a job to be submitted.
type JobSpec struct {
	// SessionID ties the job to a session.
	SessionID app.SessionID
	// Kind is the handler name (e.g. "host.run_tests").
	Kind string
	// OriginState is the room where the job was spawned.
	OriginState app.StatePath
	// OriginProposalID is the proposal that spawned this job (optional).
	OriginProposalID string
	// Payload is the with: args passed to the handler (JSON-serialisable).
	Payload map[string]any
	// Handler is the function to run.
	Handler host.Handler
}

// JobEvent is emitted on the subscription channel when a job status changes.
type JobEvent struct {
	JobID    JobID
	Status   JobStatus
	Progress any
	Result   *host.Result
	Error    string
}

// Job is the runtime representation of a submitted job.
type Job struct {
	ID               JobID
	SessionID        app.SessionID
	Kind             string
	Status           JobStatus
	OriginState      app.StatePath
	OriginProposalID string
	Payload          map[string]any
	Progress         any
	Result           *host.Result
	Error            string
	RetryCount       int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time

	// ClarificationSchema is set when status==awaiting_input.
	ClarificationSchema any
	// ClarificationAnswer is set once the user submits an answer.
	ClarificationAnswer any
}

// Scheduler is the interface for submitting and managing background jobs.
//
// Safe for concurrent use: every method holds the appropriate internal lock,
// so callers may submit, subscribe, heartbeat, and wait from many goroutines.
// There is no useful zero value — always construct via [NewScheduler] or
// [NewInMemoryScheduler]. Unknown job IDs are reported, never panicked on:
// [Scheduler.Cancel], [Scheduler.Heartbeat], [Scheduler.Awaiting], and
// [Scheduler.Resumed] return [ErrJobNotFound]; [Scheduler.Get] returns ok=false;
// [Scheduler.Subscribe] returns an already-closed channel.
type Scheduler interface {
	// Submit queues a new job and starts executing it immediately.
	// Returns the JobID on success.
	Submit(ctx context.Context, spec JobSpec) (JobID, error)
	// Cancel requests cancellation of a running job.
	Cancel(ctx context.Context, id JobID) error
	// Subscribe returns a channel that receives events for the given job, and an
	// unsubscribe function. Safe for concurrent calls. The channel is closed
	// when the job terminates.
	//
	// Terminal and unknown jobs are handled without ever leaking a live channel:
	// subscribing to an already-terminal job returns a buffered channel
	// pre-loaded with the single terminal event and already closed; subscribing
	// to an unknown id returns an already-closed empty channel. In both cases
	// the unsubscribe function is a no-op but is still safe to call.
	Subscribe(id JobID) (<-chan JobEvent, func())
	// Heartbeat updates the job's progress and updated_at timestamp and fans the
	// progress out to subscribers. Safe for concurrent calls. When write-through
	// is enabled it debounces the persisted flush (see heartbeatFlushInterval).
	// Returns ErrJobNotFound if the job is unknown.
	Heartbeat(id JobID, progress any) error
	// SubscribeSession returns a buffered channel that receives terminal
	// JobEvents for every job belonging to sessionID, an ack callback that the
	// consumer must invoke once per event AFTER it has finished processing the
	// event, and an unsubscribe function.
	//
	// The ack callback is the signal that closes the receive→process race
	// window: every event handed to the channel increments an internal pending
	// counter; ack decrements it.  WaitSessionDrained blocks while pending > 0,
	// so callers using `WaitIdle + WaitSessionDrained` (in that order) get
	// race-free quiescence: WaitIdle returns once the scheduler has fanned out
	// all events; WaitSessionDrained returns once the consumer has acked every
	// fanned-out event.  Forgetting to ack permanently parks WaitSessionDrained.
	//
	// The channel is never explicitly closed; callers must call unsubscribe to
	// release resources.  Events are dropped (not buffered to infinity) when
	// the channel is full; dropped events are not counted against pending.
	SubscribeSession(sessionID app.SessionID) (events <-chan JobEvent, ack func(), unsub func())
	// WaitSessionDrained blocks until every subscriber for sid has acked every
	// JobEvent the scheduler has fanned out for that session.  Returns ctx.Err()
	// if the context is cancelled first.
	//
	// Typical use:
	//
	//	sched.WaitIdle(ctx)               // scheduler-side: jobs all terminal
	//	sched.WaitSessionDrained(ctx, sid) // consumer-side: events all processed
	WaitSessionDrained(ctx context.Context, sid app.SessionID) error
	// Awaiting transitions an in-memory job to JobAwaitingInput and fans out
	// a JobEvent{Status: JobAwaitingInput} on all per-job and per-session
	// subscribers.  It is called by the handler (via host.RequestClarification)
	// immediately after the DB row has been flipped to awaiting_input.
	// The handler's goroutine remains alive — it will be blocking on a poll
	// loop in host.RequestClarification waiting for the answer.
	// Returns ErrJobNotFound when id is unknown.
	Awaiting(id JobID) error
	// Resumed re-increments runningCount after a job returns from
	// JobAwaitingInput.  It must be called by host.RequestClarification
	// immediately after reading the user's answer and before returning to the
	// handler, so that WaitIdle correctly blocks while the resumed handler is
	// doing its remaining work.  Fans out a JobEvent{Status: JobRunning} so
	// orchestrator listeners can update state if needed.
	// Returns ErrJobNotFound when id is unknown.
	Resumed(id JobID) error
	// IsIdle returns true if no jobs are currently running (runningCount == 0).
	// JobAwaitingInput counts as idle.  This is a non-blocking snapshot used by
	// drain loops to detect cascading dispatches without race-prone polling.
	IsIdle() bool
	// RunningCount returns the current number of jobs that are neither terminal
	// nor awaiting input.  This is a non-blocking snapshot intended for tests
	// that need to know how many handler goroutines should be parked on the
	// clock before advancing time (see testrunner.advanceAndWait).
	RunningCount() int
	// WaitIdle blocks until every job tracked by the scheduler has reached a
	// terminal or awaiting-input state (JobDone, JobFailed, JobCancelled, or
	// JobAwaitingInput).  JobAwaitingInput counts as idle because the scheduler
	// goroutine is blocked waiting on user input rather than on in-flight work.
	// Returns ctx.Err() if the context is cancelled before the scheduler drains.
	WaitIdle(ctx context.Context) error
	// Get returns a snapshot of the job identified by id.  Returns (Job{}, false)
	// when id is unknown.  Safe for concurrent calls. The returned Job is a copy
	// taken under the scheduler lock, so it is safe to read after Get returns;
	// callers must not mutate it (mutation does not affect the live job but is
	// pointless).
	Get(id JobID) (Job, bool)
}

// ErrJobNotFound is returned when a job ID is not known.
var ErrJobNotFound = fmt.Errorf("jobs: job not found")

// heartbeatFlushInterval is the minimum gap between persisted Heartbeat writes
// per job. At 500ms this debounces progress flushes to at most two per second,
// keeping a chatty handler from hammering SQLite while still surfacing progress
// promptly.
//
// Ordering invariant: lastFlush is both checked and updated inside the s.mu
// critical section in Heartbeat, so two concurrent heartbeat calls can never
// both satisfy the flush condition within the same lock acquisition.
const heartbeatFlushInterval = 500 * time.Millisecond

// terminalWriteTimeout caps the time spent writing a job's terminal-state row
// to SQLite. Long enough for normal contention; short enough that a stuck
// store does not block scheduler shutdown.
const terminalWriteTimeout = 5 * time.Second

// heartbeatWriteTimeout caps a debounced Heartbeat flush. Best-effort:
// failures log and the next heartbeat retries.
const heartbeatWriteTimeout = 2 * time.Second

// Option is a functional option for constructing an inMemoryScheduler.
type Option func(*inMemoryScheduler)

// WithClock injects a clock.Clock into the scheduler.  Defaults to
// clock.Real() when not supplied.  Use clock.NewFake in tests to drive time
// deterministically.
func WithClock(c clock.Clock) Option {
	return func(s *inMemoryScheduler) {
		if c != nil {
			s.clock = c
		}
	}
}

// sessionSub is one per-session subscription created by SubscribeSession.
//
// pending tracks events that fanoutSession has handed to ch but the consumer
// has not yet acknowledged via ack().  It closes the race window between
// fanoutSession's send and the consumer's receive+process: a caller of
// WaitSessionDrained sees pending > 0 from the moment fanoutSession decides to
// deliver an event until the consumer signals completion, regardless of where
// in the send→receive→process pipeline the event currently sits.
//
// pending is incremented under mu before the channel send; the consumer's ack
// callback decrements it.  cond is broadcast on every change so
// WaitSessionDrained re-evaluates.
type sessionSub struct {
	ch      chan JobEvent
	mu      sync.Mutex
	cond    *sync.Cond
	pending int
}

func newSessionSub() *sessionSub {
	s := &sessionSub{ch: make(chan JobEvent, 16)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// inMemoryScheduler is a goroutine-per-job in-memory implementation.
// When js is non-nil, every state transition is written through to SQLite.
type inMemoryScheduler struct {
	mu      sync.RWMutex
	jobs    map[JobID]*runningJob
	cancels map[JobID]context.CancelFunc
	// sessionSubs maps session IDs to per-session subscriber records that
	// receive terminal JobEvents for any job in that session.  Each record
	// carries its own pending counter so WaitSessionDrained can detect
	// quiescence without racing the consumer's receive→process pipeline.
	sessionSubs map[app.SessionID][]*sessionSub
	// js is the optional SQLite write-through layer; nil means pure in-memory.
	js *JobStore
	// clock is the injectable time source; defaults to clock.Real().
	clock clock.Clock
	// idle tracking: runningCount is the number of jobs that are neither
	// terminal nor awaiting-input. WaitIdle blocks until this reaches zero.
	idleMu       sync.Mutex
	runningCount int
	idleCond     *sync.Cond
}

// runningJob holds runtime state for one job.
type runningJob struct {
	job       Job
	subs      []chan JobEvent
	subMu     sync.Mutex
	done      chan struct{}
	lastFlush time.Time // guards Heartbeat debounce
	// awaitingCounted is true when Awaiting has already decremented
	// runningCount for this job.  The terminal-block in the goroutine checks
	// this flag and skips a second decrement if Awaiting fired first, so
	// runningCount never goes negative through the awaiting→terminal path.
	// Protected by s.idleMu.
	awaitingCounted bool
}

// NewInMemoryScheduler is a thin wrapper over NewScheduler(nil, opts...).
// Preserved for tests and legacy callers that don't need SQLite persistence.
// Prefer NewScheduler(nil, ...) when the distinction matters at the call site.
// Optional Option values (e.g. WithClock) may be supplied.
func NewInMemoryScheduler(opts ...Option) Scheduler {
	return NewScheduler(nil, opts...)
}

// NewScheduler is the canonical constructor.  When js is non-nil every state
// transition is written through to SQLite (Submit writes the initial row;
// Heartbeat debounces flushes to ≤ 2/s; terminal transitions are committed
// immediately).  When js is nil the scheduler runs entirely in-memory —
// identical behaviour to NewInMemoryScheduler(opts...).
//
// As a side effect of construction with js != nil, any DB rows left in
// status running or awaiting_input from a prior process are swept to failed
// (with error=ErrProcessDied), so clients see a clean terminal state after a
// crash. Failures of the sweep itself are logged but do not block startup.
//
// Optional Option values (e.g. WithClock) may be supplied.
func NewScheduler(js *JobStore, opts ...Option) Scheduler {
	s := newScheduler(js, opts...)
	if js != nil {
		sweepCtx, cancel := context.WithTimeout(context.Background(), terminalWriteTimeout)
		if n, err := js.SweepStaleJobs(sweepCtx); err != nil {
			slog.Warn("jobs: supervisor sweep failed", "err", err)
		} else if n > 0 {
			slog.Info("jobs: supervisor sweep marked orphaned rows as failed", "count", n)
		}
		cancel()
	}
	return s
}

func newScheduler(js *JobStore, opts ...Option) *inMemoryScheduler {
	s := &inMemoryScheduler{
		jobs:        make(map[JobID]*runningJob),
		cancels:     make(map[JobID]context.CancelFunc),
		sessionSubs: make(map[app.SessionID][]*sessionSub),
		js:          js,
		clock:       clock.Real(),
	}
	s.idleCond = sync.NewCond(&s.idleMu)
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *inMemoryScheduler) Submit(ctx context.Context, spec JobSpec) (JobID, error) {
	id := ulid.New()
	now := s.clock.Now()

	rj := &runningJob{
		job: Job{
			ID:               id,
			SessionID:        spec.SessionID,
			Kind:             spec.Kind,
			Status:           JobRunning,
			OriginState:      spec.OriginState,
			OriginProposalID: spec.OriginProposalID,
			Payload:          spec.Payload,
			CreatedAt:        now,
			UpdatedAt:        now,
			StartedAt:        &now,
		},
		done: make(chan struct{}),
	}

	// Detach from the caller's cancellation: a background job is meant to
	// outlive the turn that submitted it (the submitting room transitions to a
	// "…executing" state and the view refreshes when the job finishes). In web
	// mode the caller's ctx is the per-turn HTTP request context, which is
	// cancelled the instant the turn handler returns — without WithoutCancel
	// that would kill the in-flight handler (e.g. an oracle exec) with
	// "context canceled" ~immediately. We keep WithoutCancel so trace/observability
	// values still propagate, then wrap in our own WithCancel so the scheduler's
	// explicit Cancel(id) (stored below) remains the sole way to abort the job.
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	s.mu.Lock()
	s.jobs[id] = rj
	s.cancels[id] = cancel
	s.mu.Unlock()

	// Track this job as active for WaitIdle.
	s.idleMu.Lock()
	s.runningCount++
	s.idleMu.Unlock()

	// Persist initial job row if write-through is enabled.
	if s.js != nil {
		if err := s.js.UpsertJob(ctx, &rj.job); err != nil {
			slog.Warn("jobs: write-through UpsertJob on Submit failed", "id", id, "err", err)
		}
	}

	// Run the handler in a goroutine.
	go func() {
		defer close(rj.done)

		// Inject a host.JobContext when write-through is enabled, so handlers
		// can call host.RequestClarification without any extra wiring.
		// This is the canonical injection point: the scheduler knows the job ID
		// and the store at this moment, so it builds the context here.
		handlerCtx := jobCtx
		if s.js != nil {
			jc := host.NewJobContext(s.js, id,
				func(jid string) error { return s.Awaiting(jid) },
				func(jid string) error { return s.Resumed(jid) },
			)
			handlerCtx = host.WithJobContext(handlerCtx, jc)
		}
		// Inject the scheduler's clock so clarification poll loops and other
		// time-dependent handler code can use the same fake clock in tests.
		handlerCtx = host.WithClock(handlerCtx, s.clock)

		// Inject __job_id into args as a convenience for orchestrator-side
		// handler wrappers that need to build the JobContext from args rather
		// than from context (e.g. when a custom scheduler is used).
		argsWithID := make(map[string]any, len(spec.Payload)+1)
		for k, v := range spec.Payload {
			argsWithID[k] = v
		}
		argsWithID["__job_id"] = id

		result, err := spec.Handler(handlerCtx, argsWithID)
		now := s.clock.Now()

		s.mu.Lock()
		rj.job.FinishedAt = &now
		rj.job.UpdatedAt = now

		var ev JobEvent
		ev.JobID = id

		if err != nil {
			if jobCtx.Err() == context.Canceled {
				rj.job.Status = JobCancelled
				ev.Status = JobCancelled
			} else {
				rj.job.Status = JobFailed
				rj.job.Error = err.Error()
				ev.Status = JobFailed
				ev.Error = err.Error()
			}
		} else if result.Error != "" {
			rj.job.Status = JobFailed
			rj.job.Error = result.Error
			rj.job.Result = &result
			ev.Status = JobFailed
			ev.Error = result.Error
			ev.Result = &result
		} else {
			rj.job.Status = JobDone
			rj.job.Result = &result
			ev.Status = JobDone
			ev.Result = &result
		}
		termStatus := rj.job.Status
		termResult := rj.job.Result
		termErr := rj.job.Error
		termFinishedAt := rj.job.FinishedAt
		s.mu.Unlock()

		// Persist terminal status if write-through is enabled. We derive from
		// jobCtx via WithoutCancel so trace/observability values propagate but
		// the terminal write is not aborted if jobCtx itself was just cancelled
		// (which is the common case here — the handler returned because of
		// Cancel and we still want the row marked accordingly). A short
		// timeout bounds the worst-case shutdown latency.
		if s.js != nil {
			writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(jobCtx), terminalWriteTimeout)
			if dbErr := s.js.UpdateJobStatus(writeCtx, id, termStatus, termErr, termResult, termFinishedAt); dbErr != nil {
				slog.Warn("jobs: write-through UpdateJobStatus failed", "id", id, "err", dbErr)
			}
			cancelWrite()
		}

		s.fanout(rj, ev)
		s.fanoutSession(spec.SessionID, ev)

		s.mu.Lock()
		delete(s.cancels, id)
		s.mu.Unlock()

		// Signal WaitIdle: the job has reached a terminal state.
		// Skip the decrement when Awaiting already decremented runningCount for
		// this job (awaitingCounted==true); decrementing again would make the
		// counter go negative, causing WaitIdle to return prematurely for
		// subsequent submits.
		s.idleMu.Lock()
		if !rj.awaitingCounted {
			s.runningCount--
		}
		s.idleCond.Broadcast()
		s.idleMu.Unlock()
	}()

	return id, nil
}

func (s *inMemoryScheduler) Cancel(ctx context.Context, id JobID) error {
	s.mu.RLock()
	cancel, ok := s.cancels[id]
	s.mu.RUnlock()
	if !ok {
		return ErrJobNotFound
	}
	cancel()
	return nil
}

func (s *inMemoryScheduler) Subscribe(id JobID) (<-chan JobEvent, func()) {
	s.mu.RLock()
	rj, ok := s.jobs[id]
	s.mu.RUnlock()

	if !ok {
		// Return a closed channel.
		ch := make(chan JobEvent)
		close(ch)
		return ch, func() {}
	}

	// Check if the job is already terminal before registering the channel.
	// If we registered first and then found it terminal, a concurrent fanout
	// (e.g. Heartbeat calling fanoutLocked) could send on a channel we are
	// about to close, causing a panic.
	//
	// The status read and the rj.subs registration must be atomic: a previous
	// version released the RLock between the two, so a concurrent terminal
	// transition (Submit's goroutine flipping status and fanning out under
	// s.mu) could land in that window — we'd observe non-terminal status, then
	// register ch AFTER the terminal fanout had already run, leaving ch
	// registered on a terminal job with no event ever delivered (and no close).
	// We therefore hold s.mu across both the check and the append. Lock
	// ordering is s.mu -> rj.subMu, consistent with fanoutLocked, so this does
	// not deadlock.
	s.mu.RLock()
	status := rj.job.Status
	result := rj.job.Result
	errStr := rj.job.Error

	if status == JobDone || status == JobFailed || status == JobCancelled {
		s.mu.RUnlock()
		// Job is already terminal: send one event and close immediately.
		// Do NOT register ch in rj.subs — a concurrent fanout would panic on
		// the closed channel.
		ch := make(chan JobEvent, 1)
		ev := JobEvent{JobID: id, Status: status, Result: result, Error: errStr}
		ch <- ev
		close(ch)
		return ch, func() {}
	}

	ch := make(chan JobEvent, 8)

	rj.subMu.Lock()
	rj.subs = append(rj.subs, ch)
	rj.subMu.Unlock()
	s.mu.RUnlock()

	unsub := func() {
		rj.subMu.Lock()
		defer rj.subMu.Unlock()
		for i, sub := range rj.subs {
			if sub == ch {
				rj.subs = append(rj.subs[:i], rj.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}

	return ch, unsub
}

func (s *inMemoryScheduler) Heartbeat(id JobID, progress any) error {
	s.mu.Lock()
	rj, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	now := s.clock.Now()
	rj.job.Progress = progress
	rj.job.UpdatedAt = now

	ev := JobEvent{JobID: id, Status: rj.job.Status, Progress: progress}
	s.fanoutLocked(rj, ev)

	// Debounced write-through: flush to SQLite at most every heartbeatFlushInterval.
	shouldFlush := s.js != nil && s.clock.Since(rj.lastFlush) >= heartbeatFlushInterval
	var jobSnapshot Job
	if shouldFlush {
		rj.lastFlush = now
		jobSnapshot = rj.job
	}
	s.mu.Unlock()

	if shouldFlush {
		// Heartbeat has no caller context (it's invoked from inside the
		// handler), so we use a short-bounded background context. The flush is
		// debounced (≤ once per heartbeatFlushInterval) and best-effort —
		// failure logs and the next heartbeat re-attempts.
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), heartbeatWriteTimeout)
		if err := s.js.UpsertJob(writeCtx, &jobSnapshot); err != nil {
			slog.Warn("jobs: write-through UpsertJob on Heartbeat failed", "id", id, "err", err)
		}
		cancelWrite()
	}
	return nil
}

// Awaiting flips the in-memory job status to JobAwaitingInput and fans out a
// JobEvent{Status: JobAwaitingInput} on the per-job and per-session subscriber
// channels.  The handler goroutine is expected to remain alive and block on
// host.RequestClarification's poll loop until an answer is stored.
//
// This is a signal-only operation: the DB row must already have been flipped to
// awaiting_input (by jobs.JobStore.RequestClarification) before calling this.
func (s *inMemoryScheduler) Awaiting(id JobID) error {
	s.mu.Lock()
	rj, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	rj.job.Status = JobAwaitingInput
	rj.job.UpdatedAt = s.clock.Now()
	sessionID := rj.job.SessionID
	ev := JobEvent{JobID: id, Status: JobAwaitingInput}
	s.fanoutLocked(rj, ev)
	s.mu.Unlock()

	// Fan out to session subscribers (the orchestrator's listener goroutine).
	s.fanoutSession(sessionID, ev)

	// JobAwaitingInput counts as idle: the scheduler is no longer actively
	// running work for this job (it's waiting on the user).
	// Record awaitingCounted under idleMu so the terminal-block goroutine
	// can see it and skip its own decrement.
	s.idleMu.Lock()
	rj.awaitingCounted = true
	s.runningCount--
	s.idleCond.Broadcast()
	s.idleMu.Unlock()

	return nil
}

// Resumed re-increments runningCount after a job returns from awaiting_input,
// clears awaitingCounted so the terminal block can decrement correctly, and
// fans out a JobEvent{Status: JobRunning}.
//
// host.RequestClarification calls this after reading the answer and before
// returning to the handler so WaitIdle correctly blocks while the resumed
// handler continues its work.
func (s *inMemoryScheduler) Resumed(id JobID) error {
	s.mu.Lock()
	rj, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	rj.job.Status = JobRunning
	rj.job.UpdatedAt = s.clock.Now()
	sessionID := rj.job.SessionID
	ev := JobEvent{JobID: id, Status: JobRunning}
	s.fanoutLocked(rj, ev)
	s.mu.Unlock()

	s.fanoutSession(sessionID, ev)

	// Re-increment: the job is actively running again.
	s.idleMu.Lock()
	rj.awaitingCounted = false
	s.runningCount++
	s.idleMu.Unlock()
	// No broadcast here — runningCount just went up (WaitIdle is not waiting
	// for it to increase, so no listeners need waking).

	return nil
}

// WaitIdle blocks until all jobs the scheduler is tracking have reached a
// terminal state (JobDone, JobFailed, JobCancelled) or JobAwaitingInput.
// Returns ctx.Err() if the context is cancelled first.
//
// The implementation uses a single goroutine that holds idleMu while waiting.
// A cancel flag (protected by idleMu) is set when ctx is cancelled so the
// cond loop exits cleanly — preventing a goroutine leak on cancellation.
func (s *inMemoryScheduler) WaitIdle(ctx context.Context) error {
	cancelled := false
	done := make(chan struct{})
	go func() {
		s.idleMu.Lock()
		for s.runningCount > 0 && !cancelled {
			s.idleCond.Wait()
		}
		s.idleMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// Set the cancel flag and broadcast so the waiting goroutine exits.
		s.idleMu.Lock()
		cancelled = true
		s.idleMu.Unlock()
		s.idleCond.Broadcast()
		// Wait for the goroutine to exit before returning so we don't leak it.
		<-done
		return ctx.Err()
	}
}

// IsIdle returns true when no jobs are currently in the running state
// (JobAwaitingInput counts as idle).  Non-blocking snapshot intended for
// drain-loop quiescence checks.
func (s *inMemoryScheduler) IsIdle() bool {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	return s.runningCount == 0
}

// RunningCount returns the current number of jobs that are neither terminal
// nor awaiting input.  Non-blocking snapshot.
func (s *inMemoryScheduler) RunningCount() int {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	return s.runningCount
}

// Get returns a snapshot of the job (safe to read, not modify).
func (s *inMemoryScheduler) Get(id JobID) (Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rj, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	// Return a copy.
	return rj.job, true
}

// fanout broadcasts an event to all subscribers (must be called without holding s.mu).
func (s *inMemoryScheduler) fanout(rj *runningJob, ev JobEvent) {
	rj.subMu.Lock()
	defer rj.subMu.Unlock()
	for _, ch := range rj.subs {
		select {
		case ch <- ev:
		default:
			// Drop if buffer full; subscriber is too slow.
		}
	}
}

// fanoutLocked broadcasts an event (called while holding s.mu).
func (s *inMemoryScheduler) fanoutLocked(rj *runningJob, ev JobEvent) {
	rj.subMu.Lock()
	defer rj.subMu.Unlock()
	for _, ch := range rj.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SubscribeSession returns a buffered channel (capacity 16) that receives
// terminal JobEvents for every job belonging to sessionID.  The ack callback
// must be invoked once per event AFTER processing — it decrements the
// per-subscription pending counter that WaitSessionDrained waits on.  The
// unsubscribe function removes the subscription and must always be called
// (defer is fine).  Events are dropped silently when the buffer is full so
// that a slow consumer cannot stall the scheduler goroutine; dropped events
// are not counted against pending.
//
// See sessionSub for the rationale on why this returns ack rather than
// relying on len(channel) — the receive→process window cannot be observed
// safely without a sender-side counter.
func (s *inMemoryScheduler) SubscribeSession(sessionID app.SessionID) (<-chan JobEvent, func(), func()) {
	sub := newSessionSub()

	s.mu.Lock()
	s.sessionSubs[sessionID] = append(s.sessionSubs[sessionID], sub)
	s.mu.Unlock()

	ack := func() {
		sub.mu.Lock()
		if sub.pending > 0 {
			sub.pending--
		}
		sub.cond.Broadcast()
		sub.mu.Unlock()
	}

	unsub := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.sessionSubs[sessionID]
		for i, candidate := range subs {
			if candidate == sub {
				s.sessionSubs[sessionID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		// Wake any WaitSessionDrained caller so it can re-evaluate (this
		// subscriber is gone; pending no longer matters for it).
		sub.mu.Lock()
		sub.cond.Broadcast()
		sub.mu.Unlock()
	}
	return sub.ch, ack, unsub
}

// WaitSessionDrained blocks until every subscriber registered for sid has a
// pending counter of zero — i.e. the consumer has called ack for every event
// the scheduler has fanned out so far.  Returns ctx.Err() if the context is
// cancelled before the drain completes.
//
// The typical sequence (e.g. inside the testrunner's advanceAndWait or the
// orchestrator's WaitListenerIdle) is:
//
//	sched.WaitIdle(ctx)               // jobs are all terminal/awaiting
//	sched.WaitSessionDrained(ctx, sid) // events all processed by listener
func (s *inMemoryScheduler) WaitSessionDrained(ctx context.Context, sid app.SessionID) error {
	// Snapshot the current set of subscribers; new subscribers added after the
	// snapshot don't have any events fanned out for this drain wave.
	s.mu.RLock()
	subs := make([]*sessionSub, len(s.sessionSubs[sid]))
	copy(subs, s.sessionSubs[sid])
	s.mu.RUnlock()

	for _, sub := range subs {
		// cancelled is read/written under sub.mu and lets the waiting goroutine
		// break out of the cond loop even if pending never reaches zero, so it
		// cannot leak when ctx is cancelled mid-wait. Mirrors the WaitIdle
		// pattern above. A plain broadcast is racy: pending may still be >0 when
		// the goroutine re-checks, so it would re-park and leak.
		cancelled := false
		done := make(chan struct{})
		go func(sub *sessionSub) {
			sub.mu.Lock()
			for sub.pending > 0 && !cancelled {
				sub.cond.Wait()
			}
			sub.mu.Unlock()
			close(done)
		}(sub)

		select {
		case <-done:
		case <-ctx.Done():
			// Set the cancel flag and broadcast so the waiting goroutine exits,
			// then wait for it to return before we do (no leak).
			sub.mu.Lock()
			cancelled = true
			sub.cond.Broadcast()
			sub.mu.Unlock()
			<-done
			return ctx.Err()
		}
	}
	return nil
}

// fanoutSession fans a terminal event out to all session-level subscribers
// (must be called without holding s.mu).
//
// For each subscriber it increments pending BEFORE the channel send so
// WaitSessionDrained sees a non-zero counter from the moment delivery is
// attempted.  If the buffered send fails (consumer too slow, buffer full),
// pending is rolled back — dropped events don't park WaitSessionDrained.
func (s *inMemoryScheduler) fanoutSession(sessionID app.SessionID, ev JobEvent) {
	s.mu.RLock()
	subs := s.sessionSubs[sessionID]
	// Copy slice so we can release the lock before sending.
	snapshot := make([]*sessionSub, len(subs))
	copy(snapshot, subs)
	s.mu.RUnlock()

	for _, sub := range snapshot {
		sub.mu.Lock()
		sub.pending++
		sub.mu.Unlock()

		select {
		case sub.ch <- ev:
			// Delivered: ack will decrement pending after the consumer
			// finishes processing.
		default:
			// Drop: subscriber buffer is full.  Roll back the increment so a
			// dropped event does not park WaitSessionDrained.
			sub.mu.Lock()
			sub.pending--
			sub.cond.Broadcast()
			sub.mu.Unlock()
		}
	}
}

// PayloadJSON serializes the payload to JSON for storage.
func PayloadJSON(payload map[string]any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Notify is a convenience helper that fills in n.CreatedAt (if zero) and
// delegates to js.InsertNotification.  Call sites at job submit/done/failed
// can use this instead of manually setting the timestamp.
func Notify(ctx context.Context, js *JobStore, n *Notification) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	return js.InsertNotification(ctx, n)
}
