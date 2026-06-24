// Package proposal implements the draft→review→execute lifecycle for
// user-confirmed actions. It sits between the orchestrator and the host
// layer: the orchestrator drives a [Proposal] through its [Status] phases
// in response to user intents, and on the executing phase this package
// hands the accepted draft to a [host.Registry] handler via [Execute].
//
// A proposal is a named, typed artifact whose shape is declared by an
// [app.ProposalKind] (schema, prompts, execute config) compiled from the
// app's `proposals:` YAML block and attached to states via
// `proposal: <kind>`. The runtime instance — the per-session mutable
// state — is the [Proposal] value this package owns.
//
// # Algorithm
//
// A [Proposal] is a small state machine plus a draft-version log:
//
//   - [New] mints one in [StatusDrafting] with an empty draft and history.
//   - [Proposal.SetDraft] replaces the current draft and appends a
//     [HistoryEntry]. The user feedback that prompted the new draft is
//     recorded on the PREVIOUS entry (it explains the delta into this
//     version), so history reads as "draft → feedback → next draft." The
//     log is soft-capped at [MaxHistoryEntries]: once it would exceed the
//     cap the oldest entries are elided, keeping the most recent window.
//   - [Proposal.EditField] mutates a single draft field in place, for the
//     edit intent that tweaks one value without a full redraft.
//   - [Proposal.Transition] advances [Proposal.Status]. The package does
//     not enforce a legal-transition table; the orchestrator's intent
//     wiring decides which phase follows which, and this method just
//     records the move and bumps [Proposal.UpdatedAt].
//   - [Execute] fires the kind's host invocation, records a [Result], and
//     transitions to [StatusDone] or [StatusFailed].
//
// Every mutator stamps [Proposal.UpdatedAt] with the current UTC time in
// RFC3339, so the stored snapshot always reflects the last change.
//
// # Invariants
//
//   - [Proposal.Current] and [Proposal.History] are never nil after [New];
//     [Proposal.EditField] and [FromMap] also guarantee a non-nil draft map.
//   - [HistoryEntry.Version] is 1-based and equals the entry's position at
//     append time; after a cap-induced elision the field keeps its original
//     value rather than being renumbered.
//   - [Proposal.ToMap] and [FromMap] are inverses for the fields they carry:
//     a round-trip preserves id, kind, status, current, history, owner, and
//     result. JSON numbers decode as float64, so [FromMap] accepts both int
//     and float64 for [HistoryEntry.Version].
//
// # Worked example
//
// A two-version draft lifecycle, then a world-state round-trip:
//
//	p := New("p-1", "shell_command", "sess-1")   // Status = drafting
//	p.SetDraft(map[string]any{"cmd": "echo hi"}, "")
//	p.SetDraft(map[string]any{"cmd": "echo bye"}, "say bye instead")
//	// History[0].Feedback == "say bye instead"  (feedback lands on the prior version)
//	// History[1].Draft["cmd"] == "echo bye", len(History) == 2
//	p.Transition(StatusReviewing)                // Status = reviewing
//	m := p.ToMap()                               // map under $proposal in world.Vars
//	FromMap(m).Current["cmd"]                     // "echo bye"
//
// A runnable form of this trace lives in [ExampleProposal_lifecycle], and a
// minimal synchronous execution in [ExampleExecute].
//
// # Lifecycle
//
// The phases a [Proposal] moves through, and what each means to the user:
//
//	drafting          — LLM produces the initial draft (no host call here).
//	reviewing         — user sees the draft; refine / edit / accept / cancel.
//	executing         — Execute fires the host invocation; auto-transitions.
//	reviewing_result  — user sees the result; retry / rerun / new.
//	done              — terminal success.
//	failed            — terminal failure (retry remains available).
//	cancelled         — terminal cancel.
//
// The runtime registers these built-in intents over the lifecycle: refine,
// edit, accept (alias run), cancel, retry, rerun, modify_and_rerun, new.
// Authors add extra intents on top. The phase semantics and the intent
// wiring are documented in docs/stories/state-machine.md; the YAML schema
// for a kind (schema / draft / refine / execute / policy) is documented as
// `ProposalKind` in docs/embedded/app-schema.md.
//
// # Non-goals
//
//   - No legal-transition enforcement. [Proposal.Transition] records any
//     status; the orchestrator owns which phase follows which, so a single
//     authority decides legality rather than two copies that can disagree.
//   - No concurrent-edit coordination. A [Proposal] assumes a single owner
//     session ([Proposal.OwnerSession]) and is NOT safe for concurrent
//     mutation; cross-session coordination is the caller's job (e.g. the
//     world lock the orchestrator already holds per turn).
//   - No cross-session persistence of proposal objects. A proposal is an
//     ephemeral per-session artifact living under [WorldKey] in world state;
//     durable storage of outcomes is the host handler's concern, not this
//     package's.
//   - No deep cloning of draft values. [Proposal.SetDraft] copies the draft
//     map one level (see cloneDraft); nested mutable values are shared, on
//     the assumption that drafts hold JSON-shaped scalars and containers the
//     caller does not mutate after handing them over.
//
// # Reference
//
// The proposal phase model and intent wiring live in
// docs/stories/state-machine.md. The `ProposalKind` YAML schema —
// schema, draft/refine prompts, and the execute block this package
// reads — is documented in docs/embedded/app-schema.md and typed in
// [app.ProposalKind].
package proposal
