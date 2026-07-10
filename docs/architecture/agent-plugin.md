# Agent Plugin Contract

> **Status:** the operator-facing specification for the Agent plugin
> mechanism. The plugin contract is the seam that lets an external system
> (a CI-failure responder, a bounded fixer agent, a user's own MCP server)
> register itself as the LLM behind a kitsoki agent call without compiling
> into kitsoki. See [`docs/tracing/trace-format.md §5`](../tracing/trace-format.md) for the JSONL
> events each call produces.

An **agent plugin** is the component that receives a rendered prompt and
returns a structured JSON submission.  Kitsoki owns the schema validation,
trace writing, sub-event ordering, and lifecycle — the plugin is a dumb pipe
that must honour a narrow `ask / return` contract.

> **Plugins vs providers.** This doc covers *which component answers* an agent
> call. To keep the built-in claude component but point it at a different
> Anthropic-compatible backend (model + env) per invocation, see
> [`agent-providers.md`](../guide/agents/providers.md) — that mechanism is orthogonal
> and composes with the verbs below.

---

## 1. `agent_plugins:` block YAML reference

Agent plugins are declared under the top-level `agent_plugins:` key (a map of
agent alias → declaration). This is separate from the `hosts:` allow-list.

```yaml
agent_plugins:
  agent.claude:                    # default (injected when absent)
    plugin: builtin.claude_cli

  agent.my_fixer:
    plugin: subprocess
    command: /usr/local/bin/my-agent
    args: ["--mode", "fast"]
    env:
      API_KEY: "${MY_AGENT_API_KEY}"   # ${VAR} substituted at story load

  agent.remote_fixer:
    plugin: mcp_http
    endpoint: http://localhost:7301/mcp
    tool: ask                           # defaults to "ask" if omitted
    headers:
      Authorization: "Bearer ${FIXER_TOKEN}"
```

### Supported plugin types

The `plugin:` value must be one of the five names below; any other value fails
at story-load time.

| Plugin type           | Required fields | When to use                                                 |
|-----------------------|-----------------|-------------------------------------------------------------|
| `builtin.claude_cli`  | _(none)_        | Default — exec the local `claude` binary; backwards compat. |
| `builtin.inprocess`   | _(none)_        | Compiled-in Go agent. Declared in YAML but the `Agent` impl must be injected in code via `RegisterInProcess` before dispatch; tests and deterministic stubs. |
| `subprocess`          | `command:`      | External binary speaking JSON-RPC 2.0 over stdio.           |
| `mcp_http`            | `endpoint:`     | Long-running HTTP service exposing an `ask` MCP tool.       |
| `builtin.local_llm`   | `model:` **or** `endpoint:` | Cheap, offline, schema-bounded backend for routing and small `decide` gates — a local llama.cpp server over OpenAI HTTP. See [§9 Local model backend](#9-local-model-backend). |

> The **cassette** transport (pre-recorded, deterministic replay) is a fourth
> conformance transport but is *not* declarable via `agent_plugins:`. It is
> wired in test code via `testrunner.NewCassetteAgent` and registered with
> `reg.Register`; see the four-transport conformance test in
> `internal/testrunner/agent_conformance_test.go`.

### Auth and secrets

- `env:` (subprocess) and `headers:` (mcp_http) support `${VAR}`
  interpolation.
- Substitution is **single-pass, left-to-right**: if the resolved value itself
  contains `${`, that literal `${` passes through verbatim and is **not**
  re-expanded.
- Unset variables cause story load to fail fast with a clear error message
  (`agent_plugins.<name>: env var <VAR> referenced in env.<key> not set`).
- Resolved secret values are **never written to the trace JSONL**.  Key names
  MAY appear in trace metadata but values do not.

---

## 2. Calling a plugin agent from a room

An agent call is a host invoke effect — `invoke: host.agent.<verb>` — with an
`agent:` field naming the plugin alias declared in `agent_plugins:`. The
verbs are `ask`, `decide`, `extract`, `task`, and `converse`.

```yaml
states:
  executing:
    on_enter:
      - invoke: host.agent.decide      # ask | decide | extract | task | converse
        agent: agent.my_fixer         # resolves to the agent_plugins: entry
        with:
          prompt: prompts/fix.md        # rendered prompt (path or inline text)
          schema: schemas/fixer-output.json
          args:
            task: "{{ args.task }}"
            repo: "{{ world.repo }}"
        bind:
          fixer_result: submission      # bind the validated Submission to world
```

- **`agent:` is opt-in.** When omitted, the call runs on the built-in
  `agent.claude` (claude_cli) path exactly as before — existing stories that
  bind `stdout` or `submitted` are unchanged. Naming a plugin via `agent:`
  routes the call through the plugin dispatch path described here, whose result
  exposes the keys in the table below (note: **not** `stdout`).
- `schema:` (inside `with:`) is an optional path to a JSON Schema file relative
  to the story directory. When set, kitsoki validates `AskResponse.Submission`
  against it and produces an `AgentError` on failure.
- `bind:` maps world variables to keys of the dispatch result:

  | bind source  | value                                                        |
  |--------------|--------------------------------------------------------------|
  | `submission` | the validated `Submission`, parsed (nil when no `schema:`).  |
  | `submitted`  | alias for `submission` (back-compat with existing rooms).    |
  | `meta`       | opaque plugin metadata (tokens, cost, model).                |
  | `ok`         | `true` on success.                                           |
  | `exit_code`  | `0` on success.                                              |

### Evidence-backed task selection

An individual `host.agent.*` invoke may carry `selection:` metadata that pins
the intended harness profile/model/effort for that bounded task:

```yaml
- invoke: host.agent.decide
  id: merge_judge
  with:
    agent: judge
    prompt: prompts/judge_merge.md
    schema: schemas/judge_verdict.json
  bind:
    llm_verdict: submitted
  selection:
    profile: synthetic-codex
    model: syn:small:text
    effort: low
    evidence: evals/reports/merge_judge/latest.json
    fallback_profile: claude
```

The `selection:` block is evidence metadata first. `kitsoki eval` validates the
referenced story-local benchmark and report; runtime selection remains
conservative until strict pin validation and dispatch integration are enabled.

---

## 3. Wire types: `AskRequest` / `AskResponse`

See [`docs/tracing/trace-format.md §5`](../tracing/trace-format.md) for the JSONL event shapes
that surround each agent call.  The Go types are in
`internal/agent/agent.go`.

**`AskRequest`** — what kitsoki sends to the plugin:

| Field        | Type                   | Description                                      |
|--------------|------------------------|--------------------------------------------------|
| `session_id` | string                 | Session identifier.                              |
| `turn`       | int                    | Turn number (1-based).                           |
| `state_path` | string                 | State machine path at time of dispatch.          |
| `verb`       | string                 | Agent verb (`ask`, `decide`, `extract`, `task`, `converse`). |
| `prompt`     | string                 | Fully rendered prompt text.                      |
| `schema`     | JSON object (nullable) | Optional JSON Schema the `Submission` must satisfy. |
| `with`       | JSON object (nullable) | Story's `with:` block (opaque to kitsoki).       |
| `world`      | JSON object            | Read-only world snapshot.                        |
| `deadline`   | RFC3339Nano timestamp  | Soft cap; plugin SHOULD honour but is not killed if it overruns. |
| `call_id`    | string                 | Deterministic 16-hex-char identifier.            |

**`AskResponse`** — what the plugin returns:

| Field        | Type                   | Description                                      |
|--------------|------------------------|--------------------------------------------------|
| `submission` | JSON bytes (nullable)  | The agent's answer. Validated by kitsoki.       |
| `meta`       | JSON object (nullable) | Opaque to kitsoki: tokens, cost, model, etc.     |
| `sub_events` | array of store.Event   | Optional plugin-internal events (see §4).        |

---

## 4. Sub-events contract

A plugin MAY populate `AskResponse.SubEvents` to surface internal tool calls
(e.g. bounded-fixer bash/read/edit bursts) into the kitsoki trace.

**Constraints** (enforced by kitsoki; violation produces `AgentError` and no
sub-events land):

- **Namespace:** every sub-event `Kind` must start with the plugin name + `.`
  (e.g. plugin `agent.my_fixer` → sub-event kinds must start with
  `agent.my_fixer.`).
- **`call_id`:** every sub-event `call_id` must match the parent
  `AgentCalled.call_id`.
- **Size:** sub-events can be arbitrary size (no limits).
- **Timestamp:** kitsoki re-stamps each sub-event `ts` with its own monotonic
  clock.  The plugin's claimed `ts` is discarded; all sub-event timestamps
  fall within `[AgentCalled.ts, AgentReturned.ts)`.
- **Atomicity:** on any violation, `AgentCalled` is already written; kitsoki
  writes `AgentError` (not `AgentReturned`) and no sub-events land.

---

## 5. Schema validation locus

Kitsoki is the **validation authority**.  Plugins are dumb pipes.

- Plugins MAY pre-validate for fast-fail UX; kitsoki ALWAYS validates.
- Malformed JSON submission → `AgentError{Kind: "schema_invalid"}` with
  parse error in `Detail`.
- Schema-invalid JSON → `AgentError{Kind: "schema_invalid"}` with path +
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
| `subprocess`  | Spawned on first Ask; reused for the session; `Close()` kills it. Crash → respawn on next Ask; trace records the crash as `AgentError`. |
| `mcp_http`    | No kitsoki-owned lifecycle; plugin is a service. Kitsoki opens a client per session, closes it on session end. |
| `builtin.local_llm` | **Managed (`model:`)**: a llama.cpp sidecar is fetched-on-first-use and spawned lazily on the first Ask, shared across calls, and terminated on `Close()` (SIGTERM, then SIGKILL after a grace window). **Endpoint (`endpoint:`)**: attaches to an already-running server; never fetches, spawns, or kills. |
| `cassette`    | In-process; deterministic replay; no external process.        |

**Deadline** is a soft cap (`AskRequest.Deadline`).  Kitsoki enforces a hard
cap via context cancellation and records `AgentError{Kind: "deadline_exceeded"}`
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
| `transport_error`               | HTTP/TLS/dial error on `mcp_http` or `builtin.local_llm` transport (4xx, missing/empty choices, unparseable body). |

---

## 8. Examples

### subprocess agent

```yaml
agent_plugins:
  agent.my_analyzer:
    plugin: subprocess
    command: /opt/analyzers/code-analyzer
    args: ["--schema-dir", "schemas/"]
    env:
      GITHUB_TOKEN: "${GITHUB_TOKEN}"
```

### mcp_http agent

```yaml
agent_plugins:
  agent.remote_fixer:
    plugin: mcp_http
    endpoint: http://fixer-service:7301/mcp
    tool: ask
    headers:
      Authorization: "Bearer ${FIXER_SERVICE_TOKEN}"
```

### cassette agent (testing)

The cassette transport is not declared in `agent_plugins:` (the loader rejects
`plugin: cassette`). Instead, a flow's cassette fixture records the agent
exchange and the test wires `testrunner.NewCassetteAgent` into the registry.
The fixture episode carries the agent block:

```yaml
episodes:
  - id: fix_ep_01
    match:
      handler: agent.fixer
    agent:
      verb: task
      response: '{"files_changed": ["main.go"], "result": "fixed"}'
    response:
      data: {}
```

---

## 9. Local model backend

`builtin.local_llm` is an opt-in, additive backend for the **small,
high-frequency** decisions — semantic-routing's LLM tier and schema-bounded
`decide` gates — where the `claude` default is heavier than the job needs.
It is one `Agent` behind the same `Ask` contract as every other transport, so
any verb can target it with no handler change; `agent.claude`
(`builtin.claude_cli`) stays the injected default and the backend for every
call that does not name `agent.local`.

It speaks **OpenAI-compatible HTTP** (`POST /v1/chat/completions`) to a local
[llama.cpp](https://github.com/ggml-org/llama.cpp) `llama-server`. One
`agent_plugins:` entry:

```yaml
agent_plugins:
  agent.local:
    plugin: builtin.local_llm
    model: qwen2.5-1.5b-instruct   # managed mode: fetched-on-first-use, spawns a sidecar
    grammar: true                  # best-effort schema → grammar constraint (see below)
    # port: 8080                   # optional; managed sidecar bind port
    # server_bin: /path/to/llama-server  # optional; skip the binary fetch
    # endpoint: http://127.0.0.1:8080     # endpoint mode: attach to a running server, never spawn
    # api_key_env: GLM_API_KEY     # optional; bearer token for an authenticated endpoint (see below)
    # json_schema: true            # optional; OpenAI-native response_format: json_schema for any schema
```

A decl must set **either** `model:` (managed mode) **or** `endpoint:`
(attach mode), or story load fails fast with
`requires model: or endpoint:`.

### Authenticated & OpenAI-native endpoints

Two knobs extend `endpoint:` mode beyond the local llama.cpp sidecar to any
OpenAI-compatible API (e.g. GLM-5.2):

- **`api_key_env:`** names an environment variable holding a bearer token,
  sent as `Authorization: Bearer <key>` on every request. The value is the
  *env-var name*, not the secret; the secret is read at call time so it never
  has to be materialized in YAML. An unset var is a hard `transport_error`,
  not a silent unauthenticated request. Empty (the default) disables auth.
- **`json_schema: true`** sends `response_format: {type: "json_schema",
  strict: true}` for **any** schema present, independent of the
  `grammar:`/grammar-subset gate below. This is the OpenAI-native
  constrained-output path — use it for endpoints that honour `json_schema`
  natively, including for schemas outside the GBNF subset (discriminated
  unions with `if`/`then`/`const`/`enum`, e.g. `agent.codeact`'s step
  schema). `AskResponse.Meta["json_schema"]` records whether it was applied.

`json_schema` wins over `grammar` when both are set (the GBNF concept is
meaningless to a non-llama.cpp endpoint). The `claude`-CLI default and
managed-mode `local_llm` usage are unchanged when both are omitted.

The primary consumer is `agent.codeact`'s direct-API transport — see
[prior-art.md §4c "Transports: CLI vs direct API"](./prior-art.md#transports-cli-vs-direct-api)
for the selection rule and the `agents:` + `agent_plugins:` dual-declaration
config that points a `host.agent.codeact` effect at one of these endpoints.

### Grammar is best-effort, `ValidateSubmission` is the guarantee

When `grammar: true` and the call carries a JSON Schema, the backend asks
llama.cpp to constrain decoding to that schema
(`response_format: {type: "json_schema", …}`), which strongly biases the model
toward a schema-valid answer on the first try and collapses the
validate-and-retry loop **for schemas inside llama.cpp's supported grammar
subset** (flat objects, scalar fields, enums, simple typed arrays;
`stories/pr-refinement/schemas/judge_verdict.json` is comfortably inside it).

This is a *bias*, not a guarantee, for two reasons the design handles rather
than assumes away:

- **Fail-open.** If llama.cpp cannot translate the schema it logs the error,
  generates **unconstrained**, and returns **HTTP 200** — there is no
  server-side rejection.
- **Out-of-subset constructs.** llama.cpp's json-schema-to-grammar omits
  `$ref`/`$defs`, `uniqueItems`, `not`, `if`/`then`/`else`,
  `dependentSchemas`, `contains`, and `anyOf`/`oneOf` alongside sibling
  `properties`/`type`; `pattern` only works anchored `^…$`.

So `ValidateSubmission` (the schema check kitsoki already runs on every
agent answer — see [§5](#5-schema-validation-locus)) is the **only**
structural guarantee here, not a backstop. `AskResponse.Meta["grammar"]`
records whether the constraint was *actually* applied (false on a fail-open
response), so a run is auditable in runstatus.

### Load-time schema-subset check

To keep authors from hitting a silent fail-open at runtime, a `decide` effect
whose `agent:` alias resolves to a `grammar: true` `builtin.local_llm` plugin
has its `with.schema` checked against the supported subset **at story load**.
A schema using an unsupported construct fails fast with a message naming the
plugin, the schema, and the offending construct. (Initial scope: the `decide`
verb, whose schema is a `with.schema` path. `extract`/`ask` source their
schemas differently and are a follow-up; templated schema paths are skipped
because they cannot be resolved statically.)

### Validation-reject fallback to `agent.claude`

When `ValidateSubmission` rejects a local-model `Submission` (a fail-open
output, or a schema the grammar could not fully express), retrying the *same*
local backend would reproduce the deterministic failure. Instead the verb
handler **falls back to `agent.claude` for that one call**, exactly once, no
same-backend retry. The fallback reuses the **same `call_id`**, so it is one
`agent.call.*` pair, and the substitution is recorded inline:

- on fallback **success**, the `AgentReturned` payload carries
  `Meta["fallback_of"] = "<original plugin>"` plus a `substitution` object
  `{reason: "schema_invalid", original_plugin, fallback_plugin: "agent.claude"}`;
- on fallback **failure**, the `AgentError` payload carries the same
  `substitution` object.

The fallback fires **only** for `builtin.local_llm` backends. External plugins
(`subprocess`, `mcp_http`) still fail hard on `schema_invalid` — they are not
silently substituted.

### Sidecar lifecycle and acquisition (managed mode)

In managed mode the sidecar is **zero-touch**: on the first `agent.local`
call kitsoki fetches the pinned `llama-server` release archive for the host
platform and the model's GGUF weights into the user cache dir
(`~/Library/Caches/kitsoki` on macOS, `~/.cache/kitsoki` on Linux;
`{bin,models}` subdirs, overridable via `KITSOKI_CACHE_DIR`),
sha256-verifying each against a baked pin before use, then spawns `llama-server`
bound to `127.0.0.1` and health-gates on `GET /health` before the first POST.
The release archive is extracted into one per-release directory (the binary plus
its bundled `libggml*`/`libllama*` shared libraries, SONAME symlinks preserved),
and the server is launched with that directory on `LD_LIBRARY_PATH`. The first
fetch logs an explicit `downloading <artifact> …` disclosure so a multi-GB
download is never silent. Nothing binary is committed or bundled into the release.

**Older-glibc Linux (RHEL/Rocky 9, etc.).** The upstream `llama-server` Linux
build needs a newer C++ runtime (`GLIBCXX_3.4.30`, GCC 12) than these distros
ship (they cap at `3.4.29`). The fetcher probes the system `libstdc++` and, only
when it is too old, also fetches a sha-pinned compatible `libstdc++` (conda-forge
`libstdcxx-ng`) into the same per-release directory so it resolves ahead of the
system copy via `LD_LIBRARY_PATH`. On a sufficiently new system, and on macOS
(which links its own C++ runtime), this step is skipped. This keeps the
zero-touch promise on enterprise Linux without any manual `sudo`/toolchain
install. Verified end-to-end on a glibc-2.34 box (cold fetch of all three
artifacts → spawn → grammar-constrained `decide` → schema-valid verdict → warm
run reuses the cache with no re-download).

**Opt-in / no surprise downloads.** The whole subsystem is dormant unless a story
*both* declares a `builtin.local_llm` plugin *and* routes a call to it; a default
app on `agent.claude` never constructs the plugin, never spawns, and never
touches the cache or network. The download is strictly lazy — it happens only
inside an `Ask` already dispatched to a managed `agent.local`, so declaring the
plugin without routing to it fetches nothing.

`llama-server` serves a single decode slot sequentially by default, so the
sidecar launches with `--parallel N`; routing fires on nearly every turn, so
an under-provisioned slot count is the likely source of stacked latency.

To pre-warm the cache for offline/CI boxes — running the *same* fetch-and-verify
path ahead of time so no turn hits a runtime download — use the make targets:

```
make fetch-llama-server          # fetch the llama-server binary
make fetch-models                # fetch the default model's weights
make fetch-models MODEL=<id>     # fetch a specific model
```

`endpoint:` mode bypasses all fetching, spawning, and the make targets: it
attaches to a server you already run and never owns its lifecycle.

The `linux/amd64` and `darwin/arm64` pins (llama.cpp release b9444 + default
Qwen2.5-1.5B GGUF, plus the libstdc++ shim on older-glibc Linux) are filled and
proven end-to-end, so managed mode works zero-touch on both Linux/amd64 and
Apple Silicon. The macOS archive's Metal-enabled dylibs resolve via
`@loader_path`, so the same flat-extraction layout works without any extra
environment. Other platforms have no pin yet and fail loudly — use `endpoint:`
mode there.

**Measured throughput** (10-core CPU Rocky/RHEL 9.4 VM, Qwen2.5-1.5B Q4_K_M,
`--parallel 4`): generation ~6.9 tok/s, prompt ~56 tok/s, weights load ~2-3s; a
cold managed `decide` (~1.1 GB download + load + decode) ~24-28s, a warm one
~12-13s. On Apple Silicon with Metal the same default model serves a warm
grammar-constrained `decide` in well under a second (~0.7s measured on an M-series
laptop). **Calibration note:** llama.cpp grammar constrains JSON *shape*, not
numeric *range*, so a small model may emit e.g. `confidence: 95` for a `0..1`
field — the decide prompt should state field scales, and any out-of-range output
is caught by `ValidateSubmission`, which trips the `agent.claude` fallback.

### Embedding sidecar mode

A `builtin.local_llm` sidecar can be repurposed as an **embedding-only**
server by passing `WithExtraArgs("--embeddings", "--pooling", "mean")` when
constructing the `Sidecar`. The `--embeddings` flag restricts the server to
the `/v1/embeddings` endpoint; this is a **separate server instance** (separate
port, separate GGUF) from any chat sidecar — not a flag on the chat one.

`LocalEmbedder` (`internal/agent/local_llm_embed.go`) implements
`embed.Embedder` against this endpoint. It applies the model+role task prefix
from the `modelPrefixes` map and POSTs to `/v1/embeddings`. Vectors are not
re-normalized client-side — llama-server with `--pooling mean` returns
L2-normalized vectors.

**Supported models and their GGUFs:**

- `nomic-embed-text-v1.5` — default; Matryoshka truncation to 256 dims;
  asymmetric prefixes (`search_document:` / `search_query:`).
- `bge-small-en-v1.5` — 384 dims; query-only prefix (`search_query:`).

**Lifecycle mirrors `agent.local`:**

- **Endpoint mode** (`endpoint:` set): attach to an already-running embedding
  server; no fetching or spawning. Works today on any platform.
- **Managed mode** (`model:` set): fetch, sha256-verify, and spawn lazily on
  the first `Embed` call. Requires filled model pins in `fetch.go` — these are
  currently placeholders for the embedding GGUFs; use endpoint mode until they
  are proven.

The embedding sidecar is consumed by `host.agent.search`
(`internal/host/agent_search.go`) and the embedding routing tier
(`internal/orchestrator/embed_tier.go`). For full substrate details, including
the `embed.Index`, `embed.Store`, prefix discipline, and `FakeEmbedder` test
seam, see [`docs/architecture/embeddings.md`](embeddings.md).

---

*For the trace event format produced by agent calls, see
[`docs/tracing/trace-format.md §5`](../tracing/trace-format.md).*
