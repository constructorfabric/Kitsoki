// Package orchestrator — background-job on_complete bridge.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"hally/internal/app"
	"hally/internal/inbox"
	"hally/internal/jobs"
	"hally/internal/store"
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

	// Apply on_complete effects (may be empty if the app didn't declare any).
	if len(onComplete) > 0 {
		newWorld, hostCalls, sayText, effectEvents, runErr := o.machine.RunEffects(ctx, j.OriginState, w, onComplete)
		if runErr != nil {
			return fmt.Errorf("handleJobTerminal: RunEffects: %w", runErr)
		}
		// Stamp turn number on all effect events.
		for i := range effectEvents {
			effectEvents[i].Turn = turnNum
		}
		turnEvents = append(turnEvents, effectEvents...)
		w = newWorld

		// If the on_complete chain included a say: effect the text is already
		// captured as an EffectApplied{say: ...} event inside effectEvents.
		// Log it so operators can see it in structured output as well.
		if sayText != "" {
			o.logger.Info("handleJobTerminal: on_complete say",
				slog.String("job_id", ev.JobID),
				slog.String("text", sayText),
			)
		}

		// Dispatch any foreground host calls collected by the on_complete chain.
		// background: true is forbidden inside on_complete: by the loader, so all
		// calls here are synchronous.
		if len(hostCalls) > 0 {
			hostEvts, hostWorld, _, hostErr := o.dispatchHostCalls(ctx, sid, hostCalls, w, j.OriginState)
			if hostErr != nil {
				o.logger.Warn("handleJobTerminal: dispatchHostCalls",
					slog.String("job_id", ev.JobID),
					slog.String("err", hostErr.Error()),
				)
			} else {
				for i := range hostEvts {
					hostEvts[i].Turn = turnNum
				}
				turnEvents = append(turnEvents, hostEvts...)
				w = hostWorld
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
			o.logger.Warn("handleJobTerminal: RefreshSummary",
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

	if appendErr := o.store.AppendEvents(sid, turnEvents); appendErr != nil {
		return fmt.Errorf("handleJobTerminal: append events: %w", appendErr)
	}

	// Post a completion notification.
	if o.jobStore != nil {
		severity, title, body := completionNotification(ev, j)
		notifyErr := inbox.PostJobNotification(ctx, o.jobStore, sid, j, title, body, severity)
		if notifyErr != nil {
			o.logger.Warn("handleJobTerminal: PostJobNotification",
				slog.String("job_id", ev.JobID),
				slog.String("err", notifyErr.Error()),
			)
		}
	}

	o.logger.Info("orchestrator: background job completed",
		slog.String("session", string(sid)),
		slog.String("job_id", ev.JobID),
		slog.String("status", string(ev.Status)),
		slog.Int("on_complete_count", len(onComplete)),
	)
	return nil
}

// handleJobAwaitingInput is called by the per-session listener goroutine when
// a job transitions to JobAwaitingInput.  It loads the clarification schema and
// posts an action_required notification so the TUI can surface it to the user.
//
// The notification's TeleportState is the job's OriginState — selecting the
// notification teleports the user back to where the job was launched, which
// should have a state whose intents: includes answer_clarification.
func (o *Orchestrator) handleJobAwaitingInput(ctx context.Context, sid app.SessionID, ev jobs.JobEvent) error {
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
		o.logger.Warn("orchestrator: handleJobAwaitingInput: no clarification schema found",
			slog.String("job_id", ev.JobID),
		)
		return nil
	}

	// Post the action_required notification.
	if err := o.jobStore.PostClarificationNotification(ctx, sid, j, *schema); err != nil {
		return fmt.Errorf("handleJobAwaitingInput: post notification: %w", err)
	}

	o.logger.Info("orchestrator: job awaiting clarification",
		slog.String("session", string(sid)),
		slog.String("job_id", ev.JobID),
		slog.String("kind", j.Kind),
		slog.String("prompt", schema.Prompt),
	)
	return nil
}

// completionNotification returns the severity, title, and body for the
// terminal-job inbox notification.
func completionNotification(ev jobs.JobEvent, j *jobs.Job) (jobs.NotificationSeverity, string, string) {
	switch ev.Status {
	case jobs.JobDone:
		return jobs.SeveritySuccess, "Job done: " + j.Kind, ""
	case jobs.JobFailed:
		return jobs.SeverityError, "Job failed: " + j.Kind, j.Error
	case jobs.JobCancelled:
		return jobs.SeverityWarn, "Job cancelled: " + j.Kind, ""
	default:
		return jobs.SeverityInfo, "Job " + string(ev.Status) + ": " + j.Kind, ""
	}
}
