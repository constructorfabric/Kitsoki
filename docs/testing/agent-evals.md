# Agent Task Evals

Agent task evals are story-local benchmarks for bounded `host.agent.*` call
sites. They are designed for small, single-turn tasks where Kitsoki prepares the
context, narrows the tool surface, validates structured output, and lets authors
compare harness profiles before pinning a cheaper or faster model.

Default commands are offline and CI-safe:

```sh
go run ./cmd/kitsoki eval list stories/pr-refinement
go run ./cmd/kitsoki eval show stories/pr-refinement/evals/merge_judge.yaml
go run ./cmd/kitsoki eval run stories/pr-refinement/evals/merge_judge.yaml
```

`eval run` without `--live` validates the dataset, resolves the call site,
checks expected examples against the declared output schema, and reports that no
provider-backed benchmark was run. `--live` is the explicit gate for future paid
provider runs; automated tests must not use it.

## Dataset Shape

Datasets live in `evals/*.yaml` beside a story:

```yaml
kind: agent_eval
app: ../app.yaml
call: merge_judge
agent: judge

task:
  goal: "Classify whether a landed PR can be closed out."
  boundedness:
    max_turns: 1
    tool_policy: none
    prepared_context: true
  adherence_bar:
    min_pass_rate: 0.95
    max_p95_latency_ms: 8000
    max_avg_cost_usd: 0.002

matrix:
  profiles: [claude, synthetic-codex]
  models:
    synthetic-codex: [syn:large:text, syn:small:text]
  effort: [low, medium]
  repeat: 5

comparator:
  kind: enum
  field: intent

examples:
  - name: clean-closeout
    args: { pr_id: "145", merge_sha: abc1234 }
    expect: { verdict: pass, intent: accept, reason: "Merge is complete.", confidence: 0.97 }
```

The target call site should have a stable `id:`. Pins live directly on that
invoke under `selection:` and reference committed evidence under
`evals/reports/<call>/`.

## Comparators

- `exact`: actual JSON equals expected JSON.
- `field_subset`: every expected field matches the actual output.
- `enum`: one named field, such as `intent`, matches.
- `artifact_diff`: deterministic artifact comparison using an accepted subset.
- `judge`: reserved for open-ended tasks and only promotable after the judge
  call site itself has passing evidence.

## Reports

Reports are compact JSON evidence files. They record dataset/prompt/schema/
toolbox hashes when available, candidate pass rates, cost, latency, failure
samples, and the pinning decision. Raw provider logs belong in `.artifacts/`
unless they are needed for review.
