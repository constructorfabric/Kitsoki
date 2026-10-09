# Capsule CI receipts

A Capsule CI receipt is the portable evidence handoff for a run. Its canonical
schema is `capsule-ci-receipt/v1` and its `receipt_id` is the SHA-256 digest of
canonical JSON with integrity fields cleared.

The receipt joins the sealed execution envelope, a validated
`capsule-ci-verdict/v1`, artifact handles, and trace custody digest. It records
source, story, environment, executor policy, and verdict digests so promotion
cannot accidentally use evidence from another commit or environment.

The story identity is a `capsule-story-closure/v1` digest, not merely the entry
`app.yaml`. It covers every loaded/imported manifest root and behavior-bearing
prompt, schema, view/template, Starlark file, and kit lock while excluding
flows, cassettes, scenarios, and test data. Symlinked or project-escaping story
dependencies are rejected. Remote workers recompute this closure after source
materialization before running the story.

`internal/capsule/trace` defines the shared `capsule-ci-trace/v1` document
schema and event-kind constants for workspace lifecycle, environment, CI, and
sync facts. Producers should use those constants instead of string literals so
receipt rebuilders, runstatus projections, and provider comments consume one
event vocabulary.

`internal/capsule/receipt` provides deterministic build and verify operations:

- missing job, envelope, environment, verdict, or trace facts yield
  `incomplete`, never an inferred pass;
- a changed envelope, verdict, artifact order, or trace digest invalidates the
  content hash;
- signing is injected behind a verifier seam and is required only when project
  policy demands it;
- `promotion_eligible` is accepted only when the verdict's matching digests and
  required evidence validate.

Projects require receipt signatures with checked-in CI policy:

```yaml
receipt:
  require_signature: true
  signer: test-signer
```

When enabled, receipt persistence signs with the injected signer and verifies
the signer name before writing a valid receipt. Promotion gates reload the same
project policy and reject unsigned receipts, missing signers, or signer-name
mismatches. Local projects can keep unsigned receipts by omitting the policy;
tests use `receipt.FakeSigner` for deterministic no-credential coverage.
`kitsoki capsule ci run` and `kitsoki capsule mcp` expose
`--fake-receipt-signer <id>` for local/test projects that intentionally require
signed receipts without introducing real key material.

CLI/MCP CI runs write a compact controller trace sidecar and a verified receipt
alongside their local run record; status includes receipt identity and
verification. `capsule ci status` and `capsule.ci.status` now expose a compact
`capsule-ci-run-index/v1` projection with job status, pipeline outcome,
promotion eligibility, receipt verification, digest bindings, and relative
trace/receipt sidecar paths. Runstatus/provider publication can consume that
projection on top of the same receipt schema; it must not introduce a second
evidence format.

The receipt binds the requested policy and the provider capability acceptance
that was observed by the controller. It does not fabricate a physical sandbox
claim: host placement is recorded as trusted live-network compatibility, while
none/replay enforcement is an explicit container/worker deployment attestation.

`capsule ci summary` and `capsule.ci.summary` consume the run index into
`capsule-ci-provider-summary/v1`: aggregate counts, latest safe run rows, and a
Markdown body suitable for a future provider comment. The summary includes only
provider-safe fields from the index; live publication remains a separate
adapter.

## Live checkpoints and diagnosis

The controller writes `requested`, `preparing`, `running`, and terminal
`finished|failed` checkpoints before a receipt exists. Each checkpoint rewrites
the run record and compact trace through a temporary file, `fsync`, and rename;
an observer persistence failure stops the run before the next side effect. This
makes an interrupted or stalled job diagnosable from another process without
trusting the driver's terminal output.

```sh
kitsoki capsule ci diagnose --job <job-id> --json=false
kitsoki capsule ci diagnose --latest --stall-after 2m --json=false
kitsoki capsule ci status --job <job-id> --refresh
```

`capsule-ci-run-diagnosis/v1` projects the terminal error, failure kind, last
allowlisted executor event, durable stage/activity time, open-span and stall
classification, local evidence paths, and copy-ready follow-up commands. Raw
provider output remains local; the compact trace accepts only provider-safe
transport/status/duration/error fields and rejects absolute host paths or
secret-like values.

An HTTPS worker keeps a second durable custody record under
`<worker-root>/runs/<execution-id>/run.json` plus `story-trace.jsonl`. Its
timeline records source materialization, story-closure and environment-lock
verification, story launch, and terminal/cancel state. Controller and worker
request/execution identifiers join the two sides when a transport failure
prevents a terminal receipt.
`GET` status projects the live story trace into
`capsule-agent-diagnostics/v1`. The projection is deliberately lossy and
provider-safe: it retains only hashed call references, enum lifecycle/process
phases, numeric timing/count facts, last activity, and typed stall hints. It
never includes prompts, responses, absolute paths, command arguments,
environment values, or raw provider errors. The controller persists this
projection in `ExecutionStatus.Agent`, and diagnosis uses its last activity and
stall hint before falling back to the controller checkpoint time.

The worker also persists `capsule-worker-cleanup/v1` history plus
`cleanup/latest.json`. Startup and periodic passes retain newest terminal runs,
age-gate and bound every mutation, never remove active/nonterminal/invalid
runs, and delete only aged source bundles that no remaining valid run
references. Any uncertain run reference blocks source deletion. Status carries
only the path-free cleanup outcome/count/byte projection.
Remote status/cancellation uses `capsule-execution-status/v1`: a cancellation
request stays non-terminal until the worker confirms `cancelled`, and a late
`completed|failed` fact is never overwritten by a local cancellation claim.

Sync and promotion traces join the same evidence stream through
`capsule.sync.planned`, `capsule.sync.applied`, `capsule.sync.stale`, and
`capsule.sync.conflicted` facts. These events carry the plan digest,
operation/class, target ref, candidate commit, old/new target where relevant,
and the conflict continuation token when deterministic apply must pause for a
story resolver and independent lost-work review.

For a diverged stored plan, `kitsoki capsule sync conflicts --plan <digest>`
and `kitsoki capsule sync integration --plan <digest>` materialize
`capsule-sync-conflict/v1` and `capsule-sync-integration/v1` artifacts under
`.capsules/sync/`; scoped agents use `capsule.sync.conflicts` and
`capsule.sync.integration` for the same server-owned plan. After resolution,
`kitsoki capsule sync continue` or `capsule.sync.continue` applies the resolved
integration commit only when resolver decision, independent lost-work review,
and validation receipt inputs are present and the resolved commit preserves
both histories. `kitsoki capsule sync abort` and `capsule.sync.abort` emit
`capsule.sync.aborted` and can preserve a project-relative abort patch before
removing the managed integration instance. The artifacts record the merge base,
candidate/target changed paths, overlap paths, required resolver/reviewer/
validation inputs, the managed integration instance, and the continuation token
that story traces must later reference.
