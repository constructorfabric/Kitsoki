// Package inbox implements the notification inbox and teleport system (§4).
//
// The inbox provides:
//   - $inbox.unread world value for TUI badge rendering
//   - Teleport: rehydrates a target state and proposal from a notification
//   - InboxState type for the machine to render an inbox listing
//
// Teleport pushes the inbox predecessor onto the room history stack (§5.1),
// so pressing `back` from the teleport destination returns the user to wherever
// they were before visiting the inbox.
//
// # Clarification round-trip
//
// When a background handler calls host.RequestClarification, the following
// sequence executes:
//
//  1. The handler calls host.RequestClarification(ctx, schema).
//  2. host.RequestClarification calls jc.Store.RequestClarificationAny (which
//     writes the schema to the DB and flips the job row to awaiting_input),
//     then calls jc.awaiting(jobID) to signal the scheduler.
//  3. The scheduler's Awaiting method fans out a JobEvent{Status: JobAwaitingInput}
//     on per-job and per-session channels.
//  4. The orchestrator's session listener receives the event and calls
//     handleJobAwaitingInput, which loads the clarification schema and posts
//     an action_required notification via jobs.JobStore.PostClarificationNotification.
//  5. The notification's TeleportTarget carries TeleportJobID and TeleportState
//     (the job's OriginState). The TUI surfaces it as a banner.
//  6. The user selects the notification; the TUI calls Orchestrator.Teleport to
//     the TeleportState (a "*_clarifying" state in the originating room).
//  7. The user submits the answer_clarification intent with slots
//     {job_id, answer}. The machine fires the intent, which invokes
//     host.jobs.answer_clarification via an effect.
//  8. host.jobs.answer_clarification calls ClarificationAnswerer.AnswerClarification,
//     which writes the answer to the DB and flips the job back to running.
//  9. host.RequestClarification's poll loop detects the non-NULL answer and
//     returns it to the handler, which resumes normally.
// 10. The handler returns a result; the scheduler posts a JobDone event and
//     the orchestrator fires the on_complete chain.
package inbox

import (
	"context"

	"kitsoki/internal/app"
	"kitsoki/internal/jobs"
	"kitsoki/internal/world"
)

const (
	// WorldKey is the reserved world variable name for the inbox summary.
	WorldKey = "$inbox"
)

// InboxSummary is stored under $inbox in world state.
type InboxSummary struct {
	// Unread is the total count of unread notifications.
	Unread int `json:"unread"`
	// NeedsAttention is the count of action_required notifications.
	NeedsAttention int `json:"needs_attention"`
}

// ToMap converts the summary to a map for world-state storage.
func (s InboxSummary) ToMap() map[string]any {
	return map[string]any{
		"unread":          s.Unread,
		"needs_attention": s.NeedsAttention,
	}
}

// TeleportTarget describes where a teleport transition should land.
type TeleportTarget struct {
	// State is the destination state path.
	State app.StatePath
	// Slots are the slots to restore in the destination state.
	Slots map[string]any
	// ProposalID, if set, identifies the proposal to rehydrate as $proposal.
	ProposalID string
	// JobID, if set, identifies the job to rehydrate as $job.
	JobID string
}

// FromNotification extracts a TeleportTarget from a Notification.
func FromNotification(n jobs.Notification) TeleportTarget {
	return TeleportTarget{
		State:      app.StatePath(n.TeleportState),
		Slots:      n.TeleportSlots,
		ProposalID: n.TeleportProposalID,
		JobID:      n.TeleportJobID,
	}
}

// RefreshSummary queries the job store for unread counts and updates $inbox in world.
// Returns a new world with $inbox updated.
func RefreshSummary(ctx context.Context, js *jobs.JobStore, sessionID app.SessionID, w world.World) (world.World, error) {
	counts, err := js.UnreadCount(ctx, sessionID)
	if err != nil {
		return w, err
	}
	sum := InboxSummary{}
	for sev, cnt := range counts {
		sum.Unread += cnt
		if sev == jobs.SeverityActionRequired {
			sum.NeedsAttention += cnt
		}
	}
	return w.With(WorldKey, sum.ToMap()), nil
}

// PostJobNotification creates a notification for a completed job.
// Called by the scheduler after a job reaches a terminal state.
func PostJobNotification(ctx context.Context, js *jobs.JobStore, sessionID app.SessionID, j *jobs.Job, title, body string, sev jobs.NotificationSeverity) error {
	n := &jobs.Notification{
		SessionID:     sessionID,
		Severity:      sev,
		Title:         title,
		Body:          body,
		TeleportState: string(j.OriginState),
		TeleportJobID: j.ID,
		OriginKind:    "job",
		OriginRef:     "job:" + j.ID,
	}
	if j.OriginProposalID != "" {
		n.TeleportProposalID = j.OriginProposalID
	}
	return js.InsertNotification(ctx, n)
}
