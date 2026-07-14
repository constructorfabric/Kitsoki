# Arena Treatments

This package is the code-level library for reusable arena treatments. A
treatment is the action surface under comparison inside a cell. Specs name the
stable treatment ID; this package maps that ID to a driver, validates the
required variant/options, and records the action-surface metadata that rollups
and reports can consume.

Current paired-task treatments:

| ID | Action surface | Notes |
|---|---|---|
| `raw-agent` | `raw-agent` | One raw one-shot prompt. Legacy alias: `raw-codex`; also `single-briefed`, `single-naive`. |
| `strict-mcp-current` | `kitsoki-studio-mcp` | `kitsoki-mcp-driver` drives the normal Studio MCP workflow. Legacy aliases: `kitsoki-mcp`, `kitsoki`. |
| `strict-mcp-direct-driver` | `kitsoki-studio-mcp+direct-submit` | The same bugfix story/roles driven through its bounded `session.submit` loop, with `implementation_mode=agent_task`. It is not a CodeAct action-surface replacement. |
| `codex-codeact` | `kitsoki-codeact-mcp` | Direct `kitsoki-codeact-driver` launch with only `mcp__kitsoki-codeact__codeact_eval` exposed. Requires `variant.agent`; a Codex-only diagnostic, not a cross-provider headline arm. |
| `strict-mcp-codeact-broad` | `kitsoki-studio-mcp+codeact` | Studio MCP orchestration while the story implementation step uses broad `host.agent.codeact`. Legacy alias: `kitsoki-mcp-codeact`. |
| `strict-mcp-codeact-decomposed` | `kitsoki-studio-mcp+codeact-decomposed` | Compiler-bounded per-item CodeAct grants; each item must pass a host-owned deterministic gate. No fallback is allowed. |
| `strict-mcp-decomposed-fallback` | `kitsoki-studio-mcp+codeact-decomposed-fallback` | Same strict per-item executor, with at most one retry of the same item after a schema-typed reason declared on that item (`capability_missing` or `task_not_decomposable`). The retry keeps the identical grant. |

Operator discovery:

```bash
python3 tools/arena/arena.py treatments --aliases
python3 tools/arena/arena.py validate --spec tools/arena/specs/codex-codeact-action-surface.yaml
```

Validation is intentionally exact for diagnostic direct CodeAct: `codex-codeact` must use
`agent: kitsoki-codeact-driver`, not the Studio MCP driver. `kitsoki-mcp` and
`strict-mcp-codeact-broad` use the Studio MCP surface; the latter forces the story
implementation step through `host.agent.codeact`.

The strict decomposed treatments never select `agent_task` after a CodeAct
failure. A fallback receipt is recorded only when the current compiled item
allowlists the structured reason; an unlisted reason exits `needs-human`.

Add a treatment by creating a focused driver module, registering it in
`registry.py`, and documenting the stable spec fields here. Driver modules
should depend on `DriverServices` for runner-specific operations instead of
importing a concrete runner.
