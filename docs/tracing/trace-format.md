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

**`turn.start` payload — routing provenance.** A turn whose intent was
resolved by a non-LLM tier (so no `oracle.call.*` events appear) carries
`"direct": true` plus the provenance of HOW the intent was chosen. Without
this, a transition like `intent=quit → @exit` is inscrutable in the trace —
exactly the dogfood failure that motivated these fields (a pasted bug
report containing a stray `cancel` token routed to `quit` via the semantic
tier and ended the session, with the trace showing only `direct: true`).

| Payload key  | Type    | When present | Meaning                                                            |
|--------------|---------|--------------|--------------------------------------------------------------------|
| `direct`     | bool    | direct submits | The intent was submitted without the LLM router (menu pick, CLI `--intent`, or a routing tier). |
| `routed_by`  | string  | tier-routed  | The resolving tier: `deterministic`, `semantic`, `turncache`, `disambiguation`. Absent for genuinely caller-chosen submits (menu pick / `--intent`) where there is no routing to explain. |
| `match_type` | string  | optional     | Tier-specific reason: `display`/`example` (deterministic) or `synonym:<text>`/`example:<text>` (semantic). |
| `confidence` | float   | optional     | Routing confidence band (e.g. `0.90` for a semantic synonym hit). Omitted when not applicable. |

These are written by `RouteProvenance.stampOn` in
`internal/orchestrator/orchestrator.go`; see `RouteProvenance` for the
source-of-truth field docs.

**`turn.end` payload — recorded room narration.** On a successful transition,
`turn.end` carries the rendered operator-facing room view in `view` alongside
`outcome` and `to`:

| Payload key | Type   | When present        | Meaning                                                                 |
|-------------|--------|---------------------|-------------------------------------------------------------------------|
| `outcome`   | string | always              | `transitioned`, `rejected`, …                                           |
| `to`        | string | transitioned        | Destination state path.                                                 |
| `view`      | string | transitioned, non-empty | The room's `view:` template expanded against world state at the end of the turn — the deterministic narration the operator saw (banner / prose / kv / headings / the questions a clarify room poses). |

The view is recorded **in the trace** rather than reconstructed later because
the story's view templates can change mid-run (meta-mode edits) and run-to-run,
and are not guaranteed to be pinned to a git sha — so the rendered narration is
not recoverable from the story files after the fact. It is captured at render
time. There is exactly one rendered view per turn, which is why it rides
`turn.end` rather than a dedicated event (contrast `machine.say`, which can fire
several times per turn and so warranted its own kind). Omitted when empty
(rejected turns, background/scheduler turns). Written by `transitionedTurnEnd`
in `internal/orchestrator/orchestrator.go`. Replay ignores the payload.

**`session.story` / `story.changed` — the embedded effective story.** Where
`turn.end` records the rendered *narration*, these two events record the story
*source* — every file the loader touches to build the running machine
(manifests + views + prompts + scripts + fixtures under the story tree and any
imported sibling trees). This is what makes a trace a self-contained,
deterministic replay: the story on disk can be edited mid-run (`/reload`,
`/meta`) or after the session, so a replay that re-reads disk no longer
reproduces what happened — and you cannot rewind to a turn and *branch* onto a
new path, because the story effective at that turn is gone. With the source
embedded, replay reconstructs the `AppDef` from the trace
(`store.StoryAtTurn` → `app.LoadFromFiles`: materialise the files to a temp dir
and re-run `app.Load`); see `kitsoki turn --trace <trace>` with no `--app`.

- **`session.story`** is the base snapshot, appended exactly once per session
  at start (turn 0). Payload: `{"app_id", "entry", "hash", "files"}`.
- **`story.changed`** is a diff against the prior story state, appended whenever
  the effective story's content hash drifts (a `/reload` or `/meta` edit; both
  funnel through `orchestrator.Reload`). Recording the change *in the trace*
  (rather than a git sha) is required because `/reload` picks up *uncommitted*
  edits a sha cannot name. Payload: `{"hash", "prev_hash", "changed",
  "removed"}`. Reconstruction at a turn = the latest `session.story` with
  `Turn ≤ target`, then every `story.changed` up to the target turn applied in
  order (overlay `changed`, drop `removed`).

| Payload key | Type   | Event           | Meaning                                                                 |
|-------------|--------|-----------------|-------------------------------------------------------------------------|
| `app_id`    | string | session.story   | The app id (`app.id`).                                                   |
| `entry`     | string | session.story   | Root manifest path, relative to the capture root — hand to the loader.   |
| `hash`      | string | both            | sha256 (hex) over the canonical sorted file map (raw bytes).             |
| `files`     | object | session.story   | `relpath → base64(raw bytes)` for every captured file.                   |
| `prev_hash` | string | story.changed   | The hash this diff applies on top of.                                    |
| `changed`   | object | story.changed   | `relpath → base64(raw bytes)` for added/modified files.                  |
| `removed`   | array  | story.changed   | Deleted relpaths.                                                        |

Two wire-format details:

- **File paths are keyed relative to the *capture root*** — the common ancestor
  of the story's `BaseDir`, every imported manifest's directory, and any prompt
  shared/overlay dirs. Keying relative to the common ancestor preserves the
  relative layout that `import: ../sibling/app.yaml` depends on, while staying
  portable (no absolute machine paths). `entry` is keyed the same way.
- **File contents are base64-encoded.** The JSONL sink rejects non-NFC strings,
  NUL bytes, and CRLF (see §6), any of which a prompt/fixture file may
  legitimately contain. Base64 sidesteps those write-time constraints and is
  byte-faithful; the `hash` is computed over the raw bytes.

These events are written ONLY to the JSONL trace (never the legacy SQLite event
log): self-containment is a property of the trace replay reads, and the JSONL
sink continues per-turn `seq` numbering across appends, so a snapshot rides
turn 0 alongside the initial `on_enter` events and a diff rides the latest turn
without the one-batch-per-turn collision the SQLite store enforces. Written by
`Orchestrator.RecordEffectiveStory`
(`internal/orchestrator/story_record.go`); the capture/reconstruct helpers live
in `internal/store/story.go`. Replay folds both as no-ops.

---

## 4. EventKind vocabulary

All kinds use the dotted form the SPA subsystem chip logic already consumes.

| Kind                         | When written                                                 |
|------------------------------|--------------------------------------------------------------|
| `session.story`              | Once per session at start (turn 0): base snapshot of the effective story. Replay no-op. |
| `story.changed`              | When the effective story's hash drifts (`/reload`, `/meta`): a diff. Replay no-op. |
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
| `machine.say`                | Once per `say:` effect that resolves. Payload `{"text": …}`; replay no-op. Split out of `world.update` so a timeline can render narration as its own row. |
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
| `artifact.emitted`           | A host call produced a named media artifact (see §Artifact event). |

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
| `meta`       | object | Opaque oracle metadata. For the claude-CLI transport: `{ "usage": { "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", … }, "cost_usd": <float> }`, captured per invocation from the stream-json `result` event. Omitted when no usage was reported (e.g. a test stub). Plugin transports may carry their own meta (cassette `episode_id` / `match_idx`, …). |

**`oracle.call.error` payload fields:**

| Field        | Type   | Description                                          |
|--------------|--------|------------------------------------------------------|
| `verb`       | string | Oracle verb.                                         |
| `agent`      | string | Agent name (optional).                               |
| `duration_ms`| int    | Duration before the error.                           |
| `error`      | string | Human-readable error message; kind is in `AskError.Kind`. |

For the full oracle plugin contract (transports, lifecycle, auth/secrets, and
sub-events), see [`docs/architecture/oracle-plugin.md`](../architecture/oracle-plugin.md).

### Artifact event kind

`artifact.emitted` is written by `host.artifacts_dir` when the caller supplies
`src_path` + `kind` (the media-emit path) rather than a markdown `body`. The
event records the produced file as a first-class, queryable fact in the trace —
consumers (the web UI's `/artifact/{id}` route, the TUI pointer) read from this
record rather than reconstructing file paths from world state
(memory: *narration-belongs-in-trace*).

Source type: `ArtifactEvent` in `internal/journal/types.go`.

| Field        | Type   | Description                                                                          |
|--------------|--------|--------------------------------------------------------------------------------------|
| `id`         | string | Stable handle: `<basename>#<counter>`, same shape as `host.artifacts_dir` `message_id`. The web server resolves `GET /artifact/{id}` against this field. |
| `kind`       | string | Media kind: `video`, `image`, `pdf`, `html`, or `slideshow`.                        |
| `mime`       | string | MIME type, e.g. `video/mp4`, `image/png`, `application/pdf`. Auto-detected when absent from the call args. |
| `label`      | string | Human-readable display name supplied via `args["label"]`.                            |
| `path`       | string | Absolute path of the copied file under the artifacts root. Never crossed into world (the world handle carries only `id`). |
| `producer`   | string | Host call that emitted the artifact; currently always `"host.artifacts_dir"`.        |
| `size_bytes` | int64  | File size in bytes after the copy operation.                                         |
| `created_at` | string | RFC3339Nano timestamp of the copy operation.                                         |

Example wire shape:

```json
{
  "turn": 3, "seq": 7, "ts": "2026-06-09T12:00:00Z",
  "kind": "artifact.emitted",
  "state_path": "render_deck",
  "payload": {
    "id": "walkthrough#1",
    "kind": "video",
    "mime": "video/mp4",
    "label": "Architecture walkthrough",
    "path": "/home/user/project/.artifacts/session-abc/walkthrough#1.mp4",
    "producer": "host.artifacts_dir",
    "size_bytes": 12582912,
    "created_at": "2026-06-09T12:00:00.123456789Z"
  }
}
```

`artifact.emitted` events are **no-ops for replay** — `BuildJourney` ignores them.
The event is written exactly once per `host.artifacts_dir` media-emit call (i.e. once
per file copied under the artifacts root). The `id` is stable across replays because
it is derived from the filename and append counter, not from a random UUID.

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
8. **Self-contained story:** the trace carries the entire effective story
   (`session.story` at start; `story.changed` for each mid-run `/reload` /
   `/meta` edit). Replay reconstructs the `AppDef` from the trace
   (`store.StoryAtTurn` → `app.LoadFromFiles`) rather than re-reading disk, so a
   trace replays — and can be rewound and branched — deterministically even
   after the story files on disk change or are removed (`kitsoki turn --trace`
   with no `--app`).

These guarantees are enforced by a 7-layer determinism test suite
(`internal/store/`, `internal/orchestrator/`, `internal/testrunner/`).

---

## 11. `kitsoki trace to-flow` — trace → replayable flow fixture

A recorded session trace can be converted into a deterministic flow fixture
(plus a host cassette) so the session can be re-driven through the engine
without an LLM. This is how a session recorded by an older binary is replayed
through a freshly-built one so the new trace carries fields the old one lacked
(e.g. `turn.end.view`).

```
kitsoki trace to-flow <trace.jsonl> --app ../app.yaml --out <flow.yaml> \
  [--recording <cassette.yaml>] [--app-id <id>] [--initial-state <state>]
```

Then replay and capture a fresh trace:

```
kitsoki test flows <app.yaml> --flows <flow.yaml> --trace-out <fresh.jsonl>
```

(`--trace-out` wires `testrunner.FlowOptions.TracePath`, fixing the run's
authoritative JSONL sink to a known path. Point `--flows` at the single
generated fixture so its trace isn't clobbered by sibling fixtures.)

### Mapping

| Trace                          | Fixture                                                        |
|--------------------------------|----------------------------------------------------------------|
| First `machine.transition` `from` | `initial_state` (override with `--initial-state`).          |
| (none — empty by default)      | `initial_world: {}` — the app's world schema defaults + room `on_enter` effects repopulate it on replay. |
| Each `machine.transition`      | One `turns:` entry `{intent: {name, slots}}`, **slots verbatim** (string values such as `n: "1"` are preserved), in order. Transitions with an empty `intent` (e.g. synthetic timeouts) are skipped — they are not re-drivable. |
| Each `harness.returned` whose `namespace` starts with `host.` | One cassette episode, in trace order, `match: {handler: <namespace>}`, `response.data = <returned data>`. |

### Why a cassette, not `host_handlers:`

A session's host/oracle responses vary per call (e.g. `host.oracle.converse`
returns a different reply each of five invocations). `host_handlers:` declares
**one** response per handler name and so cannot reproduce a varying session. The
cassette's episodes are consumed first-unplayed-match-by-handler
(`MatchEpisode`) and the generated episodes are **not** `replay:any`, so the
i-th call to a handler consumes the i-th matching episode — exact ordered
replay of per-call-varying responses.

### Story-drift policy (no expectations emitted)

The converter deliberately emits **no** `expect_state` / `expect_world` on the
turns. A trace recorded against an earlier version of a story may route
differently against the current story (rooms added/removed on the path); strict
expectations would hard-fail replay on the first divergence and hide the rest of
the reconstruction. The generated flow is a faithful re-drive of the recorded
*intents*, not an assertion of the old path.

A consequence worth knowing: if the current story routes a turn into a room that
did **not** exist when the trace was recorded, that room's `on_enter` may invoke
a host handler the trace never recorded — so the cassette has no episode for it.
With no fallback handler the call is a hard cassette miss and the room's
`on_error:` arc fires (typically bouncing back toward idle). This is expected
drift surfaced honestly by replay, not an engine fault: the trace simply does
not contain a response for a handler the new path needs.

The transform lives in `internal/testrunner/fromtrace.go`
(`ConvertTraceToFlow`); the CLI is `cmd/kitsoki/trace.go` (`traceToFlowCmd`).
