# Continue-mode spike notes

Phase-A gate for [`continue-mode-proposal.md`](../continue-mode-proposal.md). Each
section below covers one row from §3.2 (R1–R9). Prototypes referenced
under `spike/r1-applier/`, `spike/r2-state-path/`, `spike/r3-bench/`
are throwaway — not wired into production builds and never imported
from `internal/` or `cmd/`.

## R1 — Schema-aware patch applier preserves declared types

**Reference:** `coerceWorldVar` at `internal/store/replay.go:189-213`.

**Approach.** Wrote a prototype applier at
`docs/proposals/notes/spike/r1-applier/main.go` that drives every
`testdata/apps/*/app.yaml` through:

1. Build a seed JSON doc `{"vars":{}}`.
2. For every key in `app.WorldSchema`, emit an RFC-6902 op
   `{"op":"add","path":"/vars/<key>","value":<schema-default>}`.
3. Run the op list through `evanphx/json-patch/v5` (already in
   `go.mod` as an indirect dep — no new module needed).
4. Unmarshal the result, walk `vars`, and apply
   `coerceWorldVar` against the schema entry.
5. Compare deep-equal against the typed value the engine *would*
   produce at run-time (`int64` for `type: int`, `bool` for
   `type: bool`, `string` for `type: string`).

**Run.**

```
$ go run ./docs/proposals/notes/spike/r1-applier
=== testdata/apps/background_jobs/app.yaml ===  vars passed: 2  failed: 0
=== testdata/apps/cloak/app.yaml ===            vars passed: 3  failed: 0
=== testdata/apps/dev-story/app.yaml ===        vars passed: 28 failed: 0
=== testdata/apps/parallel_smoke/app.yaml ===   vars passed: 5  failed: 0
=== testdata/apps/proposal_smoke/app.yaml ===   vars passed: 3  failed: 0
=== testdata/apps/timeout/app.yaml ===          vars passed: 1  failed: 0
TOTAL passed=42 failed=0
```

**Verdict: PASS** — with one design caveat.

**Caveat the proposal must absorb.** The proposal's §4.1 `Applier`
interface sketch returns `json.RawMessage`:

```go
type Applier interface {
    Apply(doc DocID, current json.RawMessage, ops []PatchOp) (json.RawMessage, error)
}
```

That signature **cannot pass R1**. JSON-marshalling a `map[string]any`
whose `disturbance` key is `int64(0)` and immediately unmarshalling at
the call site back into `map[string]any` re-converts `int64(0)` to
`float64(0)`. Initial prototype failed exactly this way — see
git history of the prototype if curious. The fix is to return a typed
Go map (or `world.World`) and have callers re-marshal only when
persisting. The applier owns the typed-value invariant; the bytes
are a serialization detail downstream.

**What changes in the proposal if this fails.** §4.1 must say:

> Apply(doc, current, ops) returns the post-image as a Go value
> (`world.World` for the world doc; `map[string]any` for generic
> documents) — not as `json.RawMessage`. Encoding back to JSON for
> the journal write happens at the storage edge.

The hybrid-applier idea (RFC-6902 + post-coerce) is sound. The
*interface* needs revision; nothing about the storage format
or the op vocabulary changes.

## R2 — State-path round-trip survives JSON-Pointer encoding

**Reference:** `internal/machine/parallel.go:77-80, 102-125` for the
parallel-encoding sigils.

**Approach.** Prototype at
`docs/proposals/notes/spike/r2-state-path/main.go`. Two sub-checks:

(a) **path values on `/path`.** Every state path that
`testdata/apps/*` produces — including the parallel-encoded form
`clock#clock.calendar.day1|clock.weather.dry` — goes into the
*value* side of `{"op":"replace","path":"/path","value":"<encoded>"}`.
RFC-6901 escaping is irrelevant on the value side; `#`, `|`, `.`
are ordinary JSON-string bytes.

(b) **RFC-6901 segment escaping.** Confirm that segments containing
`/` and `~` round-trip when used as JSON-Pointer *path* segments
(not as values). We don't use this in the proposal today — state
paths always ride on the value side — but the test guards against
a future refactor that decides to make `/path/<encoded>` a generic
pointer.

**Run.**

```
$ go run ./docs/proposals/notes/spike/r2-state-path
=== R2.a path values on /path ===
  PASS p="foyer"
  PASS p="cloakroom"
  PASS p="bar.dark"
  PASS p="bar.lit"
  PASS p="ended"
  PASS p="clock"
  PASS p="clock.calendar.day1"
  PASS p="clock.weather.dry"
  PASS p="clock#clock.calendar.day1|clock.weather.dry"
  PASS p="clock#clock.calendar.soggy|clock.weather.rain"
  PASS p="root#root.r1.x|root.r2.y|root.r3.z"

=== R2.b RFC-6901 escape (segments containing / and ~) ===
  PASS seg="plain" -> "/plain"
  PASS seg="with/slash" -> "/with~1slash"
  PASS seg="with~tilde" -> "/with~0tilde"
  PASS seg="with~/both" -> "/with~0~1both"

ALL PASS
```

**Verdict: PASS** — no surprises.

**What changes in the proposal if this fails.** N/A — passes. Note
for §4.1: the patch encoder must keep `state.transition` as a
plain string value, not as a structured object whose root key is
the encoded path (the latter would require RFC-6901 escaping of
`#` and `|`, which json-patch/v5 handles but would clutter the
on-disk JSONL).

## R3 — slog dual-write to SQLite journal at <100µs added per row p95

**References:** `cmd/kitsoki/trace.go:348-399` (BuildTraceLogger,
multiHandler), `internal/trace/ringbuffer.go` (existing extra
handler in the chain), `internal/store/sqlite.go:160-232`
(AppendEvents transaction shape).

**Approach.** Three benches under
`docs/proposals/notes/spike/r3-bench/`. Each opens a SQLite DB with
the exact pragmas `internal/store/sqlite.go:95-99` sets
(`journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`,
`max_open_conns=1`).

**Bench 1 — independent transactions per row (`main.go`).** This is
the *wrong* shape: each INSERT in its own `BEGIN`/`COMMIT` so every
row pays an fsync. Result:

```
events-only (baseline)    n=1000  p50=1.89ms  p95=3.00ms  p99=8.50ms
events + journal (dual)   n=1000  p50=2.09ms  p95=3.92ms  p99=7.32ms
added-per-row p95 delta = 921.665µs  (target <100µs)  FAIL
```

The baseline is *already* 3ms p95 because every commit fsyncs the
WAL. The 100µs target is unreachable when measured this way — fsync
latency on the dev hardware is dominated by ext4/disk, not by SQLite
or by Go.

**Bench 2 — dev DB single-INSERT (`dev.go`).** Copy
`~/.local/share/kitsoki/sessions.db` (18 MB, 11+ live sessions) to a
temp dir, INSERT 1000 journal rows in independent transactions:

```
dev-db single-INSERT n=1000  p50=2.24ms  p95=5.92ms  p99=13.01ms
```

Same shape: ~6ms p95 dominated by fsync; the dev DB is bigger so
the WAL append is slightly slower than the empty-DB case.

**Bench 3 — incremental INSERT inside an open transaction
(`incremental.go`).** This is the *right* shape because the proposal's
§4.9 Rule 1 explicitly mandates that the journal write piggybacks on
the existing AppendEvents transaction. One `BEGIN`, one `Prepare`,
1000 INSERTs, one `COMMIT`:

```
incremental INSERT inside open tx n=1000
  p50=6.4µs   p95=17.2µs   p99=48.2µs   max=374µs
```

**Verdict: PASS** — with a framing note. The §3.2 R3 target of "<100µs
added per row at p95" is achievable *if and only if* the journal
INSERT shares the same transaction as the matching `INSERT INTO events`
row (§4.9 Rule 1). Standalone journal commits cannot meet the target
because fsync alone is ~3-6ms on the dev hardware. The proposal already
mandates the right shape; the spike confirms the cost stays under the
target with that shape.

**What changes in the proposal if this fails.** §3.2 R3 should
re-word "Benchmark on the dev SQLite file" to "Benchmark inside the
existing AppendEvents transaction" so a future reader doesn't repeat
the per-INSERT mistake. The target itself (100µs) is fine for the
intended shape.

## R4 — Chat-append concurrency vs FSM event writes

**References:**
- `internal/host/agent.go:160-181` (`runAgentTalkWithChat` acquires
  the chat-level lock via `cs.WithLock`).
- `internal/chats/lock.go:47-61` (`Store.WithLock` writes to
  `chat_locks` table — a per-chat row, not session-scoped).
- `internal/chats/store.go:322-383` (`AppendMessage` opens its own
  DB transaction).
- `internal/orchestrator/orchestrator.go:75-89, 321-337`
  (`o.sessionLocks[sid]` — per-session in-process mutex that wraps
  every foreground-turn `AppendEvents`).
- `internal/store/external_keys.go:184` (`WithWriterLock` —
  cross-process session writer lock; used by
  `cmd/kitsoki/session.go:282` but NOT by `cmd/kitsoki/main.go` —
  i.e. `kitsoki run` does NOT take this lock).

**Inspection finding.** Under `kitsoki run`:

1. A foreground turn flows: `Turn()` → acquires
   `o.sessionLock(sid)` (in-process mutex) → `loadJourney` →
   `harness.RunTurn` → `machine.Turn` → `dispatchHostCalls` (this
   is where a host like `host.agent.ask_with_mcp` runs) →
   `AppendEvents` (its own SQL tx) → release lock.

2. The chat append inside `host.agent.ask_with_mcp` happens via
   `cs.AppendMessage(...)` which opens its **own** SQL tx, *not*
   the orchestrator's tx — they're sequential within the goroutine,
   not atomic together.

3. The chat-level lock (`cs.WithLock`) is on the `chat_locks` row,
   keyed by `chat_id`. The session writer lock (`session_locks`,
   `WithWriterLock`) is on `session_id`. These are disjoint.

**Can interleaving happen?**

- *Within one process*: NO. The chat append runs synchronously
  inside `dispatchHostCalls` while `o.sessionLock(sid)` is still
  held. The orchestrator does not parallelise host calls.
  Off-path runs `AskOffPath` which takes the same `o.sessionLock`
  via `appendOffPathEvents` (offpath.go:215) — it serialises with
  the foreground turn.

- *Across processes*: YES, in principle. `kitsoki session continue`
  takes `WithWriterLock` per command; `kitsoki run` does not.
  If a user runs the TUI and a Jira-driven `session continue` for
  the same session simultaneously, the cross-process lock is one-
  way. The proposal §5.3 ships single-attach in v1 (TUI holds
  the writer lock for the attach lifetime), which closes this
  window — but only once §5.3 is implemented. Today the window
  is open. This isn't a new bug — pre-existing.

**Verdict: PASS** (no observable interleaving under the §5.3
single-attach assumption). The mitigation §3.2 R4 proposes
("per-document write epoch — `(doc_version)` is still total order
per document, even if `(turn, seq)` is not total order *globally*")
is **not needed** as long as:

1. Phase A ships `kitsoki run --continue` taking
   `WithWriterLock` for the attach lifetime (matches §5.3 design).
2. The journal's `chat.append` typed entry is written inside the
   orchestrator's `sessionLock` critical section — i.e. the
   orchestrator, not the chat store, writes the journal entry
   *after* `cs.AppendMessage` returns. The chat row goes through
   the chat tx; the journal row goes through the orchestrator tx
   (which is the §4.9 Rule 1 single tx that also writes `events`).

**What changes in the proposal if this fails.** N/A under the
single-attach assumption. If we ever ship multi-attach (the
claude-code-sessions-proposal direction), the journal needs the
per-doc-epoch fallback proposed in §3.2 R4.

**Recommendation for §4.9 wording.** Add a Rule 8: "Chat-append
journal entries are written by the orchestrator after
`cs.AppendMessage` returns, inside the same `o.sessionLock` critical
section that wraps the foreground turn. The chat-store DB tx and the
journal/events DB tx are sequential but ordered."

## R5 — Snapshot machinery is unscheduled today

**Confirmed by grep:**

```
$ grep -rn '\.Snapshot(' internal/orchestrator/ internal/store/
internal/store/sqlite_test.go:248:	require.NoError(t, st.Snapshot(sid, snap.Turn, snap))
internal/store/sqlite_test.go:284:	require.NoError(t, st.Snapshot(sid, 5, ...))
internal/store/sqlite_test.go:285:	require.NoError(t, st.Snapshot(sid, 20, ...))
internal/store/sqlite_test.go:310:	require.NoError(t, st.Snapshot(sid, 3, ...))
internal/store/sqlite_test.go:380:	require.NoError(t, st.Snapshot(sid, 1, ...))
internal/orchestrator/observer_test.go:140:	sids, outcomes := obs.Snapshot()
internal/orchestrator/observer_test.go:233:	sids, _ := obs.Snapshot()
```

The two `observer.Snapshot()` calls are unrelated — they're against
a different type (the observer registry, not the store). The store's
`Snapshot(session, turn, snap)` is called only from
`internal/store/sqlite_test.go`. No production code path takes a
snapshot.

**Verdict: PASS** (the proposal's audit assumption is confirmed).

**What changes in the proposal if this fails.** N/A.

## R6 — Silent-mutation enumeration

Full enumeration is in
[`continue-mode-silent-mutations.md`](continue-mode-silent-mutations.md).
Summary: 11 distinct mutation sites identified across `jobs`, `chats`,
`metamode`, `tui`, and `orchestrator`. 7 → JOURNAL-IT; 4 → LEAVE-OUT.

**Verdict: PASS** (enumeration complete).

**What changes in the proposal if this fails.** Each LEAVE-OUT
decision adds a sentence to §2.1's "stay out of the journal" list
or to the §6 hard-edges section explaining what resume does not
restore.

## R7 — Transcript projection prototype

**References:**
- `internal/tui/transcript.go:139-489` for the Append constructors.
- `internal/tui/tui.go:99-192` for `RootModel` shape.
- Real-session data: walked event payloads for sessions
  `3a1168eb-43a0-4842-819b-78bc1352cf2b` (bugfix, turn=4, 67 events)
  and `3574857e-5fe1-49b0-b06c-37602b77c934` (oregon-trail,
  turn=11, 104 events) from `~/.local/share/kitsoki/sessions.db`.

**Inspection finding.** For every transcript-row kind the existing
TUI emits, here is the journal-derivable status:

| Transcript row kind | Constructor | Reconstructable from events today? |
|---|---|---|
| User turn header `> <input>` | `AppendTurn(input, view)` | YES — `TurnStarted.payload.input` |
| Rendered view body | `AppendTurn(input, view)` | **NO — view text is not in any event payload.** Engine logs it via `slog.String("view_rendered", result.View)` at `orchestrator.go:667` but the slog event is not persisted to `events` |
| System message | `AppendSystem(body)` | PARTIAL — emitted from TUI directly, no event |
| Slash-output | `AppendSlashOutput(body)` | NO — pure TUI-side |
| Off-path Q | implicit `AppendTurn(input, "")` then... | YES — `OffPathQuestion.payload.question` |
| Off-path A | `AppendOffPathAnswer("", answer)` | YES — `OffPathAnswer.payload.answer` |
| Meta-list rows | `AppendMetaList(headers, rows)` | NO — pure TUI-side; rebuildable from chat-store query, not journal |
| Error/rejection | `AppendError(input, msg)` | YES — `ValidationFailed.payload.message` or `GuardRejected.payload.message` |
| Guard hint | `AppendGuardHint(hint)` | YES — `ValidationFailed.payload.guard_hint` |
| Clarification ask | `AppendClarification(input, msg)` | PARTIAL — `clarify.requested` (proposed new typed entry, §4.7) carries the slot schema but the *rendered prompt* is computed by `ComputeClarification` at TUI time |

**Sample event payload, real bugfix session, T1:**

```
T1/0  TurnStarted        {"input":"PLTFRM-89912","turn":1}
T1/1  LLMToolCall        {"intent":"select_ticket","tool":"transition"}   [intent only, no LLM text]
T1/2  TransitionApplied  {"from":"intro","intent":"select_ticket","slots":{"ticket":"PLTFRM-89912"},"to":"ticket_confirm"}
T1/3..12  EffectApplied  {"set":{...}}  [host bind payloads]
T1/13 HostDispatched     {"args":{...},"background":false,"namespace":"host.ticket_info.fetch","rerender_fell_back":false}
T1/14 HostReturned       {"data":{"exit_code":0,"ok":true,"stdout":"...full JSON blob..."},"namespace":"..."}
T1/15..18 EffectApplied  ...
T1/19 HostReturned       ...
T1/20 TurnEnded          {"outcome":"transitioned","to":"phase_minus_1_executing"}
```

The user's input (`"PLTFRM-89912"`) is in `TurnStarted`. The state-
transition info is in `TransitionApplied`. The host bindings are in
`EffectApplied`. The host return blobs (full stdout) are in
`HostReturned`. **The rendered view that the TUI displayed for turn 1
is not in any of these.**

**Verdict: BLOCKED on rebuilding the rendered view.**

The projection is **NOT total** under the existing event payloads.
The view text — the markdown body the user actually saw — is computed
by `m.machine.Turn` and lives transiently in `TurnResult.View` (and
in trace as `slog.String("view_rendered", ...)`), but is not
persisted to `events`. Without it, §4.6 option 2 ("reconstruct from
journal projection") produces a transcript with correct intent
headers but blank bodies.

**Three resolution paths, in order of preference:**

1. **Phase A blocker (preferred).** Add a `view_rendered` field to
   the `TurnEnded` event payload (or a new `ViewRendered` event
   kind). `BuildJourney` ignores it; the transcript projection reads
   it. Cost: one additional string per turn (~5 KB for a heavy
   view); the trace already carries it so we know it's small enough.
   This makes §4.6 option 2 the right answer and §4.6 option 3
   (whole `view.rendered` typed entry) unnecessary.

2. **§4.6 option 3 with full-fidelity.** Write a typed
   `view.rendered` journal entry per turn whose body is the literal
   markdown. The proposal already lists this as the "biggest log-
   volume cost" path. The cost estimate is roughly the same as #1
   (one string per turn) so the cost concern in §4.6 is overstated.

3. **Re-render on resume (REJECTED).** Walk the journal, replay
   world+state, then call `machine.RenderState` against the restored
   world. This violates §6.8 ("No `machine.RenderState` call happens
   during replay") and §6.9 determinism — view templates may invoke
   host handlers or `world.last_error` formatting whose post-image
   could differ from the original.

**Recommendation.** Pick (1). Add the rendered-view payload field
to `TurnEnded` (or a new event kind). The change is minimal, lives
inside the orchestrator's existing AppendEvents path, and makes the
projection total. This becomes a §8 phase-A bullet.

**What changes in the proposal if this fails.** §4.6 must mandate
option 1/2 above as a phase-A coding task. §6.8 "the TUI renders
[view text] on first frame, deterministically, against the restored
world" is **wrong as written** — re-rendering would call host
handlers (which the same paragraph forbids). The wording needs to
be: "view text is read from the journal/event payload, never
recomputed."

## R8 — Determinism payload audit

**References:**
- `internal/store/event.go:14-77` — every `EventKind` constant.
- `internal/store/replay.go:67-132` — what `BuildJourney` already
  consumes from each kind.
- `internal/orchestrator/orchestrator.go:477-480, 754-768, 800-813`
  — host event emission sites.
- `internal/orchestrator/offpath.go:112-157` — off-path event emission.
- `internal/chats/store.go:322-383` — chat-message append (full
  content text stored in `chat_messages.content` column).

**Walk through every EventKind:**

| EventKind | LLM/host text field | Full text in payload? | Notes |
|---|---|---|---|
| TurnStarted | `payload.input` | YES | user's raw input |
| LLMCalled | (n/a — not emitted; constant declared at event.go:21 but never appended; replay treats it as no-op) | — | dead constant |
| **LLMToolCall** | `payload.tool, payload.intent` | **NO LLM TEXT** | only the parsed intent name + tool name; the LLM's raw response isn't there |
| ValidationFailed | `payload.code, payload.message, payload.intent` | YES — error message is full | |
| TransitionApplied | `payload.from, to, intent, slots` | YES — slots carry parsed LLM output | the LLM's *interpretation* (intent + slot fill) is here |
| EffectApplied | `payload.set, payload.increment, payload.say` | YES | `say` carries the rendered narrative string |
| HostInvoked | `payload.namespace, payload.args, payload.emit, payload.background` | YES — args fully serialised | machine-time snapshot |
| HostDispatched | `payload.namespace, payload.args, payload.rerender_fell_back, payload.background` | YES | post-rerender args |
| **HostReturned** | `payload.namespace, payload.data, payload.error` | **YES — `data` is the FULL `Result.Data` map** including `stdout`, `answer`, `markdown`, etc. | verified on real session — 200+-char stdout blobs are inline |
| OffPathEntered | `payload.from_state` | YES | |
| OffPathExited | `payload.to_state` | YES | |
| OffPathQuestion | `payload.question, payload.chat_id` | YES | full user question text |
| **OffPathAnswer** | `payload.answer, payload.chat_id` | **YES — full LLM answer text** | offpath.go:154 |
| TurnEnded | `payload.outcome, payload.to, payload.code` | partial (no view; see R7) | view text missing — R7 finding |
| StateExited/Entered | `payload.state` | YES (state-path is text-complete) | |
| IntentAccepted | (declared but unused in BuildJourney) | n/a | |
| GuardRejected | (only emitted by machine; payload depends on emit site) | YES | |
| JobSubmitted | `payload.job_id, payload.kind, ...` | YES | |
| JobCompleted | `payload.job_id, payload.status, payload.result` | YES — result is the full host return blob | verified |
| TimeoutFired | `payload.state` | YES (no LLM text involved) | |

**Cross-package text persistence:**

- **Chat appends** (`internal/chats/store.go:352-356`): the full
  `content` string lands in `chat_messages.content`. The journal's
  `chats.append` typed entry must carry `{chat_id, seq, role,
  content, metadata}` verbatim. Today the content is durable in
  `chat_messages`; the proposal needs to mirror it (or reference
  it by `(chat_id, seq)`) into a `chats.append` journal entry so
  the journal is self-sufficient for transcript reconstruction.
  Decision: **mirror it.** Cross-table FK chains during replay are
  fragile if `chat_messages` is later truncated independently.

**Two outstanding gaps:**

1. **LLMToolCall does NOT carry the LLM's raw response text.** The
   payload (`internal/orchestrator/orchestrator.go:477-480`) records
   only `tool` and `intent` (the parsed name). The harness's
   `params` map (the full tool-call argument dict the LLM
   produced) is *not* in the event. The "extracted intent" is in
   `TransitionApplied.payload.intent + slots` which together
   reconstruct what the engine *did* with the LLM's output —
   sufficient for replay because replay applies the transition
   directly, not the raw LLM bytes. Per §6.8/§6.9, "resume never
   calls the LLM"; the raw response is therefore not needed for
   *replay correctness*. It IS missing for *trace
   diagnostics* — but the trace JSONL (slog) already captures
   `harness.response.raw` separately, so this gap is non-blocking
   for the journal proposal.

   **Verdict for LLMToolCall: text-complete in the sense the
   proposal needs.** The intent+slots in `TransitionApplied` are
   the actionable record. LLMToolCall is annotation-only at replay
   time (replay.go:115 already treats it as no-op).

2. **TurnEnded does NOT carry the rendered view.** (R7 finding.) The
   view is the only LLM-derivable text not in the event log today.

**Verdict: PASS with one phase-A action.**

- For replay (state + world restoration) every payload that needs
  text already has it — confirmed against `replay.go` consumers.
- For transcript reconstruction (R7), TurnEnded must grow a
  `view_rendered` field. This is the *only* new payload field
  phase A needs.

**What changes in the proposal if this fails.** Phase A adds a
`view_rendered: string` field to `TurnEnded.payload` (or a new
`ViewRendered` event kind — preference for a payload-field change
to avoid bumping the EventKind taxonomy and breaking mode-1
cassettes; §4.3 step 4 already pre-commits to "keep `events`
populated"). Nothing else moves.

## R9 — Trace + SQLite coexistence under failure injection

**References:**
- `internal/store/sqlite.go:84-115` (open path with pragmas;
  WAL mode is on).
- `internal/store/sqlite.go:160-232` (AppendEvents transaction shape).
- `cmd/kitsoki/trace.go:348-453` (BuildTraceLogger; multiHandler).
- `internal/trace/ringbuffer.go` (existing in-chain handler).

**Inspection finding.** Phase A's §4.9 rules read against the current
code:

**Rule 1 — Single SQL transaction per turn write.** The current
`AppendEvents` (sqlite.go:160) already opens `BeginTx`, runs an
unbounded number of INSERTs, and COMMITs at the end. Adding a
second INSERT statement (`INSERT INTO journal`) inside the same
`tx.ExecContext` chain is a 1-line change at sqlite.go:218 (just
before the `tx.Commit`). The schema-modify is also low risk:
`schema.sql:60-70` shows the existing `IF NOT EXISTS` idiom. The
phase-A wiring sketch in §4.9 ("`AppendEventsAndJournal`") is
implementable directly against this code.

**Single concern:** `sqlite.go:173` does `SAVEPOINT kitsoki_append`
after `BeginTx`. The savepoint is never explicitly RELEASEd; SQLite
auto-releases on COMMIT. This is benign for adding journal inserts
inside the same tx — the savepoint just bounds the events portion.
No change needed.

**Rule 2 — slog handler is wrapped, not replaced.** `BuildTraceLogger`
already constructs a `multiHandler` chain with the ring buffer as a
fixed-position handler (`trace.go:362-364`); JSONL and pretty sinks
are added conditionally. Adding the journal handler is the same
pattern: append to the `handlers` slice. The proposal's "wrapped not
replaced" mandate matches the existing shape exactly.

The `multiHandler.Handle` (trace.go:428-437) does NOT recover panics
in its child handlers — if any handler panics, the whole chain
panics. **The proposal's §4.9 Rule 2 footnote** ("A panic in the
journal handler (recovered) demotes to an ERROR log on the *other*
handlers; the turn still completes") **is not implementable against
the current `multiHandler` without a code change.** Either:

- Wrap each child handler's `Handle` in a `defer recover()` block, or
- The journal handler itself does the recover internally.

This is a phase-A coding task, not a proposal blocker. Calling
it out so it doesn't get skipped.

**Rule 3 — JSONL is a tail, not a source of truth.** The current
trace JSONL handler (`slog.NewJSONHandler` at trace.go:376) does
not return errors to the caller — `slog`'s default behaviour is to
ignore handler errors. JSONL write failures already degrade
silently. The proposal's "WARN-level log + TUI banner" mitigation
requires the journal handler to *itself* surface the failure
(via a separate slog event or a metric), not to bubble through slog.
Achievable; phase-A code task.

**Rule 4 — Schema migration is `IF NOT EXISTS`-safe.** The current
`schema.sql:22-30` uses `CREATE TABLE IF NOT EXISTS events ...`.
Adding `CREATE TABLE IF NOT EXISTS journal (...)` follows the same
pattern. Idempotency is preserved. The `Open()` path already runs
the embedded DDL on every open. The proposal's Rule 4 ("Adding the
`journal` table follows the existing pattern") is direct.

**Rule 5 — Writer lock encloses the dual write.** `WithWriterLock`
is at `internal/store/external_keys.go:184` and is *not* taken by
`kitsoki run` today (only by `cmd/kitsoki/session.go:282` in
`session continue`). Phase A must add `WithWriterLock` to the TUI
attach path (called out in proposal §5.3). The R4 spike confirms
this is the right shape. Once the lock is around the whole TUI
attach, the dual write (Rule 1) trivially happens inside it.

**Rule 6 — Concurrent readers use SQLite WAL mode.** Already true
(sqlite.go:96). No change.

**Rule 7 — Phase B's projection rebuild is one-way.** Out of scope
for the spike.

**Three failure modes — implementability check:**

1. **SQLite write fails mid-turn.** `tx.Rollback` is already in
   place (sqlite.go:170 `defer func() { _ = tx.Rollback() }()`).
   A failed `INSERT INTO journal` inside the same tx triggers
   rollback of the events INSERTs too. Verified by reading the code
   path. Atomic. **Implementable as-is once Rule 1 lands.**

2. **JSONL write fails (disk full / EROFS).** The slog JSON handler
   swallows write errors today. The journal slog handler (a new
   sibling) must surface the failure via a separate channel. **One
   line of code; not blocked by existing structure.**

3. **slog handler panics.** The current `multiHandler.Handle` is
   not panic-safe. Phase A must add a `defer recover()` to each
   child handler call. **Small code change; not blocked by
   existing structure.**

**Verdict: PASS** — §4.9 rules are all implementable against the
current code; three small code-task notes (panic-safe
multiHandler, journal-handler-side error surfacing, TUI-side
`WithWriterLock` adoption) become phase-A bullets.

**What changes in the proposal if this fails.** §4.9 Rule 2 should
add a parenthetical: "the multiHandler at `cmd/kitsoki/trace.go:428`
must be made panic-safe in phase A — today a child-handler panic
takes down the chain". Same for Rule 5: "phase A is the first place
that calls `WithWriterLock` from the TUI attach path; it is a
new contract for `kitsoki run`."

---

## Spike summary

| # | Status | Note |
|---|---|---|
| R1 | PASS | Applier returns typed Go map, NOT `json.RawMessage` — proposal §4.1 needs revision |
| R2 | PASS | RFC-6901 escaping for `#`, `|` is not needed (value side); only `/` and `~` need escaping if they ever appear as JSON-Pointer segments |
| R3 | PASS | Achievable only when journal INSERT shares the AppendEvents tx (which §4.9 Rule 1 already mandates) |
| R4 | PASS | Within a single process no observable interleaving; multi-attach left to a separate proposal |
| R5 | PASS | Confirmed: only tests call Snapshot |
| R6 | PASS | 11 mutation sites enumerated in continue-mode-silent-mutations.md; 7 JOURNAL-IT, 4 LEAVE-OUT |
| R7 | BLOCKED | Transcript projection is NOT total — rendered view text is not in any event payload. Phase A must add `view_rendered` to TurnEnded (or a new event kind) |
| R8 | PASS | All replay-relevant payloads carry full text; only the rendered view is missing (R7) |
| R9 | PASS | §4.9 rules implementable; three small code-task notes added |

The biggest single proposal revision driven by this spike: **§4.6 + R7 must produce the view text at original-turn time, not on resume.** The proposal's current §6.8 ("TUI renders [view text] on first frame, deterministically, against the restored world") is internally contradictory because view rendering can invoke host handlers.

The second revision: **§4.1 Applier returns typed Go map, not `json.RawMessage`.** Otherwise the schema coercion is undone by the very serialisation the interface signs up for.

Everything else holds.
