// Package inbox holds the in-app notification summary and teleport
// metadata that connect background work back to the foreground session.
// It sits between [kitsoki/internal/jobs] (the SQLite-backed notification
// store) and the orchestrator/TUI: jobs persist notifications, inbox reads
// them into the `$inbox` world value the TUI badges from, and turns a
// selected notification into a [TeleportTarget] the orchestrator can
// rehydrate to.
//
// Two distinct surfaces live here:
//
//   - Summary + teleport (this file) — [RefreshSummary] derives the
//     `$inbox` badge counts from the store, [PostJobNotification] writes a
//     completion notification, and [FromNotification] decodes a stored
//     notification into the [TeleportTarget] the orchestrator lands on.
//   - The host.inbox.add adapter (jobstore_adapter.go) — the
//     production [JobStoreAdder] that fans `host.inbox.add` invocations
//     into the same store.
//
// # Algorithm
//
// The summary is a fold over the store's unread counts. [RefreshSummary]:
//
//  1. Asks the store for unread counts per severity for one session via
//     [jobs.JobStore.UnreadCount].
//  2. Sums every severity into [InboxSummary.Unread] (the total badge),
//     and additionally sums [jobs.SeverityActionRequired] into
//     [InboxSummary.NeedsAttention] (the "needs your eyes" sub-count).
//  3. Writes the summary under [WorldKey] via [world.World.With], returning
//     a new world. The input world is never mutated.
//
// Teleport is a straight projection. A [jobs.Notification] already carries
// the destination state, slots, and the proposal/job ids to rehydrate;
// [FromNotification] copies those four fields into a [TeleportTarget]
// without consulting the store. The orchestrator then rehydrates
// `$proposal` / `$job` from the ids and pushes the pre-teleport room onto
// the history stack so `back` returns the user to where they were.
//
// # Invariants
//
//   - Unread is the sum of NeedsAttention and every other severity, so
//     NeedsAttention <= Unread always holds for a summary produced by
//     RefreshSummary.
//   - RefreshSummary returns the input world unchanged on error, so a
//     failed refresh leaves a stale-but-valid `$inbox` rather than a
//     half-written one.
//   - FromNotification is total: every [jobs.Notification] yields a
//     [TeleportTarget], even an empty one. A target with an empty State is
//     the caller's signal that the notification was not teleportable.
//
// # Worked example
//
// A background "propose purchase" job finishes in the general_store room
// and posts a notification; the user later selects it to teleport back:
//
//	store has unread for session "s1":
//	  {SeverityInfo: 1, SeverityActionRequired: 1}
//
//	RefreshSummary(ctx, store, "s1", w):
//	  unread          = 1 + 1 = 2
//	  needs_attention =     1 = 1   (only the action_required row)
//	  $inbox = {"unread": 2, "needs_attention": 1}
//
//	the action_required notification:
//	  { TeleportState: "general_store.reviewing",
//	    TeleportSlots:  {"items": "6 oxen"},
//	    TeleportJobID:  "job-xyz", TeleportProposalID: "" }
//
//	FromNotification(n):
//	  { State: "general_store.reviewing",
//	    Slots: {"items": "6 oxen"}, JobID: "job-xyz", ProposalID: "" }
//
// Runnable forms of both traces live in [ExampleFromNotification] and
// [ExampleRefreshSummary].
//
// # Lifecycle
//
// All functions here are stateless; they hold no instance and so have no
// load/teardown of their own. [RefreshSummary] is called by the
// orchestrator on every turn that may change unread counts (after a job
// completes, after a notification is read) to keep `$inbox` current.
// [JobStoreAdder] is the one stateful piece: build one per
// (orchestrator, session) and install it via host.WithInboxAdder before
// dispatching host calls in that turn — see jobstore_adapter.go.
//
// # Non-goals
//
//   - No notification storage, retention, or read/unread bookkeeping —
//     that is [jobs.JobStore]'s job. inbox only reads counts and writes
//     completion rows; it never expires or prunes notifications.
//   - No history-stack manipulation. inbox produces a [TeleportTarget];
//     pushing the pre-teleport room and honouring `back` belongs to the
//     orchestrator and [kitsoki/internal/history], so teleport and
//     navigation stay testable apart.
//   - No TUI rendering. inbox decides the counts (`$inbox`); how the badge
//     and inbox listing are drawn is the TUI's concern. The two are kept
//     apart so the badge contract is a plain world value, not a widget.
//   - No clarification orchestration. The clarification round-trip
//     (request -> notify -> teleport -> answer -> resume) is driven by the
//     scheduler and orchestrator; inbox only supplies the teleport
//     projection one step of that flow uses.
//
// # Reference
//
// The background-job and inbox-notification flow — spawning background
// effects, the inbox badge, and the clarification round-trip — is
// documented under docs/stories/background-jobs (see README.md and
// runtime.md). The orchestrator-level placement of teleport and the
// room-history stack is in docs/architecture/overview.md.
package inbox
