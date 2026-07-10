// Package orchestrator — background-effect dispatch helpers.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// dispatchBackground submits hc to the scheduler, binds the JobID into world
// per hc.Bind ("job_id" → last_job_id by default if no Bind entry maps to
// "job_id"), posts an info notification, and returns the events and updated
// world for the current turn.
//
// The on_complete chain is serialised into Payload["__on_complete"] so a
// future restart can rehydrate it from the job row.
func (o *Orchestrator) dispatchBackground(
	ctx context.Context,
	sid app.SessionID,
	state app.StatePath,
	hc machine.HostInvocation,
	w world.World,
) ([]store.Event, world.World, error) {
	// Re-render args against the current world (same as the synchronous path).
	// fellBack is intentionally ignored here — the background job path doesn't
	// emit HostDispatched (the job scheduler logs its own dispatch events) and
	// the per-leaf fallback semantics already preserve usable args.
	invokeArgs, _ := rerenderHostArgs(hc, w)

	// Copy into a mutable map (rerenderHostArgs may return hc.Args directly).
	payload := make(map[string]any, len(invokeArgs))
	for k, v := range invokeArgs {
		payload[k] = v
	}

	// Stash on_complete into the payload so it can be rehydrated after a restart.
	if len(hc.OnComplete) > 0 {
		b, err := json.Marshal(hc.OnComplete)
		if err != nil {
			return nil, w, fmt.Errorf("dispatchBackground: marshal on_complete: %w", err)
		}
		payload["__on_complete"] = string(b)
	}

	// Capture the registry reference and namespace so the closure doesn't
	// capture the whole orchestrator.
	hosts := o.hosts
	namespace := hc.Namespace

	spec := jobs.JobSpec{
		SessionID:   sid,
		Kind:        hc.Namespace,
		OriginState: state,
		Payload:     payload,
		Handler: func(jobCtx context.Context, args map[string]any) (host.Result, error) {
			if hosts == nil {
				return host.Result{Error: "no host registry: cannot run " + namespace + " as background job"}, nil
			}
			// Strip internal keys before passing to the handler.
			// The scheduler injects __job_id into args for debugging; it also
			// injects host.JobContext into jobCtx directly (when js != nil) so
			// handlers can call host.RequestClarification without further wiring.
			cleanArgs := make(map[string]any, len(args))
			for k, v := range args {
				if k == "__on_complete" || k == "__job_id" {
					continue
				}
				cleanArgs[k] = v
			}
			// Install a fresh per-job usage box so a background agent call's
			// token usage reaches its AgentReturned.Meta, mirroring the
			// foreground host-dispatch path (host_dispatch.go). jobCtx already
			// carries the EventSink + AgentCallCtx inherited from the
			// dispatch-time context, so the trace write picks the box up.
			return hosts.Invoke(host.WithAgentUsageBox(jobCtx), namespace, cleanArgs)
		},
	}

	// Thread the parent session id onto the job's context. The scheduler derives
	// the handler ctx via context.WithoutCancel(ctx), which preserves values — so
	// stamping it here makes host.WithKitsokiSessionID visible inside the background
	// handler. Without this a background host.agent.task (background: true) runs with
	// an empty kitsokiSessionIDFromCtx (the studio process env has no
	// KITSOKI_SESSION_ID), which silently disabled the deterministic maker-seed
	// backstop and dropped trace-continuity/lineage for the spawned subprocess. The
	// foreground turn already carries this seam (session_runtime.go); background
	// dispatch must too — principle of least surprise.
	ctx = host.WithKitsokiSessionID(ctx, string(sid))

	jobID, err := o.scheduler.Submit(ctx, spec)
	if err != nil {
		return nil, w, fmt.Errorf("dispatchBackground: scheduler.Submit: %w", err)
	}

	o.logger.DebugContext(ctx, trace.EvJobSubmitted,
		slog.String("session_id", string(sid)),
		slog.String("namespace", hc.Namespace),
		slog.String("job_id", jobID),
		slog.String("origin_state", string(state)),
		slog.Int("on_complete_count", len(hc.OnComplete)),
	)

	// Bind the job ID into world.
	// Walk hc.Bind: if any entry maps dkey=="job_id", use that world key.
	// Otherwise default to "last_job_id". Sorted iteration (not raw map
	// order) so that when more than one entry maps to "job_id" the chosen
	// world key is deterministic across runs rather than map-randomized.
	bindKey := "last_job_id"
	for _, wkey := range slices.Sorted(maps.Keys(hc.Bind)) {
		if hc.Bind[wkey] == "job_id" {
			bindKey = wkey
			break
		}
	}
	w.Set(bindKey, jobID)

	// Always keep last_job_id up to date even if a custom key was used.
	if bindKey != "last_job_id" {
		w.Set("last_job_id", jobID)
	}

	// dispatchBackground always binds the job ID under bindKey AND under
	// "last_job_id" (an unconditional convenience binding).  We emit a
	// separate EffectApplied for each key so that on replay both are
	// restored.  When bindKey == "last_job_id" a single event covers both.
	var events []store.Event
	events = append(events, newOrchestratorEvent(store.EffectApplied, operationWorldUpdatePayload(w, "set", map[string]any{bindKey: jobID}), 0))
	if bindKey != "last_job_id" {
		events = append(events, newOrchestratorEvent(store.EffectApplied, operationWorldUpdatePayload(w, "set", map[string]any{"last_job_id": jobID}), 0))
	}
	events = append(events, newOrchestratorEvent(store.JobSubmitted, map[string]any{
		"namespace": hc.Namespace,
		"job_id":    jobID,
		"state":     string(state),
	}, 0))

	// Post an info notification (non-fatal: log and continue on error).
	if o.jobStore != nil {
		notifyErr := jobs.Notify(ctx, o.jobStore, &jobs.Notification{
			SessionID:     sid,
			Severity:      jobs.SeverityInfo,
			Title:         "Job submitted: " + hc.Namespace,
			TeleportState: string(state),
			TeleportJobID: jobID,
			OriginKind:    "job",
			OriginRef:     "job:" + jobID,
		})
		if notifyErr != nil {
			o.logger.WarnContext(ctx, trace.EvJobError,
				slog.String("session_id", string(sid)),
				slog.String("job_id", jobID),
				slog.String("phase", "post_submit_notification"),
				slog.String("err", notifyErr.Error()),
			)
		} else {
			o.logger.DebugContext(ctx, trace.EvInboxNotificationPosted,
				slog.String("session_id", string(sid)),
				slog.String("job_id", jobID),
				slog.String("severity", string(jobs.SeverityInfo)),
				slog.String("origin", "job_submitted"),
				slog.String("title", "Job submitted: "+hc.Namespace),
			)
		}
	}

	return events, w, nil
}
