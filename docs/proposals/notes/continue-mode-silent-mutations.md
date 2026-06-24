# Silent-mutation enumeration (R6)

Companion to [`continue-mode-spike.md`](continue-mode-spike.md). Each
entry below is a state-mutating function or method that, at time of
writing, does NOT emit either a typed
[`internal/store.EventKind`](../../internal/store/event.go) or a
[`internal/trace.Ev*`](../../internal/trace/trace.go) constant — i.e.
the change happens off-log.

For each: file:line, what changes, JOURNAL-IT vs LEAVE-OUT, one-sentence
reason.

## In-memory orchestrator state

### S1 — `o.pending[sid]` write (foreground clarify ask)

- **File:** `internal/orchestrator/orchestrator.go:530-534, 1192-1196, 1673-1677`
- **What:** Map entry from `app.SessionID` to a `pendingClarify{intentName, slots}` is set when `Turn`/`SubmitDirect`/`ContinueTurn` detects `intent.ErrMissingSlots` and returns `ModeClarify`. No event is appended; the existing comment at the first site explicitly says "Do NOT persist events for clarify-required outcomes (§4.2 step 4)."
- **Decision:** **JOURNAL-IT.** This is exactly the proposal's §4.7 `clarify.requested` typed entry. The slot bag and the intent name must be durable so resume can rehydrate the orchestrator's `o.pending` map.
- **Reason:** Without this, mid-clarify resume returns the user to a fresh menu and loses any slots they already supplied.

### S2 — `o.pending` clear (clarify answered or transition fired)

- **File:** `internal/orchestrator/orchestrator.go:640, 1290, 1758`
- **What:** `delete(o.pending, sid)` after a successful transition.
- **Decision:** **JOURNAL-IT.** The proposal's §4.7 `clarify.answered` typed entry, body `{intent, slots_final}`. Resume replays the typed stream; the most recent `clarify.requested` without a matching `clarify.answered` rehydrates `o.pending`.
- **Reason:** Without it, resume can't tell whether a pending clarify was answered.

### S3 — `o.cancelListeners[sid]` write/delete

- **File:** `internal/orchestrator/orchestrator.go:232 (set), 305-307 (delete)`
- **What:** A `context.CancelFunc` for the per-session background-job listener goroutine. Process-local only.
- **Decision:** **LEAVE-OUT.**
- **Reason:** Pure in-process plumbing; the goroutine is restarted from scratch on resume via `NewSession`-equivalent code (a fresh `AttachSession`). No durable state is involved.

### S4 — `o.sessionLocks[sid]` write/delete

- **File:** `internal/orchestrator/orchestrator.go:333-334 (set), 309 (delete via stopSessionListener)`
- **What:** Per-session `*sync.Mutex` lookup map; entries are created lazily on first access and removed when the session terminates.
- **Decision:** **LEAVE-OUT.**
- **Reason:** In-process mutex bookkeeping; not persistable, not relevant after process restart.

### S5 — `o.observers` append/remove

- **File:** `internal/orchestrator/observer.go:68 (append), 81 (remove via slice splice)`
- **What:** Slice of `SessionObserver` callbacks fired after every background-turn commit.
- **Decision:** **LEAVE-OUT.**
- **Reason:** In-process callback registry; observers re-register on attach. Not durable state.

## In-memory metamode state

### S6 — `ProposalLedger.Add` (draft proposal staged)

- **File:** `internal/metamode/ledger.go:81-97`
- **What:** Map entry `items[id] = &PendingProposal{State: ProposalDraft, Proposal: p, ...}` plus the random short ID generation. Today's reconstruction-from-chat-metadata path (`ReloadPending`) only sees applied proposals because the apply lifecycle is the one that writes a chat-metadata marker.
- **Decision:** **JOURNAL-IT** — partially. The proposal's §4.7 prefers "strengthen chat metadata" (option 1) over a typed journal kind. The realistic phase-A direction: emit a chat-message-metadata flag on every `Add`, not just on Apply, so the `ReloadPending` walk recovers drafts and discards as well. If chat-metadata strengthening turns out fragile (cross-chat ledger queries?), fall back to a typed `metamode.proposal.staged` journal entry.
- **Reason:** Without this, a `/meta` session that quits with three drafts in the ledger reopens with only the *applied* draft visible. The drafts are recoverable from the shadow dirs on disk, but the in-session ledger UX loses them.

### S7 — `ProposalLedger.Discard` (mark discarded)

- **File:** `internal/metamode/ledger.go:123-139`
- **What:** Sets `pp.State = ProposalDiscarded` and removes the shadow dir.
- **Decision:** **JOURNAL-IT** (per S6's chat-metadata route, or a typed `metamode.proposal.discarded` entry).
- **Reason:** Same as S6 — without a row, `ReloadPending` cannot distinguish "discarded earlier" from "never existed".

### S8 — `ProposalLedger.RecordApplied` (apply lifecycle)

- **File:** `internal/metamode/ledger.go:149-156`
- **What:** Sets `pp.State = ProposalApplied`, flips `reloadPending = true`. Already triggers a chat-message-metadata write upstream (`authoring.Apply` writes the marker) — `ReloadPending` recovers from that.
- **Decision:** **LEAVE-OUT** (already chat-metadata-derivable today; matches §4.7 option 1).
- **Reason:** The apply marker is the one path that already lands in `chat_messages.metadata`. Resume reads it via `ReloadPending`. No additional journal entry needed for this specific transition — the proposal can ride the existing chat-metadata path.

### S9 — `ProposalLedger.{ReloadPending, ConsumeReload}` flag flip

- **File:** `internal/metamode/ledger.go:160-174`
- **What:** Sticky boolean tracked across turns.
- **Decision:** **LEAVE-OUT.**
- **Reason:** Pure in-memory handshake between the ledger and the TUI controller. The flag is re-derivable from "is there an applied proposal whose effects haven't been delivered to the orchestrator yet" — handled by re-running ReloadPending on attach.

## TUI-side notifications

### S10 — `MarkNotificationRead`

- **File:** `internal/tui/tui.go:556, 569, 690`; implementation `internal/jobs/store.go:286-291`
- **What:** Sets `notifications.read_at = NOW` in the SQLite `notifications` table. The proposal §1 already calls out (`internal/trace/trace.go:119-120`) that `EvInboxItemOpened` and `EvInboxItemDismissed` are declared but never emitted.
- **Decision:** **JOURNAL-IT** — as the typed `inbox.item.opened` entry already named in the proposal's §2.2 typed-kind list.
- **Reason:** Resume must show the inbox panel with the *correct* unread/read split, otherwise the user re-sees notifications they already dismissed.

### S11 — `MarkNotificationRead` used as "snooze" path

- **File:** `internal/tui/tui.go:684-694` (the action_required banner Esc-snooze branch)
- **What:** Same DB UPDATE as S10 but with a different user intent: snooze, not "I read it". The existing comment notes this is a semantic mismatch (no dedicated snooze path).
- **Decision:** **JOURNAL-IT** — as a typed `inbox.item.dismissed` entry (the §2.2 typed-kind list distinguishes opened vs dismissed for this reason).
- **Reason:** Same as S10, plus a clean distinction between "opened" and "dismissed" lets future UX behaviour diverge without losing replay determinism.

## Chat-store mutations not riding on the FSM

### S12 — `chats.Store.Create`

- **File:** `internal/chats/store.go:97-118`
- **What:** Inserts a new `chats` row (a new chat thread). Called from the off-path path (via `Resolve`) and the meta-mode path (via `metamode.Controller`).
- **Decision:** **JOURNAL-IT** — as part of the `chats/<id>` document's initial-state ledger; not a separate event. The new chat row's initial values become the value half of a `chats/<id>.checkpoint` entry at the moment it's created.
- **Reason:** Resume must know which chats existed at the moment of quit. Today this is FK-derivable from `chats.session_id = sid`, but the journal proposal aims at self-sufficiency (§2.1 puts `chats/<id>` under the four documents).

### S13 — `chats.Store.AppendMessage`

- **File:** `internal/chats/store.go:322-383`
- **What:** Inserts into `chat_messages` (full content text), updates `chats.{last_active_at, updated_at}`. Runs inside its own DB tx; not inside the orchestrator's tx.
- **Decision:** **JOURNAL-IT** — as the typed `chats.append` patch entry.
- **Reason:** The `chats/<id>` document's transcript is the primary source for both the chat-pane render and (via §4.6 option 2) the main transcript projection of off-path/meta-mode rooms.

### S14 — `chats.Store.SetClaudeSessionID`

- **File:** `internal/chats/store.go:266-278`
- **What:** Sets `chats.claude_session_id`. Called on the first turn of an agent chat to record the claude-side session ID.
- **Decision:** **JOURNAL-IT** — as part of the `chats/<id>` document's `meta` block. The proposal §4.8 already designates `claude_session_id` as the bridge to claude's own durability layer.
- **Reason:** Without it, resume cannot reattach the claude session via `--resume <id>` and would start a fresh claude conversation, losing context.

### S15 — `chats.Store.Rename / Archive`

- **File:** `internal/chats/store.go:283-318`
- **What:** Updates `chats.title` or `chats.status`. Called via host handlers (`host.chat.rename`, `host.chat.archive`) and by the metamode controller (`adapter.go:130-138` for archive).
- **Decision:** **JOURNAL-IT** — as a `chats/<id>.patch` op against the meta block (`{"op":"replace","path":"/meta/title","value":"..."}` or `{"op":"replace","path":"/meta/status","value":"archived"}`).
- **Reason:** The chat list UI is part of the resumable state; archived chats stay hidden, renamed chats stay renamed.

### S16 — `chats.Store.Fork`

- **File:** `internal/chats/store.go:436-498`
- **What:** Creates a new chat row, copies all messages over, atomically.
- **Decision:** **JOURNAL-IT** — emit a `chats/<new_id>.checkpoint` entry containing the forked transcript (the proposal's checkpoint shape already supports this — body is the entire current document value).
- **Reason:** A forked chat is a new document; the fork is the first appearance of the new chat-id; the proposal's `LiveDocs` enumeration must include it on resume.

## Jobs-store lifecycle outside `events`

### S17 — `jobs.JobStore.UpsertJob`

- **File:** `internal/jobs/store.go:67-117`
- **What:** Inserts or replaces a `jobs` row. The orchestrator's `JobSubmitted` event payload carries `{job_id, kind, ...}` so the *event existence* is journalled; the *backing row* is not.
- **Decision:** **JOURNAL-IT** as the `jobs/<id>` document's initial-state checkpoint.
- **Reason:** Resume must restore the job-list view; the `jobs` row is the truth, but a journal entry per upsert is also durable.

### S18 — `jobs.JobStore.UpdateJobStatus`

- **File:** `internal/jobs/store.go:143-162`
- **What:** Updates `jobs.{status, error, result, finished_at, updated_at}` for a job. The orchestrator's `JobCompleted` event captures the *final* status transition; intermediate transitions (e.g. `submitted` → `running`) do NOT emit an event.
- **Decision:** **JOURNAL-IT** — as a `jobs/<id>.patch` op. The proposal §4.4 mandates a job-document checkpoint on every status transition; per-transition patch entries fall out naturally.
- **Reason:** Resume should show `running` jobs as running, not as `submitted`; without the running-state patch, the resumed view regresses.

### S19 — `jobs.JobStore.SweepStaleJobs`

- **File:** `internal/jobs/store.go:124-140`
- **What:** Bulk UPDATE on process start: any row in `running`/`awaiting_input` becomes `failed` with `error=ErrProcessDied`. Runs once at scheduler construction.
- **Decision:** **LEAVE-OUT.**
- **Reason:** This is the *resumption* path itself — it runs because the prior process crashed/exited, so by definition there is no journal "before" state to preserve. The sweep is the response to a missing journal entry, not a producer of one.

### S20 — `jobs.JobStore.RequestClarification`

- **File:** `internal/jobs/clarification.go:30-68`
- **What:** Sets `jobs.{status=awaiting_input, clarification_schema=<JSON>, ...}`. Today the orchestrator emits `EvJobAwaitingInput` as a *trace* breadcrumb but does NOT append a `JobAwaitingInput` event to `events`.
- **Decision:** **JOURNAL-IT** — as a typed `clarify.requested` journal entry for background-job-driven clarifications, with body `{job_id, schema}`. Same kind as foreground clarify (S1) but a different `origin` discriminator.
- **Reason:** Per proposal §6.3, the resumed orchestrator scans for `awaiting_input` jobs and surfaces the clarification UI immediately. Without a journal entry, the schema must be re-read from the `jobs` table — which works today (the table is durable), but only because `jobs/<id>` is one of the four physical documents in §2.1. Cross-checking via a journal entry adds an integrity check.

### S21 — `jobs.JobStore.AnswerClarification`

- **File:** `internal/jobs/clarification.go:72-93`
- **What:** Sets `jobs.{status=running, clarification_answer=<JSON>}`.
- **Decision:** **JOURNAL-IT** — as `clarify.answered` (origin: job).
- **Reason:** Matches S20.

### S22 — `jobs.JobStore.InsertNotification`

- **File:** `internal/jobs/store.go:257-282`
- **What:** Inserts a `notifications` row. The orchestrator emits `EvInboxNotificationPosted` as a trace breadcrumb (already; trace.go:118).
- **Decision:** **LEAVE-OUT** (notifications are a TUI rendering concern derivable from the `notifications` table).
- **Reason:** Notifications are *projections* of events the journal already records (job-terminal, action-required transitions). The notifications table is a query-side index, not a primary source — like the proposal's view of the `events` table in phase B.

## Session-lock and external-key tables

### S23 — `acquireLock` / `releaseLock` / `WithWriterLock`

- **File:** `internal/store/external_keys.go:184-268`
- **What:** Inserts/deletes `session_locks` rows.
- **Decision:** **LEAVE-OUT** — proposal §2.1 already designates `session_locks` as "stay out of the journal" because they're rebuilt on demand from process state.
- **Reason:** Matches the proposal's explicit exclusion.

### S24 — External-key insert/update

- **File:** `internal/store/external_keys.go:60-92`
- **What:** Inserts a row in `external_keys` (binds a transport-thread pair to a session).
- **Decision:** **LEAVE-OUT** — proposal §2.1 already designates `external_keys` as out-of-journal.
- **Reason:** Matches the proposal's explicit exclusion.

### S25 — Chat-lock acquire/release/heartbeat

- **File:** `internal/chats/lock.go:70-184`
- **What:** Inserts/updates/deletes `chat_locks` rows.
- **Decision:** **LEAVE-OUT.**
- **Reason:** Per-process mutual exclusion; same shape as `session_locks`.

## Timeout-table mutations

### S26 — `timeoutDispatcher.persist / unpersist`

- **File:** `internal/orchestrator/timeout.go:404-439`
- **What:** Insert-or-replace / delete in `timeouts` table. Today rearmed on orchestrator construction via `rearmFromStore` (proposal §2.1 already cites this as a "stay out of the journal" case).
- **Decision:** **LEAVE-OUT.**
- **Reason:** Matches §2.1's explicit timeouts-stay-out rule. The proposal still proposes `timeout.armed` / `timeout.cancelled` typed entries as readable breadcrumbs, but they're annotation-only, not load-bearing.

---

## Decision summary

26 mutation sites enumerated above (S1–S26).

| Decision | Count | Sites |
|---|---|---|
| JOURNAL-IT | 15 | S1, S2, S6, S7, S10, S11, S12, S13, S14, S15, S16, S17, S18, S20, S21 |
| LEAVE-OUT | 11 | S3, S4, S5, S8, S9, S19, S22, S23, S24, S25, S26 |

The proposal's audit cited only three sites by name (`MarkNotificationRead`, `metamode.ProposalLedger`, `o.pending`). The full walk surfaces fifteen JOURNAL-IT decisions — most of them are already covered by the proposal's typed-kind catalogue (§2.2) or by the four-document inclusion (§2.1) and don't need additions; the ones that DO need attention:

1. **Chat lifecycle (S12, S14, S15, S16)** needs a clearly-named mapping in §2.2: "`chats.append` covers the message body; the chat-document `meta` block is patched for title/status/claude_session_id/parent_chat_id changes." Today the proposal mentions only `chats.append`.

2. **Background-job clarify (S20, S21)** should share the `clarify.requested`/`clarify.answered` kinds with the foreground (S1, S2) but with an `origin` discriminator. §4.7 currently talks only about foreground.

3. **Proposal-ledger drafts and discards (S6, S7)** — the proposal's §4.7 recommends "strengthen chat metadata" (option 1). This works for *apply* (already done) but the audit shows draft and discard transitions are NOT today written to chat metadata. Either the chat-metadata path must be extended (recommended), or the journal needs `metamode.proposal.staged` and `metamode.proposal.discarded` typed kinds. Pick one in the phase-A design doc.
