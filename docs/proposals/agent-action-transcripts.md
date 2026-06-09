# Tracing: agent action transcripts — full per-call tool-use detail in the web UI

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing (with a runtime sub-slice: an Oracle-interface capability — see Decomposition)
**Epic:**   — standalone (sibling of [`trace-introspection.md`](trace-introspection.md); see "Relationship to trace-introspection")

## Why

**Every oracle verb — not just `task` — produces a rich execution transcript,
and an operator reviewing the run in `runstatus` can see none of it.** A
`host.oracle.task` agent (the bugfix autofix agent, the proposal `draft` agent)
executes a long chain of `Read` / `Grep` / `Edit` / `Bash` / sub-`Task` calls.
But a `decide` call is just as rich: it auto-attaches the kitsoki **mcp-validator**
(`submit()`) and, when bash is enabled, the **`kitsoki-bash` MCP server**
(`internal/host/oracle_decide.go:185,176`), so the model's stream is full of MCP
`tool_use` blocks plus its own reasoning prose before it submits. `ask` and
`converse` likewise emit assistant **thinking/narration** and any tool calls
their agent surface allows. This is not a `task`-only feature — *any* verb whose
operator is the claude CLI produces tool-use, thinking, and MCP-call detail.

The reason it's invisible is that this detail is captured and then either rolled
up to names-only or discarded entirely:

- For `task`, the detail pane shows the rolled-up `oracle.task.complete` with a
  list of tool *names* and a ≤200-rune `input_preview` each
  (`internal/host/oracle_task_transport.go:96`, surfaced by
  `internal/runstatus/trace.go:179` `AggregateTaskDetails`). The full inputs (the
  command that ran, the file edited, the diff), the full outputs (bash stdout,
  file contents read), and the reasoning prose are **dropped**.
- For `ask` / `decide` / `converse`, **nothing** is surfaced — not even names —
  even though the same stream carried the MCP `submit()` call, the thinking, and
  any tool use.

This is the gap dedicated agent-observability tools (Langfuse, Arize Phoenix,
LangSmith) exist to fill: a typed **Tool** observation that shows the full call
input and output, nested under the agent invocation, on a timeline. Our own
in-repo survey ([`.context/langfuse-trace-viewer-comparison.md`](../../.context/langfuse-trace-viewer-comparison.md),
idea #7) flagged "richer I/O rendering … on-demand sidecar fetch" as the cheap
polish we were missing.

The striking part: **we already capture everything we need and throw it away.**
The host runs claude with "stream-json everywhere" (`oracle_runner.go:76,124`):
`runClaudeOneShot` rewrites *every* call to `--output-format stream-json` and
`runClaudeStreamJSON` (`oracle_runner.go:283`) parses each JSONL line into
`ClaudeRun.RawEvents []json.RawMessage` (`oracle_runner.go:37`) — the *complete,
untruncated* claude transcript, including every `tool_use` input, `tool_result`
content block, and assistant thinking block. This holds for `task`, `ask`,
`decide`, and `converse` alike; the lone exception is an explicit
`--output-format json` request (ask_with_mcp's `stdout_json` envelope binding,
`oracle_runner.go:104`), which stays buffered and yields only the final result
envelope. After extracting the final reply, token usage, and the 200-rune
previews, **`RawEvents` is dropped on the floor** — surfaced as names-only for
`task`, and not at all for the other verbs. The "existing claude jsonl" the
operator wants to see is already in our hands at the wire, for nearly every call;
we just never persist it.

Two constraints from the request shape the design:

1. **Do not add this detail to the story trace (yet).** The deterministic
   `*.jsonl` story trace stays lean and replay-stable; the agent transcript is
   *evidence about* a call, not part of the run. It lives in a **sidecar**,
   referenced from the trace by a single pointer — exactly the pattern
   [`trace-annotation.md`](trace-annotation.md) uses for operator annotations,
   and the same mechanism `internal/runstatus/artifact.go:135` already uses to
   keep large prompt text out of the snapshot JSON.
2. **Make it part of the Oracle interface.** Today only the claude backend
   produces this stream. The local-model backend
   (`internal/oracle/local_llm.go`) and any subprocess / MCP-HTTP plugin must be
   able to surface their own backend-native execution detail through one
   contract, so "fully understand the actions of every agent" holds regardless
   of which operator answered the call.

## What changes

One sentence: **capture each oracle call's native execution transcript — the
claude stream-json we already parse, the OpenAI request/response for
`local_llm`, whatever a subprocess plugin chooses to emit — into a per-call
*sidecar transcript artifact* keyed by the deterministic `call_id`, reference it
from `oracle.call.complete` with a single `transcript_ref` pointer (no detail
inlined), and render it in `runstatus` as an on-demand "Agent actions" timeline
drawer with per-tool-call input/output fidelity.**

The Oracle contract gains one optional seam (`AskResponse.Transcript`, the
sidecar-bound sibling of the existing `SubEvents`); the host claude path tees
the `RawEvents` it already has into the same sidecar writer; everything else is
a new consumer of data we start persisting.

## Impact

- **Producers:**
  - `internal/oracle/oracle.go` — `AskResponse` gains an optional
    `Transcript *Transcript` field (out-of-`host` backends carry detail up to
    the dispatcher this way). Mirrors `SubEvents` but is sidecar-bound, not
    trace-bound.
  - `internal/host/oracle_runner.go` — the claude path writes
    `ClaudeRun.RawEvents` (already captured) to the sidecar via a new
    `TranscriptWriter` pulled from context (mirrors the `StreamSink` seam at
    `internal/host/stream_sink.go:89`).
  - `internal/oracle/local_llm.go` — populates `AskResponse.Transcript` from its
    one request/response pair (and any `tool_calls`).
  - Oracle dispatch (where `oracle.call.complete` is emitted) — writes the
    sidecar, attaches `transcript_ref` to the event.
- **Consumers:**
  - `internal/runstatus/server` — new JSON-RPC method
    `runstatus.session.transcript {call_id}` → the sidecar contents.
  - `tools/runstatus/src/data/source.ts` — `getTranscript(sessionId, callId)`
    on both `LiveSource` and `SnapshotSource`.
  - `tools/runstatus/src/components/` — a new `AgentActions.vue` timeline;
    `OracleDetail` gains an "Agent actions" affordance when `transcript_ref` is
    present.
  - `internal/runstatus/artifact.go` — static-export inlining of transcript
    sidecars (same path as prompt sidecars today).
- **Format:** per-call sidecar files under `<run>.transcripts/<call_id>.jsonl`;
  one new **pointer-only** attr (`transcript_ref`) on `oracle.call.complete`. No
  new event *kinds* in the story trace; no inlined detail.
- **Backward compat:** total. A run with no transcripts dir renders exactly as
  today (no "Agent actions" affordance). Old cassettes replay unchanged.
- **Docs on ship:** `docs/tracing/trace-format.md` (the `transcript_ref`
  pointer + the sidecar stream), `docs/tracing/run-status-ui.md` (the drawer),
  `docs/tracing/cassettes.md` (recorded transcript replay).

## Prior art

The request is explicitly "see prior art to justify the investment." The
finding: **dedicated tools solve the *rendering* problem we have, but none
ingests our shape without re-instrumentation, and none captures the
decision-provenance that is our moat** — so we reuse *ideas and a vocabulary*,
not a product. There is a strong case to store the claude transcript verbatim
(it already conforms to a de-facto schema) and conform our cross-backend
*normalized* shape to the OpenTelemetry GenAI conventions so a future "export to
Langfuse/Phoenix" is a serializer, not a re-architecture.

> Sources verified 2026-06-09 against vendor/spec docs (items 1–3, 6) plus the
> in-repo Langfuse survey
> ([`.context/langfuse-trace-viewer-comparison.md`](../../.context/langfuse-trace-viewer-comparison.md)).
> The Claude Code on-disk `.jsonl` schema (item 4) is **partly
> reverse-engineered** — Anthropic publishes no formal schema and has open
> issues acknowledging the stream-json event format is under-documented
> ([claude-code#24612](https://github.com/anthropics/claude-code/issues/24612),
> [#24596](https://github.com/anthropics/claude-code/issues/24596)) — which is
> itself an argument for capturing the **wire** stream we already parse rather
> than depending on the on-disk file.

### Langfuse (the reference design)

Langfuse models a trace as a **typed observation hierarchy** — ten first-class
observation kinds (Event, Span, **Generation**, **Agent**, **Tool**, Chain,
Retriever, Embedding, Evaluator, Guardrail). The *type is semantic*, so the UI
renders a **Tool** observation as its call (input args + output), a Generation
as prompt/tokens/cost (a span carrying a `model` attribute is auto-promoted to a
Generation on ingest). Trace-level `sessionId`/`metadata` propagate to children;
many traces stitch into one **session replay**. Four co-equal view modes (Tree /
Timeline-waterfall / Log / Graph). **License:** core repo is **MIT except the
`ee/` folders** (Enterprise is proprietary); self-hostable via Docker Compose /
Helm. **Ingestion:** native SDKs, framework integrations, *or* a first-class
**OTLP endpoint** (`/api/public/otel`, HTTP/JSON + HTTP/protobuf — **gRPC not yet
supported**) that recognizes `gen_ai.*` and `langfuse.*` attributes. **Cloud
pricing:** Hobby **$0** (50k units/mo), Core **$29/mo**, Pro **$199/mo**,
Enterprise **$2,499/mo**.

*Relevance — and the decisive build-vs-buy fact:* this is exactly the
per-tool-call fidelity we want and its data model is what `AgentActions.vue`
should imitate, **but you cannot POST a raw Claude `.jsonl` to Langfuse** —
ingestion is OTLP-*shaped*, so each record must first be transformed into OTLP
spans (set `model` to get a Generation, set `gen_ai.*` attrs, propagate a
session id). That confirms the posture: store native verbatim; *if* we ever
export, write an OTLP serializer — Langfuse is the *backend* we'd reuse, not the
capture path, and only for getting the data *outside* kitsoki, since inside it
the transcript already sits beside the decision trace in one UI (see
§"Build-vs-reuse synthesis"). Docs:
[OTel integration](https://langfuse.com/integrations/native/opentelemetry),
[data-model](https://langfuse.com/docs/observability/data-model),
[pricing](https://langfuse.com/pricing).

### OpenTelemetry GenAI semantic conventions

OTel has a GenAI semantic-convention set that is **entirely "Development"
(experimental), NOT stable** — opt in via
`OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental`. The client (LLM-call)
span uses `gen_ai.operation.name` (`chat`, `embeddings`, …),
`gen_ai.provider.name` (supersedes the older `gen_ai.system`),
`gen_ai.request.model`, and `gen_ai.usage.input_tokens` /
`gen_ai.usage.output_tokens`. The newer agent/tool conventions add operation
names `create_agent` / `invoke_agent` / `invoke_workflow` / **`execute_tool`**,
with an `invoke_agent` span as parent of child `chat` and `execute_tool` spans
(`gen_ai.agent.name/id`, `gen_ai.tool.definitions`).

*Relevance:* this is the only vendor-neutral vocabulary for "agent → tool →
model" and it is what Phoenix, Langfuse-via-OTLP, Laminar, Honeycomb, and (since
Mar 2026) LangSmith all consume. Risk: the agent/tool span names are the
freshest and most likely to churn — and the exact `gen_ai.tool.name` /
`gen_ai.tool.call.id` field names on `execute_tool` were *not* confirmed on the
spec page fetched (likely-but-unverified). So we **store backend-native detail
verbatim** and treat OTel GenAI as the *normalized projection* the consumer (and
a future exporter) maps to — never the on-disk source of truth. Docs:
[GenAI agent spans](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-agent-spans/),
[GenAI client spans](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/).

### Comparative landscape (build-vs-buy inputs)

| Tool | License / self-host | Ingestion model | Ingest *external* pre-recorded traces? |
|---|---|---|---|
| **Langfuse** | MIT core (`ee/` proprietary); self-host | SDK **or** OTLP (HTTP) | Yes (OTLP) |
| **Arize Phoenix** | **Elastic 2.0** (not OSI — no hosting-as-a-service); self-host | OTLP / OpenInference | Yes (OTLP) — strongest "drop-in self-host" option |
| **LangSmith** | Closed source; cloud + paid self-host | SDK (LangChain-first); **OTLP added Mar 2026** | Via OTLP, else SDK-coupled |
| **Braintrust** | Closed source | SDK (eval-first) | SDK-centric |
| **Helicone** | **Apache 2.0**; self-host | **Proxy** (gateway in front of the LLM API) | Only what flows through the proxy |
| **Laminar** | **Apache 2.0**; self-host | OTLP / OTel | Yes (OTLP) |
| **Traceloop / OpenLLMetry** | **Apache 2.0** (instrumentation libs) | OTLP-emitting instrumentation | It's the *producer* of portable OTLP spans |
| **W&B Weave** | Closed source; cloud | SDK | SDK-centric |
| **Honeycomb** | Closed source; cloud | **OTLP** (general tracing backend) | Yes (generic OTLP) |

Pattern: the **OTLP-ingest** backends (Phoenix, Langfuse, Laminar, Honeycomb,
LangSmith-since-Mar-2026) can render *our* data only if we emit OTel GenAI spans;
the SDK/proxy tools (LangSmith-SDK, Braintrust, Weave, Helicone) want to
instrument the call themselves and don't fit a system that already runs the LLM
through its own harness. OTLP + the OpenLLMetry/OpenInference span shapes are the
portable lingua franca. **None of them captures `machine.gate_decided` /
routing-tier / confidence** — the decision provenance that is kitsoki's
differentiator (`feedback_kitsoki_moat_is_architecture`). So we do not adopt a
product; we build a viewer over data we already hold and keep the *option* to
export to one of these backends via a thin OTLP serializer.

### Claude Code transcript formats (our actual source)

Two forms exist and matter:

- **Wire / stream-json** (what we capture today, `-p --output-format
  stream-json --verbose`): newline-delimited events —
  `{"type":"system","subtype":"init","session_id":…}`,
  `{"type":"assistant","message":{"content":[{"type":"text"|"tool_use",…}]}}`,
  `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":…}]}}`,
  `{"type":"result","subtype":"success","result":…,"usage":…,"total_cost_usd":…}`.
  `oracle_runner.go:269` already documents and parses exactly these shapes
  (and the research confirms a `parent_tool_use_id` on assistant events that
  marks **subagent/nested** tool use — useful for the drawer's nesting). This is
  the recommended source: in-process, real-time, no session-persistence
  dependency, already captured, and immune to the encoded-path/file-rotation
  fragility of the on-disk file.
- **On-disk session transcript** (`~/.claude/projects/<cwd-slug>/<session-id>.jsonl`,
  where `<cwd-slug>` is the working dir with path separators → hyphens):
  richer per-line records (`uuid`, `parentUuid`, `sessionId`, `cwd`, `gitBranch`,
  `version`, `message` in Anthropic shape, top-level `toolUseResult`,
  `isSidechain` for sub-agents, `summary` compaction records). The oracle agent
  path *does* mint/resume a claude session (`oracle_ask_with_mcp.go:631`
  `--session-id` / `--resume`), so this file exists for `task` runs and is a
  possible *enrichment* source. But it is **partly reverse-engineered** (no
  official schema; `type` values and `toolUseResult` shape drift across CLI
  versions), so it is a non-goal for v1 — we capture the wire stream instead.

### Existing OSS Claude Code viewers (reuse candidates)

Several OSS tools already render Claude Code transcripts, proving the rendering
is well-trodden and the jsonl shape parseable:

- **claude-code-log** (daaain, **MIT**, Python) — `.jsonl` → static HTML, zoomable
  timeline grouped by time, collapsible tool calls, special `TodoWrite` /
  `WebSearch` / `Task` rendering.
- **claude-code-trace** (delexw, **MIT**, Rust + React/Tauri) — browses
  `~/.claude/projects/`, shows tool calls with token counts, MCP-call detection,
  live tail; desktop/web/TUI/Docker.
- **claude-code-viewer** (d-kimuson) / **claude-JSONL-browser** (withLinda) /
  **claude-trace-viewer** (bkrabach) — further web viewers; the last reads
  `@mariozechner/claude-trace` logs (a tool that intercepts the API for
  wire-level capture, distinct from on-disk readers).

*Why we don't drop one in:* they (a) read the *on-disk* transcript, not our wire
capture and `call_id`; (b) ship standalone HTML/apps, not a component for our Vue
3 runstatus SPA with live SSE; and (c) have no concept of the kitsoki
decision/turn context the drawer must nest under. **`claude-code-log`'s parser is
MIT and copyable** — we use it as a *reference for the field set* (and the
per-tool rendering choices: collapsible input/output, diff for `Edit`,
command+output for `Bash`) and build the viewer native.

### OpenAI / llama.cpp tool shape (cross-backend confirmation)

In the OpenAI-compatible chat-completions response that `local_llm` already
parses (`internal/oracle/local_llm.go:99`), tool calls surface as a
`message.tool_calls` array — OpenAI's shape is `{id, type:"function",
function:{name, arguments}}` where `arguments` is a **JSON-encoded string**, and
streaming deltas arrive as `delta.tool_calls[]` fragments concatenated by
`index`. **Caveat the research surfaced:** `llama.cpp`'s own examples sometimes
return a *flattened* `{name, arguments}` (no `function` wrapper, no `id`), and
whether the nested form appears depends on the chat template / model / build —
so an extractor must tolerate both `tc.function.name` and a flat `tc.name`.

This confirms the seam is genuinely general (not claude-specific) **but that
each backend needs a small adapter**: Anthropic `tool_use{id,name,input(object)}`
vs OpenAI `function.arguments(string)` vs llama.cpp-flattened. They normalize to
one internal `{call_id, name, args, result}` shape (see the consumer-normalizer
in §"Web consumer"). Today `local_llm` serves schema-shaped `decide`/routing and
makes no tool calls, so its transcript is the single request/response pair; the
adapter future-proofs a tool-using local model. Docs:
[llama.cpp function-calling](https://github.com/ggml-org/llama.cpp/blob/master/docs/function-calling.md).

### Build-vs-reuse synthesis

- **Render:** build a native `AgentActions.vue` that imitates Langfuse's typed
  **Tool** observation; do not embed an external HTML viewer.
- **Store:** persist the **claude stream-json verbatim** to the sidecar (zero
  transform, already a de-facto schema); have `local_llm`/subprocess emit their
  native shapes.
- **Normalize:** the runstatus consumer maps native → a small **OTel-GenAI-shaped
  normalized model** for uniform rendering and a cheap future "export to
  Phoenix/Langfuse via OTLP" — but OTel is a *projection*, never the source of
  truth, because the conventions are still experimental.
- **Don't adopt a product:** the decisive reason is **product unity, not a
  technical gap**. kitsoki is *one* presentation in which the agent transcript
  sits directly alongside the workflow/decision trace that produced it — they are
  already a single product. Exporting to Langfuse would split the operator's view
  across two tools and sever the transcript from the `machine.gate_decided` /
  routing-tier / confidence context that gives it meaning. (Secondarily: none of
  these tools captures that decision provenance, and all want to own
  instrumentation we already do ourselves.) We therefore build the viewer native
  and reproduce — over data we already hold — the rendering features that make
  Langfuse worth reaching for (waterfall, typed observations, error surfacing,
  cost accrual; see §"Producers & consumers" → Consumer (SPA)), so the operator
  never has a reason to leave.

The one external dependency worth *not* reinventing is the **OTLP + GenAI
semantic-convention vocabulary** (it buys ingestion into every backend above with
no per-tool connector); the things to keep bespoke are the **viewer** and the
**decision-aware trace model** — neither has an off-the-shelf equivalent.

## Event / format model

The story trace gains **only a pointer**. The detail lives in the sidecar.

Pointer on `oracle.call.complete` (story `*.jsonl`):

```jsonc
{ "msg": "oracle.call.complete", "ts": "2026-06-09T18:22:04Z",
  "attrs": { "call_id": "2d8e4fbb0a78646d", "verb": "task", "agent": "autofix",
             "model": "claude-…", "duration_ms": 8123,
             "meta": { "input_tokens": 1200, "output_tokens": 640, "cost_usd": 0.04 },
             "transcript_ref": { "format": "claude-stream-json",
                                 "path": "transcripts/2d8e4fbb0a78646d.jsonl",
                                 "events": 42, "schema_version": 1 } } }
```

Sidecar `<run>.transcripts/2d8e4fbb0a78646d.jsonl` — backend-native, verbatim,
one event per line (claude stream-json shown):

```jsonc
{"type":"system","subtype":"init","session_id":"…"}
{"type":"assistant","message":{"content":[{"type":"text","text":"I'll read the failing test first."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01…","name":"Read","input":{"file_path":"foo_test.go"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01…","content":"package foo\n\nfunc TestBar(t *testing.T){…}"}]}}
{"type":"result","subtype":"success","result":"Fixed the off-by-one.","usage":{"input_tokens":1200,"output_tokens":640},"total_cost_usd":0.04}
```

The same sidecar is written for the **non-`task`** verbs, and shows the detail
that is invisible today — here a `decide` call's thinking and its MCP
`validator.submit` tool_use (the `kitsoki-bash` MCP tool would appear the same
way):

```jsonc
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"The error trace points at the retry loop, so the bug class is concurrency."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_02…","name":"mcp__validator__submit","input":{"choice":"concurrency","confidence":0.82}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_02…","content":"accepted"}]}}
{"type":"result","subtype":"success","result":"concurrency","usage":{"input_tokens":900,"output_tokens":210},"total_cost_usd":0.02}
```

Capture is **verb-agnostic** — the claude tee fires for whatever verb ran; the
`verb` attr on the pointer (`"task"` above, `"decide"` here) is the only thing
that differs. The renderer types `thinking` blocks as reasoning,
`mcp__<server>__<tool>` calls as MCP tool observations, and the
`mcp__validator__submit` of a `decide` as a **Guardrail** (pass/fail +
confidence).

Per-event **capture-time offsets** (ms since call start) power the waterfall.
They are *not* in the claude wire events, so rather than mutate the verbatim
stream we write them to a tiny parallel `<call_id>.timings` sidecar (event-index
→ offset). The transcript stays byte-verbatim and copyable by an off-the-shelf
claude jsonl parser; the timings sidecar is the kitsoki-side addition, and like
the transcript it is recorded into the cassette and replayed verbatim (see
§Determinism).

| Artifact | When written | Key fields |
|---|---|---|
| `transcript_ref` (trace attr) | at `oracle.call.complete` | `format`, `path`, `events`, `schema_version` — pointer only |
| `<call_id>.jsonl` (sidecar) | per call, if transcript present | backend-native events, verbatim |
| `<call_id>.timings` (sidecar) | per call, with the transcript | event-index → ms-offset; powers the waterfall, kept out of the verbatim stream |

### The `decide` submit → validate → nudge cycle (a first-class case)

The generic retry/error surfacing above (`api_retry`, `tool_result.is_error`)
does **not** capture the most diagnostic part of an `oracle.decide` call, because
of how the loop is built (`internal/host/oracle_decide.go:483`
`runDecideWithValidatorRetryLoop`):

- A single decide `call_id` is **several claude sessions**, not one — an outer
  `--resume` loop (`decideMaxOuterIterations=3`) wrapping an inner validator
  schema-retry budget (`decideDefaultMaxRetries=5`).
- The model calls `mcp__validator__submit` with a typed verdict; the
  mcp-validator checks it against `schema:` and, when a `validator:` block is
  present, a sandboxed `post_cmd` re-checks it semantically
  (`runDecideSandboxValidator:635`). Either can **reject**.
- On rejection (or an abandoned turn), the host injects a **nudge**
  (`renderDecideNudge` / `decideAbandonmentNudgeTemplate:758,59`) carrying the
  last rejection reason, as the **stdin of the next `--resume`**
  (`oracle_decide.go:523`).

The trap: in `-p` stream-json, the input prompt is *not* echoed back as an event,
so a pure `RawEvents` tee captures the `submit`, the reasoning, and the result —
but the **host's nudge, and the rejection reason inside it, are invisible**. That
is exactly the round-trip the operator wants to see. So the decide sidecar is
assembled at the loop level, not by a blind tee:

- The `TranscriptWriter` **accumulates across all outer iterations** under the one
  `call_id` — the sidecar is the concatenation of each `--resume` session's
  verbatim events, in order.
- At each outer-iteration boundary the host writes a synthetic, clearly-marked
  `_kitsoki`-typed event recording the **nudge it injected** and the **rejection
  that triggered it** (schema reject vs sandboxed `post_cmd` reject). Like the
  timings sidecar these are *additive* — claude's own events stay verbatim, and an
  off-the-shelf parser that keys on known `type` values skips them.
- The **tool-bypass** deviation (`tool_bypassed` / `verdict_recovered_from`,
  `emitDecideJournal:355`) is mirrored into the transcript as a banner row, not
  only into the trace Meta.

The drawer renders this as one legible arc on the waterfall:
`submit(verdict)` → **Guardrail: rejected** (schema / semantic reason) →
**host nudge** (a distinct, host-injected coaching row — not a model turn, not a
tool result) → re-`submit` → **Guardrail: accepted**. Iteration boundaries are
marked, so "it took 2 nudges and a sandbox reject before it submitted a valid
verdict" is visible at a glance.

```jsonc
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0a…","name":"mcp__validator__submit","input":{"decision":"refund","amount":"lots"}}]}}
{"_kitsoki":"validator_reject","source":"schema","reason":"amount: expected number, got string \"lots\""}
{"_kitsoki":"nudge","outer_iter":1,"text":"Your previous turn ended… The last submission attempt was rejected: amount: expected number…"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0b…","name":"mcp__validator__submit","input":{"decision":"refund","amount":49.0}}]}}
{"_kitsoki":"validator_accept","outer_iter":1}
{"type":"result","subtype":"success","result":"refund","usage":{"input_tokens":1400,"output_tokens":320},"total_cost_usd":0.03}
```

The Oracle-interface seam:

```go
// oracle.go — AskResponse gains (sidecar-bound sibling of SubEvents):
type Transcript struct {
    Format string            // "claude-stream-json" | "openai-chat" | "<plugin>"
    Events []json.RawMessage // backend-native, verbatim, one per line
}
// AskResponse.Transcript *Transcript `json:"transcript,omitempty"`
```

For the in-`host` claude path, the data is richer than would survive a JSON
round-trip, so it tees `ClaudeRun.RawEvents` straight into a `TranscriptWriter`
pulled from context (the `StreamSink` pattern), and the dispatcher attaches the
`transcript_ref` — `AskResponse.Transcript` is the path for *out-of-host*
backends (`local_llm`, subprocess, MCP-HTTP).

## Determinism

This is the load-bearing section — a transcript full of `Bash` outputs and file
reads is the least deterministic data in the system, so it must **never be
re-executed on replay; it is replayed from the cassette.**

- `call_id` is already deterministic (`internal/host/callid.go`,
  `sha256("oracle-call:"+appID+":"+episodeID)[:16]` under replay), so the
  sidecar filename is stable and pairs with the trace pointer by value alone.
- **Cassette fidelity:** `EpisodeOracle` (`internal/testrunner/cassette.go:75`)
  gains an optional `transcript:` block (or an `!include`d sidecar). On replay,
  the recorded transcript is written to the sidecar verbatim — the run is
  byte-identical, and the "Agent actions" drawer shows the recorded actions. No
  live tool ever runs.
- Live capture writes the real `RawEvents`; record mode (`new_episodes`) folds
  them into the episode. This matches the existing usage/cost capture flow
  (`recordOracleUsage`) — transcript is one more recorded-and-replayed artifact.
- **Capture-time stamps are recorded, never regenerated.** The waterfall's
  per-step latency comes from offsets stamped at the `TranscriptWriter` tee and
  folded into the episode alongside the transcript; on replay they are written to
  the `.timings` sidecar verbatim, so the waterfall is byte-stable. Wall-clock is
  never re-derived from replay timing.
- **The cassette-vs-live diff is a *consumer* of two transcripts, not a new
  capture path.** It compares a fresh live sidecar against the recorded one;
  determinism is unaffected (replay alone never produces a live transcript to
  diff — drift detection is an explicit, opt-in re-run).
- Golden contract: replaying a recorded cassette produces a byte-identical
  sidecar (transcript + timings).

## Producers & consumers

- **Producer (claude):** `oracle_runner.go` already holds the complete stream;
  the base case is "if a `TranscriptWriter` is in ctx, hand it `RawEvents`" — no
  new parsing. The one richer producer is the **decide loop**
  (`oracle_decide.go:483`): it accumulates `RawEvents` across every `--resume`
  outer iteration under the one `call_id`, and writes the synthetic `_kitsoki`
  nudge / validator-reject boundary events the raw stream omits (see §"The
  `decide` submit → validate → nudge cycle").
- **Producer (local_llm / subprocess):** populate `AskResponse.Transcript`; the
  dispatcher persists it. Subprocess/MCP-HTTP plugins MAY leave it nil (degrade
  to today's behavior).
- **Consumer (server):** `runstatus.session.transcript {call_id}` reads the
  sidecar lazily; never loaded into the snapshot wholesale (a `task` run can be
  megabytes).
- **Consumer (SPA):** `OracleDetail` shows an "Agent actions (42)" affordance
  when `transcript_ref` is present; clicking fetches and renders the drawer. The
  same drawer serves every verb, and static export inlines the sidecar the same
  way `artifact.go:135` inlines prompts. What it renders reproduces — over data
  we already hold — the features that make a dedicated trace tool worth reaching
  for, so the operator never leaves (this is the concrete payoff of the
  product-unity argument in §"Build-vs-reuse synthesis"):
  - **Typed rows.** assistant **thinking/reasoning**, **Tool** input/output
    (collapsible), **MCP** calls (`mcp__<server>__<tool>`), and the result with
    tokens/cost. The `decide` validator `submit` is typed as a **Guardrail**
    (Langfuse's own observation kind) — accept/reject + confidence rendered as
    pass/fail, not a generic tool call — tying the drawer to the decision
    provenance that is our moat. The full submit → reject → **host nudge** →
    re-submit → accept arc, including iteration boundaries, is rendered as
    described in §"The `decide` submit → validate → nudge cycle".
  - **Timeline waterfall.** per-step latency from capture-time stamps (see
    §Determinism), so the operator sees where the wall-clock went, not just an
    ordered list. This is Langfuse's headline view mode.
  - **Error / retry highlighting.** `tool_result.is_error`, error `result`
    subtypes, and `api_retry` events (already modeled as `StreamEvent.Subtype`,
    `stream_sink.go:43`) surface as failed steps — the first thing an operator
    looks for.
  - **Running cost/token accrual.** accumulated per assistant turn, not only the
    terminal total — surfaces "this `decide` retried twice before submitting."
  - **Session rollup.** a run's many `call_id`s group under their turn/room into
    one "all agent actions" view — the kitsoki analog of Langfuse session replay,
    the unit an operator actually reasons about.
  - **Live cassette-vs-live diff.** when a fresh live run is compared against the
    cassette it was cut from, the drawer diffs the two transcripts and flags
    tool-path **drift** — a capability *no external tool has*, because none has
    deterministic replay (see §Determinism). This is the determinism-frontier
    payoff the original request was about.

## Backward compatibility

- Old story traces: no `transcript_ref` → no affordance. Unchanged.
- Old cassettes: no `transcript:` block → replay as today, drawer absent.
- New `AskResponse.Transcript` is optional; existing oracle backends and the
  subprocess/HTTP wire are unaffected when it is nil.
- Story `*.jsonl` schema is **unchanged except one additive optional attr** — no
  new event kinds, honoring "don't add detail to the trace yet."

## Fixtures / golden traces

- A flow fixture running a `host.oracle.task` agent under a cassette whose
  episode carries a recorded `transcript:` — assert the sidecar is written
  byte-for-byte and `transcript_ref.events` matches.
- A runstatus snapshot test: `OracleDetail` renders the affordance + count from
  `transcript_ref`; `AgentActions.vue` unit test renders a tool_use/tool_result
  pair (input collapsible, output shown).
- A `local_llm` unit test asserting `AskResponse.Transcript` round-trips the
  single request/response.

## Tasks

```
## 1. Oracle-interface seam (runtime sub-slice)
- [ ] 1.1 Add Transcript type + optional AskResponse.Transcript field (oracle.go)
- [ ] 1.2 Add TranscriptWriter ctx seam (mirror stream_sink.go)
- [ ] 1.3 local_llm populates Transcript from its req/resp

## 2. Capture & reference
- [ ] 2.1 Claude path tees ClaudeRun.RawEvents to the sidecar writer (+ capture-time stamps → .timings)
- [ ] 2.2 Dispatcher writes <call_id>.jsonl + attaches transcript_ref (pointer only)
- [ ] 2.3 Deterministic filename via call_id; sidecar dir layout
- [ ] 2.4 Decide loop: accumulate RawEvents across --resume iterations under one call_id; write synthetic _kitsoki nudge/validator-reject boundary events; mirror tool_bypassed banner

## 3. Replay fidelity
- [ ] 3.1 EpisodeOracle gains optional transcript: block (+ !include)
- [ ] 3.2 Replay writes recorded transcript verbatim; golden byte-identical test
- [ ] 3.3 record mode (new_episodes) captures live transcript into episode

## 4. Web consumer
- [ ] 4.1 runstatus.session.transcript {call_id} RPC + lazy sidecar read
- [ ] 4.2 source.ts getTranscript on Live + Snapshot sources
- [ ] 4.3 AgentActions.vue timeline (typed rows, collapsible I/O); OracleDetail affordance
- [ ] 4.4 artifact.go inlines transcript + timings sidecars for static export
- [ ] 4.5 Timeline waterfall from .timings; error/retry + running-cost rows
- [ ] 4.6 Guardrail typing of validator submit; session rollup across a run's call_ids
- [ ] 4.7 Cassette-vs-live transcript diff (drift detection)
- [ ] 4.8 (optional) normalize native → OTel-GenAI-shaped model in the consumer

## 5. Document
- [ ] 5.1 docs/tracing/{trace-format,run-status-ui,cassettes}.md; trim/delete this proposal
```

## Open questions

1. **Store verbatim or normalize at write time?** Lean: **store backend-native
   verbatim**, normalize in the consumer. Keeps capture zero-cost and the
   source-of-truth honest; lets the normalized (OTel-GenAI) shape evolve without
   rewriting sidecars.
2. **Sidecar granularity** — one file per `call_id` vs one appended
   `<run>.transcripts.jsonl` with a `call_id` column. Lean: **one file per
   call** (lazy fetch by name, no scanning; mirrors prompt sidecars).
3. **Enrich from the on-disk claude transcript** (`isSidechain` sub-agents,
   richer `toolUseResult`)? Lean: **non-goal for v1** — the wire capture is
   sufficient and avoids a session-persistence dependency.
4. **Emit OTel GenAI spans as an export now, or just shape the normalized model
   that way?** Lean: **shape only**; ship the exporter when someone asks to push
   to Phoenix/Langfuse.
5. **The buffered `--output-format json` path** (ask_with_mcp's `stdout_json`
   envelope binding, `oracle_runner.go:104`) is the one call shape that does *not*
   stream per-event detail — it returns only a result envelope, so its transcript
   would be a single synthesized event (final text + usage), not a per-tool
   timeline. Lean: **synthesize a one-event transcript from the envelope** so the
   drawer is uniform across verbs, and note "buffered — no per-tool detail" in the
   UI. Switching that binding to stream-json (to recover full detail) is a
   separate change with its own envelope-contract risk, out of scope here.

## Non-goals

- **Inlining transcript detail into the story `*.jsonl`.** Explicit constraint;
  the trace gets a pointer only.
- **The waterfall / event-typing / decision-first work** — that is
  [`trace-introspection.md`](trace-introspection.md) and its children; this
  proposal is the *evidence-depth* axis, orthogonal to that *trace-shape* axis.
- **TUI rendering of agent actions** — the TUI already shows live
  `task.tool.start/end` breadcrumbs; a full transcript pane in the terminal is a
  follow-up, not v1.
- **Shipping an OTel exporter / a Langfuse integration.** We shape toward it;
  building it is out of scope.
- **Live-tail of in-flight calls in the web drawer.** The `StreamSink` seam
  (`stream_sink.go`) already does this for the TUI; wiring it to the web drawer
  is a clean follow-up, not v1.
- **Inline media rendering** of image/audio tool outputs (cf. the visual-outputs
  media artifacts) — a later enhancement once the typed-row drawer lands.
- **"Open in playground" / re-run-from-a-step** (Langfuse/LangSmith offer it) —
  it fights deterministic replay, so it's out.
- **Full-text search across a run's transcripts** — a later indexing concern.

## Relationship to trace-introspection

`trace-introspection.md` improves how the *existing* trace is *projected*
(waterfall, typed observations, decision-first detail, annotation). This
proposal adds a *new evidence layer* (the discarded agent transcript) under a
*new sidecar*, surfaced by one pointer. They compose: a typed `oracle-call`
observation (introspection) that, when it's an agent `task`, opens into this
transcript drawer. If we'd rather manage them together, this becomes a fifth
slice of that epic; kept standalone here because its producer/format/determinism
story is self-contained.

## Decomposition (if accepted as an epic)

| Slice | Kind | Scope | Depends on |
|---|---|---|---|
| transcript-oracle-seam | runtime | `AskResponse.Transcript`, `TranscriptWriter` ctx seam, `local_llm` producer | — |
| transcript-capture | tracing | claude tee + sidecar write + capture-time `.timings` + `transcript_ref`; cassette replay fidelity | seam |
| transcript-web | tracing | RPC + `source.ts` + `AgentActions.vue` (typed rows, waterfall, error/retry, cost accrual, guardrail typing, session rollup, cassette-vs-live diff) + static-export inlining | capture |

Sequencing: runtime seam → capture/determinism → web consumer.
