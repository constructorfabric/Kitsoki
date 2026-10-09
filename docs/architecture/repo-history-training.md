# Repo History Training

Kitsoki treats repository history as training material for stories, not as
ambient memory. A history lesson becomes useful only when it is represented as a
typed case, armed by a deterministic oracle, and promoted into a story artifact:
prompt text, Starlark glue, graph structure, flow fixture, agent eval, docs, or a
selection policy.

The v1 contract is `history_task.v1`. It is intentionally lane-neutral:

- `bugfix` cases use the external bakeoff RED/GREEN oracle discipline.
- `agent_eval` cases use story-local eval datasets and comparators.
- `flow_fixture`, `static_check`, and artifact-comparator cases can be added
  without changing the corpus model.
- `human_review_required` is only a queue state; it is not autonomous training
  evidence until promoted into a deterministic check.

Validate committed or drafted cases with:

```sh
go run ./cmd/kitsoki history task-cases validate tools/history-training/examples
```

Adapt an existing bugfix-bakeoff manifest into lane-neutral task cases with:

```sh
go run ./cmd/kitsoki history task-cases adapt-bugfix \
  tools/bugfix-bakeoff/external/projects/query-string/manifest.yaml \
  --bug qs1,qs2 \
  --validate
```

`make history-smoke` runs that adapter for the selected bakeoff rows and writes
the JSON review artifact under `.artifacts/history-training/readiness/` before
it performs preflight, RED/GREEN arming, handoff preparation, and `repo-bakeoff`
flow validation. The command does not call a live LLM.

## Manifest Contract

Each case records:

- source provenance (`source.corpus_ref`, repo, baseline/success refs);
- the target story and entrypoint;
- the trainable surface that would receive a promoted improvement;
- the operator-facing input or ticket;
- the oracle or comparator that arms the case;
- the cost policy for live cells;
- an artifact root under `.artifacts/`.

The validator rejects unarmed autonomous cases. For example, `red_green` requires
baseline and success refs plus an oracle command, while static checks require a
command or comparator. This keeps mining volume from becoming training noise.

## Promotion Rule

A case is not "trained" when it is mined. It is trained only when the accepted
outcome lands in a durable Kitsoki artifact and that artifact passes its
deterministic validation gate.
