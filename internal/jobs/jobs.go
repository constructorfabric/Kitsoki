// Package jobs implements the background job scheduler (§4).
//
// # Overview
//
// The Scheduler interface accepts JobSpecs and runs them as goroutines, one
// per job. Each job runs a host.Handler (with context for cancellation).
//
// # Storage
//
// Jobs and notifications are persisted in two new SQLite tables introduced via
// a migration applied on Open(). The store is session-scoped (all rows carry
// session_id); no FK constraints.
//
// When a *JobStore is supplied to NewScheduler, the scheduler performs
// SQLite write-through: Submit persists the initial job row, Heartbeat
// debounces flushes to ≤ 2/s per job (§4.3), and terminal transitions
// (done / failed / cancelled) are committed immediately.
//
// # Determinism / replay
//
// Host invocation inputs and outputs are written to the event log (via the
// existing store) so replay can substitute recorded results. The job row is
// materialized current-state; the event log is authoritative.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"hally/internal/app"
	"hally/internal/host"
	"hally/internal/ulid"
)

// JobID is a ULID string uniquely identifying a job.
type JobID = string

// JobStatus is the lifecycle state of a job.
type JobStatus string

const (
	JobRunning           JobStatus = "running"
	JobAwaitingInput     JobStatus = "awaiting_input"
	JobDone              JobStatus = "done"
	JobFailed            JobStatus = "failed"
	JobCancelled         JobStatus = "cancelled"
	// ErrProcessDied is the error string written to stale running rows when the
	// supervisor scan runs on startup.
	// TODO(supervisor-scan): implement a startup scan that marks stale running
	// rows as failed with this error value.  Currently no code writes this value.
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

// Scheduler is the interface for submitting and managing background jobs (§4.1).
type Scheduler interface {
	// Submit queues a new job and starts executing it immediately.
	// Returns the JobID on success.
	Submit(ctx context.Context, spec JobSpec) (JobID, error)
	// Cancel requests cancellation of a running job.
	Cancel(ctx context.Context, id JobID) error
	// Subscribe returns a channel that receives events for the given job,
	// and an unsubscribe function. The channel is closed when the job terminates.
	Subscribe(id JobID) (<-chan JobEvent, func())
	// Heartbeat updates the job's progress and updated_at timestamp.
	// Returns ErrJobNotFound if the job doesn't exist.
	Heartbeat(id JobID, progress any) error
	// SubscribeSession returns a buffered channel that receives terminal
	// JobEvents for every job belonging to sessionID, and an unsubscribe
	// function.  The channel is never explicitly closed; callers must call
	// the unsubscribe function to release resources.  Events are dropped
	// (not buffered to infinity) when the channel is full.
	SubscribeSession(sessionID app.SessionID) (<-chan JobEvent, func())
	// Get returns a snapshot of the job identified by id.  Returns (Job{}, false)
	// when id is unknown.  The returned Job is a copy and is safe to read; callers
	// must not mutate it.
	Get(id JobID) (Job, bool)
}

// ErrJobNotFound is returned when a job ID is not known.
var ErrJobNotFound = fmt.Errorf("jobs: job not found")

// heartbeatFlushInterval is the minimum gap between persisted Heartbeat writes
// per job (§4.3 — debounce to ≤ 2/s).
//
// Ordering invariant: lastFlush is both checked and updated inside the s.mu
// critical section in Heartbeat, so two concurrent heartbeat calls can never
// both satisfy the flush condition within the same lock acquisition.
const heartbeatFlushInterval = 500 * time.Millisecond

// inMemoryScheduler is a goroutine-per-job in-memory implementation.
// When js is non-nil, every state transition is written through to SQLite.
type inMemoryScheduler struct {
	mu      sync.RWMutex
	jobs    map[JobID]*runningJob
	cancels map[JobID]context.CancelFunc
	// sessionSubs maps session IDs to a set of subscriber channels that receive
	// terminal JobEvents for any job in that session.
	sessionSubs map[app.SessionID][]chan JobEvent
	// js is the optional SQLite write-through layer; nil means pure in-memory.
	js *JobStore
}

// runningJob holds runtime state for one job.
type runningJob struct {
	job       Job
	subs      []chan JobEvent
	subMu     sync.Mutex
	done      chan struct{}
	lastFlush time.Time // guards Heartbeat debounce
}

// NewInMemoryScheduler creates a pure in-memory Scheduler with no persistence.
// Existing in-package tests use this constructor; it is guaranteed to keep
// working without a SQLite dependency.
func NewInMemoryScheduler() Scheduler {
	return newScheduler(nil)
}

// NewScheduler creates a Scheduler backed by js for SQLite write-through.
// Submit writes the initial job row; Heartbeat debounces flushes to ≤ 2/s;
// terminal transitions (done/failed/cancelled) are committed immediately.
// When js is nil the behaviour is identical to NewInMemoryScheduler.
func NewScheduler(js *JobStore) Scheduler {
	return newScheduler(js)
}

func newScheduler(js *JobStore) *inMemoryScheduler {
	return &inMemoryScheduler{
		jobs:        make(map[JobID]*runningJob),
		cancels:     make(map[JobID]context.CancelFunc),
		sessionSubs: make(map[app.SessionID][]chan JobEvent),
		js:          js,
	}
}

func (s *inMemoryScheduler) Submit(ctx context.Context, spec JobSpec) (JobID, error) {
	id := ulid.New()
	now := time.Now()

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

	jobCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.jobs[id] = rj
	s.cancels[id] = cancel
	s.mu.Unlock()

	// Persist initial job row if write-through is enabled.
	if s.js != nil {
		if err := s.js.UpsertJob(ctx, &rj.job); err != nil {
			slog.Warn("jobs: write-through UpsertJob on Submit failed", "id", id, "err", err)
		}
	}

	// Run the handler in a goroutine.
	go func() {
		defer close(rj.done)

		result, err := spec.Handler(jobCtx, spec.Payload)
		now := time.Now()

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

		// Persist terminal status if write-through is enabled.
		if s.js != nil {
			if dbErr := s.js.UpdateJobStatus(context.Background(), id, termStatus, termErr, termResult, termFinishedAt); dbErr != nil {
				slog.Warn("jobs: write-through UpdateJobStatus failed", "id", id, "err", dbErr)
			}
		}

		s.fanout(rj, ev)
		s.fanoutSession(spec.SessionID, ev)

		s.mu.Lock()
		delete(s.cancels, id)
		s.mu.Unlock()
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
	// about to close, causing a panic. By checking first under the write lock
	// we avoid ever putting ch into rj.subs for a terminal job.
	s.mu.RLock()
	status := rj.job.Status
	result := rj.job.Result
	errStr := rj.job.Error
	s.mu.RUnlock()

	if status == JobDone || status == JobFailed || status == JobCancelled {
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
	now := time.Now()
	rj.job.Progress = progress
	rj.job.UpdatedAt = now

	ev := JobEvent{JobID: id, Status: rj.job.Status, Progress: progress}
	s.fanoutLocked(rj, ev)

	// Debounced write-through: flush to SQLite at most every heartbeatFlushInterval.
	shouldFlush := s.js != nil && now.Sub(rj.lastFlush) >= heartbeatFlushInterval
	var jobSnapshot Job
	if shouldFlush {
		rj.lastFlush = now
		jobSnapshot = rj.job
	}
	s.mu.Unlock()

	if shouldFlush {
		if err := s.js.UpsertJob(context.Background(), &jobSnapshot); err != nil {
			slog.Warn("jobs: write-through UpsertJob on Heartbeat failed", "id", id, "err", err)
		}
	}
	return nil
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
// terminal JobEvents for every job belonging to sessionID.  The returned
// unsubscribe function removes the subscription and should always be called
// (defer is fine).  Events are dropped silently when the buffer is full so
// that a slow consumer cannot stall the scheduler goroutine.
func (s *inMemoryScheduler) SubscribeSession(sessionID app.SessionID) (<-chan JobEvent, func()) {
	ch := make(chan JobEvent, 16)

	s.mu.Lock()
	s.sessionSubs[sessionID] = append(s.sessionSubs[sessionID], ch)
	s.mu.Unlock()

	unsub := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.sessionSubs[sessionID]
		for i, sub := range subs {
			if sub == ch {
				s.sessionSubs[sessionID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}
	return ch, unsub
}

// fanoutSession fans a terminal event out to all session-level subscribers
// (must be called without holding s.mu).
func (s *inMemoryScheduler) fanoutSession(sessionID app.SessionID, ev JobEvent) {
	s.mu.RLock()
	subs := s.sessionSubs[sessionID]
	// Copy slice so we can release the lock before sending.
	snapshot := make([]chan JobEvent, len(subs))
	copy(snapshot, subs)
	s.mu.RUnlock()

	for _, ch := range snapshot {
		select {
		case ch <- ev:
		default:
			// Drop: subscriber buffer is full.
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
