# Oracle Plugin Contract

> **Status:** the operator-facing specification for the Oracle plugin
> mechanism. The plugin contract is the seam that lets an external system
> (a CI-failure responder, a bounded fixer agent, a user's own MCP server)
> register itself as the LLM behind a kitsoki oracle call without compiling
> into kitsoki. See [`docs/trace-format.md §5`](trace-format.md) for the JSONL
> events each call produces.

An **oracle plugin** is the component that receives a rendered prompt and
returns a structured JSON submission.  Kitsoki owns the schema validation,
trace writing, sub-event ordering, and lifecycle — the plugin is a dumb pipe
that must honour a narrow `ask / return` contract.

---

## 1. `oracle_plugins:` block YAML reference

Oracle plugins are declared under the top-level `oracle_plugins:` key (a map of
oracle alias → declaration). This is separate from the `hosts:` allow-list.

```yaml
oracle_plugins:
  oracle.claude:                    # default (injected when absent)
    plugin: builtin.claude_cli

  oracle.my_fixer:
    plugin: subprocess
    command: /usr/local/bin/my-oracle
    args: ["--mode", "fast"]
    env:
      API_KEY: "${MY_ORACLE_API_KEY}"   # ${VAR} substituted at story load

  oracle.remote_fixer:
    plugin: mcp_http
    endpoint: http://localhost:7301/mcp
    tool: ask                           # defaults to "ask" if omitted
    headers:
      Authorization: "Bearer ${FIXER_TOKEN}"
```

### Supported plugin types

The `plugin:` value must be one of the four names below; any other value fails
at story-load time.

| Plugin type           | Required fields | When to use                                                 |
|-----------------------|-----------------|-------------------------------------------------------------|
| `builtin.claude_cli`  | _(none)_        | Default — exec the local `claude` binary; backwards compat. |
| `builtin.inprocess`   | _(none)_        | Compiled-in Go oracle. Declared in YAML but the `Oracle` impl must be injected in code via `RegisterInProcess` before dispatch; tests and deterministic stubs. |
| `subprocess`          | `command:`      | External binary speaking JSON-RPC 2.0 over stdio.           |
| `mcp_http`            | `endpoint:`     | Long-running HTTP service exposing an `ask` MCP tool.       |

> The **cassette** transport (pre-recorded, deterministic replay) is a fourth
> conformance transport but is *not* declarable via `oracle_plugins:`. It is
> wired in test code via `testrunner.NewCassetteOracle` and registered with
> `reg.Register`; see the four-transport conformance test in
> `internal/testrunner/oracle_conformance_test.go`.

### Auth and secrets

- `env:` (subprocess) and `headers:` (mcp_http) support `${VAR}`
  interpolation.
- Substitution is **single-pass, left-to-right**: if the resolved value itself
  contains `${`, that literal `${` passes through verbatim and is **not**
  re-expanded.
- Unset variables cause story load to fail fast with a clear error message
  (`oracle_plugins.<name>: env var <VAR> referenced in env.<key> not set`).
- Resolved secret values are **never written to the trace JSONL**.  Key names
  MAY appear in trace metadata but values do not.

---

## 2. Calling a plugin oracle from a room

An oracle call is a host invoke effect — `invoke: host.oracle.<verb>` — with an
`oracle:` field naming the plugin alias declared in `oracle_plugins:`. The
verbs are `ask`, `decide`, `extract`, `task`, and `converse`.

```yaml
states:
  executing:
    on_enter:
      - invoke: host.oracle.decide      # ask | decide | extract | task | converse
        oracle: oracle.my_fixer         # resolves to the oracle_plugins: entry
        with:
          prompt: prompts/fix.md        # rendered prompt (path or inline text)
          schema: schemas/fixer-output.json
          args:
            task: "{{ args.task }}"
            repo: "{{ world.repo }}"
        bind:
          fixer_result: submission      # bind the validated Submission to world
```

- **`oracle:` is opt-in.** When omitted, the call runs on the built-in
  `oracle.claude` (claude_cli) path exactly as before — existing stories that
  bind `stdout` or `submitted` are unchanged. Naming a plugin via `oracle:`
  routes the call through the plugin dispatch path described here, whose result
  exposes the keys in the table below (note: **not** `stdout`).
- `schema:` (inside `with:`) is an optional path to a JSON Schema file relative
  to the story directory. When set, kitsoki validates `AskResponse.Submission`
  against it and produces an `OracleError` on failure.
- `bind:` maps world variables to keys of the dispatch result:

  | bind source  | value                                                        |
  |--------------|--------------------------------------------------------------|
  | `submission` | the validated `Submission`, parsed (nil when no `schema:`).  |
  | `submitted`  | alias for `submission` (back-compat with existing rooms).    |
  | `meta`       | opaque plugin metadata (tokens, cost, model).                |
  | `ok`         | `true` on success.                                           |
  | `exit_code`  | `0` on success.                                              |

---

## 3. Wire types: `AskRequest` / `AskResponse`

See [`docs/trace-format.md §5`](trace-format.md) for the JSONL event shapes
that surround each oracle call.  The Go types are in
`internal/oracle/oracle.go`.

**`AskRequest`** — what kitsoki sends to the plugin:

| Field        | Type                   | Description                                      |
|--------------|------------------------|--------------------------------------------------|
| `session_id` | string                 | Session identifier.                              |
| `turn`       | int                    | Turn number (1-based).                           |
| `state_path` | string                 | State machine path at time of dispatch.          |
| `verb`       | string                 | Oracle verb (`ask`, `decide`, `extract`, `task`, `converse`). |
| `prompt`     | string                 | Fully rendered prompt text.                      |
| `schema`     | JSON object (nullable) | Optional JSON Schema the `Submission` must satisfy. |
| `with`       | JSON object (nullable) | Story's `with:` block (opaque to kitsoki).       |
| `world`      | JSON object            | Read-only world snapshot.                        |
| `deadline`   | RFC3339Nano timestamp  | Soft cap; plugin SHOULD honour but is not killed if it overruns. |
| `call_id`    | string                 | Deterministic 16-hex-char identifier.            |

**`AskResponse`** — what the plugin returns:

| Field        | Type                   | Description                                      |
|--------------|------------------------|--------------------------------------------------|
| `submission` | JSON bytes (nullable)  | The oracle's answer. Validated by kitsoki.       |
| `meta`       | JSON object (nullable) | Opaque to kitsoki: tokens, cost, model, etc.     |
| `sub_events` | array of store.Event   | Optional plugin-internal events (see §4).        |

---

## 4. Sub-events contract

A plugin MAY populate `AskResponse.SubEvents` to surface internal tool calls
(e.g. bounded-fixer bash/read/edit bursts) into the kitsoki trace.

**Constraints** (enforced by kitsoki; violation produces `OracleError` and no
sub-events land):

- **Namespace:** every sub-event `Kind` must start with the plugin name + `.`
  (e.g. plugin `oracle.my_fixer` → sub-event kinds must start with
  `oracle.my_fixer.`).
- **`call_id`:** every sub-event `call_id` must match the parent
  `OracleCalled.call_id`.
- **Size:** each sub-event is subject to the `PIPE_BUF` = 4096 byte line
  limit.  Oversize sub-events fail the whole `AskResponse`.
- **Timestamp:** kitsoki re-stamps each sub-event `ts` with its own monotonic
  clock.  The plugin's claimed `ts` is discarded; all sub-event timestamps
  fall within `[OracleCalled.ts, OracleReturned.ts)`.
- **Atomicity:** on any violation, `OracleCalled` is already written; kitsoki
  writes `OracleError` (not `OracleReturned`) and no sub-events land.

---

## 5. Schema validation locus

Kitsoki is the **validation authority**.  Plugins are dumb pipes.

- Plugins MAY pre-validate for fast-fail UX; kitsoki ALWAYS validates.
- Malformed JSON submission → `OracleError{Kind: "schema_invalid"}` with
  parse error in `Detail`.
- Schema-invalid JSON → `OracleError{Kind: "schema_invalid"}` with path +
  constraint in `Detail`.
- `schema:` absent or nil → validation skipped; raw `Submission` binds to
  world.
- `$ref` within the schema is resolved against the story's `schemas/`
  directory (filesystem-rooted).  Out-of-tree references fail at story-load
  time, not at Ask time.

---

## 6. Lifecycle

| Transport     | Lifecycle                                                      |
|---------------|----------------------------------------------------------------|
| `builtin.*`   | In-process; `Close()` on orchestrator shutdown.               |
| `subprocess`  | Spawned on first Ask; reused for the session; `Close()` kills it. Crash → respawn on next Ask; trace records the crash as `OracleError`. |
| `mcp_http`    | No kitsoki-owned lifecycle; plugin is a service. Kitsoki opens a client per session, closes it on session end. |
| `cassette`    | In-process; deterministic replay; no external process.        |

**Deadline** is a soft cap (`AskRequest.Deadline`).  Kitsoki enforces a hard
cap via context cancellation and records `OracleError{Kind: "deadline_exceeded"}`
if the plugin overruns the context deadline.

**Plugin returns after timeout:** the late response is discarded; the trace is
not retroactively rewritten.

---

## 7. Error kinds

| `AskError.Kind`                 | When                                                  |
|---------------------------------|-------------------------------------------------------|
| `schema_invalid`                | Submission fails JSON parse or JSON Schema validation. |
| `plugin_crash`                  | Subprocess exited non-zero; stderr captured in `Detail`. |
| `deadline_exceeded`             | Context deadline exceeded.                            |
| `sub_event_namespace_violation` | Sub-event Kind outside plugin namespace.              |
| `sub_event_call_id_mismatch`    | Sub-event call_id ≠ parent call_id.                   |
| `sub_event_oversize`            | Sub-event serialises to > 4096 bytes.                 |
| `transport_error`               | HTTP/TLS/dial error on `mcp_http` transport.          |

---

## 8. Examples

### subprocess oracle

```yaml
oracle_plugins:
  oracle.my_analyzer:
    plugin: subprocess
    command: /opt/analyzers/code-analyzer
    args: ["--schema-dir", "schemas/"]
    env:
      GITHUB_TOKEN: "${GITHUB_TOKEN}"
```

### mcp_http oracle

```yaml
oracle_plugins:
  oracle.remote_fixer:
    plugin: mcp_http
    endpoint: http://fixer-service:7301/mcp
    tool: ask
    headers:
      Authorization: "Bearer ${FIXER_SERVICE_TOKEN}"
```

### cassette oracle (testing)

The cassette transport is not declared in `oracle_plugins:` (the loader rejects
`plugin: cassette`). Instead, a flow's cassette fixture records the oracle
exchange and the test wires `testrunner.NewCassetteOracle` into the registry.
The fixture episode carries the oracle block:

```yaml
episodes:
  - id: fix_ep_01
    match:
      handler: oracle.fixer
    oracle:
      verb: task
      response: '{"files_changed": ["main.go"], "result": "fixed"}'
    response:
      data: {}
```

---

*For the trace event format produced by oracle calls, see
[`docs/trace-format.md §5`](trace-format.md).*
