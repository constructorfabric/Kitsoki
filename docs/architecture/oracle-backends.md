# Oracle Backends (`--oracle claude | copilot`)

> **Status:** operator + contributor reference for the pluggable coding-agent
> CLI behind every oracle verb. A **backend** chooses *which CLI kitsoki forks*
> to run a `host.oracle.*` call and intent routing: Anthropic's `claude`
> (default) or GitHub's `copilot`. It is orthogonal to
> [providers](./oracle-providers.md) (which retarget the `claude` CLI's
> endpoint) and to [oracle plugins](./oracle-plugin.md) (which choose which
> component answers).

Kitsoki's oracle verbs (`host.oracle.{ask,decide,task,converse,extract,search}`)
and the intent-routing harness fork a coding-agent CLI one-shot, pipe a rendered
prompt, and parse the agent's structured output. Historically that CLI was
hardwired to `claude`. The backend seam abstracts it so `copilot` can serve the
same role unchanged — same verbs, same schema validation, same Agent-actions
transcript.

## Selecting a backend

Global, per-session switch (precedence: flag → env → default `claude`):

```bash
kitsoki run story.yaml --oracle copilot      # or: kitsoki web --oracle copilot
KITSOKI_ORACLE=copilot kitsoki run story.yaml
```

There is no per-room/per-invocation backend selector — it is one choice for the
whole session (a story author targets the *verb contract*, not a specific CLI).

Binary resolution per backend:

| Backend | binary | override env |
|---------|--------|--------------|
| `claude` (default) | `claude` on `PATH` | `KITSOKI_ORACLE_CLAUDE_BIN` |
| `copilot` | `copilot` on `PATH` | `KITSOKI_ORACLE_COPILOT_BIN` |

## What differs under copilot

The verb handlers always build a **claude-shaped** invocation; the backend's
`TranslateInvocation` rewrites it onto the target CLI. The copilot mapping:

| Concern | claude | copilot |
|---|---|---|
| prompt delivery | piped on stdin | `-p <text>` argument |
| permission | `--permission-mode bypassPermissions` | `--allow-all-tools` |
| MCP config | `--mcp-config <file>` | `--additional-mcp-config @<file>` |
| system prompt | `--system-prompt <s>` flag | prepended into the `-p` text (copilot has no flag) |
| output | `--output-format stream-json --verbose` | `--output-format json` (JSONL) |
| working dir | `cmd.Dir` | `cmd.Dir` + `-C <dir>` |
| MCP tool name | `mcp__<server>__submit` | `<server>-submit` |
| session resume | `--session-id <id>` / `--resume <id>` | `--session-id <id>` / `--resume=<id>` (optional-value form) |
| terminal usage | tokens + `total_cost_usd` | `premium_requests` + durations; `output_tokens` summed from per-message counts (no cost) |

Claude-only flags (`--setting-sources`, `--effort`,
`--exclude-dynamic-system-prompt-sections`, `--no-session-persistence`,
`--verbose`) are dropped during translation.

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

- `internal/host/oracle_backend.go` — the `oracleBackend` interface,
  `Invocation`, `classifiedEvent`, and the context seam (`WithOracleBackend` /
  `OracleBackendFromContext`, defaulting to claude so every existing call site
  is unchanged).
- `internal/host/oracle_backend_claude.go` — the identity backend (delegates to
  the pre-existing helpers; byte-identical to the pre-seam behavior).
- `internal/host/oracle_backend_copilot.go` — flag translation + binary
  resolution + the copilot test-stub seam (`WithCopilotRunner`).
- `internal/host/oracle_stream_copilot.go` — the copilot JSONL event classifier
  (`assistant.message` / `tool.execution_*` / `result`), normalizing into the
  same `classifiedEvent` the runner consumes, so the trace/sink/transcript/usage
  paths are backend-agnostic.
- The seam is consulted entirely inside `internal/host/oracle_runner.go`
  (`resolveOracleBin`, `runClaudeStreamJSON`); the per-verb handlers are
  untouched. The orchestrator installs the selected backend per dispatch
  (`host.WithOracleBackendNamed`) in `host_dispatch.go` / `offpath.go`.
- Routing parity: `cmd/kitsoki` builds the routing harness against the copilot
  binary with `ClaudeCLIConfig.ValidatorTool = "kitsoki-validator-submit"` so the
  output contract names copilot's tool scheme.

## Tests

- `internal/host/oracle_conformance_test.go` — the **interface-compliance gate**:
  table-driven over `{claude, copilot}`, covering argv translation, JSONL/stream
  parsing of real captured fixtures (`testdata/{claude,copilot}/*.jsonl`), usage
  normalization, tool-event classification, the submit-tool name, and a full
  stub round-trip. No real binary or LLM is forked. Adding a backend = passing
  every case here against its own fixtures.
- `internal/host/oracle_copilot_smoke_test.go` — **gated** real-CLI smoke
  (`KITSOKI_ORACLE_LIVE=1`); forks the real `copilot` once against a kitsoki
  `mcp-validator` server to re-confirm the MCP tool name and the side-channel
  capture contract. Never runs in CI (incurs a real Copilot request).

## Known parity gaps

- **Tool scoping.** Copilot runs with `--allow-all-tools`; per-agent
  `allowed`/`disallowed` tool lists are not yet mapped onto copilot's
  `--allow-tool`/`--deny-tool`. (`--add-dir` *is* forwarded — copilot supports
  it with the same meaning.)
- **Cost.** Copilot reports no per-call dollar cost (only `premium_requests` +
  durations), so the cost column is empty under copilot; `output_tokens` are
  surfaced (summed per-message), but input/cache token counts are not reported
  by copilot.
