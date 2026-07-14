---
name: capsule-ci-operations
description: Operate and troubleshoot story-native Kitsoki Capsule CI with durable evidence and bounded cleanup. Use when preflighting or running `kitsoki capsule ci`, diagnosing a stalled/local/remote run, starting an HTTPS Capsule worker, checking traces or receipts, reclaiming Capsule disk usage, or performing the final two-run live remote dogfood after deterministic no-LLM proof.
---

# Capsule CI operations

Treat every run as an evidence-producing job, not an interactive retry loop.
Run from the project root containing `.kitsoki/ci.yaml`. In a Kitsoki source
checkout, invoke the current source with `go run ./cmd/kitsoki`; installed users
may replace that prefix with `kitsoki`.

When the durable CI diagnosis reaches the story layer and shows a room bounce or
host-call failure, continue with
[`kitsoki-debugging`](../kitsoki-debugging/SKILL.md) using the preserved story
trace. Keep transport/materialization failures in this workflow.

## 1. Keep implementation in a managed development workspace

If diagnosis leads to code or story changes, use the repository lifecycle:

```sh
scripts/dev-workspace.sh create --id <id> --branch agent/<id> --bootstrap
scripts/dev-workspace.sh status <id>
# edit and validate inside the reported workspace path
scripts/dev-workspace.sh commit <id> --message "<message>"
scripts/dev-workspace.sh merge <id> --gate "<focused validation>" --teardown
```

Do not manage clone, branch integration, or teardown yourself. Keep the primary
checkout read-mostly. A failed merge leaves the workspace available; inspect its
reported state, rerun the gate after recovery, and retry the supported command.

## 2. Establish a no-spend baseline

Identify the declared pipeline, story, executor, agent policy, and cleanup policy
in `.kitsoki/ci.yaml`. Then run these gates in order:

```sh
go run ./cmd/kitsoki capsule workspace list --project . --json
go run ./cmd/kitsoki capsule workspace status --project . --id <workspace> --json
go run ./cmd/kitsoki validate <story-app.yaml>
go run ./cmd/kitsoki test flows <story-app.yaml>
go run ./cmd/kitsoki capsule ci doctor <pipeline> --project . --workspace <workspace> --json=false
```

`doctor` is the required readiness gate. It may contact a configured worker's
capability endpoint and prepare the sealed envelope locally, but it does not
upload source, invoke the story, or launch an agent. It must prove a
clean/current workspace, complete story closure, sealed environment,
credentials by name, executor policy parity, bounded HTTP deadlines, and disk
headroom. Fix failed checks; do not run through them.

Treat the selected managed workspace as the execution source of truth. Its
`.kitsoki/ci.yaml`, story closure, and environment inputs—not a remembered
controller checkout—must produce the envelope. Keep controller run evidence
under the operator project's `.capsules/ci`; compare both roots explicitly when
diagnosing a digest mismatch.

Run the story locally and through the fake-remote/no-agent fixtures before any
live model call. The checked-in test path must use flows, cassettes, fake
executors, or `agents.policy: deny`. Never turn a live profile on merely to test
transport, materialization, verdict validation, tracing, or receipts.

## 3. Preflight a remote worker

Start the worker with HTTPS, bearer authentication, a durable root, truthful
isolation/network claims, and the smallest environment allowlist:

```sh
go run ./cmd/kitsoki capsule worker serve \
  --listen <host:port> \
  --root <durable-root> \
  --tls-cert <certificate.pem> \
  --tls-key <private-key.pem> \
  --token-env <TOKEN_ENV> \
  --isolation <vm-or-container> \
  --network none \
  --agent-backend claude \
  --pass-env <provider-key-env>
```

Configure only the HTTPS endpoint, credential environment name, and optional
project-relative CA file under `.kitsoki/ci.yaml`; never place a token value in
configuration, a command argument, trace, or receipt. Run `doctor` from the
controller after the worker starts. A green remote doctor proves authenticated
capability negotiation, bounded controller timeouts, and policy compatibility
without uploading source or starting the story. Prove source upload,
materialization, and digest verification with the no-agent remote run.

Worker evidence lives under `<durable-root>/runs/<execution-id>/`: `run.json`
is the durable stage/status record and `story-trace.jsonl` is the story/agent
event stream. Preserve these when controller evidence is incomplete.

## 4. Run and inspect durable checkpoints

Start one job with:

```sh
go run ./cmd/kitsoki capsule ci run <pipeline> --project . --workspace <workspace>
```

From another terminal, inspect the durable controller projection instead of
polling the driving process:

```sh
go run ./cmd/kitsoki capsule ci status --project .
go run ./cmd/kitsoki capsule ci status --project . --job <job-id> --refresh
go run ./cmd/kitsoki capsule ci diagnose --project . --latest --stall-after 2m --json=false
go run ./cmd/kitsoki capsule ci diagnose --project . --job <job-id> --json=false
```

Controller artifacts are `.capsules/ci/<job-id>.run.json`,
`.capsules/ci/<job-id>.trace.json`, and, after a valid terminal verdict,
`.capsules/ci/<job-id>.receipt.json`. The trace must show the last durable stage,
timestamped executor lifecycle, request/execution identifiers, provider-safe
failure classification, and exact source/story/environment/envelope digests.

If `diagnose` reports a stall, save its output, controller trace, worker
`run.json`, and worker story trace. Cancel once through the supported CI command
when appropriate:

```sh
go run ./cmd/kitsoki capsule ci cancel --project . --job <job-id>
```

Confirm the worker `run.json` reaches `cancelled`; a controller-only cancelled
projection with an open remote span is a cancellation defect, not success. Do
not erase the workspace or evidence and do not immediately retry. Cancellation
is two-phase: `cancel` persists `cancelling`/interrupted until the worker reports
terminal `cancelled`; use `status --job <job-id> --refresh` to query and persist
that durable worker fact. First explain any last open span or missing terminal
event.

Classify the failure before changing anything:

| Last reliable boundary | Evidence to inspect | Typical repair owner |
|---|---|---|
| `doctor` check failed | named check, remedies, workspace status, cleanup plan | project config/workspace/operator |
| source package/upload | controller executor events; worker `sources/*/meta.json` | source transport/controller |
| worker materialization/closure/environment | worker `run.json` stage/error | worker/source/story/environment lock |
| story launched, no verdict | `story-trace.jsonl` last host call/state | story/host; continue with `kitsoki-debugging` |
| `host.agent.*` open | agent start/stream/error events and allowlisted env presence | agent backend/provider/auth |
| typed verdict returned, receipt absent | controller verdict validation and receipt verification | contract/tracing/signer |
| controller cancelled, worker running | controller execution id plus worker GET/DELETE state | cancellation adapter |

Do not collapse these into “remote CI failed.” Record the boundary, request id,
execution id, last timestamp, and absent expected event. A retry without a new
deterministic hypothesis is not evidence.

## 5. Bound the live-model gate to two jobs

Use a live agent only after all no-LLM gates above are green and the user has
explicitly authorized spend. Confirm the selected project profile resolves to
the requested model and that `.kitsoki/ci.yaml` seals an allowlisted profile,
cost ceiling, unavailable-model fallback, network policy, and remote executor.

The live dogfood sample is exactly two small jobs and never a third retry:

1. Run job A; record its controller job id and remote execution id.
2. Run job B with the same sealed configuration; record both ids.
3. Diagnose and report failures as results. Fix deterministically before asking
   for a future live gate; do not replace either job with an unrecorded attempt.

Keep the task trivial: the gate proves controller-to-worker materialization,
Claude Code/provider launch, durable activity, typed verdict, trace, receipt,
and cleanup—not model capability.

## 6. Keep disk hygiene continuous

Review before applying:

```sh
go run ./cmd/kitsoki capsule cleanup plan --project . --json=false
go run ./cmd/kitsoki capsule cleanup apply --project . --json=false
```

Use `--pin-workspace <id>` for work retained for investigation. Opt into
Capsule/Go cache cleanup only after reviewing the JSON plan. Cleanup must retain
active, dirty, recent, pinned, or ownership-ambiguous workspaces and recheck
every candidate immediately before deletion. The same plan discovers
sentinel-owned nested control projects under `.capsules/projects/`; it closes
safe child workspaces through their manager first and only removes a proven
empty, inactive, old project root on a later pass. Invalid or changed project
provenance remains visible and unsafe. Never substitute manual removal.

Finish every investigation with job ids, exact artifact paths, last durable
stage, classified root cause, cleanup bytes reclaimed/skipped, and the focused
validation that proves the remedy.
