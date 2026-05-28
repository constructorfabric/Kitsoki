# Kitsoki changes for external orchestration

> **Phase A complete (2026-05-28).** §1 (Trace-as-state), §1.3
> (Runstatus alignment), §3 (Reconciliation with host-cassette work),
> and §5 Phase A (JSONL sink, sqlite removal) have been implemented and
> are documented in:
> - [`docs/trace-format.md`](../trace-format.md) — JSONL schema, event
>   vocabulary, `EventSink` contract, `call_id` derivation, replay guarantees.
> - [`docs/cli/turn.md`](../cli/turn.md) — `kitsoki turn` flags and exit codes.
> - [`docs/developer-guide.md`](../developer-guide.md) §6.1 — updated trace docs.
>
> Only §2 (Oracle plugin contract) / Phase B remains. The sections below
> cover that remaining work exclusively.

---

Two kitsoki-internal changes are needed before kitsoki can host
externally-driven story runs like the cyber-repo `pr-refinement`
loop or the `claude-autofix-agent` phase-0 control inversion:

1. **Sessions fully replayable from a JSONL trace.** *(Shipped in Phase A.)*
2. **Oracle plugin mechanism.** The `Harness` interface today is
   Anthropic-SDK-shaped (`mcp.CallToolParams` return value, claude-CLI
   subprocess assumption). To let an external system register itself
   as the LLM (autofix's bounded fixer; a CI-failure responder; a
   user's own MCP server), kitsoki needs a typed plugin contract that
   admits in-process, subprocess JSON-RPC, and MCP-over-HTTP oracles
   without each one re-implementing the harness lifecycle.

---

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
    SubEvents   []store.Event          // optional: plugin-emitted sub-events
                                       // appended verbatim to the JSONL between
                                       // OracleCalled and OracleReturned. Plugins
                                       // that have meaningful internal tool calls
                                       // (autofix's bounded-fixer bash/read/edit
                                       // bursts) MAY surface them this way; v1
                                       // plugins MAY leave it nil.
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
(full prompt, with-args, schema-ref, deadline, turn, state_path,
call_id) and one `OracleReturned` event (full submission, meta,
duration_ms, the matching call_id) to the session JSONL. These
events ARE the start/complete pair the runstatus SPA already pairs
by `call_id` (`TraceTimeline.vue:371`) — no exporter-side
synthesis from a sidecar journal. Replay
(BuildJourney) treats both as no-ops — they exist for the audit /
runstatus surfaces, not for state reconstruction. The `Submission`
field is what binds into world; that bind is an `EffectApplied`
event the fold already understands.

This subsumes `internal/host/oracle_journal.go` (the sidecar
SQLite journal) and `internal/runstatus/oracle_attrs.go` (the
`MergeOracleBodyIntoAttrs` shim that re-shapes journal bodies into
SPA attrs). Both delete in phase B; the prompt/response body fields
live directly on the events.

This is the seam that lets phase-0 work: autofix runs its
bounded-fixer agent inside its own process, exposes it as
`oracle.autofix_fixer`, and kitsoki sees one `OracleCalled` /
`OracleReturned` pair per turn — exactly the granularity the trace
needs and the runstatus drawer renders well.

### Resolutions

1. **Schema validation locus — kitsoki validates.** `Oracle.Ask`
   returns a raw `Submission`; kitsoki validates against
   `AskRequest.SchemaJSON` and surfaces `ClarifyResponse`-equivalent
   errors on failure. Plugins are dumb pipes; the validation that
   the MCP validator-server does today moves in-process, where the
   story author can reason about it. Plugins MAY pre-validate as a
   fast-fail UX but kitsoki is the source of truth.
2. **Tool-call granularity — boundary by default, sub-events
   optional.** One `OracleCalled` / `OracleReturned` pair per
   `Ask` is the minimum the kitsoki trace records. A plugin
   that has meaningful internal tool calls (autofix's
   `run_fixer` bash/read/edit bursts) MAY populate
   `AskResponse.SubEvents` with its own `store.Event` values
   (using its own `Kind`s under a plugin-namespaced prefix,
   e.g. `oracle.autofix_fixer.bash.called`). Kitsoki appends
   those events verbatim to the JSONL between the
   `OracleCalled` and `OracleReturned` lines, preserving the
   audit fidelity the trace claims. Plugins that don't care
   leave `SubEvents` nil and the boundary-only behaviour is
   recovered. This makes the "perfect deterministic trace"
   claim load-bearing all the way through the plugin boundary
   without forcing every plugin to surface internals.
3. **Deadline — soft cap, plugin MAY ignore.** `AskRequest.Deadline`
   is a hint. Subprocess and HTTP plugins are best-effort;
   in-process plugins SHOULD honour `ctx.Done()`. Kitsoki enforces a
   hard cap via `ctx` cancel and records `OracleError` if the
   plugin overruns.
4. **Auth and secrets — `env:` + `headers:` with `${VAR}` interpolation.**
   The plugin block in `hosts:` accepts `env:` (subprocess
   transport) and `headers:` (MCP-over-HTTP transport) maps whose
   values support `${ENV_VAR}` interpolation. Substitution is
   evaluated at plugin-init time, never logged, never written to
   the trace.

### Testing the oracle contract

The trace round-trip guarantees (§1, now in `docs/trace-format.md`)
and the oracle contract need their own failure-mode suite because the
plugin boundary is where untrusted, slow, or buggy external code meets
the deterministic core. Every case below is a blocking CI gate.

#### Schema validation (kitsoki-side)

- **Malformed JSON submission.** Plugin returns bytes that don't
  parse. Kitsoki surfaces a `ClarifyResponse`-equivalent error
  with the parse error inline; writes `OracleReturned` with the
  raw bytes preserved (for forensics) and a `validation_error`
  field set.
- **Schema-invalid submission.** Valid JSON, fails schema check.
  Same path as malformed: error surfaced, raw response preserved.
- **Submission with extra fields.** Schema is closed by default
  (`additionalProperties: false`) — extras fail validation.
- **Missing required field with `null` value.** Distinct from
  field-absent. Both fail validation but produce distinct error
  messages.
- **Schema not provided.** `SchemaJSON` is nil → kitsoki skips
  validation; raw `Submission` binds to world. Tested explicitly
  so the no-schema case is intentional, not an accidental bypass.
- **Schema with `$ref` to a sibling file.** Resolution is
  filesystem-rooted at the story directory; out-of-tree
  references fail at story-load time, not at Ask time.

#### Lifecycle and timeouts

- **Plugin crash before any output.** Subprocess transport: child
  exits with non-zero, no stdout. Trace records `OracleError`
  with exit code and stderr tail; `OracleReturned` is NOT
  written.
- **Plugin crash after partial output.** Subprocess writes a
  partial JSON-RPC frame, then exits. Kitsoki records
  `OracleError` with the partial bytes preserved.
- **Plugin crash after sub-events, before final response.**
  Sub-events already appended to the JSONL are kept; `OracleError`
  closes the call. Replay treats this exactly like a fresh crash
  recovery.
- **HTTP plugin connection refused.** Immediate error; `OracleError`
  with the dial error.
- **HTTP plugin TLS handshake failure.** Error preserves the TLS
  error chain.
- **HTTP plugin slow response.** Past `AskRequest.Deadline`:
  `ctx` cancel propagates; `OracleError` records the deadline
  overrun.
- **In-process plugin ignores `ctx.Done()`.** Hard timer in
  kitsoki stops waiting; records `OracleError`. In-process
  plugins MUST honour ctx.
- **Plugin returns after timeout.** Late response arrives after
  `OracleError` has been written. The late bytes are discarded;
  the trace does not retroactively rewrite.

#### Sub-events

- **Sub-events with kind outside namespace.** Refused.
- **Sub-event with `call_id` mismatching the parent.** Refused.
- **Sub-event with payload too large.** Each sub-event is subject
  to the PIPE_BUF=4096 per-line size limit. Oversize fails the
  whole `AskResponse`.
- **Nil sub-event slice vs empty slice.** Both produce zero
  sub-event lines on disk; tested separately.

#### `call_id` derivation and collisions

- **Deterministic derivation produces stable IDs.** Same input →
  same `call_id` across runs.
- **Live + cassette collision.** A hand-picked `episodeID:matchIdx`
  whose hash collides with a live `turn:state_path:seq` — kitsoki
  detects the collision at write time.
- **`replay: any` matchIdx continuity across resume.** See
  `docs/trace-format.md` §5 for the derivation rule. After reload,
  `matchIdx` continues from where it left off.

#### Auth and secrets

- **`${VAR}` missing.** Plugin init fails fast with a clear error.
  Never zero-values silently.
- **`${VAR}` value contains `${`.** Single-pass substitution;
  the literal `${` passes through verbatim.
- **Secret value not in trace.** Plugin header/env values MUST NOT
  appear in the JSONL trace. Key names MAY appear.

#### Conformance — same fixture, four transports

One in-process reference oracle, wrapped four ways:

1. **In-process Go.** Implement `Oracle` directly.
2. **Subprocess JSON-RPC.** Wrap in a Go binary speaking JSON-RPC
   over stdio.
3. **MCP-over-HTTP.** Wrap in an httptest server exposing a single
   `ask` MCP tool.
4. **Cassette.** Pre-record the reference oracle's responses.

All four must produce byte-identical JSONL modulo `Meta`, `ts`,
and `transport`. This is the test that proves the contract is
transport-agnostic.

---

## 4. What this unblocks

Once phase B lands:

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

Neither §1 (shipped) nor §2 introduces a new user-visible kitsoki
capability. Both are substrate.

---

## 5. Phase B — oracle plugin contract

1. `internal/oracle/oracle.go` — `Oracle` / `AskRequest` /
   `AskResponse`.
2. `oracleFromHarness(h Harness) Oracle` adapter so the existing
   `claude_cli` plugs in untouched.
3. `hosts:` plugin block parser; resolution at room dispatch.
4. Subprocess JSON-RPC plugin transport.
5. MCP-over-HTTP plugin transport.
6. `OracleCalled` / `OracleReturned` event kinds are *already*
   landed in phase A; phase B only adds the plugin contract that
   produces them via `Oracle.Ask` instead of the in-process
   handler. `call_id` derivation per the trace-format doc: 1:1 with
   each exchange, `key = episodeID + ":" + matchIdx` for cassette
   and `key = turn:state_path:seq` for live.
7. `AskResponse.SubEvents` support: kitsoki appends them
   verbatim to the JSONL between the `OracleCalled` and
   `OracleReturned` lines. Sub-event `Kind`s use a
   plugin-namespaced prefix (`oracle.<verb>.<plugin-internal>`)
   so the SPA's subsystem chip logic keeps working.
8. Cassette path switches `writeOracleJournalEntry` to
   `sink.Append` only. Cassette `§6.3` validator constraint
   (`replay: any` + `oracle:` forbidden) relaxes: multiple matches
   produce multiple event pairs with *distinct* `call_id`s
   (different `matchIdx`) sharing one `episode_id`.
9. Delete in one commit: `internal/host/oracle_journal.go`,
   `internal/journal/sqlite.go` (if no remaining consumers), and
   any remaining cassette journal-write code paths. The binary as a
   whole loses sqlite.
10. Full oracle-contract test suite (§2 "Testing the oracle
    contract" above): all sub-cases green. Exit gate: a stories-side
    test declares an MCP-over-HTTP oracle backed by a Go test server
    and the room runs end-to-end with no Anthropic SDK on the call
    path.

---

## 6. Out of scope

- The PR-refinement story itself, or any other consumer story.
- The autofix-oracle MCP server (lives in `claude-autofix-agent`).
- Driver design (lives in the consumer repo).
- Multi-tenant oracle hosting, oracle health/observability beyond
  the per-turn audit events.
- Renaming or restructuring the existing `Harness` interface; v1
  keeps it as the in-process default plugin.

---

## 7. Decision needed

Approve §2 (Oracle plugin) as scoped here. Phase A (§1, trace-as-state)
is complete. Realistic estimate for phase B: **~1–1.5 weeks**. Phase B
is the only remaining blocker for the control-inversion use cases
described in §4.
