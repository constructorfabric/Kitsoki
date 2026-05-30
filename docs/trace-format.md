# Kitsoki JSONL Trace Format

The trace is the session. Every kitsoki entry point — `kitsoki run` (TUI),
`kitsoki turn` (headless driver), `session continue` — writes the same
append-only JSONL file. There is no separate SQLite event log, no slog JSONL
side-channel, and no exporter-side synthesis: what you read in the file is
exactly what the engine wrote.

---

## 1. File shape

```
{"kind":"session.header","schema_version":1,"written_at":"<RFC3339Nano>"}
{"turn":1,"seq":0,"ts":"<RFC3339Nano>","kind":"turn.start","state_path":"foyer","payload":{...}}
{"turn":1,"seq":1,"ts":"<RFC3339Nano>","kind":"turn.input","state_path":"foyer","payload":{...}}
...
```

- **Line 1** is always the `session.header`.
- **Lines 2+** are event lines, one `store.Event` per line.
- Every line ends with exactly `\n` (LF only; no CRLF, no missing trailing newline).
- The file as a whole ends with `\n` after the last event.

---

## 2. `session.header` schema

```json
{
  "kind":           "session.header",
  "schema_version": 1,
  "written_at":     "2026-01-15T09:00:00.000000000Z"
}
```

| Field            | Type   | Description                                       |
|------------------|--------|---------------------------------------------------|
| `kind`           | string | Always `"session.header"`.                        |
| `schema_version` | int    | Currently `1`. Files with a higher version than   |
|                  |        | the reader supports are refused at open.          |
| `written_at`     | string | RFC3339Nano UTC timestamp of file creation.        |

---

## 3. Event schema (`store.Event`)

Every non-header line has this shape:

```json
{
  "turn":       1,
  "seq":        0,
  "ts":         "2026-01-15T09:00:00.123456789Z",
  "kind":       "turn.start",
  "state_path": "foyer",
  "payload":    {"input": "go west", "intent": ""}
}
```

| Field        | Type   | Required | Description                                                  |
|--------------|--------|----------|--------------------------------------------------------------|
| `turn`       | int64  | yes      | Monotonic turn number within the session, starting at 1.    |
| `seq`        | int    | yes      | Dense per-turn sequence number starting at 0.               |
| `ts`         | string | yes      | RFC3339Nano in UTC with explicit `Z` suffix.                 |
| `kind`       | string | yes      | Dotted event kind (see §4).                                  |
| `state_path` | string | no       | Active state at event write time. Non-empty on all events    |
|              |        |          | except off-path events which carry `parent_turn` instead.   |
| `payload`    | object | yes      | Event-specific data (`{}` for events with no payload data).  |
| `parent_turn`| int64  | no       | Set on off-path events; the foreground turn that was in      |
|              |        |          | flight when the off-path batch was appended.                 |
| `call_id`    | string | no       | Oracle call identifier (OracleCalled, OracleReturned,        |
|              |        |          | OracleError only). See §5 for derivation.                    |
| `episode_id` | string | no       | Cassette episode ID (OracleCalled only, when cassette-backed).|
| `match_idx`  | int    | no       | 0-based match counter for `replay:any` cassette episodes.    |

**`state_path` semantics for transition events:**
- `machine.state_exited` carries the state being **exited** (the FROM state).
- `machine.state_entered` carries the state being **entered** (the TO state).
- All other events carry the active state at the moment they were written.

---

## 4. EventKind vocabulary

All kinds use the dotted form the SPA subsystem chip logic already consumes.

| Kind                         | When written                                                 |
|------------------------------|--------------------------------------------------------------|
| `turn.start`                 | At the start of every user turn.                            |
| `turn.input`                 | When user input is received (before harness is called).     |
| `turn.end`                   | At the end of every user turn.                              |
| `oracle.ask.start`           | Immediately before the LLM harness is invoked.              |
| `oracle.tool_call`           | When the LLM produces a tool call result.                   |
| `oracle.call.start`          | When an oracle verb is dispatched (verb/agent/model metadata only; prompt is not embedded, see §Oracle event kinds). |
| `oracle.call.complete`       | When the oracle verb response lands (full response).        |
| `oracle.call.error`          | When the oracle verb returns an error.                      |
| `oracle.off_path.question`   | User asks a free-form off-path question. Replay no-op.      |
| `oracle.off_path.answer`     | Oracle returns an off-path reply. Replay no-op.             |
| `machine.intent_accepted`    | An intent call passes Validate.                             |
| `machine.validation_failed`  | Machine.Validate rejects a tool call.                       |
| `machine.guard_rejected`     | All guards for a transition failed.                         |
| `machine.transition`         | After a successful transition fires.                        |
| `machine.state_exited`       | Machine leaves a state (compound or leaf).                  |
| `machine.state_entered`      | Machine enters a state (compound or leaf).                  |
| `machine.off_path_entered`   | User activates off-path mode.                               |
| `machine.off_path_exited`    | User returns from off-path mode.                            |
| `machine.timeout`            | Synthetic timeout-fired turn.                               |
| `harness.called`             | Host side-effect dispatched (pre-bind args).                |
| `harness.dispatched`         | Host handler invoked (post-rerender args).                  |
| `harness.returned`           | Host invocation completed.                                  |
| `harness.error`              | Orchestrator dispatch loop failed loudly.                   |
| `world.update`               | One effect applied during a transition.                     |
| `scheduler.submitted`        | Background job dispatched.                                  |
| `scheduler.completed`        | Background job reached a terminal state.                    |

**Forward compatibility:** unknown `kind` values are preserved verbatim on
round-trip — `BuildJourney` ignores them; the JSONL reader passes them through
unchanged. A trace written by a newer kitsoki still loads under an older one
up to the point of an unknown kind that matters for state reconstruction.

### Oracle event kinds

Every oracle call produces exactly two events: `oracle.call.start` and
`oracle.call.complete` (or `oracle.call.error` on failure).  These events are **no-ops
for replay** — `BuildJourney` ignores them — but they carry the response and
oracle metadata for audit and the runstatus SPA. Large prompts and responses
(>1KB) are written to sidecar files under the configured prompts directory and
the event payload references them via `prompt_file` / `response_file`; smaller
payloads remain inline.

| Kind                   | When written                                               |
|------------------------|------------------------------------------------------------|
| `oracle.call.start`    | After `Oracle.Ask` returns (so cassette `episode_id` / `match_idx` from `resp.Meta` are available). |
| `oracle.call.complete` | After schema validation passes; carries `Submission` + `Meta`. |
| `oracle.call.error`    | When `Oracle.Ask` returns an error, or schema validation fails, or a sub-event constraint fires. |

**`oracle.call.start` payload fields:**

| Field          | Type   | Description                                        |
|----------------|--------|----------------------------------------------------|
| `verb`         | string | Oracle verb: `ask`, `decide`, `extract`, `task`, `converse`. |
| `agent`        | string | Agent name (optional).                             |
| `model`        | string | Model name (optional).                             |
| `prompt_file`  | string | Relative path (from the trace dir) to the prompt sidecar when the rendered prompt exceeds ~1KB and a prompts dir is configured; omitted otherwise. |
| `input`        | object | Verb-specific input descriptor (e.g. `{schema_path}`). |

**`oracle.call.complete` payload fields:**

| Field        | Type   | Description                                          |
|--------------|--------|------------------------------------------------------|
| `verb`       | string | Oracle verb.                                         |
| `agent`      | string | Agent name (optional).                               |
| `model`      | string | Model name (optional).                               |
| `duration_ms`| int    | Round-trip duration in milliseconds.                 |
| `response`   | object | Parsed `Submission` + any verb-specific fields. Omitted when `response_file` is set (large responses). |
| `response_file` | string | Relative path (from the trace dir) to the response sidecar when the response exceeds ~1KB and a prompts dir is configured; omitted otherwise. |
| `meta`       | object | Opaque oracle metadata (tokens, cost, transport, …). |

**`oracle.call.error` payload fields:**

| Field        | Type   | Description                                          |
|--------------|--------|------------------------------------------------------|
| `verb`       | string | Oracle verb.                                         |
| `agent`      | string | Agent name (optional).                               |
| `duration_ms`| int    | Duration before the error.                           |
| `error`      | string | Human-readable error message; kind is in `AskError.Kind`. |

For the full oracle plugin contract (transports, lifecycle, auth/secrets, and
sub-events), see [`docs/oracle-plugin.md`](oracle-plugin.md).

---

## 5. `call_id` derivation

`call_id` is a 64-bit hex string derived from:

```
sha256("oracle-call:" + appID + ":" + key)[:16]
```

where `key` is:

- **Live call:** `turn + ":" + state_path + ":" + seq`
- **Cassette-backed call:** `episodeID + ":" + matchIdx`

`call_id` is 1:1 with each oracle exchange. The runstatus SPA pairs
`oracle.call.start` with `oracle.call.complete` by this field. For `replay:any`
cassette episodes, `episode_id` groups reuses while `call_id` remains unique
per exchange (different `matchIdx` → different `call_id`).

### Sub-events (B-4)

A plugin may populate `AskResponse.SubEvents` with plugin-internal events. These
are appended verbatim to the JSONL between the `oracle.call.start` and `oracle.call.complete`
lines with the following constraints (all enforced by kitsoki; violations produce
`oracle.call.error` instead of `oracle.call.complete` and no sub-events land):

- **Namespace:** every sub-event `kind` must start with the dispatching oracle
  plugin name + `.` (e.g. `oracle.autofix_fixer.bash.called`).
- **`call_id`:** every sub-event `call_id` must match the parent `oracle.call.start` call_id.
- **Size:** sub-events can be arbitrary size (no limits).
- **Timestamp:** kitsoki re-stamps each sub-event `ts` at append time using its
  own monotonic clock. The plugin's claimed `ts` is discarded. This guarantees all
  sub-event timestamps fall within `[oracle.call.start.ts, oracle.call.complete.ts)`.

---

## 6. Line constraints (write-time enforcement)

All constraints are enforced at `JSONLSink.Append` time; violations return an
error and leave the file unmodified.

| Constraint           | Limit / rule                                                   |
|----------------------|----------------------------------------------------------------|
| Line ending          | Exactly `\n`; CRLF is rejected.                               |
| NUL bytes            | Rejected in any field.                                         |
| Unicode normalisation| All string values must be NFC; NFD input is rejected.          |
| NaN / Inf            | `encoding/json` rejects them; that default is preserved.       |
| Timestamps           | RFC3339Nano in UTC with explicit `Z` suffix.                   |

---

## 7. Read-time rejection (all return errors; the file is not opened for append)

| Condition                                 | Error message                                    |
|-------------------------------------------|--------------------------------------------------|
| File does not end with `\n`               | `trace corrupted: missing trailing newline at EOF` |
| CRLF line ending at line N                | `trace corrupted: CRLF line ending at line N`    |
| NUL byte in line N                        | `trace corrupted: NUL byte in line N`            |
| Line 1 is not `session.header`            | `trace missing session.header on line 1`         |
| Duplicate `session.header`                | `duplicate session.header at line N`             |
| `schema_version` > maxSchemaVersion       | `schema_version N on disk exceeds highest supported M` |
| Duplicate `(turn, seq)`                   | `duplicate (turn,seq) at line N`                 |
| Out-of-order `(turn, seq)`                | `out-of-order (turn,seq) at line N`              |
| Gap in `seq` within a turn                | `gap in seq within turn T at line N`             |
| BOM at start of file                      | (NUL byte or non-UTF8 rejection)                 |
| Torn last line (missing trailing newline) | `trace corrupted: missing trailing newline at EOF` |
| File replaced (inode changed) during session | `trace file replaced under us`                 |
| File locked by another writer             | `trace file is locked by another writer`         |

---

## 8. `EventSink` contract

`store.EventSink` is the write-side abstraction:

```go
type EventSink interface {
    Append(ev Event) error   // marshal one event and append it
    History() History        // in-memory history since open
}
```

`JSONLSink` implements `EventSink`:

- **`OpenJSONL(path)`** acquires an exclusive advisory flock (fails immediately
  if another writer holds it), writes the `session.header` line on creation,
  and keeps an in-memory history slice for `History()`.
- **`Append`** is O(1) per event: marshal → write → fsync → extend history.
  The sink assigns dense per-turn `seq` numbers; callers MUST NOT rely on
  `ev.Seq` being preserved (it is overwritten).
- **`History()`** returns the in-memory event slice accumulated since `OpenJSONL`.
  Useful for computing "events written this turn" without re-reading the file.
- **`Close`** releases the flock.
- **`Lines()`** returns a defensive copy of the raw bytes the sink wrote for
  each event (one `[]byte` per event, without trailing `\n`), in the same
  order as `History()`.  `Snapshot.RawLines` is populated from `Lines()` when
  the caller uses `runstatus.FromSink`; this is a byte-copy-equal path, not
  encoder-pair-equal.  `FromHistory` (when called without a sink) re-marshals
  each event and is encoder-pair-equal.
  Memory: O(N) per session; acceptable for phase A scale.

---

## 9. Default path schemes

Two path schemes exist for different entry points:

| Entry point                    | Path scheme                                         | Why                                         |
|--------------------------------|-----------------------------------------------------|---------------------------------------------|
| `kitsoki run` (TUI)            | `~/.kitsoki/sessions/<app>/<sha8>-tui-<sid>.jsonl` | Home-anchored; deterministic key = session  |
| `kitsoki turn --trace <path>`  | Caller-supplied; explicit                           | Driver owns the path                        |
| `session continue` (headless)  | `~/.kitsoki/sessions/<app>/<sha8>-<slug>.jsonl`    | Deterministic per transport:thread for resume|

The TUI path uses `DefaultTracePath(app, "tui", sessionID)`. The home-anchored
scheme gives deterministic paths for resumed sessions (same session → same file).

A `DefaultRunTracePath(appID)` helper (in `internal/store`) walks upward from
cwd to find a `.kitsoki/` directory or `.kitsoki-root` marker and anchors the
path there, creating `.kitsoki/sessions/` if needed. The trace is named
`<UTC-timestamp>-<appID>.jsonl`.

---

## 10. Replay-determinism guarantees

The trace is lossless and replay is deterministic:

1. **Byte-identity round-trip:** read JSONL → write back via `JSONLSink` →
   `bytes.Equal` on file contents. Serialisation drift fails this immediately.
   `runstatus.FromSink` uses `sink.Lines()` to populate `Snapshot.RawLines`
   with the exact bytes the writer wrote (byte-copy-equal); `FromHistory` falls
   back to encoder-pair marshalling when no sink is available.
2. **Fold idempotence:** `BuildJourney(history)` twice returns deep-equal
   `(state, world, turn)`. A third call after JSONL round-trip returns the same.
3. **Live ≡ replay equivalence:** run a fixture live, reload from JSONL, continue
   — final `(state, world)` equals the no-reload baseline at every resume point.
4. **Crash-mid-write recovery:** a torn last line is detected and discarded;
   fold returns the state of the last fully-committed turn.
5. **Forward compat:** unknown kinds are preserved on round-trip; older readers
   fold over them as no-ops.
6. **Cassette matchIdx continuity:** `replay:any` episodes keep their match
   counter across process restarts; post-resume call_ids are distinct from
   pre-resume ones.
7. **Exporter pass-through:** `FromHistory` emits `Snapshot.Events` as the JSONL
   lines parsed into `TraceEvent` values — no exporter-side synthesis, no
   back-fill, no timestamp fudging.

These guarantees are enforced by a 7-layer determinism test suite
(`internal/store/`, `internal/orchestrator/`, `internal/testrunner/`).
