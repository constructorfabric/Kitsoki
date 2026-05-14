// Package orchestrator — Timeout: runtime (proposal gap §9.5).
//
// State declarations of the form
//
//	timeout:
//	  after: "10d"
//	  target: "{{ phase.next.continue }}"
//
// cause the orchestrator to auto-transition out of the declaring state
// after the declared duration has elapsed on the orchestrator's clock.
// Cancelled when any normal exit fires before the timer.  Survives
// orchestrator restarts via a small SQLite table.
//
// # Design
//
// One dispatcher per orchestrator instance, holding at most one pending
// entry per session (the OT canonical use case — one landmark waiting
// for a reply at a time).  Each entry owns a clock.Timer; firing schedules
// a synthetic turn via a callback into the orchestrator that emits
// StateExited/StateEntered/TimeoutFired and runs the destination state's
// on_enter chain.  Exiting the timeout state cancels the timer.
//
// # Persistence
//
// Pending entries are written to a `timeouts` table (one row per
// (session_id, state_path)) when armed and deleted when fired or cancelled.
// On orchestrator start (RearmPersisted), every row is reloaded and a fresh
// timer is scheduled at (fires_at - now).  Entries whose fires_at is already
// in the past fire immediately on rearm so a crash during a long timeout
// still lands the session in the target state.
package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
)

// timeoutSchemaDDL creates the single table used by the timeout dispatcher.
// Idempotent via IF NOT EXISTS.  Unique on (session_id, state_path) so each
// session has at most one timeout per state.
const timeoutSchemaDDL = `
CREATE TABLE IF NOT EXISTS timeouts (
  session_id  TEXT NOT NULL,
  state_path  TEXT NOT NULL,
  target      TEXT NOT NULL,
  fires_at_ms INTEGER NOT NULL,
  PRIMARY KEY (session_id, state_path)
) STRICT;`

// timeoutEntry tracks one pending timeout for one session.
type timeoutEntry struct {
	statePath app.StatePath
	target    app.StatePath
	firesAt   time.Time
	timer     clock.Timer
	// done is closed once this entry has fired or been cancelled, so a
	// concurrent Cancel after a firing goroutine has already started can
	// detect the race and skip the duplicate dispatch.
	done chan struct{}
}

// timeoutDispatcher owns all pending timeouts for one orchestrator instance.
//
// Concurrency: pending is guarded by mu.  The per-entry firing goroutine
// runs the synthetic transition outside the lock so a long-running
// orchestrator turn does not block Cancel.
type timeoutDispatcher struct {
	clk    clock.Clock
	db     *sql.DB
	logger *slog.Logger
	// orch is the owning orchestrator; supplied via setOrchestrator after
	// New so the wiring in orchestrator.New stays a one-liner.
	orch *Orchestrator

	mu sync.Mutex
	// pending: session → state-path → entry.  Per-state to keep the
	// future-friendly case (multiple Timeout-bearing states in distinct
	// regions) cheap to support.
	pending map[app.SessionID]map[app.StatePath]*timeoutEntry
}

// newTimeoutDispatcher returns a dispatcher backed by clk and (optionally) db.
// When db is non-nil the timeouts schema is applied on construction and every
// arm/cancel is mirrored to the table.  When db is nil the dispatcher runs
// in-memory only (the common case in unit tests).
func newTimeoutDispatcher(clk clock.Clock, db *sql.DB, logger *slog.Logger) (*timeoutDispatcher, error) {
	if clk == nil {
		clk = clock.Real()
	}
	if logger == nil {
		logger = slog.Default()
	}
	d := &timeoutDispatcher{
		clk:     clk,
		db:      db,
		logger:  logger,
		pending: make(map[app.SessionID]map[app.StatePath]*timeoutEntry),
	}
	if db != nil {
		if _, err := db.Exec(timeoutSchemaDDL); err != nil {
			return nil, fmt.Errorf("orchestrator.timeoutDispatcher: schema: %w", err)
		}
	}
	return d, nil
}

func (d *timeoutDispatcher) setOrchestrator(o *Orchestrator) { d.orch = o }

// arm schedules a timeout fire after `after` from now on the dispatcher's
// clock for (sid, statePath → target).  If an entry already exists for
// (sid, statePath) it is cancelled and replaced.  No-op if d is nil
// (orchestrators built without a dispatcher get no Timeout support, which
// matches the silently-ignored Effect.Background behaviour when WithScheduler
// is unset).
func (d *timeoutDispatcher) arm(sid app.SessionID, statePath, target app.StatePath, after time.Duration) {
	if d == nil {
		return
	}
	if after <= 0 {
		// Zero or negative duration: fire immediately on the next clock tick.
		// We still register so Cancel can short-circuit.
		after = 0
	}
	firesAt := d.clk.Now().Add(after)

	d.mu.Lock()
	// Cancel any pre-existing entry for this state-path before replacing.
	if existing := d.lookupLocked(sid, statePath); existing != nil {
		d.removeLocked(sid, statePath)
		if existing.timer != nil {
			existing.timer.Stop()
		}
		close(existing.done)
	}

	entry := &timeoutEntry{
		statePath: statePath,
		target:    target,
		firesAt:   firesAt,
		done:      make(chan struct{}),
	}
	entry.timer = d.clk.NewTimer(after)
	d.insertLocked(sid, statePath, entry)
	d.mu.Unlock()

	d.persist(sid, statePath, target, firesAt)

	d.logger.DebugContext(context.Background(), trace.EvTimeoutArmed,
		slog.String("session_id", string(sid)),
		slog.String("state", string(statePath)),
		slog.String("target", string(target)),
		slog.Duration("after", after),
		slog.Time("fires_at", firesAt),
	)

	go d.runEntry(sid, entry)
}

// cancel removes any pending timeout for (sid, statePath).  Idempotent.
//
// Cancellation must be safe-against the natural race where the firing
// goroutine is already executing inside fireSynthetic: we close done and
// stop the timer; the firing goroutine checks done and skips the synthetic
// turn if it lost the race.
func (d *timeoutDispatcher) cancel(sid app.SessionID, statePath app.StatePath) {
	if d == nil {
		return
	}
	d.mu.Lock()
	entry := d.lookupLocked(sid, statePath)
	if entry == nil {
		d.mu.Unlock()
		return
	}
	d.removeLocked(sid, statePath)
	d.mu.Unlock()

	if entry.timer != nil {
		entry.timer.Stop()
	}
	// Signal the firing goroutine (if any) that this entry has been cancelled.
	select {
	case <-entry.done:
		// Already closed by a firing goroutine; nothing to do.
	default:
		close(entry.done)
	}

	d.unpersist(sid, statePath)
	d.logger.DebugContext(context.Background(), trace.EvTimeoutCancelled,
		slog.String("session_id", string(sid)),
		slog.String("state", string(statePath)),
	)
}

// cancelAll cancels every pending timeout for the session.  Called when
// the session reaches a terminal state and the listener is being torn down.
func (d *timeoutDispatcher) cancelAll(sid app.SessionID) {
	if d == nil {
		return
	}
	d.mu.Lock()
	entries := d.pending[sid]
	delete(d.pending, sid)
	d.mu.Unlock()
	for _, e := range entries {
		if e.timer != nil {
			e.timer.Stop()
		}
		select {
		case <-e.done:
		default:
			close(e.done)
		}
		d.unpersist(sid, e.statePath)
	}
}

// runEntry blocks on entry.timer.C() and dispatches the synthetic turn when
// the deadline elapses.  Runs as its own goroutine — never holds d.mu while
// calling into the orchestrator.
//
// Invariants:
//   - entry.done is closed only AFTER the synthetic turn's events have been
//     committed (or the firing has been short-circuited by a cancel).
//   - entry remains in d.pending until fireTimeout has fully returned, so a
//     concurrent WaitTimeoutsDrained sees the entry, blocks on done, and
//     observes the committed events when it returns.
//
// This makes WaitTimeoutsDrained a correct quiescence barrier for the
// testrunner: after it returns, the synthetic transition is visible on the
// next LoadJourney call.
func (d *timeoutDispatcher) runEntry(sid app.SessionID, entry *timeoutEntry) {
	defer func() {
		// Always close done on exit, regardless of fire/cancel path.
		select {
		case <-entry.done:
		default:
			close(entry.done)
		}
	}()

	select {
	case <-entry.timer.C():
		// Check that we have not been replaced/cancelled in the meantime.
		d.mu.Lock()
		cur := d.lookupLocked(sid, entry.statePath)
		if cur != entry {
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()

		// Check the cancel signal explicitly.
		select {
		case <-entry.done:
			return
		default:
		}

		if d.orch == nil {
			// Defensive: remove + unpersist and return.
			d.mu.Lock()
			d.removeLocked(sid, entry.statePath)
			d.mu.Unlock()
			d.unpersist(sid, entry.statePath)
			return
		}
		ctx := context.Background()
		if err := d.orch.fireTimeout(ctx, sid, entry.statePath, entry.target); err != nil {
			d.logger.ErrorContext(ctx, trace.EvTimeoutError,
				slog.String("session_id", string(sid)),
				slog.String("state", string(entry.statePath)),
				slog.String("target", string(entry.target)),
				slog.String("phase", "fire"),
				slog.String("err", err.Error()),
			)
		}
		// Remove from pending and unpersist AFTER firing has fully landed.
		// WaitTimeoutsDrained may observe the entry up to this point; once
		// removed and done is closed, the synthetic turn's events are
		// guaranteed to be in the event log.
		d.mu.Lock()
		// Only remove if still the same entry (defensive against a
		// concurrent arm() that replaced us — shouldn't happen because we
		// just fired, but keep the check for safety).
		if d.lookupLocked(sid, entry.statePath) == entry {
			d.removeLocked(sid, entry.statePath)
		}
		d.mu.Unlock()
		d.unpersist(sid, entry.statePath)
	case <-entry.done:
		// Cancelled before deadline.
		return
	}
}

func (d *timeoutDispatcher) lookupLocked(sid app.SessionID, sp app.StatePath) *timeoutEntry {
	m := d.pending[sid]
	if m == nil {
		return nil
	}
	return m[sp]
}

func (d *timeoutDispatcher) insertLocked(sid app.SessionID, sp app.StatePath, entry *timeoutEntry) {
	m := d.pending[sid]
	if m == nil {
		m = make(map[app.StatePath]*timeoutEntry)
		d.pending[sid] = m
	}
	m[sp] = entry
}

func (d *timeoutDispatcher) removeLocked(sid app.SessionID, sp app.StatePath) {
	m := d.pending[sid]
	if m == nil {
		return
	}
	delete(m, sp)
	if len(m) == 0 {
		delete(d.pending, sid)
	}
}

// waitDrained blocks until every entry for sid whose deadline is ≤ Now()
// has finished firing (entry.done closed).  Future-dated entries are
// ignored — they're still pending after the call returns.
func (d *timeoutDispatcher) waitDrained(ctx context.Context, sid app.SessionID) error {
	if d == nil {
		return nil
	}
	// Collect the firing entries that we care about.  We re-snapshot until
	// no entries are "due but in-flight".  In practice this resolves in
	// one or two iterations because each firing entry is short-lived: the
	// goroutine acquires the session lock, writes events, closes done.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	for {
		d.mu.Lock()
		now := d.clk.Now()
		var dueEntries []*timeoutEntry
		for _, e := range d.pending[sid] {
			if !e.firesAt.After(now) {
				dueEntries = append(dueEntries, e)
			}
		}
		d.mu.Unlock()

		if len(dueEntries) == 0 {
			return nil
		}

		// Wait for each due entry to finish (done closed) or ctx cancel.
		for _, e := range dueEntries {
			select {
			case <-e.done:
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Until(deadline)):
				return context.DeadlineExceeded
			}
		}
		// Re-check: firing may have armed a new timeout (if the target
		// state itself has Timeout: and clock has advanced past its
		// deadline).  Loop to drain those too.
	}
}

// snapshot returns a copy of every pending entry for sid (used in tests).
func (d *timeoutDispatcher) snapshot(sid app.SessionID) []timeoutSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.pending[sid]
	out := make([]timeoutSnapshot, 0, len(m))
	for sp, e := range m {
		out = append(out, timeoutSnapshot{
			StatePath: sp,
			Target:    e.target,
			FiresAt:   e.firesAt,
		})
	}
	return out
}

// timeoutSnapshot is the public read-only view of a pending entry.
type timeoutSnapshot struct {
	StatePath app.StatePath
	Target    app.StatePath
	FiresAt   time.Time
}

// persist writes (or replaces) the row for (sid, statePath).
func (d *timeoutDispatcher) persist(sid app.SessionID, sp, target app.StatePath, firesAt time.Time) {
	if d.db == nil {
		return
	}
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO timeouts (session_id, state_path, target, fires_at_ms) VALUES (?, ?, ?, ?)`,
		string(sid), string(sp), string(target), firesAt.UnixMilli(),
	)
	if err != nil {
		d.logger.WarnContext(context.Background(), trace.EvTimeoutError,
			slog.String("session_id", string(sid)),
			slog.String("state", string(sp)),
			slog.String("phase", "persist"),
			slog.String("err", err.Error()),
		)
		return
	}
	// Site 15: emit timeout.armed as a post-commit standalone journal write.
	if d.orch != nil {
		d.orch.appendJournal(journalEntry(sid, 0, 0, time.Now(),
			journal.KindTimeoutArmed, "",
			map[string]any{
				"state_path":  string(sp),
				"target":      string(target),
				"fires_at_ms": firesAt.UnixMilli(),
			}))
	}
}

func (d *timeoutDispatcher) unpersist(sid app.SessionID, sp app.StatePath) {
	if d.db == nil {
		return
	}
	_, err := d.db.Exec(
		`DELETE FROM timeouts WHERE session_id = ? AND state_path = ?`,
		string(sid), string(sp),
	)
	if err != nil {
		d.logger.WarnContext(context.Background(), trace.EvTimeoutError,
			slog.String("session_id", string(sid)),
			slog.String("state", string(sp)),
			slog.String("phase", "unpersist"),
			slog.String("err", err.Error()),
		)
		return
	}
	// Site 16: emit timeout.cancelled as a post-commit standalone journal write.
	if d.orch != nil {
		d.orch.appendJournal(journalEntry(sid, 0, 0, time.Now(),
			journal.KindTimeoutCancelled, "",
			map[string]any{
				"state_path": string(sp),
				"reason":     "cancelled",
			}))
	}
}

// rearmFromStore reloads every persisted entry from the DB and re-arms a
// timer for each.  Called by Orchestrator.NewSession (after listener start)
// and by RearmPersisted (for process restarts).
//
// Entries whose fires_at is in the past relative to the current clock are
// armed with after=0 so they fire as soon as the goroutine starts.  This
// preserves the "timeout still fires after a crash" contract.
func (d *timeoutDispatcher) rearmFromStore(sid app.SessionID) error {
	if d == nil || d.db == nil {
		return nil
	}
	rows, err := d.db.Query(
		`SELECT state_path, target, fires_at_ms FROM timeouts WHERE session_id = ?`,
		string(sid),
	)
	if err != nil {
		return fmt.Errorf("orchestrator: rearmFromStore: %w", err)
	}
	defer rows.Close()

	type row struct {
		state, target string
		firesAtMs     int64
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.state, &r.target, &r.firesAtMs); err != nil {
			return fmt.Errorf("orchestrator: rearmFromStore: scan: %w", err)
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("orchestrator: rearmFromStore: rows: %w", err)
	}

	now := d.clk.Now()
	for _, r := range rs {
		firesAt := time.UnixMilli(r.firesAtMs)
		after := firesAt.Sub(now)
		if after < 0 {
			after = 0
		}
		d.logger.DebugContext(context.Background(), trace.EvTimeoutRearmed,
			slog.String("session_id", string(sid)),
			slog.String("state", r.state),
			slog.String("target", r.target),
			slog.Time("fires_at", firesAt),
			slog.Duration("after", after),
		)
		// Use arm so the in-memory entry and (already-present) DB row stay
		// consistent; arm's INSERT OR REPLACE is a no-op for the unchanged
		// fires_at value.
		d.arm(sid, app.StatePath(r.state), app.StatePath(r.target), after)
	}
	return nil
}

// rearmAllFromStore is the bulk variant used at orchestrator startup, when
// the dispatcher has no idea which sessions are active.  It scans every
// timeouts row, groups by session, and calls rearmFromStore for each.
func (d *timeoutDispatcher) rearmAllFromStore() error {
	if d == nil || d.db == nil {
		return nil
	}
	rows, err := d.db.Query(`SELECT DISTINCT session_id FROM timeouts`)
	if err != nil {
		return fmt.Errorf("orchestrator: rearmAllFromStore: %w", err)
	}
	defer rows.Close()
	var sids []app.SessionID
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return fmt.Errorf("orchestrator: rearmAllFromStore: scan: %w", err)
		}
		sids = append(sids, app.SessionID(s))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sid := range sids {
		if err := d.rearmFromStore(sid); err != nil {
			return err
		}
	}
	return nil
}

// ─── orchestrator-side wiring ────────────────────────────────────────────────

// armTimeoutForState inspects the destination state and, if it declares a
// Timeout, arms an entry for it.  Always cancels any pre-existing timeout
// on the previous state for the session.
//
// Called by Turn/RunIntent/SubmitDirect/handleJobTerminal AFTER the events
// for the transition have been committed but before returning to the caller.
//
// `prevState` is the state being exited; pass "" for the session-start case.
// `newState` is the state freshly entered.
func (o *Orchestrator) armTimeoutForState(sid app.SessionID, prevState, newState app.StatePath) {
	if o.timeouts == nil {
		return
	}
	// Cancel the previous state's timeout if it had one armed.  The cancel
	// is keyed on (sid, prevState) so a normal exit by any intent is
	// detected without inspecting prevState's schema.
	if prevState != "" && prevState != newState {
		o.timeouts.cancel(sid, prevState)
	}
	if newState == "" {
		return
	}
	s := lookupStateByPath(o.def, newState)
	if s == nil || s.Timeout == nil {
		return
	}
	after, err := app.ParseDuration(s.Timeout.After)
	if err != nil || after <= 0 {
		// Loader validation should have caught this; log and skip.
		o.logger.WarnContext(context.Background(), trace.EvTimeoutError,
			slog.String("session_id", string(sid)),
			slog.String("state", string(newState)),
			slog.String("phase", "arm_invalid_after"),
			slog.String("after", s.Timeout.After),
		)
		return
	}
	o.timeouts.arm(sid, newState, app.StatePath(s.Timeout.Target), after)
}

// fireTimeout runs a synthetic timeout-fired turn for (sid, fromState → target).
// Emits a TurnStarted / TimeoutFired / TransitionApplied / StateExited /
// StateEntered / on_enter effects / TurnEnded sequence so replay reconstructs
// the transition exactly as a normal turn would have done.
func (o *Orchestrator) fireTimeout(ctx context.Context, sid app.SessionID, fromState, target app.StatePath) error {
	// Take the per-session lock so we serialise against any concurrent
	// foreground Turn or handleJobTerminal.
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	defer sessMu.Unlock()

	journey, err := o.loadJourney(sid)
	if err != nil {
		return fmt.Errorf("fireTimeout: load journey: %w", err)
	}
	// Defensive: if a regular turn already left the timeout state before
	// our goroutine acquired the lock, drop the synthetic turn.
	if journey.State != fromState {
		o.logger.Debug("orchestrator: fireTimeout: state no longer matches; dropping",
			slog.String("session", string(sid)),
			slog.String("from", string(fromState)),
			slog.String("current", string(journey.State)),
		)
		return nil
	}

	// Validate target exists.
	tgtState := lookupStateByPath(o.def, target)
	if tgtState == nil {
		return fmt.Errorf("fireTimeout: target state %q not found", target)
	}

	turnNum := journey.Turn + 1

	var events []store.Event
	events = append(events, newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":   int64(turnNum),
		"kind":   "timeout",
		"source": string(fromState),
	}, turnNum))
	events = append(events, newOrchestratorEvent(store.TimeoutFired, map[string]any{
		"from": string(fromState),
		"to":   string(target),
	}, turnNum))
	events = append(events, newOrchestratorEvent(store.TransitionApplied, map[string]any{
		"from":   string(fromState),
		"to":     string(target),
		"intent": "__timeout__",
	}, turnNum))
	events = append(events, newOrchestratorEvent(store.StateExited, map[string]any{
		"state": string(fromState),
	}, turnNum))
	events = append(events, newOrchestratorEvent(store.StateEntered, map[string]any{
		"state": string(target),
	}, turnNum))

	// Run target.on_enter via the machine and dispatch any host calls.
	// RunEffectsAndState (not RunEffects) so an emit_intent inside the
	// timeout target's on_enter steers the final landing state — without
	// this the session pins at `target` even when an emit_intent has
	// already routed it onward.  (P1-D from the dev-story-bugfix-unify
	// Opus review.)
	w := journey.World
	if len(tgtState.OnEnter) > 0 {
		emitState, newWorld, hostCalls, _, effectEvents, runErr := o.machine.RunEffectsAndState(ctx, target, w, tgtState.OnEnter)
		if runErr != nil {
			return fmt.Errorf("fireTimeout: on_enter: %w", runErr)
		}
		w = newWorld
		for i := range effectEvents {
			effectEvents[i].Turn = turnNum
		}
		events = append(events, effectEvents...)
		if emitState != "" && emitState != target {
			target = emitState
		}

		if len(hostCalls) > 0 {
			hostEvents, hostWorld, _, redirect, hostErr := o.dispatchHostCalls(ctx, sid, hostCalls, w, target)
			if hostErr != nil {
				o.logger.WarnContext(ctx, trace.EvTimeoutError,
					slog.String("session_id", string(sid)),
					slog.String("state", string(fromState)),
					slog.String("phase", "fire_dispatch_host_calls"),
					slog.String("err", hostErr.Error()),
				)
			} else {
				for i := range hostEvents {
					hostEvents[i].Turn = turnNum
				}
				events = append(events, hostEvents...)
				w = hostWorld
				if redirect != "" {
					target = redirect
				}
			}
		}
	}

	events = append(events, newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome": "timeout",
		"to":      string(target),
	}, turnNum))

	for i := range events {
		events[i].Turn = turnNum
	}
	// Site 14: dual-write journal entries for the timeout-fired synthetic turn.
	ftJEntries := journalEntriesForEvents(sid, turnNum, time.Now(), events,
		journey.World, w, "", target, "")
	if err := o.store.AppendEventsAndJournal(sid, events, ftJEntries); err != nil {
		return fmt.Errorf("fireTimeout: append events: %w", err)
	}

	o.logger.InfoContext(ctx, trace.EvTimeoutFired,
		slog.String("session_id", string(sid)),
		slog.String("from", string(fromState)),
		slog.String("to", string(target)),
		slog.Int64("turn", int64(turnNum)),
	)

	// If the destination state itself has a Timeout, arm it.  The fromState
	// timeout has already been removed by the firing goroutine, so pass ""
	// as prevState to avoid a redundant cancel.
	o.armTimeoutForState(sid, "", target)

	// Honour terminal landings.
	if tgtState.Terminal {
		o.stopSessionListener(sid)
	}

	return nil
}

// timeoutPending exposes the dispatcher's per-session snapshot for tests.
// Returns nil when no dispatcher is wired.
func (o *Orchestrator) timeoutPending(sid app.SessionID) []timeoutSnapshot {
	if o.timeouts == nil {
		return nil
	}
	return o.timeouts.snapshot(sid)
}

// TimeoutPendingStates returns the state paths that currently have an armed
// Timeout: entry for sid.  Order is unspecified.  Test-facing helper —
// production callers should never need to inspect pending entries directly.
func (o *Orchestrator) TimeoutPendingStates(sid app.SessionID) []app.StatePath {
	if o.timeouts == nil {
		return nil
	}
	snaps := o.timeouts.snapshot(sid)
	out := make([]app.StatePath, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.StatePath)
	}
	return out
}

// shutdownTimeouts stops every pending in-memory timer without deleting the
// persisted rows.  Intended for tests that need to retire one orchestrator
// before bringing up a replacement against the same store, mirroring the
// production "process exit" case where in-memory state is lost but rows
// remain for the next instance to rearm.
func (o *Orchestrator) shutdownTimeouts() {
	if o.timeouts == nil {
		return
	}
	o.timeouts.mu.Lock()
	pending := o.timeouts.pending
	o.timeouts.pending = make(map[app.SessionID]map[app.StatePath]*timeoutEntry)
	o.timeouts.mu.Unlock()
	for _, m := range pending {
		for _, e := range m {
			if e.timer != nil {
				e.timer.Stop()
			}
			select {
			case <-e.done:
			default:
				close(e.done)
			}
		}
	}
}

// ShutdownTimeoutsForTest is a test-only helper that retires the in-memory
// timeout dispatcher without touching the persisted rows.  Equivalent to
// `kill -9` on the orchestrator process from the dispatcher's perspective.
func (o *Orchestrator) ShutdownTimeoutsForTest() { o.shutdownTimeouts() }

// ArmTimeoutForInitialState arms a Timeout: entry for the seeded initial
// state of a session.  Flow fixtures that bootstrap a session by writing
// synthetic events bypass the normal transition path that calls
// armTimeoutForState, so this method exists for the testrunner to plug the
// gap.  Production code never needs to call it.
func (o *Orchestrator) ArmTimeoutForInitialState(sid app.SessionID, state app.StatePath) {
	o.armTimeoutForState(sid, "", state)
}

// WaitTimeoutsDrained blocks until every pending timeout entry for sid that
// has *already fired* on the clock has finished writing its synthetic turn.
// Entries whose deadline is still in the future are ignored (they remain
// pending after the call returns).
//
// Returns ctx.Err() if the context is cancelled before the drain completes.
//
// Used by the flow-test runner: after `advance_clock` pushes virtual time
// forward, the scheduler-drain steps don't observe the timeout dispatcher's
// goroutine — this method closes that gap.
func (o *Orchestrator) WaitTimeoutsDrained(ctx context.Context, sid app.SessionID) error {
	if o.timeouts == nil {
		return nil
	}
	return o.timeouts.waitDrained(ctx, sid)
}

// ─── re-arm at orchestrator construction ─────────────────────────────────────

// rearmPersistedTimeouts is called by Orchestrator.New when a dispatcher is
// configured.  It is best-effort: failures log and proceed so a corrupt
// timeouts row does not block the orchestrator from starting.
func (o *Orchestrator) rearmPersistedTimeouts() {
	if o.timeouts == nil {
		return
	}
	if err := o.timeouts.rearmAllFromStore(); err != nil {
		o.logger.WarnContext(context.Background(), trace.EvTimeoutError,
			slog.String("phase", "rearm_persisted"),
			slog.String("err", err.Error()),
		)
	}
}

