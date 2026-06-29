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

## Performance Benches

`kitsoki agent-bench` is the reusable harness for live-model performance and
quota regressions around `host.agent.*` calls. It is deliberately separate from
`eval run`: eval datasets prove the contract and comparator, while benches score
real traces against budgets such as token count, cost, tool calls, read fanout,
elapsed time, final state, and whether a structured submit occurred.

The broader prompt/tool/task-shape hardening loop is documented in
[Model Task Engineering](model-task-engineering.md).

The default command is offline and CI-safe:

```sh
go run ./cmd/kitsoki agent-bench score stories/deliver/agent-bench/decompose_glm.yaml \
  --trace .artifacts/dogfood-four-bugs/deliver-glm-post-host.trace.jsonl \
  --json-out .artifacts/agent-bench/deliver-decompose-glm52/report.json \
  --markdown-out .artifacts/agent-bench/deliver-decompose-glm52/report.md \
  --slidey-out .artifacts/agent-bench/deliver-decompose-glm52/deck.slidey.json
```

If any budget or expectation fails, the command exits non-zero and prints the
specific violated control. That makes a bad GLM run reproducible without
spending another provider call.

Reports also include agent-call lifecycle counters:
`agent_calls_started`, `agent_calls_finished`, `agent_calls_errored`, and
`agent_calls_in_flight`. A trace with an `agent.call.start` event but no
terminal returned/error event fails explicitly as `agent_calls_in_flight`; this
separates provider/runtime stalls from ordinary prompt failures such as too many
tool calls or a missing structured submit.

Live execution is explicit:

```sh
go run ./cmd/kitsoki agent-bench run stories/deliver/agent-bench/decompose_glm.yaml --live \
  --json-out .artifacts/agent-bench/deliver-decompose-glm52/report.json
```

The manifest's `run.command` is argv-style, not shell text, and is refused
unless `--live` is present. `agent-bench run` removes the target trace before
executing the command so old failed attempts do not contaminate the score. Keep
live traces and reports under `.artifacts/`. Automated tests should use
`agent-bench score` with checked-in or generated synthetic traces; they must not
pass `--live`.

Bench manifests use `version: agent_bench/v1`:

```yaml
version: agent_bench/v1
cases:
  - id: deliver-decompose-glm52
    trace: ../../../.artifacts/agent-bench/deliver-decompose-glm52/trace.jsonl
    run:
      workdir: ../../..
      timeout: 15m
      command: [go, run, ./cmd/kitsoki, drive, stories/deliver/app.yaml, --harness, live]
    budgets:
      max_tool_calls: 20
      max_read_calls: 12
      max_input_tokens: 150000
      max_output_tokens: 8000
      max_cost_usd: 1.00
    expectations:
      require_submit: true
      forbidden_tools: [Agent, Task, AskUserQuestion]
```

For GLM-5.2 tuning, use the deliver decomposer bench as the control loop:

1. Record one gated live run.
2. Score it offline and inspect the failed budget.
3. Change the story prompt, tool policy, provider profile, or quota controls.
4. Re-run only when the offline report explains what should improve.
