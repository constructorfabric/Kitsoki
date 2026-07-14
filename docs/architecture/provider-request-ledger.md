# Provider request ledger

`provider-request-ledger/v1` is the append-only accounting boundary for a
stored agent trace. It emits one row for every `agent.call.complete` or
`agent.call.error` paired with its `agent.call.start`; retries are distinct
because they have distinct call IDs. Duplicate terminal evidence is rejected,
never merged or overwritten.

Each row binds run, attempt, stage, requested/resolved model identity,
monotonic start/end timestamps, token/cache buckets, decimal money fields, and
a SHA-256 digest of the paired evidence. Failed rows remain in the ledger even
when usage or billing is unavailable.

`friction/v1` is a separate trace-only sidecar. It reports observed tool,
schema, retry, and no-state-change counts with line-level evidence. Token
attribution and first-success timing are marked unavailable with a reason when
the trace cannot support them; unavailable never means zero.

The initial API is `internal/agentbench.BuildProviderRequestLedger` and
`AnalyzeFriction`. It is intentionally provider-free, so historical evidence
can be reprocessed without live spend.

`provider-result/v1` is the result-level companion used by restartable Arena
attempts. `BuildProviderResultReceipts` emits terminal results in trace order,
including failed and retried calls, an attempt ordinal, API duration, and a
hash of the raw provider usage object when one exists. A result receipt is not
an estimate: absent usage is retained as unavailable evidence and must not be
converted into zero-cost spend.

Batch 3's `AnalyzeFriction` derives the `friction/v1` sidecar from the same
stored trace without provider access. It reports evidence-backed tool errors,
schema failures, retries, no-state-change calls, wasted tokens, crawl tokens,
and time-to-first-success. A metric with no supporting event remains
unavailable with a reason, rather than being reported as a misleading zero.
