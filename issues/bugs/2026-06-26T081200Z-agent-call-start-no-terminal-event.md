---
id: 2026-06-26T081200Z-agent-call-start-no-terminal-event
title: "Interrupted or stalled live provider agent calls can leave traces with agent.call.start but no terminal error event"
target: kitsoki
filed_at: 2026-06-26T08:12:00Z
status: open
severity: P1
component: observability
kitsoki_rev: 10bc2e2e
trace_ref: ".artifacts/agent-bench/deliver-decompose-glm52/trace.jsonl"
external: {}
assignee: ""
url: "issues/bugs/2026-06-26T081200Z-agent-call-start-no-terminal-event.md"
---

## Body

An interrupted GLM-5.2 dogfood run of the `deliver` decomposition step produced
a trace containing `agent.call.start` for the decomposer, but no terminal
`agent.call.complete` / `agent.call.error` event before the run was manually
interrupted after the trace stopped growing.

This makes live provider reliability hard to diagnose: the trace cannot tell
whether the provider call is still running, hung, timed out, interrupted, or
returned without being recorded. A later successful GLM-5.2 run did write
`agent.call.complete` with nested usage metadata, so the remaining issue is the
interrupted/stalled path.

## Expected

Every live `host.agent.*` call writes exactly one terminal lifecycle event after
`agent.call.start`, including cancellation and timeout paths:

- returned/success when the call completes, with available usage/cost metadata;
- error when the provider call fails, times out, is interrupted, or is canceled,
  with the concrete error and any available usage/cost metadata.

## Actual

The trace has:

- `session.header`
- `session.story`
- `harness.dispatched` for `host.agent.task`
- `agent.call.start` for the GLM-5.2 decomposer

No terminal event was observed for the interrupted attempt. The reusable bench
now reports:

```text
agent_calls started=1 finished=0 errored=0 in_flight=1
ERROR: agent_calls_in_flight 1: trace has start event(s) without returned/error terminal event
ERROR: required submit was not observed
```

## Impact

- Blocks reliable quota/cost accounting for live provider calls.
- Makes GLM prompt/tool performance hard to separate from provider/runtime
  stalls.
- Leaves dogfood reports with ambiguous failures unless a secondary harness
  detects the missing terminal lifecycle event.

## Evidence

- Interrupted trace:
  `.artifacts/agent-bench/deliver-decompose-glm52/post-hardening-stalled.trace.jsonl`
- Interrupted scored report:
  `.artifacts/agent-bench/deliver-decompose-glm52/post-hardening-stalled-report.json`
- Successful comparison trace:
  `.artifacts/agent-bench/deliver-decompose-glm52/proposal-only-success.trace.jsonl`
- Successful comparison report:
  `.artifacts/agent-bench/deliver-decompose-glm52/proposal-only-success-report.json`
- Dogfood report:
  `.context/glm-decompose-dogfood-2026-06-26.md`
- Slidey report:
  `.artifacts/glm-decompose-dogfood/deck.slidey.json`

## Notes

The agent-bench harness was hardened to make this failure explicit, but that is
only observability at the scorer layer. The runtime should close interrupted,
timed-out, and provider-stalled lifecycles in the trace itself.
