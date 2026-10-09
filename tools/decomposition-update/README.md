# tools/decomposition-update

The deterministic core of a **managed decomposition delta**: apply an
additive/edit change to an EXISTING decomposition graph without a blind
overwrite. No LLM — `apply_delta.py` is a pure, testable transaction.

```
python3 tools/decomposition-update/apply_delta.py <base> <delta> \
  --out <out> --versions-dir <dir> --event-log <plan-evolution.jsonl> \
  [--list-key changes|briefs] [--skip-validate] [--ledger <ledger.json>] [--version-id <id>]
```

- `base` / `delta` — YAML documents. `delta` must carry `trigger` (string),
  `provenance: {kind, ref}`, and a non-empty `operations` list (today:
  `add_change` only additive; `remove_change` / `replace_change` are
  rejected when the target id is locked by an active ledger state).
- `--out` — where the applied result is written. `--versions-dir` gets a
  copy of `base` BEFORE the write (`decomposition.<version-id>.yaml`) —
  the versioning half of "managed". `--event-log` gets one appended JSONL
  `plan_evolution` event per successful apply.
- `--list-key` (default `changes`) — the top-level list key BOTH `base` and
  `delta` operate on. The dev-workflow ledger graph uses `changes` (each
  item `{id, state, ...}`); a deliver-shaped decomposition manifest uses
  `briefs` (each item `{id, brief, gate_command, deps, ...}`) — same
  transaction machinery, different graph vocabulary.
- `--skip-validate` — skip the `tools/validate-decomposition-graph.py`
  candidate check. That validator is shaped for the `changes` graph; a
  caller with a different `--list-key` (deliver) runs its own deterministic
  validation downstream instead (deliver's `lint` room,
  `stories/deliver/scripts/lint_decomposition.star`).

stdout is always a single-line JSON envelope with an explicit `"route":
"ok"|"fail"` field (never bare `ok`/exit-code alone) — a caller branching in
a kitsoki story needs a tri-state string it can `bind: <slot>: stdout_json.route`
and reset to `""` before the invoke, the same idiom `lint_decomposition.star`
uses; a bool's zero-value would otherwise coincide with "failed" and break a
flow-test harness's discovery pass (see
[`stories/deliver/rooms/redecompose.yaml`](../../stories/deliver/rooms/redecompose.yaml)'s
comment for the mechanics).

## Consumers

- [`stories/decompose-update/`](../../stories/decompose-update/) — a
  standalone review-then-apply demo story (adversarial `host.agent.decide`
  gate in front of the transaction). Its own flow drives the SELF-TEST
  wrapper (`tests/run_no_llm.py`), not a real base/delta pair.
- [`stories/deliver/`](../../stories/deliver/) — the real caller. `deliver`'s
  `decompose` room detects a prior `decomposition.yaml` on disk
  (`scripts/detect_prior_decomposition.star`) and, when one exists, routes to
  `redecompose` instead of letting the decomposer agent overwrite it: the
  decomposer authors an additive delta, then `redecompose_apply` invokes THIS
  tool directly with `--list-key briefs --skip-validate` (deliver's own
  `lint` room, entered next, is the deterministic validation step for that
  shape). See `redecompose_managed_delta` in
  [`stories/deliver/flows/`](../../stories/deliver/flows/redecompose_managed_delta.yaml).

## Tests (no LLM)

```
python3 tools/decomposition-update/tests/run_no_llm.py
```

Exercises an accepted delta (versions the base, writes `out`, appends the
event log), a delta with a dependency cycle (rejected, no artifacts written),
and a delta missing `provenance` (rejected with a specific message).
