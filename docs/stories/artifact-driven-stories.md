# Artifact-driven stories

Artifact-driven stories produce durable work products: proposals, PRDs, QA reports, screenshots, videos, decks, terminal completion JSON, and similar handoff files. The runtime now has the first substrate for treating that work as a durable **artifact job** instead of only as a live session or a scratch workspace.

## Artifact jobs

An artifact job is a user-facing product record. It is not a scheduler goroutine and does not replace `internal/jobs`. The scheduler still owns handler execution and clarification; `internal/artifactjob` records the stable job identity that other surfaces can list, reattach, index, and eventually share.

A job row stores:

| Field | Purpose |
|---|---|
| `job_id` | Stable ID for the work item and `/run/<job-id>` URL. |
| `session_id` | Primary Kitsoki session/run that owns the trace. |
| `app_id` / `story` | App or story identity for filtering. |
| `origin_kind` / `origin_ref` / `origin_url` | Front door that created the job: dev-story, operation, GitHub, CLI, etc. |
| `status` | `running`, `awaiting_input`, `interrupted`, `done`, `failed`, `cancelled`, or `archived`. |
| `run_url` / `trace_path` | Stable viewer link and immutable JSONL source. |
| `workspace_instance_id` | Optional editable workspace handle for future instance adoption. |
| `terminal_artifact_handle` | Optional by-handle artifact emitted at completion. |
| `summary` / `phase` | Compact operator-facing status. |
| `visibility` / `owner` | Local/private/shared/auth-gated metadata for local and hosted stores. |

Local storage is implemented by `artifactjob.SQLiteStore`, which can share the same SQLite connection as the session store. Tests and flow harnesses can use `artifactjob.MemoryStore`.

## Run and artifact index

Trace JSONL remains immutable. The run/artifact index is a rebuildable projection over those trace bytes:

1. Register or load the artifact job.
2. Bind the job to its session, trace path, and run URL.
3. Rebuild the index with `artifactjob.ReindexTrace`.
4. Resolve emitted artifacts by `(job_id, handle)`.

The index consumes existing `artifact.emitted` events rather than introducing a new artifact recorder. It accepts both the journal shape (`ev: artifact.emitted`, `body: {...}`) and slog trace shape (`msg: artifact.emitted`, attrs/body fields), so completed runs can be indexed after the fact.

## Restart semantics

Artifact jobs make interruption explicit. On process startup, a caller can run `Store.SweepInterrupted(reason)` to mark `running` and `awaiting_input` rows as `interrupted`. The row remains listable and can offer a story-specific resume action, but the registry never pretends a dead goroutine survived.

## Authoring model

The remaining story-authoring work is intentionally not hidden in the registry package. Future slices add:

- `artifacts:` story declarations for workspace items and lifecycle policy.
- `iface.instance.*` host calls for get-or-create workspaces, share, publish, dispose, and retention scans.
- Dev-story migration from `design_workspace.star` / `publish_design.py` onto those host calls.
- TUI/web artifact-job console rows, actions, and galleries.

Until those slices land, stories can still use `host.artifacts_dir` for emitted artifacts and can register jobs from runtime integration code without changing YAML loader behavior.

## Verification

The substrate is covered without real LLM or GitHub calls:

```sh
go test ./internal/artifactjob
```

That test suite exercises memory and SQLite stores, interruption sweep, `/run/<job-id>` URL minting, and deterministic reindexing of `artifact.emitted` trace rows.
