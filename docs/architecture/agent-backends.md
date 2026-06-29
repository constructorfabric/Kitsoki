# Agent Backends (`--agent claude | copilot | codex`)

> **Status:** operator + contributor reference for the pluggable coding-agent
> CLI behind every agent verb. A **backend** chooses *which CLI kitsoki forks*
> to run a `host.agent.*` call and intent routing: Anthropic's `claude`
> (default), GitHub's `copilot`, or OpenAI's `codex`. It is orthogonal to
> [providers](./agent-providers.md) (which retarget the `claude` CLI's
> endpoint) and to [agent plugins](./agent-plugin.md) (which choose which
> component answers).

Kitsoki's agent verbs (`host.agent.{ask,decide,task,converse,extract,search}`)
and the intent-routing harness fork a coding-agent CLI one-shot, pipe a rendered
prompt, and parse the agent's structured output. Historically that CLI was
hardwired to `claude`. The backend seam abstracts it so `copilot` can serve the
same role unchanged — same verbs, same schema validation, same Agent-actions
transcript.

## Selecting a backend

Global, per-session switch (precedence: flag → env → default `claude`):

```bash
kitsoki run story.yaml --agent copilot      # or: kitsoki web --agent copilot
KITSOKI_AGENT=copilot kitsoki run story.yaml
```

There is no per-room/per-invocation backend selector — it is one choice for the
whole session (a story author targets the *verb contract*, not a specific CLI).

Binary resolution per backend:

| Backend | binary | override env |
|---------|--------|--------------|
| `claude` (default) | `claude` on `PATH` | `KITSOKI_AGENT_CLAUDE_BIN` |
| `copilot` | `copilot` on `PATH` | `KITSOKI_AGENT_COPILOT_BIN` |
| `codex` | `codex` on `PATH` | `KITSOKI_AGENT_CODEX_BIN` |

## What differs per backend

The verb handlers always build a **claude-shaped** invocation; the backend's
`TranslateInvocation` rewrites it onto the target CLI. The mappings:

| Concern | claude | copilot | codex |
|---|---|---|---|
| prompt delivery | piped on stdin | `-p <text>` argument | piped on stdin (`codex exec` reads instructions from stdin) |
| permission | `--permission-mode bypassPermissions` | `--allow-all-tools` | `--dangerously-bypass-approvals-and-sandbox` (required — see note below) |
| MCP config | `--mcp-config <file>` | `--additional-mcp-config @<file>` | `-c mcp_servers.<name>.{command,args,env}` TOML overrides (file read + converted) |
| system prompt | `--system-prompt <s>` flag | prepended into the `-p` text (no flag) | prepended into the stdin prompt (no flag) |
| output | `--output-format stream-json --verbose` | `--output-format json` (JSONL) | `--json` (JSONL) |
| working dir | `cmd.Dir` | `cmd.Dir` + `-C <dir>` | `cmd.Dir` + `-C <dir>` |
| MCP tool name | `mcp__<server>__submit` | `<server>-submit` | bare `submit` (server is a separate JSONL field; live-pinned) |
| session resume | `--session-id <id>` / `--resume <id>` | `--session-id <id>` / `--resume=<id>` | first call creates the session (id captured from `thread.started`); resume via the `exec resume <id>` subcommand |
| transcript format | `claude-jsonl` | `copilot-jsonl` | `codex-jsonl` |
| terminal usage | tokens + `total_cost_usd` | `premium_requests` + durations (no cost) | tokens (`input/cached_input/output/reasoning_output`), no cost |

Codex's `--json` wire protocol is two-layer: top-level event types
(`thread.started`, `turn.started`, `item.started`, `item.completed`,
`turn.completed`) wrap nested item types (`agent_message`, `command_execution`,
`mcp_tool_call`, `reasoning`). The final reply is the **last** `agent_message`
item's text; `classifyCodexEvent` surfaces it as `assistant.message` so the
runner's latest-wins reply assembly applies.

Claude-only flags (`--setting-sources`, `--effort`,
`--exclude-dynamic-system-prompt-sections`, `--no-session-persistence`,
`--verbose`) are dropped during translation.

> **Codex requires `--dangerously-bypass-approvals-and-sandbox`.** `codex exec`
> auto-cancels *every* MCP tool call ("user cancelled MCP tool call") in
> non-interactive mode — verified live (codex-cli 0.139.0) across
> `approval_policy="never"`, every sandbox mode, per-server trust keys, and both
> ephemeral (`-c`) and persisted (`codex mcp add`) registration. Because the
> schema-validator `submit` tool is load-bearing for parity (validation + the
> nudge/abandonment-recovery loop + `post_cmd` verifiers), the codex backend runs
> with the bypass flag so the tool can execute.
>
> This does **not** make Codex's sandbox the write-mode boundary. For Kitsoki
> hosted work, the boundary is the story/tooling layer: read-only agent rooms
> deny mutating tools, Bash MCP applies its profile, validators run through the
> validator sandbox, and read-only write attempts are mediated by the Kitsoki
> write-mode gate / operator-ask bridge. In other words, the Codex CLI sandbox is
> bypassed so MCP tools are callable; Kitsoki still owns whether a hosted agent
> may mutate the target workspace. **Select `--agent codex` only in a trusted /
> externally-sandboxed environment**, since the bypass flag disables Codex's own
> sandbox and approval gate for the whole run.

### Session resume & usage

The decide/task/converse retry loops set `--session-id <uuid>` on the first
attempt and `--resume <uuid>` on each nudge round to carry context forward.
Copilot exposes the same two flags (it declares `--resume` as an optional-value
flag, so the value is forwarded as `--resume=<uuid>`), verified live to carry
context across rounds. Copilot's terminal `result` reports `premium_requests` +
durations rather than token totals; the runner sums each `assistant.message`'s
`outputTokens` and injects `output_tokens` into the usage map when the terminal
event omits it (a no-op for claude, which reports the total directly).

### Model names

Stories and the router specify **claude** model ids (`opus`, `sonnet`,
`haiku`, `claude-…`). Copilot does not understand these, so the copilot backend
**drops a claude model id** and lets copilot use its own configured / `auto`
model. Control copilot's model via copilot's own config (`~/.copilot/config.json`)
or `COPILOT_MODEL`; a genuine copilot model id (e.g. `gpt-5`) set on an
agent/effect *is* forwarded.

## Implementation

- `internal/host/agent_backend.go` — the `agentBackend` interface,
  `Invocation`, `classifiedEvent`, and the context seam (`WithAgentBackend` /
  `AgentBackendFromContext`, defaulting to claude so every existing call site
  is unchanged).
- `internal/host/agent_backend_claude.go` — the identity backend (delegates to
  the pre-existing helpers; byte-identical to the pre-seam behavior).
- `internal/host/agent_backend_copilot.go` — flag translation + binary
  resolution + the copilot test-stub seam (`WithCopilotRunner`).
- `internal/host/agent_backend_codex.go` / `internal/host/agent_stream_codex.go`
  — the codex equivalents: claude-argv → `codex exec` translation (including the
  `--mcp-config` JSON → `-c mcp_servers.*` TOML conversion and the `exec resume
  <id>` mapping), the codex JSONL classifier, and the `WithCodexRunner` stub seam.
- `internal/host/agent_stream_copilot.go` — the copilot JSONL event classifier
  (`assistant.message` / `tool.execution_*` / `result`), normalizing into the
  same `classifiedEvent` the runner consumes, so the trace/sink/transcript/usage
  paths are backend-agnostic.
- The seam is consulted entirely inside `internal/host/agent_runner.go`
  (`resolveAgentBin`, `runClaudeStreamJSON`); the per-verb handlers are
  untouched. The orchestrator installs the selected backend per dispatch
  (`host.WithAgentBackendNamed`) in `host_dispatch.go` / `offpath.go`.
- Routing parity: `cmd/kitsoki` builds the routing harness against the copilot
  binary with `ClaudeCLIConfig.ValidatorTool = "kitsoki-validator-submit"` so the
  output contract names copilot's tool scheme.

## Tests

- `internal/host/agent_conformance_test.go` — the **interface-compliance gate**:
  table-driven over `{claude, copilot, codex}`, covering argv translation,
  JSONL/stream parsing of real captured fixtures
  (`testdata/{claude,copilot,codex}/*.jsonl`), usage
  normalization, tool-event classification, the submit-tool name, and a full
  stub round-trip. No real binary or LLM is forked. Adding a backend = passing
  every case here against its own fixtures.
- `internal/host/agent_copilot_smoke_test.go` — **gated** real-CLI smoke
  (`KITSOKI_AGENT_LIVE=1`); forks the real `copilot` once against a kitsoki
  `mcp-validator` server to re-confirm the MCP tool name and the side-channel
  capture contract. Never runs in CI (incurs a real Copilot request).
- `internal/host/agent_codex_smoke_test.go` — the codex counterpart (same
  gate); it pins codex's MCP submit-tool name (the `<server>__submit` placeholder
  is a best guess until this test confirms it against the real binary).

## Known parity gaps

- **Tool scoping.** Copilot runs with `--allow-all-tools`; per-agent
  `allowed`/`disallowed` tool lists are not yet mapped onto copilot's
  `--allow-tool`/`--deny-tool`. (`--add-dir` *is* forwarded — copilot supports
  it with the same meaning.)
- **Cost.** Copilot reports no per-call dollar cost (only `premium_requests` +
  durations), so the cost column is empty under copilot; `output_tokens` are
  surfaced (summed per-message), but input/cache token counts are not reported
  by copilot. Codex also reports **no dollar cost** (token counts only).
- **Codex MCP execution requires the bypass flag.** Resolved by design, not a
  gap: see the note above — `codex exec` cannot execute the validator `submit`
  tool without `--dangerously-bypass-approvals-and-sandbox`, so `--agent codex`
  must run in a trusted/externally-sandboxed environment. (`ValidatorToolName` is
  live-pinned to bare `submit`.)
- **Codex session resume (UNVERIFIED live).** Resume maps onto the `codex exec
  resume <id>` subcommand (`--json` and a stdin prompt accepted per the CLI
  help); a fresh run has no `--session-id` (the session is created per run and
  its `thread_id` is captured from `thread.started`). This path has not been run
  against the live binary — confirm in a verify phase.
