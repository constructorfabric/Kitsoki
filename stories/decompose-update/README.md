# decompose-update

Story wrapper for the managed decomposition-update transaction.

The story has an adversarial review room before the deterministic transaction
gate. Tests stub `host.agent.decide`, so the release gate does not call a live
LLM or incur model cost.

```bash
go run ./cmd/kitsoki test flows stories/decompose-update/app.yaml --flows stories/decompose-update/flows/managed_delta.yaml
```

The wrapped transaction lives in
[`tools/decomposition-update/apply_delta.py`](../../tools/decomposition-update/README.md).
The deterministic tool versions the prior graph, validates candidate deltas
before writing, rejects corrupted variants, and appends `plan-evolution.jsonl`
events that goal-seeker reports project into PM artifacts.

This story's own flow only drives the self-test wrapper
(`tests/run_no_llm.py`) behind an adversarial review gate — a demo of the
review-then-apply shape, not a real base/delta pair. The real caller is
[`stories/deliver/`](../deliver/): its `redecompose` room invokes
`apply_delta.py` directly (`--list-key briefs --skip-validate`) when a prior
decomposition already exists, instead of letting the decomposer agent
overwrite it (proposal: deliver-canonical-decomposition B2c). See
`tools/decomposition-update/README.md`'s Consumers section for the full
picture.
