## Proposal — Continue mode (durable sessions via a unified trace journal)

**Status:** Draft v3. Spike (R1-R9) completed; call-site enumeration
landed. Phase A implementation has begun: the pure-Go
`internal/journal/` foundation is in `main` (27 tests green;
schema-aware applier, in-memory Writer/Reader, checkpoint policy).
SQLite-backed Writer/Reader, dual-write wiring, the `--continue`
flag, and the new CLI verbs are queued as Wave 2/3 work. v3 absorbs
the spike + call-site findings, which surfaced three design
contradictions in v2 that this draft resolves:

1. **View text must be journalled at original-turn time** (R7).
   v2's §4.6 "reconstruct from journal projection" was contradicted
   by §6.8's "render on first frame" wording — view re-rendering
   can invoke host handlers (forbidden by determinism). v3 mandates
   a `view.rendered` typed entry written at each turn end; resume
   reads it back verbatim. See §4.6 and §6.8.
2. **§4.9 Rule 1 carves an exception for in-memory-only typed
   entries.** Clarify, disambiguation, meta-mode ledger transitions,
   and inbox lifecycle have no `events` counterpart today
   (clarify-required outcomes explicitly *do not* persist events);
   the carve-out is that these typed entries write to the journal
   without a paired events row, still inside the session writer
   lock. See §4.9.
3. **Timeouts are confirmed out-of-tx, diagnostic-only.** `armTimeout`
   runs *after* `AppendEvents` returns; v3 affirms timeout entries
   are post-commit side effects (the `timeouts` table is still the
   source of truth for re-arming). See §2.1.

Plus four clarifications: the `Applier` interface returns a typed
Go map and consumers call `CoerceWorldVars` explicitly (R1; §4.1);
the §3.2 R3 benchmark target only holds *inside* an open
`AppendEvents` transaction; clarify entries grow an `origin:
foreground|background` field (§4.7); meta-mode ledger needs
explicit `metamode.proposal.staged|discarded` typed kinds because
the chat-metadata-only path is insufficient today (§4.7). The full
audit + spike summary lives in §A.

**Goal.** Make a `kitsoki run` session durable in the TUI: the user can
quit at any point and reattach later with `kitsoki run … --continue`
(or pick a session from a list) and pick up exactly where they left
off — same state path, same world, same transcript, same inbox, same
pending clarifications and bg-job awaits, same meta-mode session if
one was open.

**Mechanism.** The trace stream is promoted from a diagnostic side-car
into the canonical session journal. The journal is **hybrid**: some
entries are typed (host calls, clarification schemas, timeout arms —
they carry semantic payloads that a generic patch cannot reconstruct);
the rest are atomic JSON-Patch ([RFC 6902](https://datatracker.ietf.org/doc/html/rfc6902))
ops against a small set of named **physical** documents (`world`, the
single `state` pointer, `jobs/<id>`, `chats/<id>`). The kitsoki author
sees one log; replay knows how to handle both shapes. Periodic
full-document checkpoints bound replay cost.

**TL;DR.**

- **One log, two consumers.** The existing trace JSONL becomes a
  durable, append-only event journal. `--trace` still streams the
  same bytes; `--continue` replays them.
- **Resume is a pure replay.** No harness call, no host handler
  re-invocation, no LLM round-trip during `--continue`. The journal
  carries every byte the engine needs to reconstruct state: world
  patches, the literal LLM-returned text from previous turns, host
  return blobs verbatim, off-path answers verbatim. The first time
  the engine talks to claude after resume is when the user types
  their *next* turn — and that call uses claude's own
  `--resume <session_id>` mechanism (complementary durability;
  §4.8). Both R8 (§3.2) and the §6.9 determinism contract pin this.
- **Hybrid entries, not pure patches.** Mutations to typed-payload
  documents (host invocations, clarification schemas, timeout
  arms) keep their typed shape — replay needs the semantic payload
  to rebuild handler-local state. World/state/chat mutations
  become JSON-Patch ops on the named document they target. v1
  oversold "every mutation is a patch"; v2 is honest about the
  shape split (see §2.2).
- **Schema-aware patch applier (not optional).** `world.Vars` is
  `map[string]any` and round-trips numeric values as `float64`
  through `encoding/json`, breaking expr-lang guards like
  `world.x % 100`. The existing `coerceWorldVar` logic in
  `internal/store/replay.go:179` is the template; the journal's
  patch applier consults `app.WorldSchema` per write to restore
  declared types. Without this, a resumed session diverges from a
  non-resumed one. v1 spike A1 is now a §3.2 requirement.
- **Physical documents only.** v1 §2 listed `inbox` and
  `proposals/<id>` as separate documents. They aren't —
  `internal/inbox/inbox.go:51` declares `WorldKey = "$inbox"` and
  `internal/proposal/proposal.go:81` declares `WorldKey = "$proposal"`;
  both live inside `world.Vars`. v2 collapses them into the `world`
  document and tracks only the four physical stores that exist
  today (§2.1).
- **Checkpoints in-band.** Every N turns (default 20) the engine
  appends a `*.checkpoint` entry per live document carrying the
  full current value and the version it supersedes. Replay starts
  from the latest checkpoint per document, not from turn 0.
- **The existing event log stays.** `EffectApplied`,
  `TransitionApplied`, `HostInvoked`/`HostReturned` etc. carry
  semantic payloads that drive `BuildJourney` deterministically
  today. v2 does **not** retire them in phase B; instead, the
  journal *is* an append-only ledger of those same payloads in
  JSONL form, plus the JSON-Patch entries for world/state/chat
  documents. The `events` SQLite table becomes a query-side
  index over the journal (rebuildable on startup).
- **No clashes with the existing trace + SQLite store.** Phase A
  is the dangerous window: `events` and `journal` are both written
  per turn. The two writes happen inside the **same SQL transaction**
  ([§4.9](#49-coexistence-with-the-existing-trace--store)) so they
  succeed or fail together — never leaving a `journal` row whose
  corresponding `events` row is missing (or vice versa). The slog
  handler chain that today produces `--trace` JSONL is wrapped, not
  replaced, so the existing trace-pretty path and ring buffer keep
  working unchanged. JSONL write failures are downgraded to warnings
  (SQLite is the truth); SQLite write failures abort the turn (the
  user sees `EX_TEMPFAIL`).
- **`kitsoki run … --continue`** opens the TUI against an
  existing session — picked by `--id`, by `--key transport:thread`,
  or interactively from a session-picker UI when neither is given.
- **Single-attach in v1.** TUI `--continue` holds the session
  writer lock for the whole attach lifetime. A concurrent
  `kitsoki session continue` (external orchestrator) gets
  `EX_TEMPFAIL=75`. Multi-attach is owned by
  [`claude-code-sessions-proposal.md`](claude-code-sessions-proposal.md);
  this proposal is durability only (§5.3).
- **Critical assumptions need empirical confirmation.** See §3.

This proposal is orthogonal to the existing external-orchestrator
path (`kitsoki session continue`): that already resumes by external
key via the SQLite store. What’s missing is the TUI-side resume and
the unified journal that makes both paths read from the same source
of truth.

---

### 1. What’s missing today

The store layer already does most of what durability requires:

- An append-only `events` table keyed by `(session_id, turn, seq)` —
  [`internal/store/schema.sql`](../../internal/store/schema.sql).
- A `snapshots` table for periodic checkpoints (Turn, StatePath,
  WorldJSON, RNGSeed). The `RNGSeed` column exists but is unused
  today (nothing writes or consumes it; see §6.6).
- `Orchestrator.LoadJourney` reconstructs `(state, world)` by loading
  the latest snapshot and replaying events since
  ([`internal/orchestrator/orchestrator.go:1843`](../../internal/orchestrator/orchestrator.go)).
- A session writer lock and external-key index for cross-process
  coordination (`kitsoki session continue` already uses these).
- A separately-persisted `timeouts` table
  ([`internal/orchestrator/timeout.go:50`](../../internal/orchestrator/timeout.go))
  that survives restart already.

What’s missing or wrong today:

1. **The TUI never resumes.** [`cmd/kitsoki/main.go:295`](../../cmd/kitsoki/main.go)
   always calls `orch.NewSession(ctx)` and discards any prior
   session for the same app. Humans have no equivalent to
   `session continue`.
2. **Snapshots are never actually taken on a schedule.** The
   `Snapshot()` method exists at
   [`internal/store/sqlite.go:295`](../../internal/store/sqlite.go);
   only tests call it. A fresh run that crashes at turn 500 replays
   500 turns of events on the next `LoadJourney`. Today’s sessions
   are short enough that this is fine; chats / bg-jobs / meta-mode
   push them an order of magnitude longer.
3. **`events` and `trace` are two parallel views of the same activity.**
   `events` is structured and durable but opaque (binary-shaped JSON
   in SQLite, only meaningful to `BuildJourney`); the trace file is
   human-readable but throwaway. Resume reads `events`; debugging
   reads `trace`; they can drift silently. Today
   `EvMachineTransition` (trace) and `TransitionApplied` (events)
   live in two different taxonomies — different names, overlapping
   but not identical payloads.
4. **No first-class log for non-FSM mutable state.** Today:
   - Chat appends happen inside the chat-level lock at
     [`internal/host/oracle.go:213`](../../internal/host/oracle.go),
     not the session writer lock — so they’re ordered with respect
     to other chat writers but not with respect to FSM events.
   - Pending clarifications are kept **in-memory only** in
     [`internal/orchestrator/orchestrator.go:70-71`](../../internal/orchestrator/orchestrator.go),
     with an explicit comment that clarify-required outcomes are
     *not* persisted. A session that quits with a pending
     clarification cannot resume into `ContinueTurn` today.
   - The meta-mode `ProposalLedger`
     ([`internal/metamode/ledger.go:52`](../../internal/metamode/ledger.go))
     is reconstructed only from chat metadata via `ReloadPending`;
     pending/applied/discarded proposal state has no persistent
     row of its own.
   - Inbox-item lifecycle events `inbox.item.opened` and
     `inbox.item.dismissed` are declared in
     [`internal/trace/trace.go:119-120`](../../internal/trace/trace.go)
     but never emitted; the TUI calls `MarkNotificationRead`
     directly. Today these are silent state changes.
5. **The TUI transcript pane is not re-derivable from events.**
   [`internal/tui/transcript.go`](../../internal/tui/transcript.go)
   accumulates entries via `AppendTurn` etc. — there is no "render
   entire transcript from journal" code path. Naive resume opens
   the TUI with an empty transcript even when the world and state
   are correctly restored. This is the most visible gap and v2
   addresses it explicitly (§4.6).
6. **`session delete` is too narrow for what users expect "forget"
   to mean.** [`internal/store/sqlite.go:363`](../../internal/store/sqlite.go)
   removes only `events`, `snapshots`, `external_keys`,
   `session_locks`, `sessions`. It does not touch `timeouts`,
   `chats`, `chat_messages`, `chat_locks`, or `jobs` — those rows
   are orphaned.

The TUI-resume gap is the user-visible itch. The unified-journal gap
is what makes resume *cheap and consistent* — and unblocks several
adjacent features (`kitsoki replay --tui` against a recorded session,
flow tests that assert against a JSON-Patch sequence, time-travel
debugging).

---

### 2. Concepts

#### 2.1 The four physical documents

The journal tracks four documents — one row per real backing store.
v1 over-counted by listing logical concerns (`inbox`, `proposals`)
that don't have their own physical store.

| Document      | Shape                                | Physical backing today                                     |
|---------------|--------------------------------------|------------------------------------------------------------|
| `world`       | `{vars: {…}}` — includes `$inbox`, `$proposal`, `$jobs.*`, etc. | `events`/`snapshots` (rebuilt by `BuildJourney`)         |
| `state`       | `{path: "<state path>"}` — includes parallel-encoded form `root#leaf_a|leaf_b` | `events` (`TransitionApplied`) + `snapshots` |
| `chats/<id>`  | `{messages: [...], meta: {...}}`     | `chats` + `chat_messages` tables in `internal/chats/store.go` |
| `jobs/<id>`   | `{status, result, awaiting_input, schema, …}` | `jobs` table in `internal/jobs/store.go`                |

Two stores **stay out** of the journal because they're independent
sources of truth that already survive restart:

- **`timeouts`**
  ([`internal/orchestrator/timeout.go:50`](../../internal/orchestrator/timeout.go))
  — re-armed from the table on orchestrator construction
  (`rearmFromStore`, line 448). The journal does not rebuild this;
  it reads the table directly on resume. Adding `timeout.armed` /
  `timeout.cancelled` journal entries gives the trace a readable
  breadcrumb but is not the load-bearing source of truth.
- **`external_keys`, `session_locks`** — metadata for the session
  itself; rebuilt on demand. Not journalled.

Three further pieces of state are **TUI- or orchestrator-resident**
and need explicit handling (see §4.5–§4.7):

- **Pending clarification** (`o.pending` map,
  [`orchestrator.go:70`](../../internal/orchestrator/orchestrator.go))
  — must become a journalled entry; otherwise mid-clarify resume
  is impossible.
- **Meta-mode ledger** (`ProposalLedger`,
  [`internal/metamode/ledger.go:52`](../../internal/metamode/ledger.go))
  — must become journalled or pinned to chat-message metadata
  durably enough that `ReloadPending` is exact.
- **TUI transcript pane** — needs a *projection* of journal entries
  into render-ready turn rows. See §4.6.

#### 2.2 Journal entry shapes

Two shapes coexist:

**Patch entry** — for the four physical documents:

```json
{
  "ts": 1731519602.123,
  "session_id": "01HF…",
  "turn": 17,
  "seq": 3,
  "ev": "world.patch",
  "doc": "world",
  "doc_version": 18,
  "body": {
    "ops": [
      {"op": "replace", "path": "/vars/coat_in_cloakroom", "value": true}
    ]
  }
}
```

`world.patch`, `state.transition`, `chats.append`, `jobs.update`
are the four patch event kinds.

**Typed entry** — for events whose semantic payload cannot be
reconstructed from a patch sequence:

```json
{
  "ts": 1731519602.456,
  "session_id": "01HF…",
  "turn": 17,
  "seq": 4,
  "ev": "host.invoked",
  "doc": null,
  "body": {
    "namespace": "host.oracle.ask_with_mcp",
    "args": { … },
    "background": false
  }
}
```

Typed kinds in v1 of the journal format: `host.invoked`,
`host.dispatched`, `host.returned`, `validation.rejected`,
`guard.rejected`, `clarify.requested`, `clarify.answered`,
`disambig.presented`, `disambig.chosen`, `view.rendered`,
`offpath.question`, `offpath.answer`, `offpath.chat.resolved`,
`timeout.armed`, `timeout.cancelled`, `timeout.fired`,
`inbox.item.opened`, `inbox.item.dismissed`,
`metamode.proposal.staged`, `metamode.proposal.applied`,
`metamode.proposal.discarded`. Each is the journal equivalent of
an existing `EventKind` or `trace.Ev*` constant — or it is **new
in v3** because the spike found a silent mutation site that has
no event/trace counterpart today (those are: `disambig.*`,
`view.rendered`, `inbox.item.*`, `metamode.proposal.staged`,
`metamode.proposal.discarded`). The schema rules out making typed
kinds extensible from app YAML (the kitsoki engine owns the set).

The `view.rendered` kind is load-bearing for transcript rehydration
(§4.6): it carries the literal markdown text the TUI displayed at
the end of each turn. Without it, resume would have to re-render
the view template, which v3 forbids (§6.8 determinism contract).

#### 2.3 Versioning and checkpoints

Each patch document has a monotonic per-session version. Resume
rebuilds each document independently, so a stuck `chats/<id>` does
not block a clean `world` replay. A checkpoint entry carries
`ev: "<doc>.checkpoint"` and `body.full` (the entire current
document value) plus `doc_version` (the highest version it
supersedes). Replay starts from the latest checkpoint per document;
patches with `doc_version <= checkpoint.doc_version` are dropped.

Cadence: globally every 20 turns after `TurnEnded`. Per-document
overrides for high-churn docs — `chats/<id>` ticks once per 10
appended messages (v1 said 50, which is too coarse for oracle-ask
flows that append two messages per call). Per-document policy is a
constant table in the journal package; turning it into YAML config
is phase C.

A checkpoint is only emitted for documents that changed since the
last one. World in a state with no effects doesn't churn.

---

### 3. Spike (required before phase A)

v1 framed §0 as "validated assumptions"; the audit found several of
those rows were not assumptions to test but constraints to design
to. v2 rewrites the spike as a small set of mechanically-executable
experiments whose pass/fail gate is whether the proposal's
*requirements* (not its hopes) hold.

#### 3.1 What the spike produces

A single notes file at `docs/proposals/notes/continue-mode-spike.md`
with one heading per row below: commands run, captured fragments,
pass/fail, and a "what changes in the proposal if this fails" line.

#### 3.2 Spike requirements

| # | Requirement | How to verify | Failure response |
|---|---|---|---|
| R1 | Schema-aware patch applier preserves declared types across round-trip. Numeric world vars declared `type: int` must come back as `int64` after `json.Unmarshal` + JSON-Patch replace; `type: bool` must not pass through as `float64(1)`; `type: object` arrays must round-trip element-typed. **Reference:** `coerceWorldVar` at [`internal/store/replay.go:179`](../../internal/store/replay.go) is the existing template. | Build a small applier prototype that walks `app.WorldSchema` after every patch; round-trip every world fixture under `testdata/apps/*/flows/*.yaml` and assert deep-equal against the pre-trip value. | The patch payload format moves from RFC-6902 to a kitsoki-specific typed-diff. Layout stays the same; only the body of `world.patch` changes. |
| R2 | State-path representation survives. Compound paths (`a.b.c`) and parallel-encoded paths (`root#leaf_a|leaf_b`, see [`internal/machine/parallel.go:75`](../../internal/machine/parallel.go)) must round-trip through `{"op":"replace","path":"/path","value":"<encoded>"}` and re-parse cleanly via the existing `machine` parser. RFC 6901 escaping of `/`, `~`, `#`, `|` is checked. | For every state-path emitted in `testdata/apps/*/flows/*.yaml` runs, encode → patch → decode and assert equality. | Add a dedicated `state.transition` typed entry instead of a patch op; the field stays a string but is parsed at write/read time. |
| R3 | Adding the `journal` `INSERT` *inside an already-open `AppendEvents` transaction* adds <100µs per row at p95. **A standalone-transaction insert is fsync-dominated at 3–6ms; the dual-write target only holds when the journal piggybacks the existing turn-write tx** (§4.9 Rule 1). **Reference:** `BuildTraceLogger` at [`cmd/kitsoki/trace.go`](../../cmd/kitsoki/trace.go) and the ring buffer at [`internal/trace/ringbuffer.go`](../../internal/trace/ringbuffer.go). | Benchmark inside an open transaction on the dev SQLite file with WAL + busy-timeout configured as today. **Spike outcome (R3): measured 17µs p95 inside an open tx; standalone 3–6ms.** | If transaction-piggyback is not feasible at any site: drop the SQLite mirror in v1; the JSONL file alone is authoritative; phase B builds the `journal` SQL table. |
| R4 | Concurrency: chat appends today bypass the session writer lock (they hold a chat-level lock at [`internal/chats/lock.go:47`](../../internal/chats/lock.go)). Decide whether journal writes for chat appends acquire the session writer lock too (slow path) or whether the journal accepts cross-lock interleaving (fast path) with `(turn, seq)` ordering compromised. | Stress test: drive a session through a `host.oracle.ask_with_mcp` call (chat append) interleaved with a foreground turn. Inspect the resulting journal for ordering anomalies. | If cross-lock interleaving is observable: introduce a per-document write epoch — `(doc_version)` is still total order *per document*, even if `(turn, seq)` is not total order *globally*. Resume reads doc-by-doc anyway, so this is acceptable. Document it explicitly. |
| R5 | Snapshot machinery is not on a schedule today. **Confirmed by audit** — `grep -rn '\.Snapshot(' internal/orchestrator/` returns only test callers. The journal-checkpoint path is the natural place to add scheduling. | n/a — confirmed by grep. | n/a. |
| R6 | Enumerate every silent mutation site (state change that does not currently emit either a typed `EventKind` or a `trace.Ev*`). Audit found at least: `MarkNotificationRead` ([`internal/tui/tui.go:556`](../../internal/tui/tui.go)), `metamode.ProposalLedger` updates ([`internal/metamode/ledger.go`](../../internal/metamode/ledger.go)) outside chat-metadata writes, `o.pending` writes ([`orchestrator.go:528`](../../internal/orchestrator/orchestrator.go)). Decide for each: (a) journal it, (b) leave outside journal and document why. | A small file `docs/proposals/notes/continue-mode-silent-mutations.md` listing every mutation site and decision. | Each "leave outside journal" decision adds a footnote to §2 explaining what resume does *not* restore. |
| R7 | **Spike outcome (R7): NOT total via projection from events alone.** The TUI's view text exists only transiently in `TurnResult.View`; no current event payload carries it. Naive projection cannot reconstruct what the user actually saw on the previous turn. **v3 resolution:** journal the rendered view text per turn as a `view.rendered` typed entry (see §2.2, §4.6). This makes transcript rehydration a pure read — no view-template re-evaluation, no determinism breach. **Reference:** [`internal/tui/tui.go:99`](../../internal/tui/tui.go) for what RootModel actually holds; [`internal/tui/transcript.go`](../../internal/tui/transcript.go) for the transcript's accumulator. | Confirmed by spike — see `docs/proposals/notes/continue-mode-spike.md` §R7. | n/a (resolved in v3). |
| R8 | **Determinism: resume never calls the LLM.** Every payload kind that today carries LLM output (`LLMToolCall` body, `host.oracle.ask_with_mcp` return, `OffPathAnswer`, chat-message append text) must already include the *literal* response bytes — no pointer-style "resolve on read" lookups, no recall fallback. Confirm by reading each event-emission site and asserting the payload is text-complete. | Walk every `EventKind` in [`internal/store/event.go:17`](../../internal/store/event.go) and every typed journal kind in §2.2; for each, identify the field carrying the LLM output and confirm it is the full text. Cross-check `internal/store/replay.go` to see what payload `BuildJourney` already reads. | If any payload is a pointer/handle: phase A adds the missing text fields to the event/journal payload before the schema-aware applier ships. |
| R9 | **Trace + SQLite coexistence under failure.** Inject a SQLite write failure mid-turn (e.g. `pragma journal_mode=DELETE` followed by a forced lock) and confirm the journal write either commits with the rest of the turn or aborts the turn cleanly — never leaves a half-written turn. Inject a JSONL write failure (disk full / EROFS) and confirm the SQLite write still commits and the TUI surfaces a warning but does not abort. Inject a slog handler panic and confirm the `--trace-pretty` path keeps writing. | Three small failure-injection tests under `internal/journal/coexistence_test.go`. | If atomicity is not preserved: §4.9 must mandate a single SQL transaction wrapping both `INSERT INTO events` and `INSERT INTO journal`; the slog handler must be made panic-safe (recover + downgrade to ERROR). |

---

### 4. Implementation

#### 4.1 New package — `internal/journal`

Owns the journal format, document registry, schema-aware patch
applier, checkpoint policy, and replay. Sketch:

```go
package journal

type DocID string                 // "world", "state", "chats/01HF…", "jobs/01HF…"
type Version int64                // monotonic per session+doc

type Entry struct {
    Ts         time.Time
    Session    app.SessionID
    Turn       app.TurnNumber
    Seq        int
    Kind       string             // "world.patch", "state.transition", "host.invoked", …
    Doc        DocID              // empty for typed-only entries
    DocVersion Version             // 0 for typed-only entries
    Body       json.RawMessage    // shape-typed by Kind
}

type Writer interface {
    Append(Entry) error
    AppendCheckpoint(DocID, json.RawMessage) error
    Flush() error
}

type Reader interface {
    LoadDocument(sid app.SessionID, doc DocID) (current json.RawMessage, version Version, err error)
    ReplayFrom(sid app.SessionID, doc DocID, from Version) iter.Seq[Entry]
    ReplayTyped(sid app.SessionID) iter.Seq[Entry]  // typed entries in (turn,seq) order
}

// Applier consults app.WorldSchema (or per-doc schema) before applying
// each op, restoring declared types after json.Unmarshal coerces
// numerics to float64. Mirrors `coerceWorldVar` semantics.
//
// Apply returns json.RawMessage (the post-patch bytes); consumers
// who need a typed Go value call CoerceWorldVars (or its per-doc
// equivalent) after json.Unmarshal. This is an explicit two-step
// pattern, not an implicit one — JSON has no int64 vs float64
// distinction, so coercion cannot survive a Marshal→Unmarshal
// round-trip if it lived inside Apply's output bytes.
type Applier interface {
    Apply(doc DocID, current json.RawMessage, ops []PatchOp) (json.RawMessage, error)
}

// CoerceWorldVars walks a freshly-unmarshalled world.Vars map and
// restores each value to its app.WorldSchema-declared Go type.
// Engine-injected keys ($inbox, $proposal, last_error, $jobs.*) are
// passed through unchanged.
func CoerceWorldVars(vars map[string]any, schema app.WorldSchema) error
```

**API contract (canonical apply pattern):**

```go
postBytes, err := applier.Apply(journal.DocWorld, currentBytes, ops)
// ...
var vars map[string]any
_ = json.Unmarshal(postBytes, &vars)
_ = journal.CoerceWorldVars(vars, def.WorldSchema)  // <-- explicit, required
```

The phase-A implementation already ships this shape; see
[`internal/journal/applier.go`](../../internal/journal/applier.go).

JSON-Patch library choice: **commit to RFC 6902** (operations list,
not the merge-patch RFC 7396). v1 conflated the two in the spike
table. `go.mod` already pulls in `tidwall/sjson` and `gjson` as
indirects via huh/cobra; an experiment in R1 confirms whether
sjson's path edits are sufficient (likely yes for our op set:
`replace`, `add`, `remove`, `test`) or whether we adopt
`evanphx/json-patch` for a faithful RFC 6902 implementation. Default
recommendation: `evanphx/json-patch` for correctness, unless the
spike measures meaningful binary-size cost.

#### 4.2 Storage

```sql
CREATE TABLE journal (
    session_id   TEXT    NOT NULL,
    turn         INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    ts           INTEGER NOT NULL,
    kind         TEXT    NOT NULL,
    doc          TEXT,                -- nullable for typed-only entries
    doc_version  INTEGER,             -- nullable for typed-only entries
    body_json    TEXT    NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;

CREATE INDEX journal_doc_idx ON journal (session_id, doc, doc_version);
```

One table, indexed by `(session_id, doc, doc_version)` so a resume
loads each document with one range scan. The on-disk JSONL is a
mirror, written by the same code path when `--trace` is set. The
SQLite table is authoritative; the JSONL is a tail.

#### 4.3 Migration from `events` / `snapshots`

v1 promised that `events` and `snapshots` become projections of the
journal. The audit found this is **only partially true**:
`EffectApplied` payloads carry typed `{set, increment, say}`
semantics that the JSON-Patch ops on `world` don't preserve; host
events carry arg/result blobs not present in any patch chain.

v2's migration is therefore:

| Step | Action | Reversible? |
|------|--------|-------------|
| 1 | Land `journal` table + writer. Dual-write: at every existing `store.AppendEvents` call site ([`internal/store/sqlite.go:156`](../../internal/store/sqlite.go), [`internal/orchestrator/orchestrator.go`](../../internal/orchestrator/orchestrator.go), [`internal/orchestrator/oncomplete.go`](../../internal/orchestrator/oncomplete.go), [`internal/orchestrator/offpath.go:211`](../../internal/orchestrator/offpath.go), [`internal/orchestrator/timeout.go`](../../internal/orchestrator/timeout.go)) emit the equivalent journal entry. World/state mutations emit a patch entry computed from the pre/post snapshot; host events emit a typed entry carrying the same fields the `EventKind` payload already does. | Yes — drop the journal table |
| 2 | Land journal reader + schema-aware applier + replay. Use it only behind a `KITSOKI_RESUME_FROM_JOURNAL=1` env-var for testing. | Yes |
| 3 | Add the `--continue` TUI flag. Defaults to journal-based replay. The pre-existing `LoadJourney` path is unchanged. | Yes |
| 4 | Flip the default: `LoadJourney` itself reads from the journal for world/state. The `events` table is rebuilt as a query-side index on session open (an indexer reads the journal and populates `events` if it's empty/stale). Host events stay in `events` because their payload shape doesn't change; they're double-recorded (journal as typed entry + events row) during phase A and converge to "journal is truth, events is index" in phase B. | Yes — flag flip |
| 5 | Retire the in-orchestrator direct writes to `snapshots`; checkpoints become journal entries; `LatestSnapshot` is rebuilt from the latest `world.checkpoint` + `state.checkpoint` on demand. | One-way after this point |

Phase B's exit criterion is *not* "delete the `events` table" — it's
"journal is the only mutation log written by application code; the
`events` and `snapshots` tables are indices/projections rebuildable
from the journal." Keeping the tables avoids breaking mode-1
adversarial cassettes that assert on the `EventKind` taxonomy
([`internal/store/event.go:14`](../../internal/store/event.go) notes
this is a breaking-change surface). v2 explicitly chooses
compatibility over taxonomic purity.

#### 4.4 Checkpoint policy

- Globally: every 20 turns per session, after `TurnEnded` lands.
- Per-document overrides:
  - `chats/<id>`: one checkpoint per 10 appended messages
    (oracle-ask flows append two messages per call).
  - `jobs/<id>`: checkpoint on every status transition (these are
    rare and tiny).
  - `world`: 20 turns, unchanged.
  - `state`: 20 turns, unchanged.
- A checkpoint is only emitted for documents that changed since
  the last one.
- An explicit `kitsoki session checkpoint --id …` admin command for
  test fixtures and bisecting.

Realistic sizing on `dev-story` (a heavy in-tree app): ~5–15
mutations per turn across world + 1–2 chats. At 20 turns × 15 ops
that's 300 patches between checkpoints; well inside what RFC 6902
applies in milliseconds. Oregon-trail-scale sessions (200 turns)
generate ~3k patches without checkpoints — hence the 20-turn cadence.

#### 4.5 Resume path in detail

```
LoadSessionFromJournal(sid) -> *resumeBundle {
    // 1. Patch documents.
    docs := journal.ListLiveDocs(sid)
    for each doc in docs:
        snap, version := journal.LatestCheckpoint(sid, doc)
        if snap is nil: current[doc] = doc.zero_value; version = 0
        else: current[doc] = snap.body.full
        for entry in journal.ReplayFrom(sid, doc, version + 1):
            current[doc] = applier.Apply(doc, current[doc], entry.body.ops)

    // 2. Typed entries — replay in (turn,seq) order for side effects
    // that rebuild handler-local state.
    for entry in journal.ReplayTyped(sid):
        switch entry.Kind {
        case "clarify.requested": pending[sid] = decodePending(entry.body)
        case "clarify.answered":  delete(pending, sid)
        case "metamode.proposal.staged": ledger.stage(...)
        case "metamode.proposal.applied": ledger.markApplied(...)
        // host.invoked/returned: pure diagnostic on resume; world.patch
        // already restored their side effects.
        }

    // 3. Timeouts come from their own table, not the journal.
    timeouts := timeout.RearmFromStore(sid)

    return assemble(current, pending, ledger, timeouts)
}
```

`assemble` produces a `resumeBundle` consumed by `orch.AttachSession(sid)`:
a new orchestrator method that replaces `NewSession(ctx)` for the
continue path. It seeds the orchestrator's in-memory caches
(`o.pending`, the metamode controller's `Session`) and hands the
TUI everything `NewSession` would have, plus a transcript projection
(§4.6).

**Hard determinism contract for `LoadSessionFromJournal`:**

- No `harness.Harness` is constructed during replay.
- No `host.Registry` handler is dispatched during replay.
- No `transport.Registry` is consulted during replay (transports are
  *output* surfaces, not durable state; they're rebuilt from
  `external_keys` on first send).
- No `machine.RenderState` call happens during replay — view text
  comes from the journalled `view.rendered` typed entry written at
  original-turn time (§4.6). The TUI's first frame uses the
  journalled text verbatim; the view template is not re-evaluated.
  This is non-negotiable: re-rendering can invoke host handlers via
  template expressions, which would breach this contract.
- No `time.Now()` reads inside the apply loop — patches carry their
  own `Ts`; the replay clock is the journal's clock.
- No network I/O. Period.

These constraints are enforced by construction: `journal.Reader`
and `journal.Applier` are pure-data types with no `Harness` /
`Registry` / `http.Client` fields in their constructor signatures.
A code-review checklist item; later a linter could enforce it.

#### 4.6 Transcript rehydration

This is the most visible piece of state v1 silently ignored. The
TUI's transcript pane
([`internal/tui/transcript.go`](../../internal/tui/transcript.go))
is an append-only accumulator fed by `AppendTurn` /
`AppendOffPathQuestion` / `AppendOffPathAnswer` / etc. There is no
existing "render entire transcript from world+events" function.

**v3 design (spike R7 result):**

The spike confirmed that the live `TurnResult.View` text is the
*only* place the rendered view exists today — no event payload
carries it. A projection-from-events approach (v2's option 2)
cannot reconstruct what the user actually saw without re-evaluating
the view template, which can invoke host handlers and would
violate the §6.8 determinism contract.

v3 therefore mandates **journalling the rendered text per turn**:

- At the existing `TurnEnded` emission point in `orchestrator.go`,
  *also* write a `view.rendered` typed journal entry whose body is
  `{view_text: "<markdown>", state_path: "<path>"}`.
- At resume, the transcript pane reads the latest N
  `view.rendered` entries plus any interleaved `chats.append` /
  `offpath.answer` / `disambig.presented` entries and reconstructs
  the rows in `(turn, seq)` order using `transcriptModel`'s
  existing constructors.
- No view-template evaluation happens during resume. The TUI's
  first-frame render uses the journalled text verbatim; the
  *next* turn (driven by user input) is the first time the engine
  re-evaluates anything.

**Determinism note:** every transcript row at resume time comes
from journal text written at original-turn time. If a view template
embeds `time.Now()` or any other non-determinism, the resumed
transcript shows the original value — not a fresh one — because
the journal carries the value, not the template.

**Size cost:** at ~2 KB per state with a non-trivial view, 200 turns
of an oregon-trail-grade session adds ~400 KB. Acceptable. Phase C
can compress old `view.rendered` entries if needed; v3 ships
uncompressed.

**Engine-level work this implies (phase A scope):**

- Emit `view.rendered` at every `TurnEnded` site.
- Emit `disambig.presented` / `disambig.chosen` at the existing
  disambiguation flow (currently slog-only; see
  [`internal/orchestrator/disambig.go`](../../internal/orchestrator/) —
  spike R6 enumerated the exact sites).
- Emit `inbox.item.opened` / `inbox.item.dismissed` at the
  `MarkNotificationRead` call sites in `internal/tui/tui.go`
  (currently silent).

#### 4.7 Pending clarification + meta-mode ledger

Pending clarify state today lives in `o.pending` (in-memory). v3
adds two typed journal kinds:

- `clarify.requested` — body: `{origin: "foreground"|"background",
  intent, slots_so_far, slots_needed, schema, job_id?}`. Emitted in
  `orchestrator.go` at the existing `ModeClarify` write site
  (line 528) for foreground; emitted by `jobs.RequestClarification`
  ([`internal/jobs/clarification.go`](../../internal/jobs/clarification.go))
  for background. The `origin` field is load-bearing: resume routes
  foreground clarifies into `o.pending` and background clarifies
  into the job's awaiting-input state. Same typed kind, two
  consumers — `origin` is the discriminator.
- `clarify.answered` — body: `{origin, intent, slots_final, job_id?}`.
  Emitted at the existing `ContinueTurn` clear site (foreground)
  and at `jobs.AnswerClarification` (background).

Resume replays the typed-entry stream; the most recent unmatched
`clarify.requested` per `(origin, job_id)` rehydrates the
corresponding pending state.

**§4.9 Rule 1 carve-out applies here.** Today's foreground clarify
path explicitly does *not* persist an `events` row
([`orchestrator.go:519`](../../internal/orchestrator/orchestrator.go)
comment "Do NOT persist events for clarify-required outcomes"). The
journal write is therefore an in-memory-only typed entry; it lives
inside the session writer lock but has no paired `events` insert.
See §4.9 Rule 1 (exception list).

Meta-mode `ProposalLedger` today is rebuilt from chat-message
metadata via `ReloadPending`. The spike (R6) found that the chat
metadata path is **not** exhaustive — ledger drafts (the `staged`
edge from the in-memory `ProposalLedger.Stage`) and discards (the
`discarded` edge) do not write chat-message rows today. That makes
chat-metadata-as-ledger insufficient for resume.

v3 therefore adds three typed journal kinds (already in §2.2):

- `metamode.proposal.staged` — body: `{proposal_id, kind,
  rendered_excerpt, mode, agent}`. Emitted at the ledger `Stage` call
  in [`internal/metamode/ledger.go`](../../internal/metamode/ledger.go).
- `metamode.proposal.applied` — body: `{proposal_id, applied_at,
  result_summary}`. Emitted at the existing `Apply` site (which
  also already writes a chat message; the journal entry is the
  one we replay on resume).
- `metamode.proposal.discarded` — body: `{proposal_id,
  discarded_at, reason}`. Emitted at the `Discard` site.

Resume replays the typed-entry stream and rebuilds the
`ProposalLedger` directly from the journal — chat-metadata
fallback becomes a secondary source for backward compatibility
with sessions started before phase A landed. New sessions use the
journal as the authoritative ledger source.

Like clarify, these typed entries fall under the §4.9 Rule 1
carve-out: no paired `events` row, but inside the session writer
lock.

#### 4.8 Relationship to claude's own session persistence

The kitsoki journal is **complementary** to claude's session
persistence, not a replacement for it. Both layers carry context
for different reasons.

| Layer                       | Owns                                                                                  | Lives in                                                       |
|-----------------------------|---------------------------------------------------------------------------------------|----------------------------------------------------------------|
| kitsoki journal             | State machine state, world vars, chat-message text, host call results, FSM events    | `journal` SQLite table + optional `<sid>.jsonl` mirror         |
| claude session              | LLM-side conversation context (system prompt history, tool-use trail, model state)   | `~/.claude/projects/<workspace>/<claude_session_id>.jsonl`     |

The bridge is the `claude_session_id` column already on
[`chats`](../../internal/chats/schema.sql) ([`internal/chats/doc.go`](../../internal/chats/doc.go)).
It's recorded in the journal as part of `chats/<id>` document state.

On **resume**, kitsoki touches only its own journal — claude is not
invoked. The chat history reconstituted from `chats.append` typed
entries is sufficient for the TUI to render the conversation; the
LLM is not consulted.

On the **first turn after resume**, when the user types into the
chat, kitsoki calls `claude -p --resume <claude_session_id>` exactly
as it does today
([`internal/host/oracle_ask_with_mcp.go`](../../internal/host/oracle_ask_with_mcp.go)).
Claude reads its own JSONL session file and the conversation context
is whole — even though the kitsoki process restarted between turns.

Two consequences:

1. **Resume is cheap even for long conversations.** The journal
   doesn't have to encode the full LLM context window; claude's
   session file already does that. The journal records only the
   text kitsoki *needs* to render and to drive the state machine.
2. **Journal `chats/<id>` and claude's session file can diverge
   without breaking either.** If claude's session file is deleted
   between runs, the journal still resumes the kitsoki side
   correctly; the next `--resume` against claude fails and we fall
   back to starting a new claude session (same behaviour as today
   for a "chat with lost claude session"). Document this in the
   error UX (§6.2).

#### 4.8a Recording-format relationship

`kitsoki replay --recording rec.yaml` (existing recording format) is
**not** touched. The two stay separate: recordings are LLM-call
replay fixtures used by tests; the journal is session-state
durability used by humans resuming TUI sessions.

#### 4.9 Coexistence with the existing trace + store

The proposal introduces a new SQLite table (`journal`), a new
JSONL mirror, and a new slog handler chain. All three must coexist
with three pieces of pre-existing machinery without creating drift
bugs:

1. The current `events` / `snapshots` tables (still written in
   phase A, projected from journal in phase B).
2. The current `--trace` / `--trace-pretty` JSONL outputs and the
   in-memory ring buffer in
   [`internal/trace/ringbuffer.go`](../../internal/trace/ringbuffer.go).
3. The session writer lock in
   [`store.WithWriterLock`](../../internal/store/sqlite.go), already
   used by both `kitsoki run` and `kitsoki session continue`.

The coexistence rules:

**Rule 1 — Single SQL transaction per turn write, with a narrow
exception for in-memory-only typed entries.**

The default: every write that today calls `store.AppendEvents` is
wrapped in a transaction that *also* inserts the corresponding
`journal` rows. Either both succeed or both fail; there is no
partial-write window where `events` has the turn but `journal`
doesn't (or vice versa).
[`internal/store/sqlite.go:156`](../../internal/store/sqlite.go) is
the single mutation point in phase A. A failure injection test
(spike R9) enforces this property.

```go
// Phase A wiring inside store.AppendEvents (sketch).
func (s *sqliteStore) AppendEventsAndJournal(sid, events, journal) error {
    tx, err := s.db.Begin()
    if err != nil { return err }
    defer tx.Rollback()
    if err := appendEventsTx(tx, sid, events); err != nil { return err }
    if err := appendJournalTx(tx, sid, journal); err != nil { return err }
    return tx.Commit()
}
```

**Exception — in-memory-only typed entries.** A small set of typed
kinds describe state changes that today are *intentionally not
persisted as events*. The call-site enumeration
([`docs/proposals/notes/continue-mode-call-sites.md`](notes/continue-mode-call-sites.md))
identified these as standalone-tx sites:

- `clarify.requested` / `clarify.answered` — `orchestrator.go:519`
  explicitly does not write an events row.
- `disambig.presented` / `disambig.chosen` — slog-only today; no
  event kind exists for them.
- `metamode.proposal.staged` / `metamode.proposal.discarded` —
  ledger state changes that don't write chat-message rows today.
- `inbox.item.opened` / `inbox.item.dismissed` — silent today.

For these kinds the rule is: **inside the session writer lock; own
SQL transaction; no paired events insert.** They still survive a
crash because the transaction is committed before the operation
returns; they still serialise correctly because the writer lock
encloses them. They simply don't have an events-row partner, and
that's accepted as a design property — not a bug to fix in phase A.

Phase B (when host events become projections) revisits the carve-out:
if those kinds gain `EventKind` constants for any other reason
(e.g. to feed mode-1 adversarial cassettes), the carve-out
collapses naturally. v3 does not force that change.

**Exception — post-commit side effects.** A third class of entry
fires *after* `AppendEvents` returns: `timeout.armed`,
`timeout.cancelled`, `timeout.fired`. These are diagnostic
mirrors of the `timeouts` table (which remains the source of
truth for re-arming on restart, per §2.1). They run in their own
transactions, post-commit, and are accepted as such. Resume reads
the `timeouts` table directly, not the journal, for these.

**Rule 2 — slog handler is wrapped, not replaced.** Today
[`cmd/kitsoki/main.go:228`](../../cmd/kitsoki/main.go) installs a
trace logger as the slog default for the duration of `kitsoki run`.
The journal writer is added as an *additional* handler in the same
chain (multi-handler slog wrapper), not as a replacement. Consequences:

- `--trace` / `--trace-pretty` files keep working unchanged.
- The ring buffer keeps receiving every event.
- Existing call sites that use `slog.DebugContext(ctx, "turn.start", …)`
  do not need to change — the journal handler intercepts the
  structured fields it cares about and writes the matching journal
  row.
- A panic in the journal handler (recovered) demotes to an ERROR
  log on the *other* handlers; the turn still completes. R9 covers
  this case.

> **Phase-A code task.** The existing `multiHandler` at
> [`cmd/kitsoki/trace.go:428`](../../cmd/kitsoki/trace.go) is
> **not panic-safe today** — a panic in any sub-handler propagates
> and kills the calling slog call. The spike (R9) confirmed this
> is fixable cheaply: wrap each sub-handler `Handle` invocation in
> a `defer recover()` that logs the panic at ERROR level on the
> surviving handlers. Phase A includes this as a small dedicated
> change before the journal handler is added to the chain.

**Rule 3 — JSONL is a tail, not a source of truth.** The on-disk
`<sid>.jsonl` mirror is best-effort. JSONL write failures (disk
full, EROFS, permissions) are logged at WARN level and surface as a
TUI banner ("journal mirror lost: …") but **do not abort the turn**.
The SQLite `journal` table is the source of truth; the JSONL is a
human-readable convenience.

This asymmetry is deliberate: SQLite has ACID guarantees we depend
on; a JSONL append has no atomicity story across crashes mid-line.
Treating SQLite as truth means we never have to reconcile two
divergent serializations of the same event.

**Rule 4 — Schema migration is `IF NOT EXISTS`-safe.** Adding the
`journal` table follows the existing pattern in
[`internal/store/schema.sql`](../../internal/store/schema.sql)
(idempotent DDL run on every `store.Open`). No migration framework
is introduced; the embedded DDL is the authoritative schema.
Existing sessions opened against the upgraded binary see an empty
`journal` table; their `events` rows still drive `BuildJourney`
unchanged. New sessions opened by the upgraded binary populate both
tables in lockstep.

**Rule 5 — Writer lock encloses the dual write.** The session
writer lock in `WithWriterLock` already wraps the turn-write
critical section. The journal write happens *inside* that lock, in
the same goroutine, before the SQL transaction commits. No new
locking primitive is introduced.

**Rule 6 — Concurrent readers use SQLite WAL mode.** The existing
schema runs in WAL mode (set by `Open()`); readers (`kitsoki session
journal --from N`, `kitsoki inspect`) get a consistent snapshot
without blocking the writer. The JSONL mirror is not WAL-protected;
readers that want crash-consistent journal data should read SQLite,
not the JSONL.

**Rule 7 — Phase B's projection rebuild is one-way.** When `events`
is rebuilt as a projection of `journal`, the rebuild is triggered
*only* if `events` is empty *and* `journal` is non-empty for that
session. The reverse direction never runs (a stale `journal` is
never rebuilt from `events`). This prevents a partial phase-B
rollback from accidentally overwriting fresh journal data with
older event-table state.

**What this gives us in practice.** A user upgrading from a
pre-v1 binary to a phase-A binary sees no behaviour change on
existing sessions (the `journal` table is empty for them; reads
fall through to `events`). A user starting a new session under
phase A has both tables populated atomically. A user upgrading from
phase A to phase B sees `LoadJourney` switch to journal-backed
reads; `events` is still present as an index. A user downgrading
phase B → phase A loses nothing (rule 7).

---

### 5. The `--continue` UX

#### 5.1 Flag form

```
kitsoki run testdata/apps/cloak/app.yaml --continue
kitsoki run app.yaml --continue --id 01HFAB…
kitsoki run app.yaml --continue --key jira:PLTFRM-12345
kitsoki run app.yaml --no-implicit-resume
```

- **No selector** → present a picker (§5.3) listing sessions for
  this `app_id`, newest first, with state/turn/last-active columns.
  The picker reuses the column layout of `kitsoki session list`.
- **`--id`** → resume by session id, ignoring app filter (caller
  asserts the app matches).
- **`--key`** → resume by external key, same lookup
  `session continue` uses.
- **`--no-implicit-resume`** → force a fresh session even if one is
  active (§5.2 prompt suppressed).

App-id mismatch (the on-disk session was for a different app) is a
hard error: the YAML alphabet may not be compatible. Same-app
version drift is handled by replay — patches against missing world
keys are reported and the user is shown a recoverable error overlay
(§6.2).

#### 5.2 Implicit resume

If the user runs `kitsoki run app.yaml` without `--continue` and the
store contains *exactly one* active session for this app, the TUI
prints a one-line prompt:

```
You have an active session for cloak from 2 hours ago, turn 12 (in cloakroom).
[Enter] to continue · [n] start fresh · [q] quit
```

Default is continue. This is the path most TUI users will actually
hit — explicit `--continue` is for scripting and for users with
multiple active sessions.

#### 5.3 Multi-attach is single-attach in v1

The TUI `--continue` path acquires the session writer lock for the
**entire attach lifetime**, not just for one turn. A concurrent
`kitsoki session continue` (external orchestrator via loop.py or a
webhook) gets `EX_TEMPFAIL=75` and is expected to back off.

This is a deliberate v1 scope cut: properly multi-attached sessions
(TUI + Jira simultaneously) are the cross-transport-drives story
owned by
[`claude-code-sessions-proposal.md`](claude-code-sessions-proposal.md);
v1 only ships single-attach durability. The user-visible UX is the
"the session is already attached elsewhere" message:

```
$ kitsoki run app.yaml --continue --id 01HF…
session busy: another process (kitsoki run @ host laptop, PID 14823)
holds the writer lock for 01HF…
Either close that attached session or run:
    kitsoki session detach --id 01HF…
to break a stale lock.
```

A `kitsoki session detach --id` admin command reaps a stale writer
lock (matching the `chat unlock` pattern). Stale-lock reaping
already happens on `WithWriterLock` acquire (cross-host: refuse;
same-host with dead PID: reap and proceed).

The R4 spike outcome decides whether chat-host calls inside the
attached session need to take a chat lock that the long-lived
session writer lock blocks. Tentative: chat appends already use a
separate `chat_locks` row and can proceed regardless of the session
writer lock. Confirm.

#### 5.4 Session picker

A small Bubble Tea overlay built from the same data
`kitsoki session list` already produces. Reachable two ways:
(a) `kitsoki run --continue` without a selector, (b) a new Esc-menu
item "sessions…" inside the running TUI. Selecting a row triggers
an in-process restart against the chosen session.

#### 5.5 Status pre-resume

```
$ kitsoki run app.yaml --continue --id 01HF…
Resuming 01HF… (cloak, turn 12, last active 2h ago)
  • replaying 2 patch documents from checkpoints at turn 0
  • 47 patches applied
  • 3 typed entries replayed (1 pending clarify rehydrated)
  • transcript projection: 12 turns reconstructed
  • state: cloakroom · world: 7 vars · 1 chat · 0 inbox items · 0 timeouts armed
[TUI opens]
```

Resume cost is visible by default. A `--quiet` flag suppresses the
header.

---

### 6. Hard edges

#### 6.1 App-version drift on resume

A session journal can outlive an app edit. Cases:

1. **Compatible edit** — view templates, prompts, intent examples
   change; world schema and state graph are stable. Resume succeeds;
   the resumed view is rendered against the new templates.
2. **Additive world var** — a new var appears with a schema default.
   Resume treats the missing var in the journal as `default` and
   writes a synthetic `world.patch {op: add, path: /vars/<new>,
   value: <default>}` at the first replayed turn after the version
   bump. The schema-aware applier (R1) makes this trivial.
3. **Removed world var** — a var the journal sets is no longer in
   the schema. Resume warns. v1 §5.1 noted the engine tolerates
   orphan keys; v2 keeps the orphan in `world.Vars` and re-emits
   a deprecation warning in the trace.
4. **Engine-injected vars** (`$inbox`, `$proposal`, `last_error`,
   `$jobs.*`) are not in `app.WorldSchema`. The applier short-circuits
   for these: they round-trip via plain JSON without schema-aware
   coercion. The applier maintains an internal "engine-injected
   keys" list that mirrors the registrations in
   [`internal/inbox/inbox.go:51`](../../internal/inbox/inbox.go) and
   [`internal/proposal/proposal.go:81`](../../internal/proposal/proposal.go).
5. **Renamed state path** — the resumed state path no longer exists.
   This is a hard error; the user is given the option to (a) abandon
   the session, (b) teleport to a named recovery state declared in
   app.yaml (`recovery_state:` — a new optional top-level key,
   §6.7), or (c) drop to the off-path runtime
   ([`../meta-mode.md`](../stories/meta-mode.md)) for manual rescue.
6. **Intent alphabet shrunk** — a transition the journal applied is
   no longer declared. Diagnostic warning at resume time; it does
   not block resume, because the journal’s state record is
   authoritative for *current* state.

`app.AppDef` already carries a version. Resume records the running
version and writes a `journal.appversion` entry if it differs from
the last one in the log — gives the trace a visible cut point for
the "before/after" of an app edit.

#### 6.2 Resume errors

Hard errors (refuse to open the TUI, print to stderr, exit 1):

- App-id mismatch.
- Journal corruption (a patch body fails to parse; chain unrecoverable
  past that point).
- State-path renamed and no `recovery_state:` declared.

Soft errors (open the TUI with a banner; user can act):

- Removed world var.
- Out-of-alphabet historical transition.
- Stale background job whose runtime is no longer registered.

The banner reuses the inbox-notification surface so the user sees it
immediately on the TUI’s first frame.

#### 6.3 Background jobs at resume

A session with an in-flight bg job suspended at resume is the
trickiest case. The journal records `job.submitted` /
`job.terminal` / `job.awaiting_input` already; on resume the
orchestrator scans for any job in `submitted` or `awaiting_input`
state and:

- For `submitted` jobs with a registered runtime: reattaches via
  `jobs.Scheduler`.
- For `awaiting_input`: surfaces the clarification UI immediately,
  before the user types anything else. The pending clarification
  payload comes from the `clarify.requested` typed entry (§4.7).
- For jobs whose runtime is no longer registered: see §6.2 soft
  error.

#### 6.4 Pending foreground clarify

Distinct from background-job clarification. The orchestrator's
`o.pending` map ([`orchestrator.go:70`](../../internal/orchestrator/orchestrator.go))
holds the *foreground* clarify state — when the LLM returned a
partial slot fill and the engine emitted `ModeClarify`. Resume
replays the typed `clarify.requested` stream (§4.7); the TUI opens
into its clarify view rather than the normal action menu.

#### 6.5 Off-path mid-flight

`EvOffPathChatResolved` fires today ([`offpath.go:69`](../../internal/orchestrator/offpath.go))
but the resolved chat id is not journalled with enough specificity
to replay. v2 adds it to the typed-entry set: `offpath.chat.resolved`
with body `{chat_id, room, scope_key}`. Resume reads the most recent
one before any `offpath.exit` and pre-loads the off-path overlay.

#### 6.6 RNG state

`Snapshot.RNGSeed` ([`internal/store/event.go:107`](../../internal/store/event.go))
is declared but unused; no code generates or consumes it. The
proposal does **not** introduce RNG-state durability in v1. If/when
the engine introduces randomness (e.g. for the oregon-trail random
events), a typed `rng.seeded` journal kind tracks the seed at session
start and at any reseed; resume restores it from the latest entry.
Until then, the RNGSeed column stays a documented placeholder.

#### 6.7 `recovery_state:` top-level YAML key

```yaml
recovery_state: foyer
```

Optional. If set, resume-time state-path-rename failures teleport
here instead of becoming a hard error. Defaults to unset; opt-in per
app. The
[`oregon-trail-proposal.md`](oregon-trail-proposal.md) example app
declares it as a demo of graceful upgrades.

#### 6.8 Determinism contract

The proposal's load-bearing promise: `kitsoki run --continue` is a
**pure replay**. Concretely, between the user invoking the command
and the TUI's first frame:

| Side effect             | Allowed during resume? | Why                                                                 |
|-------------------------|------------------------|---------------------------------------------------------------------|
| SQLite reads            | Yes                    | The journal is the source of truth                                  |
| Filesystem reads        | Yes                    | JSONL mirror, claude session file metadata                          |
| `harness.Harness` call  | **No**                 | Resume would be non-deterministic; cost; latency                    |
| `host.*` handler call   | **No**                 | Side-effects on external systems (Jira, Bitbucket, MCP, filesystem) |
| `transport.*` send      | **No**                 | Would re-post the same content; user-visible duplication            |
| `claude` CLI invocation | **No**                 | Resume reads from kitsoki's journal, not from claude                |
| `time.Now()`            | **No** (in apply loop) | Patches carry their own `Ts`; apply loop uses journal time          |
| Network I/O             | **No**                 | Period                                                              |

The first time any of the "No" rows can fire is **after** resume
completes and the user submits their next input. At that point the
engine is in the same logical state as a non-resumed session — and
`host.oracle.ask_with_mcp` calling `claude --resume <session_id>`
benefits from claude's own session persistence (§4.8).

A violation of this contract is a P0 bug. The §4.5 constructor
signatures enforce it by construction (no `Harness` /
`host.Registry` / `transport.Registry` parameters on
`journal.Reader` or `journal.Applier`); a code-review checklist
item makes it explicit; a future linter could mechanize the rule.

This is also why the proposal does **not** try to "catch up" stale
external state on resume. If a Jira ticket received a new comment
between when the user quit and when they resumed, that comment is
*not* magically pulled in by `--continue`. The next time a transport
poll runs (driven by `kitsoki session continue` from loop.py, not
by `--continue`), the comment lands as a normal inbound event.
Determinism wins.

#### 6.9 Privacy and size

The journal carries chat transcripts, LLM-returned text, and world
vars — all data already in `events` today, just more visible.
Mitigations:

- **Default retention**: keep the latest N (default 50) sessions per
  app and prune older completed ones. Active sessions are never
  pruned automatically.
- **`kitsoki session forget`**: explicit deletion verb that removes
  the journal, `events`, `snapshots`, `external_keys`,
  `session_locks`, `chats` + `chat_messages` + `chat_locks` for any
  chat whose `app_id`+`session_id` references this session, `jobs`
  rows owned by this session, and `timeouts`. Today's
  `DeleteSession` ([`internal/store/sqlite.go:363`](../../internal/store/sqlite.go))
  only covers the first five — phase A widens it explicitly.

`--trace` continues to honour its existing redact flag for sensitive
fields; the journal table uses the same redaction list when
`KITSOKI_REDACT_AT_REST=1` is set (off by default for the same
reason the trace file's redact is off by default — debuggability >
caution for a single-user CLI).

---

### 7. CLI surface

#### 7.1 `kitsoki run`

```
--continue                 resume an existing session (picker if no selector)
--id <session-id>          resume by id (with --continue)
--key <transport:thread>   resume by external key (with --continue)
--no-implicit-resume       force a fresh session even if one is active
```

#### 7.2 `kitsoki session`

```
session checkpoint --id <id> [--doc <doc>]   force a checkpoint
session journal    --id <id> [--from <ver>]  dump journal entries (JSON to stdout)
session forget     --id <id>                 delete everything for this session (wide DELETE — §6.8)
session detach     --id <id>                 break a stale writer lock
```

`session journal` is the inspect tool — it's the JSONL fragment
`--trace` would have written, filtered to one session, suitable
for piping into `jq` or feeding back into a fixture.

---

### 8. What ships in phase A vs phase B vs phase C

#### Phase A — Durability and resume

**Done (already in `main` as of v3):**

- ✅ §3 spike completed and notes files landed:
  - `docs/proposals/notes/continue-mode-spike.md` (R1–R9)
  - `docs/proposals/notes/continue-mode-silent-mutations.md` (R6: 26 sites)
  - `docs/proposals/notes/continue-mode-call-sites.md` (32 dual-write sites)
- ✅ `internal/journal` package: types, in-memory Writer/Reader,
  schema-aware Applier, checkpoint policy. 27 tests green.

**Wave 2 (queued, parallel-dispatchable):**

- SQLite-backed `journal.Writer` / `journal.Reader`; new `journal`
  table (idempotent DDL added to `internal/store/schema.sql`).
- `kitsoki run --continue` with `--id`, `--key`, and the picker UI.
- Implicit-resume prompt (§5.2).
- `kitsoki session checkpoint|journal|forget|detach` (forget is
  the *wide* delete; §6.9).
- `recovery_state:` top-level key + tests.
- `multiHandler` panic-safety wrap (§4.9 Rule 2 footnote).

**Wave 3 (queued, after Wave 2):**

- Dual-write integration: walk every site in
  `continue-mode-call-sites.md`, add the journal write inside the
  existing `AppendEvents` tx (Rule 1 default) or in its own tx
  inside the writer lock (Rule 1 exception, for clarify / disambig /
  metamode.proposal / inbox.item.*).
- New typed-kind emission sites: `view.rendered` at `TurnEnded`;
  `disambig.presented` / `disambig.chosen` at the disambiguation
  flow; `inbox.item.opened` / `inbox.item.dismissed` at
  `MarkNotificationRead`; `clarify.requested` / `clarify.answered`
  with `origin` field at both fg and bg sites;
  `metamode.proposal.staged|applied|discarded` at ledger
  transitions; `offpath.chat.resolved` at the existing slog site.
- `orchestrator.AttachSession` — the resume entrypoint.
- Transcript projection from `view.rendered` + interleaved entries
  (§4.6).
- Foreground pending-clarify rehydration (§6.4).
- Background-job rehydration (§6.3).
- Resume hard- and soft-error UX (§6.2).

Exit criteria: every `testdata/apps/*` app can be run through to
turn N, killed, resumed via `--continue`, and finishes its flow
identically to a non-resumed run. Flow tests gain a
`--mid-flight-restart` mode that exercises this path. Crash-during-clarify
is in the matrix.

#### Phase B — Journal as the source of truth

- Flip `LoadJourney` to read from the journal for `world` and
  `state`. `events` / `snapshots` rows are rebuilt as indices on
  startup if absent.
- Retire direct writes to `snapshots`; checkpoints become journal
  entries; `LatestSnapshot` is derived.
- Host-event writes stay double-recorded (journal typed entry +
  `events` row) and converge to "journal is truth, events is index."
- Update `inspect` and `session show` to read journal entries
  directly (no behaviour change, but the data path is uniform).
- Add `--trace-pretty` rendering of `*.patch` entries that shows
  the operation list rather than a flat event line.

Exit criteria: journal is the only mutation log written by
application code; the other tables are projections.

#### Phase C — Adjacent wins (out of scope for v1/v2)

- `kitsoki replay --tui --session <id>`: scrub forward and backward.
- Time-travel debugging in flow tests.
- Cross-session diff: `kitsoki session diff A B`.
- Mid-flight migration: a `migration:` block in `app.yaml` so the
  §6.1 cases 2–4 become declarative.
- Multi-attach (TUI + Jira on the same session) — handed off to
  [`claude-code-sessions-proposal.md`](claude-code-sessions-proposal.md).

---

### 9. Open questions

1. **R4 outcome — chat-append ordering.** If chat appends and FSM
   events can interleave under separate locks, the journal’s
   `(turn, seq)` is total per document but not total across documents.
   Replay reads document-by-document anyway, so the practical impact
   is limited to the trace’s readability. Confirm in spike.
2. **JSONL location when `--trace` is unset.** Default to
   `~/.local/share/kitsoki/journal/<sid>.jsonl`? Or rely solely on
   the SQLite mirror? Trade-off: per-session files survive crashes
   and are easy to share; SQLite is the single-file deploy story.
   Tentative: opt-in JSONL via `--journal-jsonl`; SQLite always.
3. **`EventKind` taxonomy and mode-1 cassettes.** Phase B keeps
   `events` populated to preserve cassette compatibility, but a
   future cleanup may want to retire some kinds. Out of scope for
   v2; mention only.
4. **Per-document checkpoint cadence as YAML config.** Phase C.
5. **Multi-attach lock semantics.** v1 ships single-attach; v2
   continues that. If users want concurrent Jira + TUI on the same
   session, that's
   [`claude-code-sessions-proposal.md`](claude-code-sessions-proposal.md).
6. **External-only state** (Jira/Bitbucket thread reads). Tentative
   v1 answer: out-of-band; external systems are their own truth.

---

### 10. Non-goals

- **Resume never re-invokes the LLM, the harness, or any host
  handler.** The journal is sufficient. If a payload can't be
  replayed without recall, the payload is broken (R8 gates this).
  See the §6.8 determinism contract.
- **Resume never re-sends a transport message.** The journal carries
  the *fact* of a transport post; resume does not re-post.
- **Not a daemon.** `kitsoki serve` is a separate proposal.
- **Not session sharing across users.** One human, one session at a
  time.
- **Not an undo/redo UI.** Time-travel ships in phase C.
- **Not a replacement for `--harness replay`.** Recordings replay
  LLM oracle answers used in tests; the journal replays state used
  in human sessions.
- **Not a multi-attach mechanism.** See
  [`claude-code-sessions-proposal.md`](claude-code-sessions-proposal.md).
- **Not a replacement for claude's own session persistence.** The
  two are complementary; see §4.8.

---

### A. Appendix — v1 → v2 audit findings absorbed

For traceability. Each row maps to a v2 §.

| Audit finding | Where addressed in v2 |
|---|---|
| World vars round-trip badly through `encoding/json` (`coerceWorldVar` already solves this for events) | TL;DR (schema-aware applier bullet); §3.2 R1; §4.1 Applier interface; §6.1 case 4 |
| `app.StatePath` is sometimes parallel-encoded (`root#leaf_a|leaf_b`) | §3.2 R2 |
| `inbox` and `proposals/<id>` live inside `world.Vars`, not as separate documents | §2.1 table; §6.1 case 4 |
| Pending clarification is in-memory only (`o.pending`) — no resume path today | §1 #4; §2.1 "TUI- or orchestrator-resident"; §4.7; §6.4 |
| Chat appends bypass the session writer lock | §1 #4; §3.2 R4; §5.3 |
| `EffectApplied`/`HostInvoked` payloads can't be reconstructed from a JSON-Patch sequence | TL;DR (hybrid entries bullet); §2.2; §4.3 step 4 |
| TUI transcript pane is an append-only accumulator, not derived from events | §1 #5; §3.2 R7; §4.6 |
| Timeout state is in its own table, survives restart already | §2.1 "stay out of the journal" |
| Meta-mode `ProposalLedger` is rebuilt from chat metadata only | §2.1 "TUI- or orchestrator-resident"; §4.7 |
| `session delete` is too narrow | §1 #6; §6.8 |
| `Snapshot.RNGSeed` unused today | §6.6 |
| `inbox.item.opened/dismissed` declared but never emitted | §1 #4; §8 phase A bullet (new typed kinds) |
| `EventKind` is a breaking-change surface for mode-1 cassettes | §4.3 (phase B keeps `events`); §9 #3 |
| JSON-Patch library: RFC 6902 vs 7396 mixed in v1 | §4.1 (commit to 6902) |
| Replay performance numbers under-baked | §4.4 (5–15 ops/turn realistic on dev-story) |
| v1 was silent about determinism — resume must not re-invoke the LLM, harness, host handlers, or transports | TL;DR (resume-is-pure-replay bullet); §3.2 R8; §4.5 hard determinism contract; §4.6 transcript projection note; §6.8 determinism contract section; §10 non-goals |
| v1 was silent about how the new journal coexists with the existing trace + `events`/`snapshots` machinery — drift risks | TL;DR (no-clashes bullet); §3.2 R9 (atomicity under failure injection); §4.9 coexistence rules (single SQL transaction, wrapped slog handler, JSONL-as-tail, IF-NOT-EXISTS schema, lock semantics, WAL readers, one-way phase B projection rebuild) |
| Bridge to claude's own session persistence (`claude_session_id`) was not explained | §4.8 (complementary durability layers — kitsoki journal owns FSM state; claude session file owns LLM context) |

**v2 → v3 (spike + call-sites findings folded in):**

| Audit / spike finding | Where addressed in v3 |
|---|---|
| **R7 BLOCKED** — view text is not derivable from event payloads; v2 §4.6 option 2 (projection) is impossible without re-rendering, which violates determinism | §2.2 (new `view.rendered` typed kind); §4.6 rewritten to mandate journalled view text; §6.8 (drops "render on first frame" wording) |
| Call-sites finding 1 — `clarify.requested` has no `events` row to pair with; §4.9 Rule 1 violation | §4.9 Rule 1 carve-out for in-memory-only typed entries; §4.7 explains the discriminator and the lock semantics |
| Call-sites finding 2 — disambiguation has no `EventKind`; transcript projection would have blank rows | §2.2 (new `disambig.presented` / `disambig.chosen` typed kinds); §4.6 phase-A engine-work list |
| Call-sites finding 3 — `timeout.armed`/`cancelled` are post-commit side effects; can't share the turn-write tx | §2.1 reaffirmed; §4.9 Rule 1 post-commit-side-effects exception |
| R6 — silent mutations enumerated (26 sites; 15 JOURNAL-IT, 11 LEAVE-OUT) | `docs/proposals/notes/continue-mode-silent-mutations.md` is the authoritative list; §2.2 typed kinds list includes the 15 to journal |
| Journal package agent — `Applier.Apply` returns bytes; coercion can't survive `json.Unmarshal`; explicit `CoerceWorldVars` step needed | §4.1 sketch shows the canonical apply pattern (Apply → Unmarshal → Coerce); already shipped in [`internal/journal/applier.go`](../../internal/journal/applier.go) |
| Spike R3 — standalone-tx INSERT is fsync-dominated at 3–6ms; benchmark target (<100µs p95) only holds inside an open AppendEvents tx | §3.2 R3 reworded |
| Spike R6 + R7 — meta-mode ledger drafts/discards don't write chat metadata today; chat-metadata-as-ledger is insufficient | §4.7 pivots from "strengthen chat metadata" (v2 option 1) to "journal three typed kinds" (`metamode.proposal.{staged,applied,discarded}`); resume reads journal as authoritative |
| Spike R9 — existing `multiHandler` is not panic-safe today; phase-A code task needed | §4.9 Rule 2 footnote: phase A wraps each sub-handler `Handle` in `defer recover()` before the journal handler is added |
| Foreground vs background clarify need a discriminator | §4.7 — `clarify.requested`/`answered` bodies grow an `origin: foreground|background` field; the same kind serves both consumers |
