# Arena Treatments

This package is the code-level library for reusable arena treatments. A
treatment is the action surface under comparison inside a cell. Specs name the
stable treatment ID; this package maps that ID to a driver, validates the
required variant/options, and records the action-surface metadata that rollups
and reports can consume.

Current paired-task treatments:

| ID | Action surface | Notes |
|---|---|---|
| `raw-codex` | `raw-agent` | One raw one-shot prompt. Aliases: `single-briefed`, `single-naive`. |
| `codex-codeact` | `kitsoki-codeact-mcp` | Direct `kitsoki-codeact-driver` launch with only `mcp__kitsoki-codeact__codeact_eval` exposed. Requires `variant.agent`. |
| `kitsoki-mcp` | `kitsoki-studio-mcp` | `kitsoki-mcp-driver` drives the normal Studio MCP workflow. Alias: `kitsoki`. |
| `kitsoki-mcp-codeact` | `kitsoki-studio-mcp+codeact` | Studio MCP orchestration while the story implementation step uses `host.agent.codeact`. |

Operator discovery:

```bash
python3 tools/arena/arena.py treatments --aliases
python3 tools/arena/arena.py validate --spec tools/arena/specs/codex-codeact-action-surface.yaml
```

Validation is intentionally exact for direct CodeAct: `codex-codeact` must use
`agent: kitsoki-codeact-driver`, not the Studio MCP driver. `kitsoki-mcp` and
`kitsoki-mcp-codeact` use the Studio MCP surface; the latter forces the story
implementation step through `host.agent.codeact`.

Add a treatment by creating a focused driver module, registering it in
`registry.py`, and documenting the stable spec fields here. Driver modules
should depend on `DriverServices` for runner-specific operations instead of
importing a concrete runner.
