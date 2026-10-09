---
name: model-task-engineering
description: Tune and validate a provider/model task in Kitsoki with a controlled, repeatable harness. Use when a model performs poorly on a story step, when a provider quota/rate behavior needs task hardening, or when the user asks to improve GLM/GPT/Claude performance with traceable artifacts. Produces an offline-scored benchmark report, a Slidey deck, and concrete story/prompt/tool changes without calling live LLMs from tests.
---

# Model Task Engineering

Use this skill to turn a flaky or expensive model step into a controlled
engineering loop. The goal is not to "try again"; the goal is to make the task
environment easy enough that the selected model can succeed reliably, then prove
that with reproducible traces and offline scoring.

## Modes

Choose the smallest mode that proves the decision.

### Single-boundary diagnosis

Use this for one room, agent call, or bench case. It retains the trace-first
loop below and must not be presented as a model-wide comparison.

### Campaign optimization

Use this for a model/treatment/corpus grid. Arena owns planning, arming,
resumption, immutable cell attempts, and rollups; `internal/agentbench` owns
trace and lifecycle scoring. Do not add a second scheduler or scorer to a
story or adapter.

Before any live request, require a `task-optimization/v1` manifest and resolved
content-addressed lock; distinct learning and frozen confirmation splits; a
deterministic plan with cell count plus estimated token/time/spend envelope; and
candidate/treatment preflight that records requested/effective model and effort.
Unsupported pairs are `unsupported` or `pending`, never model failures.

Each provider request needs an immutable receipt. Keep CodeAct action
capabilities, its generator runtime, external-agent sandboxing, and oracle
sandboxing as separate boundaries. Missing artifacts, invalid terminal state,
unmatched runtime/call events, model/effort mismatch, oracle leakage, or
incomplete usage all block a solve claim.

Use staged ablations and replicate only decision-boundary cells. Freeze the
champion before confirmation; do not patch process, corpus, oracle, or metrics
after confirmation begins. Campaign artifacts are canonical JSON, Markdown, and
a `.slidey.json` deck under `.artifacts/task-optimization/<study>/`. Explicit
operator authorization and the study live gate are required before spending;
flows and CI exercise planning and offline analysis only.

## Core loop

1. Pick one task boundary.
   - Prefer one story room, one agent call, one bench manifest case.
   - Write down the intended success artifact and deterministic acceptance gate.
   - Confirm whether the target trace is stale before scoring it.
2. Score the current trace offline.
   - Use `go run ./cmd/kitsoki agent-bench score <bench.yaml>`.
   - Write `--json-out`, `--markdown-out`, and `--slidey-out` artifacts.
   - Do not use `agent-bench run --live` unless the user explicitly asked for a live provider run.
3. Classify the failure.
   - Lifecycle: `agent_calls_in_flight > 0`, missing terminal call events, timeout.
   - Tool fanout: too many tools, forbidden tools, recursive agent/task use.
   - Context fanout: too many reads, files, or input tokens.
   - Output pressure: max output too small, unclear schema, no compact success target.
   - Task ambiguity: weak artifact contract, broad discovery, hidden prerequisites.
4. Engineer the task environment.
   - Narrow the tool list before changing the model.
   - Make the authoritative input explicit.
   - Cap fallback discovery.
   - Move deterministic work out of the LLM and into scripts or validators.
   - Make submit/acceptance mechanics explicit.
   - Keep the change general; do not encode the one trace's answer.
5. Re-score offline.
   - The score must fail on stale/stalled traces and pass on the successful trace.
   - Update budgets only when the successful run proves the old budget was unrealistic.
6. Produce evidence.
   - Keep generated review artifacts under `.artifacts/<topic>/`.
   - Keep transient notes under `.context/`.
   - File bugs for runtime or observability gaps found during the loop.

## Commands

Score an existing trace:

```sh
go run ./cmd/kitsoki agent-bench score stories/deliver/agent-bench/decompose_glm.yaml \
  --case deliver-decompose-glm52 \
  --trace .artifacts/agent-bench/deliver-decompose-glm52/proposal-only-success.trace.jsonl \
  --json-out .artifacts/model-task-engineering/glm52/report.json \
  --markdown-out .artifacts/model-task-engineering/glm52/report.md \
  --slidey-out .artifacts/model-task-engineering/glm52/deck.slidey.json
```

Drive the story wrapper without live LLMs:

```sh
go run ./cmd/kitsoki test flows stories/model-task-engineering/app.yaml
```

Run a live provider case only when explicitly requested:

```sh
go run ./cmd/kitsoki agent-bench run <bench.yaml> --case <case> --live
```

## Evidence standard

A completed tuning run should leave:

- The bench manifest used for the task.
- The exact trace path that was scored.
- JSON and Markdown score reports.
- A Slidey deck JSON for review.
- The story/prompt/tool changes that improved the task.
- Test output showing no live LLM was used in automated validation.
- Any filed bug for harness/runtime gaps discovered along the way.

## Guardrails

- Automated tests must never call a live provider.
- Do not hide provider failures by deleting budgets or weakening submit/final-state expectations.
- Do not widen tools to fix a model failure unless the task genuinely requires the capability.
- Prefer prompt/tool/task-shape changes over model-specific hacks.
- Do not treat a self-reported agent success as sufficient; score the trace and inspect the artifact.
