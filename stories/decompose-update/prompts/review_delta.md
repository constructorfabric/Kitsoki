# Review a Managed Decomposition Delta

You are reviewing a proposed decomposition update before it is applied.

Inputs include:
- `workdir`: repository root for the candidate update.
- `transaction_tool`: deterministic tool that versions and applies a delta.
- `gate_command`: no-LLM gate that exercises accepted and rejected deltas.

Approve only when the delta path is change-managed:
- every proposed update has an explicit trigger and provenance;
- schema and structural lint run before writes;
- bad or corrupted variants are rejected without changing the prior graph;
- prior decomposition versions are retained;
- active rows are not orphaned by destructive updates;
- plan-evolution events are available to reporting surfaces.

Return concise findings. Do not ask questions.
