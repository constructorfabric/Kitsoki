# punch-list

`punch-list` runs a generic `punch-list/v1` YAML worklist through Kitsoki stories.
It is the reusable form of the top-10 dogfood plan: load a manifest, enforce
profile/model policy, drive each item, verify independently, and write a report.

The story deliberately separates live work from deterministic tests:

- `host.starlark.run` scripts load/lint manifests, recompute the board, enforce
  policy, and write the final `.artifacts/punch-list/report.md`; `host.run` is
  still used for the deterministic verifier command bridge.
- `host.agent.task` is only the live Studio MCP driving boundary. Flow fixtures
  stub it; automated tests never call a real LLM.

For fixed GPT-5.5 dogfood manifests, set:

```yaml
defaults:
  harness: live
  profile: codex-native
  model: gpt-5.5
```

The linter rejects live work that drifts to Claude, missing story paths,
duplicate item IDs, implementation items without deterministic verifiers, and
verifier commands that appear to invoke LLM/live execution.

For ladder-driven live work, set `harness: ladder` and provide a
`harness_ladder:` block on defaults or on the item:

```yaml
defaults:
  harness: ladder
  profile: synthetic-codex
  model: hf:zai-org/GLM-5.2
  require_trace_model: false
  harness_ladder:
    models:
      - { backend: codex, provider: synthetic-codex, model: hf:zai-org/GLM-5.2 }
      - { backend: codex, provider: codex-native, model: gpt-5.5 }
    efforts: [low, medium, high, xhigh, max]
```

The policy checker validates the ladder shape and the drive/implementation rooms
pass it through to the maker `host.agent.task` call as `with.harness_ladder`.

Manifests may also carry `sandbox:` on defaults or an item. The drive and
implementation rooms pass that policy to `host.agent.task`; malformed runtime
strength/degrade/path settings fail at story load or host-call parse time.

## Live driver evidence

The live `drive` and `implementation` rooms require the driver handoff to include
the requested `model` and a non-empty `trace_path` when
`require_trace_model: true`. If a dispatched driver cannot actually use Studio
MCP tools, the story parks at `needs-human` instead of letting a deterministic
verifier create a false pass.

Run the focused no-LLM checks:

```bash
go run ./cmd/kitsoki test flows stories/punch-list/app.yaml
```

The durable 10-item GPT-5.5 dogfood manifest lives at
`testdata/top10_gpt55.yaml`; the deterministic demo/regression flow is
`flows/happy_top10_gpt55.yaml`.
