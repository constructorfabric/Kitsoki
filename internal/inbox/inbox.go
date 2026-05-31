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

// InboxSummary is the badge state stored under [WorldKey] in world state.
// It is a derived projection of the store's unread counts, not a source of
// truth: the only supported way to obtain a meaningful summary is
// [RefreshSummary]. The zero value (both counts 0) is a valid "nothing
// unread" summary and is what a freshly initialised world reports.
type InboxSummary struct {
	// Unread is the total count of unread notifications.
	Unread int `json:"unread"`
	// NeedsAttention is the count of action_required notifications.
	NeedsAttention int `json:"needs_attention"`
}

// ToMap renders the summary as the `map[string]any` shape world state and
// pongo2 templates consume, rather than storing the struct directly:
// world values must be JSON-ish maps so views can read `$inbox.unread`
// without a Go-typed accessor. Keys mirror the json tags.
func (s InboxSummary) ToMap() map[string]any {
	return map[string]any{
		"unread":          s.Unread,
		"needs_attention": s.NeedsAttention,
	}
}

// TeleportTarget describes where a teleport transition should land and
// what to rehydrate once there. It is the orchestrator's input, decoupled
// from the stored [jobs.Notification] so navigation can be exercised
// without the store. An empty State means "not teleportable" — the caller
// must check before navigating.
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

// FromNotification projects a stored notification's teleport fields into a
// [TeleportTarget]. It is total and pure: it touches no store and never
// errors, so a missing destination surfaces as an empty
// [TeleportTarget.State] rather than a failure. Safe for concurrent use.
func FromNotification(n jobs.Notification) TeleportTarget {
	return TeleportTarget{
		State:      app.StatePath(n.TeleportState),
		Slots:      n.TeleportSlots,
		ProposalID: n.TeleportProposalID,
		JobID:      n.TeleportJobID,
	}
}

// RefreshSummary folds the store's per-severity unread counts into an
// [InboxSummary] and returns a new world with [WorldKey] updated; the input
// world is never mutated. On a store error it returns w unchanged together
// with the error, so a failed refresh leaves the previous (stale but valid)
// `$inbox` in place rather than a partial summary. Safe for concurrent use
// to the extent the underlying [jobs.JobStore] is — it holds no state of
// its own and only reads.
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

// PostJobNotification writes a single completion notification for a job
// that reached a terminal state, stamping the job's origin state and id (and
// proposal id, if any) so the resulting notification teleports back to where
// the work began. The scheduler calls it once per terminal job. It does not
// itself refresh `$inbox`; the orchestrator calls [RefreshSummary]
// afterward. Safe for concurrent use to the extent the underlying
// [jobs.JobStore] is.
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
