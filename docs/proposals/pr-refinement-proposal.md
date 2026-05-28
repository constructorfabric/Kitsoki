# Kitsoki changes for external orchestration

**Status:** Draft v1. Nothing implemented yet.

Two kitsoki-internal changes are needed before kitsoki can host
externally-driven story runs like the cyber-repo `pr-refinement`
loop or the `claude-autofix-agent` phase-0 control inversion:

1. **Sessions fully replayable from a JSONL trace.** Today the event
   log lives in SQLite; replay (`store.BuildJourney`) only runs
   in-process against that store. To let an external driver own
   session lifecycle ("pull trace → run one turn → push trace"), the
   on-disk JSONL must be the authoritative form *and* the
   import/export round-trip must be lossless.
2. **Oracle plugin mechanism.** The `Harness` interface today is
   Anthropic-SDK-shaped (`mcp.CallToolParams` return value, claude-CLI
   subprocess assumption). To let an external system register itself
   as the LLM (autofix's bounded fixer; a CI-failure responder; a
   user's own MCP server), kitsoki needs a typed plugin contract that
   admits in-process, subprocess JSON-RPC, and MCP-over-HTTP oracles
   without each one re-implementing the harness lifecycle.

Both are prerequisites for any external orchestrator. Nothing else
in this proposal — no story design, no driver design — is in scope.
Motivating examples (cyber-repo PR refinement, autofix phase 0) are
referenced only to keep the contracts honest.

## 1. Trace-as-state

### Principle

**One trace format. One sink. Every path.** Whether a session is
driven by the TUI, by `session continue` from a Bitbucket poller, or
by `kitsoki turn` from an external driver, the on-disk session
artefact is the same fully-replayable JSONL. There is no
"interactive uses sqlite, headless uses jsonl" split — that would
mean two serialisation contracts to keep in sync, and a lossy
sqlite-to-jsonl export every time something wants to look at a
session it didn't start.

The JSONL trace is the session. Everything else (sqlite, recording
cassettes, slog) is a derived view or a debug aid.

### What we have today

- **SQLite event log** (`internal/store/`). Source of truth for
  interactive sessions. `store.BuildJourney` already proves the
  event stream is self-sufficient — sqlite is the storage, not the
  model.
- **Recording JSONL** (`internal/harness/recording.go`). Harness-
  scoped cassette for the `replay` harness; not a session log.
- **slog trace JSONL** (`internal/trace/trace.go`). Debug logs,
  lossy by design.

### What we want

The orchestrator writes every event to a `JSONLSink` — one line per
event, the existing `store.Event` shape, atomically rewritten on
each `Append` (write tmp, fsync, rename). Every entry point uses it:

| Entry point        | Trace location                             |
|--------------------|--------------------------------------------|
| `kitsoki turn`     | `--trace path/to/trace.jsonl` (explicit)   |
| `kitsoki session continue` | `~/.kitsoki/sessions/<key>.jsonl` (default; `--trace` override) |
| `kitsoki tui`      | same default path, namespaced by app+key   |
| Replay / tests     | the same JSONL files used as fixtures      |

`session continue` and the TUI gain no new persistence story —
they're already keyed by `(app, transport:thread)`; that key just
maps to a path under `~/.kitsoki/sessions/` instead of a sqlite row.

```go
// internal/runner/runner.go (new orchestrator-facing seam)
type EventSink interface {
    Append(ev store.Event) error
    History() store.History  // in-memory tail for recent-turns, etc.
}
type JSONLSink struct { Path string; hist store.History }
func OpenJSONL(path string) (*JSONLSink, error)   // load + fold-ready
func (s *JSONLSink) Append(ev store.Event) error  // atomic rewrite
```

Load semantics: `OpenJSONL` reads the file into memory, hands the
history to `BuildJourney` for the `(state, world, turn)` fold, and
keeps the slice live for the rest of the session. Append: extend
the slice + atomic rewrite. A driver / TUI / continue call that
crashes mid-write leaves the prior trace intact.

The orchestrator takes an `EventSink`, not a `*store.Store`. Every
call site that currently INSERTs to sqlite becomes `sink.Append`.

### What happens to SQLite

The sqlite event tables go away. What remains, if anything:

- **External-key index** (`internal/store/external_keys.go`).
  Resolves `(app, transport:thread) → session_id` for `session
  continue`. Replace with a directory layout:
  `~/.kitsoki/sessions/<app>/<sha256(transport:thread)>.jsonl`.
  No index needed; the path *is* the lookup.
- **Off-path side-channel rows.** Off-path events already live in
  the same event stream (`OffPathEntered` etc.); they're just
  appended to the same JSONL. The `max(existing)+1` turn-numbering
  rule from `BuildJourney` is preserved verbatim — it's a property
  of the event stream, not of sqlite.
- **Anything else.** Nothing else in `internal/store/` is
  load-bearing for state. The package shrinks to `Event`, `History`,
  `BuildJourney`, and the new `JSONLSink`. The `database/sql`
  dependency drops out of the orchestrator entirely.

### Surface

```
kitsoki turn --app stories/foo/app.yaml \
             --trace path/to/trace.jsonl \
             --intent <name> [--slot k=v …]
```

- Trace doesn't exist → create it (header line + `TurnStarted` for
  turn 1).
- Trace exists → load, fold, run one turn, append.
- Stdout: the new events appended this turn, as JSONL — drivers
  that want streaming don't have to diff the file.
- Exit code: accepted / rejected / terminal.

`kitsoki session continue` and the TUI are unchanged from the
user's point of view; only the on-disk artefact changes from a
sqlite row to a jsonl file. `kitsoki session export` is no longer
a thing — the file already *is* the trace.

### Contract details

- **Schema = the existing `store.Event`.** Same `Kind` enum
  (`internal/store/event.go`), same JSON-encoded `Payload`. No new
  types for the trace-as-state path. (§2 adds `OracleCalled` /
  `OracleReturned` kinds — those land in the same JSONL.)
- **Identity round-trip.** Reading a trace, running zero turns,
  writing it back produces byte-identical bytes. First of the five
  replay-determinism tests below — it catches drift the moment an
  event payload sprouts non-deterministic serialisation.
- **Forward compat.** `BuildJourney`'s default case ignores unknown
  kinds, so a trace written by a newer kitsoki still replays under
  an older one (up to the point of an unknown kind that mattered
  for state). A header line `{"kind":"SessionHeader","schema_version":1}`
  gives us migration space if we ever need it.

### Testing replay determinism

Trace-as-state is only as good as the guarantee that the trace is
lossless and replay is deterministic. Five test layers, every one a
blocking gate in CI:

1. **Byte-identity round-trip.** For every checked-in cassette
   (`testdata/**`, plus every fixture under
   `stories/*/flows/*.yaml`): load JSONL → write back via
   `JSONLSink` → `bytes.Equal` on the file contents. Catches
   serialisation drift (map ordering, time formatting, trailing
   newlines, integer-vs-float in `any` payloads). Runs in single-
   digit ms per cassette.

2. **Fold idempotence.** `BuildJourney(history)` twice in the same
   process returns deep-equal `(state, world, turn)`. Then a third
   call after a JSONL round-trip returns the same again. Catches
   non-determinism in the fold itself — pointer identity in `world.Vars`,
   map iteration leaking out of effect ordering, slice aliasing
   between events and world.

3. **Live ≡ replay equivalence.** The headline guarantee. For each
   fixture: run the full intent sequence live against an
   `InProcessOracle` stub (deterministic, returns fixed submissions
   keyed by turn) → capture trace A. Replay trace A from JSONL,
   run *the same next intent* live → capture trace B. Then run the
   whole sequence live from scratch in one go → capture trace C.
   Assert `A+B` event stream equals `C` event-for-event, modulo
   wall-clock timestamps which are zeroed in test mode. This is
   what makes phase-0's "trace is the only state" claim load-
   bearing: you can stop and resume anywhere and get the same run.

4. **Crash-mid-write recovery.** Simulate driver death between
   `Append` and `rename` by killing the writer after the `.tmp` is
   created. Reopening the trace must read the *prior* state
   (atomic-rename guarantee), not the partial. Property test:
   N random crash points across a long fixture; every reopen
   yields a fold-equal state to the last fully-committed turn.

5. **Forward-compat / corruption.** Hand-crafted inputs:
   - Unknown `EventKind` line (must be silently ignored by
     BuildJourney; must round-trip byte-identical).
   - `schema_version` higher than the binary supports (must
     error explicitly, not load silently).
   - Truncated last line (must be detected and reported as
     "trace corrupted at line N" — distinct from the
     mid-write recovery case, which has the prior file intact).
   - Duplicate `(turn, seq)` (must error).
   - Out-of-order `(turn, seq)` (must error — events are written
     monotonically).

Plus a property-based suite (`testing/quick` or
`pgregory.net/rapid`): generate random valid intent sequences
against the cloak fixture, run live → JSONL → reload → continue,
assert final `(state, world)` matches the no-reload baseline. This
is the test that catches the gap between "round-trips byte-identical"
and "is actually replayable" — they're not the same property.

Total runtime budget for the whole replay-determinism suite: under
10s on CI. Per [`feedback_fast_tests`](../../../.claude/projects/-home-cloud-user-code-kitsoki/memory/feedback_fast_tests.md)
the test loop has to be tight enough that authors actually run it.

### Open questions

1. **Off-path turn numbering.** The `max(existing)+1` rule exists
   today to avoid sqlite PK collisions. Without sqlite the
   collision can't happen, but the numbering is observable in the
   trace and downstream tools (runstatus, visual analyser) rely on
   it. Keep it verbatim.
2. **Recent-turns lookup.** Today reads from sqlite via a windowed
   query. `JSONLSink.History()` makes the same data available
   in-memory; the harness call site switches to "ask the sink"
   instead of "ask the store."
3. **Default trace path.** `~/.kitsoki/sessions/<app>/<hash>.jsonl`
   is the proposed default for `session continue` / TUI. Confirm
   the directory layout and the hash scheme (sha256 of
   `transport:thread`? full string with collision handling?).
4. **Big traces.** A long agentic burst can produce a multi-MB
   trace. Read-fold-append-rewrite each turn is O(N) on file size.
   At Phase 0 scale (tens of KB per session) irrelevant; if it
   becomes one, switch the write path to append-only with a
   periodic compaction pass. Flagged, not solved.
5. **Migration.** Existing sqlite sessions in `~/.kitsoki/`. One
   ship-time conversion (`kitsoki migrate-sessions`) that dumps
   each session to JSONL and then deletes the sqlite file. Then
   sqlite goes away from the binary.

## 2. Oracle plugin mechanism

### What we have today

`internal/harness/harness.go`:

```go
type Harness interface {
    RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error)
    Close() error
}
```

Three impls:

- `claude_cli.go` — exec the local `claude` binary; assumes
  `mcp.CallToolParams` is what comes back (because the validator MCP
  server is what Claude is talking to).
- `live.go` — anthropic-sdk-go direct.
- `replay.go` — replays a recorded YAML cassette.
- `recording.go` — wraps a live harness to capture the cassette.

Two coupling issues:

- **Return type is MCP-shaped.** `mcp.CallToolParams` bakes in the
  assumption that the LLM speaks MCP and the orchestrator's
  validator is the recipient. An external oracle (autofix's
  `bounded-fixer-agent`, an arbitrary user MCP server) doesn't
  necessarily round-trip through that validator and shouldn't have
  to fake one.
- **Lifecycle is in-process.** `Close()` assumes a subprocess or
  in-process resource. There's no shape for "the oracle is a
  long-running HTTP service I send a request to."

### What we need

A plugin contract that:

- Lets an external process register itself as the oracle for one or
  more `oracle.*` host calls in a story, without compiling into
  kitsoki.
- Preserves the safety stack the calling system already owns
  (env-filter, bash allowlist, budget tracker, audit log) — kitsoki
  must not require the plugin to expose those primitives, only to
  honour a generic ask/return contract.
- Stays backwards-compatible with the existing Harness so
  cloak/oregon-trail/bugfix keep running with `claude_cli` unchanged.

### Shape

Split the abstraction in two:

```go
// internal/harness/harness.go (unchanged for Anthropic-shaped harnesses)
type Harness interface { RunTurn(...); Close() error }

// internal/oracle/oracle.go (new — the plugin contract)
type Oracle interface {
    Ask(ctx context.Context, req AskRequest) (AskResponse, error)
    Close() error
}

type AskRequest struct {
    SessionID   app.SessionID
    TurnNumber  app.TurnNumber
    StatePath   app.StatePath
    PromptText  string                 // fully rendered
    SchemaJSON  json.RawMessage        // optional JSON-Schema for the response
    WithArgs    map[string]any         // the story's `with:` block
    World       world.World            // read-only snapshot
    Deadline    time.Time              // soft; oracle SHOULD honour
}

type AskResponse struct {
    Submission  json.RawMessage        // validated against SchemaJSON if present
    Meta        map[string]any         // tokens, cost, model — opaque to kitsoki
}
```

`AskRequest`/`AskResponse` is the wire format. It's narrow on
purpose — no MCP types, no tool-call concept, just "render a prompt,
get a schema-shaped JSON back." A `Harness`-backed oracle is one
adapter (`oracleFromHarness(h Harness) Oracle`); an MCP-over-HTTP
oracle is another; an in-process Go oracle is the third.

### Transports

Three plugin transports, one contract:

| Transport       | When                                                      | Plugin owns                                    |
|-----------------|-----------------------------------------------------------|------------------------------------------------|
| in-process Go   | Compiled-in custom oracle (tests, stub, deterministic)    | `Oracle` impl                                  |
| subprocess JSON-RPC over stdio | CLI binary the user trusts; lowest ceremony  | One method `oracle.ask`; framing per JSON-RPC 2.0 |
| MCP-over-HTTP   | Long-running external service (autofix's bounded fixer)   | A single `ask` MCP tool; kitsoki is the client |

The story declares the oracle in `hosts:` next to other host
declarations:

```yaml
hosts:
  oracle.claude:                   # default; what cloak/oregon-trail use
    plugin: builtin.claude_cli
  oracle.autofix_fixer:
    plugin: mcp_http
    endpoint: http://localhost:7301/mcp
    tool: ask
```

A room's `oracle:` block (today implicit; resolved to the global
harness) becomes explicit:

```yaml
on_enter:
  - oracle: oracle.autofix_fixer
    with: { task: "{{ args.task }}", repo: "{{ world.repo }}" }
    schema: schemas/fixer-output.json
    bind: world.fixer_result
```

Backwards compat: rooms with no `oracle:` declaration resolve to
`oracle.claude` (the existing default). Existing stories don't
change.

### Lifecycle

Plugin lifecycle stays on kitsoki:

- **in-process:** registered at boot; `Close()` on shutdown.
- **subprocess:** spawned on first `Ask`; reused for the session;
  `Close()` kills the subprocess. Recovery on crash is "respawn on
  next ask"; the trace records the crash as `OracleError`.
- **MCP-over-HTTP:** no kitsoki-owned lifecycle; the plugin is a
  service. Kitsoki opens a client per session, closes it on session
  end. Health is the plugin's problem.

Audit and replay: every `Ask` writes one `OracleCalled` event
(prompt, with-args, schema-ref, deadline) and one `OracleReturned`
event (submission, meta) to the session log. Replay (BuildJourney)
treats both as no-ops — they exist for the audit / runstatus
surfaces, not for state reconstruction. The `Submission` field is
what binds into world; that bind is an `EffectApplied` event the
fold already understands.

This is the seam that lets phase-0 work: autofix runs its
bounded-fixer agent inside its own process, exposes it as
`oracle.autofix_fixer`, and kitsoki sees one `OracleCalled` /
`OracleReturned` pair per turn — exactly the granularity the trace
needs and the runstatus drawer renders well.

### Open questions

1. **Schema validation locus.** Today the MCP validator-server
   validates `submit` calls and the harness errors when the LLM
   skips it. With the Oracle abstraction the validation moves into
   kitsoki: `Oracle.Ask` returns `Submission`; kitsoki validates
   against `SchemaJSON` and surfaces `ClarifyResponse`-equivalent
   errors. Confirm we want validation in kitsoki, not in the plugin
   (the cyber-repo `verify-process-review` validator suggests yes).
2. **Tool calls and side effects inside a single `Ask`.** A long
   agentic burst (autofix's `run_fixer` does many internal bash /
   read / edit tool calls before submitting) is opaque to kitsoki —
   one `OracleCalled` / `OracleReturned` pair. That's deliberate:
   the plugin owns its own tool-call audit and replay; kitsoki
   records only the boundary. Confirm this is the right level of
   detail for the runstatus surface (the alternative — streaming
   tool-call events through the plugin contract — adds a lot of
   shape).
3. **Deadline propagation.** `AskRequest.Deadline` is a soft cap.
   Subprocess and HTTP plugins can ignore it; in-process plugins
   can honour `ctx.Done()`. Acceptable for v1?
4. **Auth and secrets.** Plugin endpoints need credentials (MCP
   bearer token, subprocess env). Today `hosts:` carries no secret
   surface. Lean: plugin block accepts `env:` and `headers:` with
   `${ENV_VAR}` interpolation, evaluated at plugin-init time, never
   logged.

## 3. What this unblocks

Once both land:

- The cyber-repo `pr-refinement` loop ports to a kitsoki story whose
  `executing` room makes one `oracle: oracle.bugfix_refiner` call per
  turn. The driver round-trips the trace JSONL between Bitbucket and
  kitsoki; nothing else moves.
- `claude-autofix-agent`'s phase-0 control inversion uses
  `oracle.autofix_planner` / `oracle.autofix_fixer` / `oracle.autofix_pr_review`
  as three plugin endpoints. The bounded-fixer safety stack stays
  inside autofix's process; kitsoki owns intents, transitions, and
  the trace.
- Runstatus (the merged HTTP+SSE surface) renders any oracle the
  same way, because every plugin transport produces the same
  `OracleCalled` / `OracleReturned` events.

Neither change introduces a new user-visible kitsoki capability.
Both are substrate.

## 4. Phasing

### Phase A — JSONL sink, sqlite removal

1. `EventSink` interface + `JSONLSink` (atomic rewrite, in-memory
   history).
2. Orchestrator + every call site switches to `EventSink`. SQLite
   `Store` usages collapse onto `JSONLSink`.
3. `kitsoki turn --trace path` direct entry point.
4. `session continue` / TUI switch to default JSONL path under
   `~/.kitsoki/sessions/`.
5. `kitsoki migrate-sessions` one-shot for existing sqlite users;
   delete sqlite from the binary afterwards.
6. Full replay-determinism test suite (§1 "Testing replay
   determinism": byte-identity, fold idempotence, live≡replay,
   crash recovery, forward-compat / corruption, property suite).
   All five layers passing is the phase A exit gate.

Exit: kitsoki binary has no `database/sql` dependency; every
entry point produces the same JSONL artefact; replay-determinism
suite green at every commit on CI.

### Phase B — oracle plugin contract

1. `internal/oracle/oracle.go` — `Oracle` / `AskRequest` /
   `AskResponse`.
2. `oracleFromHarness(h Harness) Oracle` adapter so the existing
   `claude_cli` plugs in untouched.
3. `hosts:` plugin block parser; resolution at room dispatch.
4. Subprocess JSON-RPC plugin transport.
5. MCP-over-HTTP plugin transport.
6. `OracleCalled` / `OracleReturned` event kinds + BuildJourney
   ignore + runstatus drawer entries.
7. Conformance test suite the three transports each run against a
   fixed in-process reference oracle.

Exit: a stories-side test declares an MCP-over-HTTP oracle backed by
a Go test server and the room runs end-to-end with no Anthropic SDK
on the call path.

## 5. Out of scope

- The PR-refinement story itself, or any other consumer story.
- The autofix-oracle MCP server (lives in `claude-autofix-agent`).
- Driver design (lives in the consumer repo).
- Multi-tenant oracle hosting, oracle health/observability beyond
  the per-turn audit events.
- Renaming or restructuring the existing `Harness` interface; v1
  keeps it as the in-process default plugin.

## 6. Decision needed

Approve §1 (trace-as-state) and §2 (Oracle plugin) as scoped here.
Both are kitsoki-internal and roughly one week each behind a clean
green light; phase A unblocks phase B (the plugin's `Ask`
events have to land in the JSONL that phase A makes round-trippable).
