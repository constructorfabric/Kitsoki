# Arena Treatment Catalog

Arena comparisons vary tasks, candidates, and treatments. A treatment is the
action surface under test: raw agent prompting, direct CodeAct, Studio MCP
orchestration, or a workflow that delegates one step to CodeAct.

The reusable library lives in `tools/arena/arena/treatments/`:

| Module | Responsibility |
|---|---|
| `base.py` | Driver result, injected runner services, and public catalog entry contracts. |
| `capabilities.py` | CodeAct capability presets, canonical hashes, and launch-plan assertions. |
| `registry.py` | Stable treatment IDs, aliases, validation, and catalog metadata. |
| `raw_codex.py` | Raw one-shot prompt treatment. |
| `codex_codeact.py` | Direct `kitsoki-codeact-driver` treatment. |
| `kitsoki_mcp.py` | Studio MCP and Studio MCP plus CodeAct treatments. |

Operator UX is intentionally first-class:

```bash
python3 tools/arena/arena.py treatments --aliases
python3 tools/arena/arena.py validate --spec tools/arena/specs/codex-codeact-action-surface.yaml
python3 tools/arena/arena.py doctor --spec tools/arena/specs/codex-codeact-action-surface.yaml
```

`validate` is pure spec validation: no Docker, no LLM. `doctor` additionally
checks local prerequisites such as Docker context/daemon availability. A Docker
startup failure is an infrastructure result (`blocked` / `infra:harness`), not a
model loss.

## Current Treatments

| Treatment ID | Alias | Action surface | Required fields | Notes |
|---|---|---|---|---|
| `raw-codex` | `single-briefed`, `single-naive` | `raw-agent` | none | One raw prompt through the configured backend. |
| `codex-codeact` | none | `kitsoki-codeact-mcp` | `variant.agent` | Launches `kitsoki-codeact-driver` with Codex shell/apps disabled and only `mcp__kitsoki-codeact__codeact_eval` exposed. |
| `kitsoki-mcp` | `kitsoki` | `kitsoki-studio-mcp` | none | `kitsoki-mcp-driver` orchestrates the normal Studio MCP workflow. |
| `kitsoki-mcp-codeact` | none | `kitsoki-studio-mcp+codeact` | none | Studio MCP drives the workflow, while the story implementation step runs through `host.agent.codeact`. |

Direct CodeAct treatments should use a named capability preset. The default
`repo_patch` preset grants repository file read/write plus read-only git probes;
the runner records the canonical JSON hash in the cell metrics so reports can
prove which surface was used.

Direct CodeAct validation is exact: `codex-codeact` requires
`agent: kitsoki-codeact-driver`. The Studio MCP profiles are separate surfaces:
`kitsoki-mcp` uses the normal Studio MCP driver, while `kitsoki-mcp-codeact`
uses Studio MCP orchestration and forces the mutating implementation step
through `host.agent.codeact`.

## CodeAct-vs-Codex Runbook

The reusable path runs the action-surface matrix and writes durable arena
artifacts:

```bash
make arena-showdown-plan
make arena-showdown-run
```

The live spend path is separate and still double-gated:

```bash
ARENA_PAIRED_TASK_ENABLE_CODEX=1 make arena-showdown-live
```

Artifacts are written under `ARENA_SHOWDOWN_OUT` (default
`.artifacts/arena/codeact-showdown`). The run bundle, especially `summary.json`,
`report.md`, and the per-cell JSON files, is the source of truth. Do not present
a generated video as arena evidence unless it is produced from a completed real
run or a trace captured from one.

## Adding a Treatment

1. Add a focused driver module under `tools/arena/arena/treatments/`.
2. Register the stable ID, aliases, and public fields in `registry.py`.
3. Keep runner-specific operations behind `DriverServices`; treatment modules
   should not import `tools/arena/lib/paired_task_runner.py`.
4. Document the treatment in this catalog and in the package README.
5. Add a no-LLM test that imports `arena.treatments` directly and validates the
   registry, validation rules, and any capability/permission assertions.
