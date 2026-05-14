// Package inbox — jobstore_adapter.go: production-side host.InboxAdder
// implementation that fans host.inbox.add invocations into the
// SQLite-backed jobs.JobStore.
//
// Background.  internal/host/inbox_add.go defines the InboxAdder seam
// consumed by host.inbox.add.  In tests the seam is filled by
// host.MemInboxAdder (an in-memory recorder).  In production the
// orchestrator installs the adapter declared here, which calls
// js.InsertNotification(ctx, &n) with the per-turn session ID — the
// same path inbox.PostJobNotification uses when a background job
// reaches a terminal state.
//
// Lives in package inbox (not host) because host already imports
// jobs's siblings without depending on jobs directly, and jobs
// imports host — so the adapter can't live in host.  inbox already
// imports both, so it's the natural home.
//
// # Session-ID plumbing
//
// host.inbox.add carries no session ID in its YAML args (the author
// should not have to know one).  We bind the session ID into the
// adapter at orchestrator-dispatch time: the orchestrator builds
// inbox.NewJobStoreAdder(store, sid) per turn and installs it via
// host.WithInboxAdder(ctx, adapter).  The handler retrieves it
// from ctx and calls AddInbox, which knows which session to write into.
//
// # Severity mapping
//
// host.inbox.add's `kind:` argument (one of checkpoint / ack / info /
// action_required) translates to jobs.NotificationSeverity:
//
//   checkpoint       → SeverityActionRequired (operator should review)
//   action_required  → SeverityActionRequired (operator must act)
//   ack              → SeveritySuccess        (positive ack)
//   info / unknown   → SeverityInfo           (default)
//
// "checkpoint" maps to SeverityActionRequired rather than a distinct
// "Attention" tier because the inbox UI surfaces both as "needs your
// eyes" — a single tier is the simplest accurate mapping today.  If a
// distinct tier lands later, swap severityForKind.
package inbox

import (
	"context"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

// JobStoreAdder bridges host.InboxAdder calls into jobs.JobStore.
// Construct one per (orchestrator, session) pair; install via
// host.WithInboxAdder before invoking any host.* call in that turn.
type JobStoreAdder struct {
	store     *jobs.JobStore
	sessionID app.SessionID
}

// NewJobStoreAdder builds an adapter that writes notifications
// against `sid` via `store`.  Both arguments should be non-nil; a
// nil store causes AddInbox to return a "not initialized" error so
// the host handler surfaces it via Result.Error (the YAML on_error:
// arc, if any, then fires).
func NewJobStoreAdder(store *jobs.JobStore, sid app.SessionID) *JobStoreAdder {
	return &JobStoreAdder{store: store, sessionID: sid}
}

// AddInbox satisfies the host.InboxAdder seam.  Maps the
// InboxNotification's kind to a jobs.NotificationSeverity, builds a
// jobs.Notification (origin_kind = "host_call", origin_ref = thread
// when supplied), and inserts via JobStore.InsertNotification.
// Returns the assigned notification ID.
func (a *JobStoreAdder) AddInbox(ctx context.Context, n host.InboxNotification) (string, error) {
	if a == nil || a.store == nil {
		return "", fmt.Errorf("inbox adapter not initialized")
	}
	sev := severityForKind(n.Kind)
	notif := &jobs.Notification{
		SessionID:     a.sessionID,
		Severity:      sev,
		Title:         n.Title,
		Body:          n.Body,
		TeleportState: n.State,
		OriginKind:    "host_call",
		OriginRef:     n.Thread,
	}
	if err := a.store.InsertNotification(ctx, notif); err != nil {
		return "", err
	}
	return notif.ID, nil
}

// severityForKind translates host.inbox.add's `kind:` argument into
// the jobs.NotificationSeverity used by the inbox storage layer.
// Unknown kinds fall back to Info — the always-on contract has no
// required severity.
func severityForKind(kind string) jobs.NotificationSeverity {
	switch kind {
	case "checkpoint":
		// Checkpoints are author-asserted "operator should review"
		// pauses.  Map to ActionRequired — the badge surface treats
		// it the same as a job that needs human input.
		return jobs.SeverityActionRequired
	case "action_required":
		return jobs.SeverityActionRequired
	case "ack":
		return jobs.SeveritySuccess
	case "info":
		return jobs.SeverityInfo
	default:
		return jobs.SeverityInfo
	}
}
