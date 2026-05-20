// Package orchestrator — background-job on_complete bridge.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// handleJobTerminal is called by the per-session listener goroutine when a job
// reaches a terminal state (done/failed/cancelled). It applies the saved
// on_complete effect chain (if any), appends a synthetic background-completion
// turn to the event log, and posts a notification to the inbox.
//
// The on_complete effects were serialised into Payload["__on_complete"] as a
// JSON array of app.Effect values (see dispatchBackground). We round-trip them
// back via JSON unmarshal — app.Effect uses only primitive/composite types with
// json tags so this is lossless.
//
// $inbox refresh strategy: rather than holding the session world in memory
// across goroutines, we emit a synthetic EffectApplied event that sets
// $inbox.{unread,...} to the fresh counts. The next Turn call rebuilds world
// from the event log, so the badge reflects the new notification immediately.
func (o *Orchestrator) handleJobTerminal(ctx context.Context, sid app.SessionID, ev jobs.JobEvent) error {
	o.logger.DebugContext(ctx, trace.EvJobTerminal,
		slog.String("session_id", string(sid)),
		slog.String("job_id", ev.JobID),
		slog.String("status", string(ev.Status)),
	)

	// outcomeForObservers is built inside the locked critical section and
	// dispatched to SessionObservers AFTER we release the per-session
	// lock — see notifyBackgroundTurn at the bottom of this function.
	// We must not hold the lock across the observer call because the
	// canonical TUI observer eventually does a tea.Program.Send back
	// into the TUI goroutine, which may itself re-enter the orchestrator
	// to (e.g.) recompute the menu via LoadJourney; holding sessMu would
	// deadlock that path.
	var outcomeForObservers *TurnOutcome

	// Serialise read-modify-write against the foreground Turn path: both
	// compute turnNum = journey.Turn + 1 from the live event log, so without
	// this lock the listener goroutine can read journey.Turn before the
	// foreground Turn has committed turn N's events, then write its own
	// turn-N events and collide on the (session_id, turn, seq) PK.  The lock
	// must wrap loadJourney through AppendEvents — narrowing it to just
	// AppendEvents would not close the read-then-write window.  See
	// Orchestrator.sessionLocks for details.
	sessMu := o.sessionLock(sid)
	sessMu.Lock()
	lockHeld := true
	unlock := func() {
		if lockHeld {
			sessMu.Unlock()
			lockHeld = false
		}
	}
	defer unlock()

	// Load the journey so we know current state and world.
	journey, err := o.loadJourney(sid)
	if err != nil {
		return fmt.Errorf("handleJobTerminal: load journey: %w", err)
	}

	// Load the job row to recover on_complete and metadata.
	// Prefer scheduler.Get (avoids a DB round-trip in the common path).
	// Fall back to jobStore when the scheduler has no record.
	var j *jobs.Job
	if o.scheduler != nil {
		if jj, found := o.scheduler.Get(ev.JobID); found {
			j = &jj
			// Attach the result from the live event (the in-memory copy may
			// not have been updated yet by the time the listener goroutine runs).
			if ev.Result != nil && j.Result == nil {
				j.Result = ev.Result
			}
			if ev.Error != "" && j.Error == "" {
				j.Error = ev.Error
			}
		}
	}
	if j == nil && o.jobStore != nil {
		j, err = o.jobStore.GetJob(ctx, ev.JobID)
		if err != nil {
			return fmt.Errorf("handleJobTerminal: get job: %w", err)
		}
	}
	if j == nil {
		return fmt.Errorf("handleJobTerminal: job %q not found (no scheduler Get + no jobStore)", ev.JobID)
	}

	// Recover on_complete effects from the job payload. They were stored as a
	// JSON-encoded []app.Effect under the "__on_complete" key.
	var onComplete []app.Effect
	if raw, ok := j.Payload["__on_complete"]; ok && raw != nil {
		var jsonStr string
		switch v := raw.(type) {
		case string:
			jsonStr = v
		default:
			// Might have been re-decoded as map[string]any by json.Unmarshal
			// on DB load; re-encode and then unmarshal as []app.Effect.
			b, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("handleJobTerminal: re-encode on_complete: %w", err)
			}
			jsonStr = string(b)
		}
		if err := json.Unmarshal([]byte(jsonStr), &onComplete); err != nil {
			return fmt.Errorf("handleJobTerminal: unmarshal on_complete: %w", err)
		}
	}

	// Build the world for the on_complete pass.
	w := journey.World
	w.Vars["last_job_id"] = ev.JobID
	w.Vars["last_job_status"] = string(ev.Status)
	if ev.Result != nil && ev.Result.Data != nil {
		w.Vars["last_job_result"] = ev.Result.Data
	}

	// Synthetic turn number: one beyond the current event-log turn.
	turnNum := journey.Turn + 1

	// Start building the new synthetic turn's events.
	var turnEvents []store.Event
	turnEvents = append(turnEvents, newOrchestratorEvent(store.TurnStarted, map[string]any{
		"turn":   int64(turnNum),
		"kind":   "background_completion",
		"job_id": ev.JobID,
	}, turnNum))

	// currentState tracks the live state path as we walk on_complete and any
	// on_error redirects.  It starts at the job's origin state; an on_error
	// redirect during host-call dispatch updates it; a Target: effect (handled
	// below) also updates it.  This is what the synthetic TurnOutcome reports
	// as NewState to observers.
	currentState := j.OriginState
	// onErrorRedirected, when true, signals that a host-call inside the
	// on_complete chain hit its on_error path and the session has already
	// landed on the error state.  In that case the Target effect's transition
	// is suppressed — on_error wins over Target (per the design note: on_error
	// is itself a terminal state-change).
	onErrorRedirected := false

	// Apply on_complete effects (may be empty if the app didn't declare any).
	// RunEffectsAndState (not RunEffects) so an emit_intent inside the
	// on_complete chain steers the final landing state.  Without this an
	// emit fired during background-job completion would execute its host
	// calls and apply its effects, but the orchestrator would still pin
	// the synthetic turn's state to OriginState rather than the emit-
	// resolved leaf.  (P1-D from the dev-story-bugfix-unify Opus review.)
	if len(onComplete) > 0 {
		o.logger.DebugContext(ctx, trace.EvJobOnCompleteRun,
			slog.String("session_id", string(sid)),
			slog.String("job_id", ev.JobID),
			slog.Int("effect_count", len(onComplete)),
		)
		emitState, newWorld, hostCalls, sayText, effectEvents, runErr := o.machine.RunEffectsAndState(ctx, j.OriginState, w, onComplete)
		if runErr != nil {
			// An on_complete effect failed (e.g. set: with a bad expr).
			// Preserve the fail-fast invariant: skip the synthetic Target
			// transition entirely — no advance on partial application.
			return fmt.Errorf("handleJobTerminal: RunEffects: %w", runErr)
		}
		// Stamp turn number on all effect events.
		for i := range effectEvents {
			effectEvents[i].Turn = turnNum
		}
		turnEvents = append(turnEvents, effectEvents...)
		w = newWorld
		if emitState != "" && emitState != j.OriginState {
			// on_complete chain emitted onward; the new leaf takes
			// precedence over OriginState for the synthetic turn's
			// reported NewState.  A subsequent Target effect (handled
			// below) only fires when no emit_intent has already routed.
			currentState = emitState
		}

		// If the on_complete chain included a say: effect the text is already
		// captured as an EffectApplied{say: ...} event inside effectEvents.
		// Log it so operators can see it in structured output as well.
		if sayText != "" {
			o.logger.InfoContext(ctx, trace.EvJobOnCompleteRun,
				slog.String("session_id", string(sid)),
				slog.String("job_id", ev.JobID),
				slog.String("phase", "say"),
				slog.String("text", sayText),
			)
		}

		// Dispatch any foreground host calls collected by the on_complete chain.
		// background: true is forbidden inside on_complete: by the loader, so all
		// calls here are synchronous.
		//
		// on_error: redirects from on_complete host calls are accepted and the
		// session lands on the named error state — TransitionApplied events
		// emitted by dispatchHostCalls already carry the redirect, so the
		// replayer restores the correct state on restart.  We track the
		// redirect so the Target dispatch below can defer to it.
		if len(hostCalls) > 0 {
			hostEvts, hostWorld, _, hostRedirect, hostErr := o.dispatchHostCalls(ctx, sid, hostCalls, w, j.OriginState)
			if hostErr != nil {
				o.logger.WarnContext(ctx, trace.EvJobError,
					slog.String("session_id", string(sid)),
					slog.String("job_id", ev.JobID),
					slog.String("phase", "on_complete_dispatch"),
					slog.String("err", hostErr.Error()),
				)
			} else {
				for i := range hostEvts {
					hostEvts[i].Turn = turnNum
				}
				turnEvents = append(turnEvents, hostEvts...)
				w = hostWorld
				if hostRedirect != "" {
					currentState = hostRedirect
					onErrorRedirected = true
				}
			}
		}

		// Target dispatch: scan the on_complete chain for the FIRST effect
		// whose target: field is non-empty (and whose when: guard, if any,
		// passes against the post-effects world).  Emit a synthetic
		// transition so the session advances out of the *_executing state
		// without requiring an operator keystroke.
		//
		// on_error short-circuits Target — if a host call already redirected
		// to an error state, we leave the session there.
		if !onErrorRedirected {
			targetEvents, targetState, targetErr := o.resolveAndApplyOnCompleteTarget(ctx, sid, onComplete, w, currentState, turnNum)
			if targetErr != nil {
				// Validation should have caught a missing target at load time;
				// surface a warning rather than crashing the synthetic turn so
				// the session can still recover via a subsequent keystroke.
				o.logger.WarnContext(ctx, trace.EvJobError,
					slog.String("session_id", string(sid)),
					slog.String("job_id", ev.JobID),
					slog.String("phase", "on_complete_target"),
					slog.String("err", targetErr.Error()),
				)
			} else if len(targetEvents) > 0 {
				// targetEvents already carry the synthetic transition,
				// state-exit/enter, and on_enter effect events.  Apply.
				for i := range targetEvents {
					targetEvents[i].Turn = turnNum
				}
				turnEvents = append(turnEvents, targetEvents...)
				currentState = targetState
			}
		}
	}

	// Emit a JobCompleted event so the event log captures the terminal transition.
	completedPayload := map[string]any{
		"job_id": ev.JobID,
		"status": string(ev.Status),
	}
	if ev.Error != "" {
		completedPayload["error"] = ev.Error
	}
	turnEvents = append(turnEvents, newOrchestratorEvent(store.JobCompleted, completedPayload, turnNum))

	// Refresh $inbox: query unread counts and emit an EffectApplied so the next
	// Turn replay reconstructs the badge without a live DB call.  This is simpler
	// than holding the world across goroutines and avoids any concurrency issue.
	if o.jobStore != nil {
		refreshedWorld, refreshErr := inbox.RefreshSummary(ctx, o.jobStore, sid, w)
		if refreshErr != nil {
			o.logger.WarnContext(ctx, trace.EvJobError,
				slog.String("session_id", string(sid)),
				slog.String("job_id", ev.JobID),
				slog.String("phase", "refresh_inbox_summary"),
				slog.String("err", refreshErr.Error()),
			)
		} else {
			inboxVal := refreshedWorld.Vars[inbox.WorldKey]
			turnEvents = append(turnEvents, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{inbox.WorldKey: inboxVal},
			}, turnNum))
			w = refreshedWorld
		}
	}

	// Close the synthetic turn.
	turnEvents = append(turnEvents, newOrchestratorEvent(store.TurnEnded, map[string]any{
		"outcome":    "background_completion",
		"job_status": string(ev.Status),
	}, turnNum))

	// Stamp turn number on all events (belt-and-suspenders: already done above
	// per-block, but this ensures nothing slips through).
	for i := range turnEvents {
		turnEvents[i].Turn = turnNum
	}

	// Site 9: dual-write journal entries for the background-completion turn.
	// Pre-world is `journey.World` (captured before on_complete ran); post-world is `w`.
	jcNow := time.Now()
	jcJEntries := journalEntriesForEvents(sid, turnNum, jcNow, turnEvents,
		journey.World, w, "", currentState, "")
	if appendErr := o.store.AppendEventsAndJournal(sid, turnEvents, jcJEntries); appendErr != nil {
		return fmt.Errorf("handleJobTerminal: append events: %w", appendErr)
	}

	// AppendEvents committed.  Release the per-session lock now so the
	// observer callback below (which the TUI uses to re-render its
	// transcript) can safely re-enter the orchestrator without
	// deadlocking against the foreground Turn path.
	unlock()

	// Post a completion notification.
	if o.jobStore != nil {
		severity, title, body := completionNotification(ev, j)
		notifyErr := inbox.PostJobNotification(ctx, o.jobStore, sid, j, title, body, severity)
		if notifyErr != nil {
			o.logger.WarnContext(ctx, trace.EvJobError,
				slog.String("session_id", string(sid)),
				slog.String("job_id", ev.JobID),
				slog.String("phase", "post_completion_notification"),
				slog.String("err", notifyErr.Error()),
			)
		} else {
			o.logger.DebugContext(ctx, trace.EvInboxNotificationPosted,
				slog.String("session_id", string(sid)),
				slog.String("job_id", ev.JobID),
				slog.String("severity", string(severity)),
				slog.String("title", title),
				slog.String("origin", "job_terminal"),
			)
		}
	}

	// Build a synthetic TurnOutcome for observers.  We reload the
	// journey from the event log rather than reusing the in-memory `w`
	// because dispatchHostCalls inside on_complete may have fired an
	// on_error arc that redirected the state; the replay-driven journey
	// is the canonical source of post-commit state.
	//
	// All work below happens AFTER unlock — observers may re-enter the
	// orchestrator.
	if postJourney, jerr := o.loadJourney(sid); jerr == nil {
		newAllowed := o.machine.AllowedIntents(postJourney.State, postJourney.World)
		allowedNames := make([]string, len(newAllowed))
		for i, ai := range newAllowed {
			allowedNames[i] = ai.Name
		}
		view, rerr := o.machine.RenderState(postJourney.State, postJourney.World)
		if rerr != nil {
			// Non-fatal: still surface the outcome with whatever view we have.
			o.logger.Warn("handleJobTerminal: RenderState",
				slog.String("err", rerr.Error()),
			)
		}
		mode := ModeTransitioned
		if st := lookupStateByPath(o.def, postJourney.State); st != nil && st.Terminal {
			mode = ModeCompleted
		}
		outcomeForObservers = &TurnOutcome{
			Mode:           mode,
			View:           view,
			NewState:       postJourney.State,
			AllowedIntents: allowedNames,
			TurnNumber:     turnNum,
		}
	} else {
		// Reload failed — log and skip observer notification rather than
		// guessing at a possibly-inconsistent outcome.  The event log is
		// still complete; the TUI will catch up on its next poll/keystroke.
		o.logger.Warn("handleJobTerminal: post-commit loadJourney",
			slog.String("err", jerr.Error()),
		)
	}

	o.logger.InfoContext(ctx, trace.EvJobTerminal,
		slog.String("session_id", string(sid)),
		slog.String("job_id", ev.JobID),
		slog.String("status", string(ev.Status)),
		slog.Int("on_complete_count", len(onComplete)),
		slog.String("phase", "committed"),
	)

	// Fan out to observers (TUI re-render, audit log, etc.).  Done last
	// so any panic inside an observer cannot prevent the notification or
	// the structured-log line above from happening.
	if outcomeForObservers != nil {
		o.notifyBackgroundTurn(sid, outcomeForObservers)
	}
	return nil
}

// resolveAndApplyOnCompleteTarget scans the on_complete effect list for the
// first effect whose Target: field is non-empty (and whose When: guard, if any,
// passes against the post-effects world). When found it:
//
//   - resolves Target relative to the origin state,
//   - asserts the resolved path exists in the state graph,
//   - runs the target's on_enter effects (if any),
//   - emits TransitionApplied + StateExited + StateEntered + on_enter events.
//
// Returns the synthetic events (caller stamps turn number) and the new state.
// If no Target effect fires, returns (nil, currentState, nil). Subsequent
// Target effects past the first are warn-logged and ignored — multi-Target
// per on_complete chain is undefined; first-wins.
//
// originState is the state the job was launched from (j.OriginState) and is
// used both as the "from" of the transition and as the base for resolving
// relative target refs. currentState is the live state path used as the
// "from" of the transition events; usually equal to originState but may
// differ if a prior on_error redirect moved the session before we reached
// this dispatch.
func (o *Orchestrator) resolveAndApplyOnCompleteTarget(
	ctx context.Context,
	sid app.SessionID,
	onComplete []app.Effect,
	w world.World,
	originState app.StatePath,
	turnNum app.TurnNumber,
) ([]store.Event, app.StatePath, error) {
	var (
		firstIdx    = -1
		firstTarget string
		extras      []int // indices of subsequent Target effects (warn-log only)
	)
	env := expr.Env{
		Slots: map[string]any{},
		World: w.Vars,
		Event: map[string]any{},
	}
	for i, eff := range onComplete {
		if eff.Target == "" {
			continue
		}
		// Honour the per-effect When: guard.  A false guard skips the
		// whole effect, Target included.  Errors during guard evaluation
		// are surfaced — authors get a loud failure rather than a silently-
		// skipped transition.
		if strings.TrimSpace(eff.When) != "" {
			prog, cerr := expr.CompileBool(eff.When)
			if cerr != nil {
				return nil, originState, fmt.Errorf("on_complete[%d].when %q: compile: %w", i, eff.When, cerr)
			}
			ok, eerr := expr.EvalBool(prog, env)
			if eerr != nil {
				return nil, originState, fmt.Errorf("on_complete[%d].when %q: eval: %w", i, eff.When, eerr)
			}
			if !ok {
				continue
			}
		}
		if firstIdx == -1 {
			firstIdx = i
			firstTarget = eff.Target
			continue
		}
		extras = append(extras, i)
	}
	if firstIdx == -1 {
		return nil, originState, nil // no Target effect fired
	}
	for _, idx := range extras {
		o.logger.WarnContext(ctx, trace.EvJobOnCompleteRun,
			slog.String("session_id", string(sid)),
			slog.String("phase", "extra_target_ignored"),
			slog.Int("on_complete_index", idx),
			slog.String("target", onComplete[idx].Target),
			slog.String("first_target", firstTarget),
		)
	}

	// Resolve relative target refs ("../foo") against the origin state path.
	resolved := resolveOnCompleteTarget(string(originState), firstTarget)
	// Template-bearing targets (containing "{{") would have been left for
	// runtime evaluation by the loader.  Render against the post-effects
	// env so authors can pick a target dynamically.
	if strings.Contains(resolved, "{{") {
		rendered, rerr := expr.RenderValue(resolved, env)
		if rerr != nil {
			return nil, originState, fmt.Errorf("on_complete[%d].target render: %w", firstIdx, rerr)
		}
		if s, ok := rendered.(string); ok {
			resolved = resolveOnCompleteTarget(string(originState), strings.TrimSpace(s))
		} else {
			return nil, originState, fmt.Errorf("on_complete[%d].target template did not render to string (got %T)", firstIdx, rendered)
		}
	}
	tgtState := lookupStateByPath(o.def, app.StatePath(resolved))
	if tgtState == nil {
		return nil, originState, fmt.Errorf("on_complete[%d].target %q (resolved %q) does not exist", firstIdx, firstTarget, resolved)
	}

	o.logger.DebugContext(ctx, trace.EvJobOnCompleteRun,
		slog.String("session_id", string(sid)),
		slog.String("phase", "target_dispatch"),
		slog.String("from", string(originState)),
		slog.String("to", resolved),
	)

	// Build the transition event sequence.  Mirrors machine.Turn's contract
	// (§8 in machine.go): TransitionApplied → StateExited → StateEntered →
	// (on_enter EffectApplied*).
	target := app.StatePath(resolved)
	var events []store.Event
	events = append(events, newOrchestratorEvent(store.TransitionApplied, map[string]any{
		"from":   string(originState),
		"to":     resolved,
		"intent": "__on_complete_target__",
	}, turnNum))
	// Single-level exit/enter is sufficient for the leaf-targeted case the
	// validator allows.  Multi-level / compound entry would require the
	// stateExit/EnterPathsAware machinery from the machine package; we don't
	// pull it in here because on_complete: target: is intended for the
	// "advance out of the *_executing state to its sibling" pattern, where
	// both states share a parent and the exit/enter is one hop.
	events = append(events, newOrchestratorEvent(store.StateExited, map[string]any{
		"state": string(originState),
	}, turnNum))
	events = append(events, newOrchestratorEvent(store.StateEntered, map[string]any{
		"state": resolved,
	}, turnNum))

	// Run target.on_enter via the machine so any set/say/invoke effects fire
	// the same way a foreground transition would.  Host calls collected here
	// are dispatched synchronously — on_enter on the target of an
	// on_complete: dispatch must not itself spawn another background job
	// (background: true inside on_enter at the new state is fine when the
	// state was entered by a regular turn, but it would cascade arbitrarily
	// here; left as a known gap — see test coverage).
	//
	// RunEffectsAndState (not RunEffects) so an emit_intent inside the
	// on_complete target's on_enter steers the final landing leaf —
	// without this the session pins to `target` even when an emit has
	// already routed it onward.  (P1-D from the dev-story-bugfix-unify
	// Opus review.)
	if len(tgtState.OnEnter) > 0 {
		emitState, _, hostCalls, _, enterEvents, runErr := o.machine.RunEffectsAndState(ctx, target, w, tgtState.OnEnter)
		if runErr != nil {
			return nil, originState, fmt.Errorf("on_complete target on_enter: %w", runErr)
		}
		events = append(events, enterEvents...)
		if emitState != "" && emitState != target {
			target = emitState
		}
		if len(hostCalls) > 0 {
			hostEvts, _, _, _, hostErr := o.dispatchHostCalls(ctx, sid, hostCalls, w, target)
			if hostErr != nil {
				o.logger.WarnContext(ctx, trace.EvJobError,
					slog.String("session_id", string(sid)),
					slog.String("phase", "on_complete_target_on_enter_dispatch"),
					slog.String("err", hostErr.Error()),
				)
			} else {
				events = append(events, hostEvts...)
			}
		}
	}

	return events, target, nil
}

// resolveOnCompleteTarget mirrors app.resolveTarget so the orchestrator can
// resolve on_complete target: refs without exporting the loader internal.
// Accepts both slash- and dot-separated absolute refs and "../" relative
// references.
func resolveOnCompleteTarget(statePath, target string) string {
	if !strings.HasPrefix(target, "..") {
		return strings.ReplaceAll(target, "/", ".")
	}
	parts := strings.Split(statePath, ".")
	segs := strings.Split(target, "/")
	for _, seg := range segs {
		if seg == ".." {
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		} else if seg != "." && seg != "" {
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, ".")
}

// handleJobAwaitingInput is called by the per-session listener goroutine when
// a job transitions to JobAwaitingInput.  It loads the clarification schema and
// posts an action_required notification so the TUI can surface it to the user.
//
// The notification's TeleportState is the job's OriginState — selecting the
// notification teleports the user back to where the job was launched, which
// should have a state whose intents: includes answer_clarification.
func (o *Orchestrator) handleJobAwaitingInput(ctx context.Context, sid app.SessionID, ev jobs.JobEvent) error {
	o.logger.DebugContext(ctx, trace.EvJobAwaitingInput,
		slog.String("session_id", string(sid)),
		slog.String("job_id", ev.JobID),
	)
	if o.jobStore == nil {
		// No persistent store: cannot post a notification or fetch the schema.
		return nil
	}

	// Load the job row to recover origin state and kind.
	var j *jobs.Job
	var err error
	if o.scheduler != nil {
		if jj, found := o.scheduler.Get(ev.JobID); found {
			j = &jj
		}
	}
	if j == nil {
		j, err = o.jobStore.GetJob(ctx, ev.JobID)
		if err != nil {
			return fmt.Errorf("handleJobAwaitingInput: get job: %w", err)
		}
	}
	if j == nil {
		return fmt.Errorf("handleJobAwaitingInput: job %q not found", ev.JobID)
	}

	// Fetch the clarification schema stored by the handler.
	schema, err := o.jobStore.GetClarificationSchema(ctx, ev.JobID)
	if err != nil {
		return fmt.Errorf("handleJobAwaitingInput: get schema: %w", err)
	}
	if schema == nil {
		// Schema not yet persisted (race); log and skip.
		o.logger.WarnContext(ctx, trace.EvJobError,
			slog.String("session_id", string(sid)),
			slog.String("job_id", ev.JobID),
			slog.String("phase", "awaiting_input_no_schema"),
		)
		return nil
	}

	// Post the action_required notification.
	if err := o.jobStore.PostClarificationNotification(ctx, sid, j, *schema); err != nil {
		return fmt.Errorf("handleJobAwaitingInput: post notification: %w", err)
	}
	o.logger.DebugContext(ctx, trace.EvInboxNotificationPosted,
		slog.String("session_id", string(sid)),
		slog.String("job_id", ev.JobID),
		slog.String("severity", string(jobs.SeverityActionRequired)),
		slog.String("origin", "job_awaiting_input"),
	)

	o.logger.InfoContext(ctx, trace.EvJobAwaitingInput,
		slog.String("session_id", string(sid)),
		slog.String("job_id", ev.JobID),
		slog.String("kind", j.Kind),
		slog.String("prompt", schema.Prompt),
		slog.String("phase", "notified"),
	)
	return nil
}

// completionNotification returns the severity, title, and body for the
// terminal-job inbox notification.
func completionNotification(ev jobs.JobEvent, j *jobs.Job) (jobs.NotificationSeverity, string, string) {
	switch ev.Status {
	case jobs.JobDone:
		// Chat-aware: when the result carries chat metadata, produce a
		// chat-friendly notification.
		if ev.Result != nil {
			if chatID, ok := ev.Result.Data["chat_id"].(string); ok && chatID != "" {
				title := "Reply ready"
				body := ""
				// Strip source-color sentinels before truncate — the
				// notification text is plain-string-typed and a
				// rune-counting truncate could otherwise split a
				// 4-rune sentinel sequence.
				if answer, ok := ev.Result.Data["answer"].(string); ok && answer != "" {
					answer = sourcecolor.Strip(answer)
					title = "Reply ready — " + truncate(answer, 60)
					body = truncate(answer, 200)
				} else if stdout, ok := ev.Result.Data["stdout"].(string); ok && stdout != "" {
					body = truncate(sourcecolor.Strip(stdout), 200)
				}
				return jobs.SeveritySuccess, title, body
			}
		}
		return jobs.SeveritySuccess, "Job done: " + j.Kind, ""
	case jobs.JobFailed:
		// Chat-aware failure notification.
		if ev.Result != nil {
			if chatID, ok := ev.Result.Data["chat_id"].(string); ok && chatID != "" {
				return jobs.SeverityError, "Reply failed — " + j.Kind, j.Error
			}
		}
		return jobs.SeverityError, "Job failed: " + j.Kind, j.Error
	case jobs.JobCancelled:
		// Chat-aware cancellation notification.
		if ev.Result != nil {
			if chatID, ok := ev.Result.Data["chat_id"].(string); ok && chatID != "" {
				return jobs.SeverityWarn, "Reply cancelled — " + j.Kind, ""
			}
		}
		return jobs.SeverityWarn, "Job cancelled: " + j.Kind, ""
	default:
		return jobs.SeverityInfo, "Job " + string(ev.Status) + ": " + j.Kind, ""
	}
}

// truncate returns s trimmed of whitespace, with newlines collapsed to spaces,
// and truncated to at most n runes. An ellipsis is appended when truncated.
func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}
