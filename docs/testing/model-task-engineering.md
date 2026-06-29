# Model Task Engineering

Model task engineering is the repeatable loop for making a provider-backed story
step reliable. It was introduced while hardening the GLM-5.2 deliver decomposer:
the same loop applies to Claude, Codex, GLM, or any future provider profile.

The loop is built around `kitsoki agent-bench` and the
`stories/model-task-engineering` story. The story scores existing traces and
produces review artifacts; live provider runs remain behind the explicit
`agent-bench run --live` gate.

## What the Loop Controls

- Task boundary: one story room, agent definition, or bench manifest case.
- Model environment: tools, prompt, authoritative inputs, fallback discovery,
  output budget, and submit contract.
- Provider usage: tokens, cost, wall time, tool calls, read calls, files read,
  lifecycle events, and in-flight agent calls.
- Evidence: JSON report, Markdown report, Slidey deck, trace path, and
  deterministic tests.

## Recommended Workflow

1. Create or pick an `agent_bench/v1` manifest case for the task.
2. Run or capture one trace. A live run must be explicit:

   ```sh
   go run ./cmd/kitsoki agent-bench run <bench.yaml> --case <case> --live
   ```

3. Score the trace offline and write all review artifacts:

   ```sh
   go run ./cmd/kitsoki agent-bench score <bench.yaml> \
     --case <case> \
     --trace <trace.jsonl> \
     --json-out .artifacts/model-task-engineering/<case>/report.json \
     --markdown-out .artifacts/model-task-engineering/<case>/report.md \
     --slidey-out .artifacts/model-task-engineering/<case>/deck.slidey.json
   ```

4. Classify failures:
   - lifecycle: started agent calls without terminal returned/error events.
   - tool fanout: too many tools or forbidden tool use.
   - context fanout: excessive reads, files, or input tokens.
   - output pressure: output budget too low for a valid artifact.
   - ambiguity: broad discovery, unclear success target, or missing submit rule.
5. Change the task environment, not just the budget:
   - remove unnecessary tools;
   - point the prompt at the authoritative input;
   - cap fallback discovery;
   - move deterministic parsing/linting into scripts;
   - keep the submit/final-state expectation strict.
6. Re-score the old trace and the new trace. The old failure should remain
   explainable, and the new success should pass without weakening the gate.

## Story Wrapper

The `stories/model-task-engineering` story wraps the offline score step. It is
useful when dogfooding this process through Kitsoki itself:

```sh
go run ./cmd/kitsoki test flows stories/model-task-engineering/app.yaml
go run ./cmd/kitsoki run stories/model-task-engineering/app.yaml
```

The flow fixture stubs `host.run`, so it is CI-safe and does not call a model.

## Artifact Contract

Every serious tuning run should produce:

- `report.json`: machine-readable pass/fail and metrics.
- `report.md`: reviewable human summary suitable for `.context/` or issues.
- `deck.slidey.json`: a shareable Slidey status deck.
- the scored trace path;
- the exact story/prompt/tool diff;
- filed bugs for any harness/runtime gap uncovered by the run.

## GLM-5.2 Deliver Decomposer Example

The GLM-5.2 decomposer succeeded after the task was narrowed from broad repo
discovery to proposal-first decomposition:

- tools reduced to `Read`, `Edit`, and `Write`;
- `docs/proposals/` made authoritative;
- fallback discovery capped;
- output budget raised to match the observed valid artifact;
- lifecycle scoring added so interrupted calls fail instead of looking green.

The same pattern is the default playbook for future provider-specific trouble:
measure first, change the task environment, re-score offline, and keep evidence.
