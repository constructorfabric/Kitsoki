# Deterministic context packets

`internal/agentbench.CompileContextPacket` implements `context-packet/v1`, the
Batch 4 boundary for pre-emptive task context. It is an offline compiler: no
provider is contacted and no model is asked to choose or reorder context.

Stable requirements are sorted first by source and ID and form the cacheable
prefix. Volatile task requirements and exemplars follow in their own sorted
section. The packet records independent SHA-256 hashes for the stable prefix
and task section, so a later optimization can change task context without
silently changing the contract prefix.

The compiler is intentionally small. Mining trace-derived requirements and
dispatching a model-based packet refiner are later treatments, not implicit
side effects of packet construction.
