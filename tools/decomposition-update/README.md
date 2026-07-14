# tools/decomposition-update

The deterministic core of a **managed decomposition delta**: apply an
additive/edit change to an EXISTING decomposition graph without a blind
overwrite. No LLM — `host.decomposition.update` is a pure, testable native
transaction exposed to stories through Starlark.

The `decomposition_update.star` adapter accepts `base`, `delta`, `out`,
`versions_dir`, `event_log`, `list_key`, `skip_validate`, `ledger`, and
`version_id` inputs and returns a structured `{route, ok, summary, error}`
result.

- `base` / `delta` — YAML documents. `delta` must carry `trigger` (string),
  `provenance: {kind, ref}`, and a non-empty `operations` list (today:
  `add_change` only additive; `remove_change` / `replace_change` are
  rejected when the target id is locked by an active ledger state).
- `out` — where the applied result is written. `versions_dir` gets a
  copy of `base` BEFORE the write (`decomposition.<version-id>.yaml`) —
  the versioning half of "managed". `--event-log` gets one appended JSONL
  `plan_evolution` event per successful apply.
- `list_key` (default `changes`) — the top-level list key BOTH `base` and
  `delta` operate on. The dev-workflow ledger graph uses `changes` (each
  item `{id, state, ...}`); a deliver-shaped decomposition manifest uses
  `briefs` (each item `{id, brief, gate_command, deps, ...}`) — same
  transaction machinery, different graph vocabulary.
- `skip_validate` — skip the graph candidate check
  candidate check. That validator is shaped for the `changes` graph; a
  caller with a different `--list-key` (deliver) runs its own deterministic
  validation downstream instead (deliver's `lint` room,
  `stories/deliver/scripts/lint_decomposition.star`).

The result always carries an explicit `route: "ok"|"fail"` field (never a bare
bool or exit code), so a story can branch on the tri-state result without
confusing an unset value with failure (see
[`stories/deliver/rooms/redecompose.yaml`](../../stories/deliver/rooms/redecompose.yaml)'s
comment for the mechanics).

## Consumers

- [`stories/decompose-update/`](../../stories/decompose-update/) — a
  standalone review-then-apply demo story (adversarial `host.agent.decide`
  gate in front of the transaction). Its self-test runs in the native handler,
  not in a Python subprocess.
- [`stories/deliver/`](../../stories/deliver/) — the real caller. `deliver`'s
  `decompose` room detects a prior `decomposition.yaml` on disk
  (`scripts/detect_prior_decomposition.star`) and, when one exists, routes to
  `redecompose` instead of letting the decomposer agent overwrite it: the
  decomposer authors an additive delta, then `redecompose_apply` invokes the
  native capability with `list_key: briefs` and `skip_validate: true` (deliver's own
  `lint` room, entered next, is the deterministic validation step for that
  shape). See `redecompose_managed_delta` in
  [`stories/deliver/flows/`](../../stories/deliver/flows/redecompose_managed_delta.yaml).

## Tests (no LLM)

The native handler self-test exercises an accepted delta (versions the base,
writes `out`, appends the event log), a delta with a dependency cycle
(rejected, no artifacts written), and a delta missing `provenance` (rejected
with a specific message).
