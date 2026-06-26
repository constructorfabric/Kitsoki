# punch-list

`punch-list` runs a generic `punch-list/v1` YAML worklist through Kitsoki stories.
It is the reusable form of the top-10 dogfood plan: load a manifest, enforce
profile/model policy, drive each item, verify independently, and write a report.

The story deliberately separates live work from deterministic tests:

- `host.run` scripts load/lint manifests, recompute the board, enforce policy,
  run deterministic verifiers, and write `.artifacts/punch-list/<run-id>/report.md`
  plus `.artifacts/punch-list/<run-id>/deck.slidey.json`.
- `host.agent.task` is only the live Studio MCP driving boundary. Flow fixtures
  stub it; automated tests never call a real LLM.

For GPT-5.5 dogfood manifests, set:

```yaml
defaults:
  harness: live
  profile: codex-native
  model: gpt-5.5
```

The linter rejects live work that drifts to Claude, missing story paths,
duplicate item IDs, implementation items without deterministic verifiers, and
verifier commands that appear to invoke LLM/live execution.

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
