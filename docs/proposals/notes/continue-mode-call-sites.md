# Continue-mode dual-write call-site mapping

**Purpose.** Work-order for Phase A of the continue-mode proposal: every existing
site that mutates persistent session state must also write a journal entry in the
same SQL transaction. One section per mutation site.

---

## A. `store.AppendEvents` callers — orchestrator.go (foreground turns)

### 1. Turn — rejected (non-clarify) path

- **File:** `internal/orchestrator/orchestrator.go:559`
- **Mutates today via:** `store.AppendEvents` (EventKind=TurnStarted, LLMToolCall, ValidationFailed, GuardRejected, IntentAccepted, TurnEnded with outcome=rejected)
- **Trigger:** User turn; harness returned a valid intent; machine.Turn produced a ValidationError with a code other than ErrMissingSlots (e.g. GUARD_FAILED, INTENT_NOT_ALLOWED).
- **Journal entries to emit:**
  - kind: `state.transition` — doc: `state` — body: none (state is unchanged; omit or emit a no-op patch confirming current state)
  - kind: `world.patch` — doc: `world` — body: ops computed from pre/post world snapshot (typically empty if no EffectApplied events in the failure set; emit anyway as zero-length ops list to signal "world snapshot at this turn")
  - kind: `guard.rejected` — doc: null — body: `{intent, error_code, guard_hint}` (verbatim from ValidationError fields)
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx as the `events` write
- **Notes:** The machine events in `result.Events` may include ValidationFailed, GuardRejected, IntentAccepted with a partial per-arm story. The journal typed entry `guard.rejected` is a summary; per-arm detail lives in the existing `events` rows and does not need a separate journal entry. World is unchanged on this path so `world.patch` ops list will be empty — emit it anyway for checkpoint-cadence accounting.

---

### 2. Turn — success path

- **File:** `internal/orchestrator/orchestrator.go:620`
- **Mutates today via:** `store.AppendEvents` (EventKind=TurnStarted, LLMCalled[absent], LLMToolCall, IntentAccepted, TransitionApplied, EffectApplied×N, StateExited×N, StateEntered×N, HostInvoked×N, HostDispatched×N, HostReturned×N, JobSubmitted×M, TurnEnded with outcome=transitioned)
- **Trigger:** User turn (via `Turn()`); harness succeeded; machine.Turn produced a successful transition; host calls dispatched.
- **Journal entries to emit:**
  - kind: `world.patch` — doc: `world` — body: ops from pre/post `world.Vars` snapshot (covers all EffectApplied set/increment mutations, including host bind results)
  - kind: `state.transition` — doc: `state` — body: `{from, to, intent}` (verbatim from TransitionApplied payload `{from, to, intent}`)
  - kind: `host.invoked` — doc: null — body: `{namespace, args, background: false}` — one entry per HostInvoked event in the set (pre-bind args at machine time)
  - kind: `host.dispatched` — doc: null — body: `{namespace, args, rerender_fell_back}` — one entry per HostDispatched event (post-rerender args)
  - kind: `host.returned` — doc: null — body: `{namespace, data, error}` — one entry per HostReturned event
  - kind: `jobs/<id>.update` (or `jobs.submitted`) — doc: `jobs/<id>` — body: `{status: "submitted", kind, origin_state}` — one entry per JobSubmitted event
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx as the `events` write
- **Notes:** EffectApplied events carry `set:`, `increment:`, and `say:` shapes. The `world.patch` entry must encode both set and increment operations as JSON-Patch ops. `say:` effects have no world impact so they do not appear in the patch ops (they may appear as a separate typed entry `host.said` if transcript projection needs them — see §4.6). The `LLMToolCall` event payload `{tool, intent}` is not duplicated in the journal because it is harness diagnostic noise, not session state; flag: this is a minor divergence from the proposal's "every semantic payload" intent.

---

### 3. RunIntent — rejected path

- **File:** `internal/orchestrator/helpers.go:296`
- **Mutates today via:** `store.AppendEvents` (EventKind=TurnStarted, ValidationFailed, GuardRejected, TurnEnded outcome=rejected)
- **Trigger:** Programmatic intent dispatch (flow test / `kitsoki turn` CLI); machine rejected.
- **Journal entries to emit:**
  - kind: `world.patch` — doc: `world` — body: empty ops list (world unchanged)
  - kind: `guard.rejected` — doc: null — body: `{intent, error_code, guard_hint}`
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** `RunIntent` is in `internal/orchestrator/helpers.go:296`. Same mapping as site 1 for the rejected branch.

---

### 4. RunIntent — success path

- **File:** `internal/orchestrator/helpers.go:338`
- **Mutates today via:** `store.AppendEvents` (same success set as Turn but without LLMToolCall)
- **Trigger:** Programmatic intent dispatch; machine succeeded.
- **Journal entries to emit:** Same set as site 2 minus `host.invoked`/`host.dispatched`/`host.returned` if no host calls in this specific invocation; journal writer should always emit at least `world.patch` + `state.transition`.
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** `RunIntent` is the `internal/orchestrator/helpers.go` function referenced at line 338 (AppendEvents for success). Mapping identical to site 2.

---

### 5. SubmitDirect — rejected path

- **File:** `internal/orchestrator/orchestrator.go:1220`
- **Mutates today via:** `store.AppendEvents` (TurnStarted, ValidationFailed, TurnEnded outcome=rejected)
- **Trigger:** TUI menu selection via `SubmitDirect`; machine validation failed.
- **Journal entries to emit:**
  - kind: `world.patch` — doc: `world` — body: empty ops list
  - kind: `guard.rejected` — doc: null — body: `{intent, error_code, guard_hint}`
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** `direct: true` in TurnStarted payload marks this as a non-LLM turn. Journal entry should preserve this provenance for trace readability.

---

### 6. SubmitDirect — success path

- **File:** `internal/orchestrator/orchestrator.go:1271`
- **Mutates today via:** `store.AppendEvents` (same success set as Turn without LLMToolCall)
- **Trigger:** TUI menu selection; machine succeeded; host calls dispatched.
- **Journal entries to emit:** Same as site 2.
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx

---

### 7. ContinueTurn — success path

- **File:** `internal/orchestrator/orchestrator.go:1738`
- **Mutates today via:** `store.AppendEvents` (TurnStarted with `clarify: true`, TransitionApplied, EffectApplied×N, HostInvoked×N, HostDispatched×N, HostReturned×N, TurnEnded outcome=transitioned)
- **Trigger:** User completed slot-fill; TUI calls `ContinueTurn`; machine succeeded.
- **Journal entries to emit:**
  - kind: `clarify.answered` — doc: null — body: `{intent, slots_final}` (merged slots from `pendingClarify + supplementSlots`)
  - kind: `world.patch` — doc: `world` — body: ops from pre/post snapshot
  - kind: `state.transition` — doc: `state` — body: `{from, to, intent}`
  - host event typed entries (same as site 2) if host calls fired
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** This is the companion to the `clarify.requested` typed entry emitted at site 8. Resume uses the most recent unmatched `clarify.requested` / `clarify.answered` pair to reconstruct `o.pending`. The `clarify.answered` entry must include `slots_final` (the fully merged slot map) so resume can short-circuit `o.pending` reconstruction without re-running the merge logic.

---

## B. ModeClarify / `o.pending` write — in-memory only

### 8. Turn / RunIntent / SubmitDirect — ModeClarify path (o.pending write)

- **File:** `internal/orchestrator/orchestrator.go:521` (Turn), `internal/orchestrator/helpers.go` (RunIntent), `internal/orchestrator/orchestrator.go:1182` (SubmitDirect)
- **Mutates today via:** In-memory only — `o.pending[sid] = &pendingClarify{...}`. No events are written (`// Do NOT persist events for clarify-required outcomes` comment at line 519).
- **Trigger:** Machine returned ErrMissingSlots. Happens in Turn, RunIntent, and SubmitDirect.
- **Journal entries to emit:**
  - kind: `clarify.requested` — doc: null — body: `{intent, slots_so_far, slots_needed, schema}` where `slots_needed` is `ComputeClarification(...)` output and `schema` is the slot-fill schema
- **Inside which lock:** `o.mu` (the small mutex guarding `o.pending`), NOT the session writer lock; the proposal §4.7 says this entry should be emitted even though no `events` row is written today. **This is the only site that needs a standalone `journal` write outside the AppendEvents transaction.**
- **Transaction wrapping:** Standalone `INSERT INTO journal` (no `events` row to pair with). The proposal says "must be inside the same SQL tx as `events` write" — but today no events are written here. The journal write should be its own short transaction, or the ModeClarify branch must be changed to also write a minimal `events` row (e.g. a `TurnStarted`+`TurnEnded` outcome=clarify pair) so the journal row can share a tx.
- **Notes:** **Most surprising finding** — this is a site with zero persistence today. Dual-write here requires either (a) adding a `events` write to this path (invasive) or (b) issuing a standalone `journal` INSERT (diverges from Rule 1 in §4.9). The proposal §4.9 Rule 1 says both writes must share a tx, but that constraint presupposes an `events` row exists. The implementer must decide: add synthetic events here or carve out an exception for `clarify.requested`. Recommend option (a): emit a `TurnStarted`+`TurnEnded(outcome=clarify)` event pair so the journal write has a paired events row, then write `clarify.requested` in the same tx.

---

## C. `store.AppendEvents` in `internal/orchestrator/oncomplete.go`

### 9. handleJobTerminal — background-completion turn

- **File:** `internal/orchestrator/oncomplete.go:228`
- **Mutates today via:** `store.AppendEvents` (TurnStarted kind=background_completion, EffectApplied×N from on_complete chain, HostInvoked×N, HostDispatched×N, HostReturned×N, EffectApplied `set $inbox`, JobCompleted, TurnEnded outcome=background_completion)
- **Trigger:** Background job reaches terminal state (done/failed/cancelled); `handleJobTerminal` fires from the per-session listener goroutine.
- **Journal entries to emit:**
  - kind: `world.patch` — doc: `world` — body: ops from pre/post world snapshot (covers on_complete set effects and `$inbox` refresh)
  - kind: `jobs/<id>.update` — doc: `jobs/<jobID>` — body: `{status, error?, result?}` (from JobCompleted payload)
  - kind: `host.invoked` / `host.dispatched` / `host.returned` — doc: null — one triplet per host call in the on_complete chain
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx as the `events` write
- **Notes:** The `$inbox` world var is refreshed here via a live DB call (`inbox.RefreshSummary`) and its new value written as an EffectApplied. The `world.patch` ops must include this `$inbox` set operation so resume does not need to re-run `RefreshSummary`. The `jobs/<id>.update` document patch for terminal status is technically redundant with the `jobs` table write in `internal/jobs/store.go` (site 19), but the journal entry is the load-bearing copy for resume. The `PostJobNotification` call after the commit (line 235) is a `notifications` table INSERT — see site 20.

---

## D. `store.AppendEvents` in `internal/orchestrator/offpath.go`

### 10. AskOffPath — question-only (agent failure path)

- **File:** `internal/orchestrator/offpath.go:121` and `offpath.go:138`
- **Mutates today via:** `store.AppendEvents` (OffPathQuestion at turn=maxTurn+1)
- **Trigger:** Off-path user question; agent call failed before returning an answer.
- **Journal entries to emit:**
  - kind: `offpath.question` — doc: null — body: `{question, chat_id}`
- **Inside which lock:** session writer lock (`mu` via `appendOffPathEvents`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** Off-path events are allocated a fresh turn number (`maxTurn+1`) to avoid PK collisions with foreground turn events. The journal `(turn, seq)` for these entries will match the allocated off-path turn. The `doc_version` in the journal schema should be 0 for typed entries.

---

### 11. AskOffPath — question + answer (success path)

- **File:** `internal/orchestrator/offpath.go:158`
- **Mutates today via:** `store.AppendEvents` (OffPathQuestion, OffPathAnswer)
- **Trigger:** Off-path user question; agent returned an answer; both events appended in one call.
- **Journal entries to emit:**
  - kind: `offpath.question` — doc: null — body: `{question, chat_id}`
  - kind: `offpath.answer` — doc: null — body: `{answer, chat_id}`
- **Inside which lock:** session writer lock (`mu` via `appendOffPathEvents`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** The `answer` field carries the **full literal text** of the agent reply. This satisfies R8 (determinism: resume never calls the LLM; the answer is in the journal). The `chat_id` links to the `chats/<id>` document that also holds the appended messages; the journal has both the typed entry and the `chats.append` doc entries for full fidelity.

---

### 12. MarkOffPathEntered

- **File:** `internal/orchestrator/offpath.go:185`
- **Mutates today via:** `store.AppendEvents` (OffPathEntered)
- **Trigger:** User enters off-path mode from the TUI.
- **Journal entries to emit:**
  - kind: `offpath.entered` — doc: null — body: `{from_state}`
- **Inside which lock:** session writer lock (`mu` via `appendOffPathEvents`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** Proposal §6.5 defines `offpath.chat.resolved` as a typed kind; `offpath.entered` is the matching pair.

---

### 13. MarkOffPathExited

- **File:** `internal/orchestrator/offpath.go:193`
- **Mutates today via:** `store.AppendEvents` (OffPathExited)
- **Trigger:** User returns to on-path mode.
- **Journal entries to emit:**
  - kind: `offpath.exited` — doc: null — body: `{to_state}`
- **Inside which lock:** session writer lock (`mu` via `appendOffPathEvents`)
- **Transaction wrapping:** must be inside the same SQL tx

---

## E. `store.AppendEvents` in `internal/orchestrator/timeout.go`

### 14. fireTimeout — synthetic timeout-fired turn

- **File:** `internal/orchestrator/timeout.go:671`
- **Mutates today via:** `store.AppendEvents` (TurnStarted kind=timeout, TimeoutFired, TransitionApplied, StateExited, StateEntered, EffectApplied×N from on_enter, HostInvoked×N, HostDispatched×N, HostReturned×N, TurnEnded outcome=timeout)
- **Trigger:** A `Timeout:` declaration's deadline elapsed; `fireTimeout` fires from a timer goroutine.
- **Journal entries to emit:**
  - kind: `timeout.fired` — doc: null — body: `{from, to}` (verbatim from TimeoutFired payload)
  - kind: `state.transition` — doc: `state` — body: `{from, to, intent: "__timeout__"}`
  - kind: `world.patch` — doc: `world` — body: ops from on_enter effects in the target state
  - host event typed entries if on_enter dispatched host calls
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** The `timeouts` table row for `(sid, state_path)` is deleted after a successful fire via `unpersist`. The proposal §2.1 notes the `timeouts` table is an independent source of truth; the `timeout.fired` journal entry is a trace breadcrumb, not the authoritative persistence. On resume, `timeout.RearmFromStore` reads the table (site 15), not the journal.

---

## F. Timeout table writes — `internal/orchestrator/timeout.go`

### 15. timeoutDispatcher.persist — arm a new timeout

- **File:** `internal/orchestrator/timeout.go:409` (`INSERT OR REPLACE INTO timeouts ...`)
- **Mutates today via:** Direct SQL write to `timeouts` table
- **Trigger:** A new state with `Timeout:` is entered (via `armTimeoutForState` called from Turn/RunIntent/SubmitDirect/handleJobTerminal after commit). Runs outside the session writer lock (the commit has already happened).
- **Journal entries to emit:**
  - kind: `timeout.armed` — doc: null — body: `{state_path, target, fires_at_ms}`
- **Inside which lock:** `d.mu` (timeoutDispatcher mutex only; NOT the session writer lock)
- **Transaction wrapping:** The `persist` call happens after `AppendEvents` has committed. The journal entry for `timeout.armed` is therefore NOT inside the AppendEvents transaction. It should be a standalone short journal INSERT, or the arm call should be moved inside the turn-write transaction. **This is a coexistence-rule exception that needs a design decision.**
- **Notes:** The proposal §2.1 explicitly says `timeouts` stays outside the journal because it survives restart directly. The `timeout.armed` entry is trace-only per §2.1. Implementer decision: either move `timeout.armed` journal write to inside the turn tx (requires reorganising callsite so arm runs before commit) or accept the slight ordering gap as a known exception and document it.

---

### 16. timeoutDispatcher.unpersist — cancel or fire completion

- **File:** `internal/orchestrator/timeout.go:423` (`DELETE FROM timeouts ...`)
- **Mutates today via:** Direct SQL DELETE from `timeouts` table
- **Trigger:** `cancel()` is called (e.g. on normal state exit) or `runEntry` cleanup after fire.
- **Journal entries to emit:**
  - kind: `timeout.cancelled` — doc: null — body: `{state_path}` (on cancel only; fire-cleanup does not need a separate entry since `timeout.fired` already marks the terminal event)
- **Inside which lock:** `d.mu`
- **Transaction wrapping:** Standalone journal INSERT (same caveat as site 15)
- **Notes:** `cancelAll` (line 207) calls `unpersist` for every pending entry on session termination; each generates a `timeout.cancelled` entry. For trace fidelity these are useful but none are load-bearing for resume (the table already shows empty for a terminal session).

---

## G. `store.AppendEvents` in `internal/orchestrator/teleport.go`

### 17. Teleport

- **File:** `internal/orchestrator/teleport.go:117`
- **Mutates today via:** `store.AppendEvents` (TurnStarted kind=teleport, TransitionApplied, EffectApplied×N for target.Slots + teleport_job_id + teleport_proposal_id, TurnEnded outcome=transitioned)
- **Trigger:** TUI inbox selection with a TeleportTarget; `Teleport()` fires.
- **Journal entries to emit:**
  - kind: `state.transition` — doc: `state` — body: `{from: priorState, to: target.State, intent: "teleport"}`
  - kind: `world.patch` — doc: `world` — body: ops for each slot merge + teleport_job_id + teleport_proposal_id (from EffectApplied events)
- **Inside which lock:** session writer lock (`sessMu`)
- **Transaction wrapping:** must be inside the same SQL tx
- **Notes:** The teleport flow always pairs with a `MarkNotificationRead` call (site 21) which happens **before** the `AppendEvents` transaction in the same goroutine. The notification read is a separate DB write; the journal `inbox.item.opened` entry is emitted alongside it (see site 21).

---

## H. Direct table writes in `internal/chats/store.go`

All chat store writes happen under the chat-level lock (`chat_locks`), NOT the session writer lock. Per the proposal §3.2 R4 (spike required), these writes may interleave with foreground turn writes. The journal entries for chat mutations use per-document `doc_version` ordering (monotonic per `chats/<id>`) which is total within the document even if `(turn, seq)` is not globally ordered.

### 18. chats.Create / chats.Resolve (insert)

- **File:** `internal/chats/store.go:107` (Create), `internal/chats/store.go:240` (Resolve insert path)
- **Mutates today via:** `INSERT INTO chats ...`
- **Trigger:** First agent.ask/agent.talk call for a room+scope_key combo; `Resolve` races to create if none exists. Also called by `offpath.go` when resolving the off-path chat thread.
- **Journal entries to emit:**
  - kind: `chats/<id>.update` (or `chats.created`) — doc: `chats/<id>` — body: `{app_id, room, scope_key, title, status: "active", claude_session_id: ""}` (full initial document value as a JSON-Patch `add` op on `/`)
- **Inside which lock:** chat-level lock (separate from session writer lock)
- **Transaction wrapping:** Chat-level SQLite tx; journal write should be added to the same tx
- **Notes:** The `chats/<id>` document in the journal carries `{messages: [], meta: {...}}`. On first creation the messages array is empty. `Resolve` uses a tx that wraps the SELECT+INSERT, so the journal write can join that same tx.

---

### 19. chats.AppendMessage

- **File:** `internal/chats/store.go:353`
- **Mutates today via:** `INSERT INTO chat_messages ...` (inside a tx with an `UPDATE chats SET last_active_at`)
- **Trigger:** Every agent.ask/agent.talk turn appends user + assistant messages; off-path also appends via the agent.talk handler.
- **Journal entries to emit:**
  - kind: `chats/<id>.append` — doc: `chats/<id>` — body: `{seq, role, content, metadata}` as a JSON-Patch `add` op on `/messages/-`
- **Inside which lock:** chat-level lock (per `AppendMessage`'s internal tx)
- **Transaction wrapping:** Same tx as the `INSERT INTO chat_messages`
- **Notes:** This is the highest-frequency journal write in a chat-heavy session (~2 messages per agent.ask turn). The proposal §4.4 checkpoint policy overrides this doc to checkpoint every 10 appended messages. The `content` field carries the **full LLM-returned text** satisfying R8. The `metadata` field carries `claude_session_id` updates and any other per-message metadata.

---

### 20. chats.Archive

- **File:** `internal/chats/store.go:305`
- **Mutates today via:** `UPDATE chats SET status = 'archived' ...`
- **Trigger:** `/meta new` TUI command; meta-mode controller archiving a chat before starting a new meta session.
- **Journal entries to emit:**
  - kind: `chats/<id>.update` — doc: `chats/<id>` — body: `[{"op": "replace", "path": "/meta/status", "value": "archived"}]`
- **Inside which lock:** no session lock (chat archive is a direct store call)
- **Transaction wrapping:** Standalone UPDATE; journal write is a standalone INSERT in a short tx
- **Notes:** No session writer lock wraps this call. The journal writer must acquire its own short tx.

---

### 21. chats.Rename

- **File:** `internal/chats/store.go:285`
- **Mutates today via:** `UPDATE chats SET title = ? ...`
- **Trigger:** Chat rename command (not yet exposed in TUI but store method exists).
- **Journal entries to emit:**
  - kind: `chats/<id>.update` — doc: `chats/<id>` — body: `[{"op": "replace", "path": "/meta/title", "value": "<newTitle>"}]`
- **Inside which lock:** none
- **Transaction wrapping:** Standalone UPDATE; journal is a standalone short tx

---

### 22. chats.SetClaudeSessionID

- **File:** `internal/chats/store.go:266`
- **Mutates today via:** `UPDATE chats SET claude_session_id = ? ...`
- **Trigger:** First `agent.ask_with_mcp` call for a chat assigns the claude session ID. Called at `internal/host/agent_ask_with_mcp.go:477` and `internal/host/agent.go:206`.
- **Journal entries to emit:**
  - kind: `chats/<id>.update` — doc: `chats/<id>` — body: `[{"op": "replace", "path": "/meta/claude_session_id", "value": "<claudeSID>"}]`
- **Inside which lock:** chat-level lock (agent.ask holds `chat_locks` row)
- **Transaction wrapping:** Should join the agent.ask chat-level tx
- **Notes:** This is critical for R8 / §4.8: the `claude_session_id` is the bridge that lets `--resume` hand off to claude's own session file. If this update is not in the journal, a resumed session can't call `claude --resume <id>` on the next turn. The `metadata` field of `AppendMessage` may also carry the updated claude session ID (the agent.ask handler writes it before appending the user message per `agent_ask_with_mcp.go:456`), but the standalone `SetClaudeSessionID` update also needs a journal entry.

---

### 23. chats.Fork

- **File:** `internal/chats/store.go:473` and `internal/chats/store.go:486`
- **Mutates today via:** `INSERT INTO chats ...` + `INSERT INTO chat_messages SELECT ...` (in one tx)
- **Trigger:** Meta-mode `authoring.fork` tool creates a fork chat for diff-ing a proposed change.
- **Journal entries to emit:**
  - kind: `chats/<id>.created` — doc: `chats/<newID>` — body: full document snapshot `{messages: [...copied_messages...], meta: {..., parent_chat_id: parentChatID}}`
- **Inside which lock:** no session writer lock; the Fork tx is self-contained
- **Transaction wrapping:** Join the existing Fork tx
- **Notes:** The copied messages can be large. The proposal §4.4 says `jobs/<id>` gets a checkpoint on every status transition; a similar policy for fork-created chats (checkpoint immediately on create since the full content is known) avoids replaying a large insert sequence. Flag: the journal body on creation may exceed SQLite BLOB limits for very long parent chats.

---

## I. Direct table writes in `internal/jobs/store.go`

### 24. jobs.UpsertJob — job submitted/created

- **File:** `internal/jobs/store.go:98` (`INSERT OR REPLACE INTO jobs ...`)
- **Mutates today via:** Direct SQL UPSERT to `jobs` table
- **Trigger:** `scheduler.Submit` → `JobStore.UpsertJob` when a background effect is dispatched (see `effects.go:dispatchBackground`). Also on status updates.
- **Journal entries to emit:**
  - kind: `jobs/<id>.update` — doc: `jobs/<id>` — body: `{status, kind, origin_state, origin_proposal_id, payload_sketch}` (omit `__on_complete` from payload for brevity; include job ID)
- **Inside which lock:** no session lock (jobs store is accessed from the scheduler goroutine)
- **Transaction wrapping:** Standalone UPSERT; journal is a standalone short tx
- **Notes:** The `jobs/<id>` document in the journal carries the current job status for resume. On resume, if the job is in `submitted` or `awaiting_input`, the orchestrator reattaches it. The `jobs.UpsertJob` function is also called for status updates (running, done, failed, cancelled, awaiting_input) — each update should emit a `jobs/<id>.update` journal entry.

---

### 25. jobs.UpdateJobStatus — status transitions

- **File:** `internal/jobs/store.go:143`
- **Mutates today via:** `UPDATE jobs SET status=? ...`
- **Trigger:** Scheduler marks job running, done, failed, cancelled, or awaiting_input.
- **Journal entries to emit:**
  - kind: `jobs/<id>.update` — doc: `jobs/<id>` — body: `{status, error?, result?}` as a JSON-Patch replace on `/status` (and `/result` / `/error` if set)
- **Inside which lock:** no session lock
- **Transaction wrapping:** Standalone UPDATE; journal is a standalone short tx
- **Notes:** The proposal §4.4 says `jobs/<id>` checkpoints on every status transition — this is the site where that checkpoint policy fires. Status transitions: submitted → running → done/failed/cancelled, or running → awaiting_input.

---

### 26. jobs.InsertNotification

- **File:** `internal/jobs/store.go:270`
- **Mutates today via:** `INSERT INTO notifications ...`
- **Trigger:** `inbox.PostJobNotification` after background job terminal (oncomplete.go:235), `dispatchBackground` after job submit (effects.go:134), `handleJobAwaitingInput` after job asks for clarification.
- **Journal entries to emit:**
  - kind: `inbox.item.created` — doc: null — body: `{notification_id, severity, title, body, teleport_state, teleport_slots, teleport_job_id, teleport_proposal_id}`
- **Inside which lock:** none
- **Transaction wrapping:** Standalone INSERT; journal is a standalone short tx
- **Notes:** The proposal named `inbox.item.opened` and `inbox.item.dismissed` but did not name `inbox.item.created`. Notifications are part of the `$inbox` world var (summarised as unread counts); however the full notification row is in the `notifications` table, not in `world.Vars`. On resume, the notifications table persists independently — the journal entry is a trace breadcrumb. However, the `$inbox` world var summary **is** inside `world.Vars` and is captured by `world.patch` entries whenever `inbox.RefreshSummary` fires (see site 9). The `inbox.item.created` typed entry provides semantic visibility without being load-bearing.

---

## J. Silent mutations — inbox read/dismiss

### 27. TUI MarkNotificationRead — inbox item opened

- **File:** `internal/tui/tui.go:556`, `internal/tui/tui.go:569`, `internal/tui/tui.go:690`
- **Mutates today via:** `jobs.JobStore.MarkNotificationRead` → `UPDATE notifications SET read_at=? WHERE id=?`
- **Trigger:** (a) Inbox item selected without teleport target (line 556), (b) Inbox item selected before teleport fires (line 569), (c) Action-required banner dismissed via Esc (line 690 — used as "snooze").
- **Journal entries to emit:**
  - For (a) and (b): kind: `inbox.item.opened` — doc: null — body: `{notification_id, notification_title}`
  - For (c): kind: `inbox.item.dismissed` — doc: null — body: `{notification_id, notification_title}` (Esc is treated as snooze/dismiss semantics; proposal's `inbox.item.dismissed` kind covers this)
- **Inside which lock:** TUI goroutine only; no session lock; no transaction
- **Transaction wrapping:** Standalone UPDATE; journal is a standalone short tx
- **Notes:** The `EvInboxItemOpened` and `EvInboxItemDismissed` trace constants are declared in `internal/trace/trace.go:119-120` but **never emitted** today. This site was called out explicitly in the proposal §1 #4. The TUI calls `MarkNotificationRead` directly without going through the orchestrator, so there is no `events` row to pair with. The journal write is standalone. The `notifications.read_at` column is the real persistence; the journal entry is trace-visible metadata.

---

## K. Silent mutations — metamode ProposalLedger

### 28. ProposalLedger.Add

- **File:** `internal/metamode/ledger.go:81`
- **Mutates today via:** In-memory map write only — `l.items[id] = &PendingProposal{State: ProposalDraft, ...}`
- **Trigger:** `authoring.propose` tool handler creates a new proposal during a meta-mode agent.ask turn.
- **Journal entries to emit:**
  - kind: `metamode.proposal.staged` — doc: null — body: `{proposal_id, state: "draft", created_at}`
- **Inside which lock:** `l.mu` (ProposalLedger mutex)
- **Transaction wrapping:** In-memory only today; journal write is a standalone INSERT
- **Notes:** The proposal §4.7 recommendation is "option 1: strengthen chat metadata" rather than adding typed journal kinds. However, the proposal §8 Phase A list includes `metamode.proposal.<verb>` kinds for phase A. There is a contradiction: if option 1 is used, chat-metadata writes (via `chats.AppendMessage` with metadata) are the persistence, and no standalone `metamode.proposal.*` journal entries are needed. If option 2 is used, these journal entries ARE needed. The proposal recommends option 1 for v1. **Implementer must decide which path to take before writing Phase A code.** See "Proposal vs reality" below.

---

### 29. ProposalLedger.Discard

- **File:** `internal/metamode/ledger.go:123`
- **Mutates today via:** In-memory state change + `authoring.Discard(pp.Proposal)` (filesystem cleanup of shadow dir)
- **Trigger:** `authoring.discard` tool handler during meta-mode session.
- **Journal entries to emit:**
  - kind: `metamode.proposal.discarded` — doc: null — body: `{proposal_id}`
- **Inside which lock:** `l.mu`
- **Transaction wrapping:** Standalone journal INSERT (no DB write today beyond the in-memory change)
- **Notes:** Same option-1 vs option-2 decision as site 28. If option 1, the discard is tracked via a chat-message metadata write that `ReloadPending` can find.

---

### 30. ProposalLedger.RecordApplied

- **File:** `internal/metamode/ledger.go:149`
- **Mutates today via:** In-memory state change: `pp.State = ProposalApplied` + `l.reloadPending = true`
- **Trigger:** `authoring.apply` tool handler completes successfully; the applied proposal is recorded.
- **Journal entries to emit:**
  - kind: `metamode.proposal.applied` — doc: null — body: `{proposal_id}`
- **Inside which lock:** `l.mu`
- **Transaction wrapping:** Standalone journal INSERT
- **Notes:** `RecordApplied` also sets `reloadPending=true` which signals `Controller.Send` to return `SendResult.ReloadRequested=true`. The TUI then reloads the orchestrator. This reload is a process-level side effect that does not need a journal entry of its own; resume will reconstruct it by finding `metamode.proposal.applied` and comparing against current app YAML.

---

## L. Silent mutations — disambiguation flow

### 31. TUI disambiguation — choice persistence

- **File:** `internal/tui/tui.go:485-486` (handleDisambiguationChoice), `internal/tui/disambiguation.go:67`
- **Mutates today via:** In-memory only — the chosen candidate is passed directly to `SubmitDirect` which then calls `AppendEvents`. No separate disambiguation-specific event is written.
- **Trigger:** User picks a disambiguation candidate from the TUI overlay.
- **Journal entries to emit:**
  - kind: `disambig.chosen` — doc: null — body: `{intent, candidate_label, slots_chosen}` (emit before/alongside the `SubmitDirect` AppendEvents tx)
- **Inside which lock:** TUI goroutine; the actual AppendEvents for the chosen intent fires under the session writer lock in `SubmitDirect`
- **Transaction wrapping:** Should be added to the `SubmitDirect` AppendEvents tx — the disambiguation choice is upstream of the actual turn write and should be folded into the same journal batch
- **Notes:** The `EvDisambigPresented` and `EvDisambigChosen` trace constants are declared (`trace.go:113-114`) but there is no `EventKind` constant for disambiguation in `store/event.go`. The choice is implicitly persisted through `SubmitDirect`'s events, but there is no explicit "user picked option N" event. For transcript projection (§4.6), `disambig.presented` and `disambig.chosen` typed entries are needed so `ReconstructFromJournal` can render the disambiguation list row. **This is a silent mutation gap not listed in the proposal's Phase A items** — it should be added.

---

## M. Additional chat mutations (not in original scope list)

### 32. chats.SetClaudeSessionID via metamode.controller

- **File:** `internal/metamode/controller.go:521`, `internal/metamode/adapter.go:160-161`
- **Mutates today via:** `chats.Store.SetClaudeSessionID` → `UPDATE chats SET claude_session_id = ? ...`
- **Trigger:** Meta-mode controller's `Send` method updates the claude session ID after a new agent.ask call starts a new claude session.
- **Journal entries to emit:** Same as site 22 — kind: `chats/<id>.update` with a replace op on `/meta/claude_session_id`
- **Inside which lock:** meta-mode session lock (controller's own mutex)
- **Transaction wrapping:** Standalone UPDATE; journal is a standalone short tx
- **Notes:** This is a second call site for `SetClaudeSessionID` (the first is `internal/host/agent.go`). Both must emit the same journal entry kind.

---

## N. `store.AppendEvents` in helpers.go (RunIntent)

> Covered by sites 3 and 4 above.

---

## Summary table

| # | File:line | EventKind(s) today | Journal kinds emitted | Lock | Tx |
|---|-----------|--------------------|-----------------------|------|----|
| 1 | `orchestrator.go:559` | TurnStarted, LLMToolCall, ValidationFailed, GuardRejected, TurnEnded | `world.patch`, `guard.rejected` | session writer | same as AppendEvents |
| 2 | `orchestrator.go:620` | TurnStarted, LLMToolCall, IntentAccepted, TransitionApplied, EffectApplied×N, StateExited×N, StateEntered×N, HostInvoked×N, HostDispatched×N, HostReturned×N, JobSubmitted×M, TurnEnded | `world.patch`, `state.transition`, `host.invoked`×N, `host.dispatched`×N, `host.returned`×N, `jobs/<id>.update`×M | session writer | same as AppendEvents |
| 3 | `helpers.go:296` | TurnStarted, ValidationFailed, TurnEnded | `world.patch`, `guard.rejected` | session writer | same as AppendEvents |
| 4 | `helpers.go:338` | TurnStarted, TransitionApplied, EffectApplied×N, TurnEnded | `world.patch`, `state.transition`, host entries | session writer | same as AppendEvents |
| 5 | `orchestrator.go:1220` | TurnStarted, ValidationFailed, TurnEnded | `world.patch`, `guard.rejected` | session writer | same as AppendEvents |
| 6 | `orchestrator.go:1271` | TurnStarted, TransitionApplied, EffectApplied×N, TurnEnded | `world.patch`, `state.transition`, host entries | session writer | same as AppendEvents |
| 7 | `orchestrator.go:1738` | TurnStarted(clarify), TransitionApplied, EffectApplied×N, TurnEnded | `clarify.answered`, `world.patch`, `state.transition`, host entries | session writer | same as AppendEvents |
| 8 | `orchestrator.go:521` / `helpers.go` / `orchestrator.go:1182` | **(none — no persist today)** | `clarify.requested` | `o.mu` only | **standalone tx (no paired events row)** |
| 9 | `oncomplete.go:228` | TurnStarted, EffectApplied×N, HostInvoked×N, HostDispatched×N, HostReturned×N, EffectApplied($inbox), JobCompleted, TurnEnded | `world.patch`, `jobs/<id>.update`, `host.invoked`×N, `host.dispatched`×N, `host.returned`×N | session writer | same as AppendEvents |
| 10 | `offpath.go:121,138` | OffPathQuestion (agent failure) | `offpath.question` | session writer (via appendOffPathEvents) | same as AppendEvents |
| 11 | `offpath.go:158` | OffPathQuestion, OffPathAnswer | `offpath.question`, `offpath.answer` | session writer (via appendOffPathEvents) | same as AppendEvents |
| 12 | `offpath.go:185` | OffPathEntered | `offpath.entered` | session writer (via appendOffPathEvents) | same as AppendEvents |
| 13 | `offpath.go:193` | OffPathExited | `offpath.exited` | session writer (via appendOffPathEvents) | same as AppendEvents |
| 14 | `timeout.go:671` | TurnStarted, TimeoutFired, TransitionApplied, StateExited, StateEntered, EffectApplied×N, TurnEnded | `timeout.fired`, `state.transition`, `world.patch`, host entries | session writer | same as AppendEvents |
| 15 | `timeout.go:409` | *(timeouts table direct write)* | `timeout.armed` | `d.mu` only | **standalone tx, post-commit** |
| 16 | `timeout.go:423` | *(timeouts table direct delete)* | `timeout.cancelled` | `d.mu` only | **standalone tx** |
| 17 | `teleport.go:117` | TurnStarted, TransitionApplied, EffectApplied×N, TurnEnded | `state.transition`, `world.patch` | session writer | same as AppendEvents |
| 18 | `chats/store.go:107,240` | *(chats table INSERT)* | `chats/<id>.update` (create) | chat-level | same chat tx |
| 19 | `chats/store.go:353` | *(chat_messages INSERT)* | `chats/<id>.append` | chat-level | same chat tx |
| 20 | `chats/store.go:305` | *(chats UPDATE status)* | `chats/<id>.update` (archive) | none | standalone tx |
| 21 | `chats/store.go:285` | *(chats UPDATE title)* | `chats/<id>.update` (rename) | none | standalone tx |
| 22 | `chats/store.go:266` | *(chats UPDATE claude_session_id)* | `chats/<id>.update` (claude_session_id) | chat-level | same chat tx |
| 23 | `chats/store.go:473,486` | *(chats INSERT + chat_messages INSERT SELECT)* | `chats/<id>.created` (fork, full snapshot) | none | same Fork tx |
| 24 | `jobs/store.go:98` | *(jobs INSERT OR REPLACE)* | `jobs/<id>.update` | none | standalone tx |
| 25 | `jobs/store.go:143` | *(jobs UPDATE status)* | `jobs/<id>.update` | none | standalone tx |
| 26 | `jobs/store.go:270` | *(notifications INSERT)* | `inbox.item.created` | none | standalone tx |
| 27 | `tui/tui.go:556,569,690` | *(notifications UPDATE read_at — silent)* | `inbox.item.opened`, `inbox.item.dismissed` | TUI goroutine only | standalone tx |
| 28 | `metamode/ledger.go:81` | *(in-memory only)* | `metamode.proposal.staged` | `l.mu` | standalone tx |
| 29 | `metamode/ledger.go:123` | *(in-memory only)* | `metamode.proposal.discarded` | `l.mu` | standalone tx |
| 30 | `metamode/ledger.go:149` | *(in-memory only)* | `metamode.proposal.applied` | `l.mu` | standalone tx |
| 31 | `tui/tui.go:485` + `disambiguation.go:67` | *(in-memory only — no EventKind)* | `disambig.chosen` | TUI goroutine | fold into SubmitDirect AppendEvents tx |
| 32 | `metamode/controller.go:521` | *(chats UPDATE claude_session_id via adapter)* | `chats/<id>.update` (claude_session_id) | meta-mode session lock | standalone tx |

---

## Proposal vs reality

The following discrepancies were found between the proposal text and the actual codebase:

1. **`clarify.requested` needs a standalone tx (breaks Rule 1 §4.9).** The proposal §4.9 Rule 1 states every journal write must be in the same SQL transaction as the corresponding `events` write. But the `ModeClarify` path (site 8) **intentionally writes zero `events` rows** — the comment at `orchestrator.go:519` says "Do NOT persist events for clarify-required outcomes". The journal entry for `clarify.requested` has no paired events row to share a transaction with. The proposal §4.7 does not address this contradiction. **Recommendation before coding:** either add a synthetic `TurnStarted + TurnEnded(outcome=clarify)` event pair to the ModeClarify path so Rule 1 can be satisfied, or carve out an explicit exception in §4.9.

2. **MetaMode ledger: option-1 vs option-2 ambiguity.** The proposal §4.7 recommends "option 1: strengthen chat metadata" but §8 Phase A explicitly lists `metamode.proposal.<verb>` typed journal kinds as Phase A deliverables. These are contradictory — option 1 means no standalone ledger journal entries; option 2 means standalone entries. **The implementer cannot start Phase A ledger work without resolving this.** The code audit confirms the ledger state is purely in-memory (`ledger.go:52-56`); `ReloadPending` reconstructs from chat messages via `adapter.go`, but the `Discard` and `RecordApplied` transitions are not reliably tracked in chat metadata today (the `Discard` call only cleans the filesystem, with no message written; `RecordApplied` has the same gap). Option 1 as stated in the proposal is not fully achievable without adding chat-metadata writes for discard and apply transitions.

3. **Disambiguation has no `EventKind` and is absent from the Phase A list.** The `disambig.presented` / `disambig.chosen` trace constants (`trace.go:113-114`) have never been wired to a store event. The TUI's disambiguation overlay is purely in-memory; the chosen candidate lands in `SubmitDirect` which writes the resulting turn's events — but there is no "user saw N options and picked option M" record anywhere. The proposal §4.6 transcript projection says "disambig.presented row has no journal counterpart"; the proposal does not list `disambig.chosen` as a Phase A typed kind. For full transcript projection fidelity, this gap must be closed. **Block or explicitly defer?** If §4.6 transcript projection is Phase A, `disambig.presented`/`disambig.chosen` must be added in Phase A. If transcript projection is Phase B, this can wait.

4. **`HostInvoked` is emitted by the machine, not the orchestrator.** The proposal §4.3 table step 1 says dual-write happens at `store.AppendEvents` call sites. But `HostInvoked` events are created inside `machine.go:931` and `machine.go:1144` (in `RunEffects` and `Turn`) and returned as part of `result.Events`. The orchestrator collects them into the events batch and then calls `AppendEvents`. The journal writer must therefore process the `result.Events` slice to extract `HostInvoked` payloads and emit `host.invoked` typed entries — it cannot intercept them at the machine level. This is not a blocker but the implementation detail is worth noting: the `AppendEventsAndJournal` wrapper (sketch in §4.9) must walk the events slice to produce the right typed entries.

5. **`timeout.armed` and `timeout.cancelled` are post-commit writes (breaks Rule 1 §4.9 for these entries).** `armTimeoutForState` is called after `AppendEvents` returns (e.g. `orchestrator.go:636`). There is no mechanism to move the `timeouts.persist` write inside the events transaction without significant refactoring. The proposal acknowledges this in §2.1 ("timeouts stay out of the journal as load-bearing truth") but §2.2 includes `timeout.armed` / `timeout.cancelled` as typed journal kinds for trace visibility. These entries will inevitably have a small temporal gap relative to the events tx that preceded them. Document this as an explicit exception to Rule 1.
