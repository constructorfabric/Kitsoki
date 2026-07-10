# Epic: persistent artifact jobs (durable runs, shareable artifacts)

**Status:** In progress. The first substrate pass has shipped a local artifact-job registry and rebuildable run/artifact index in `internal/artifactjob`; the story-loader, dev-story adoption, runstatus serving, hosted store, and TUI/web console slices remain open.
**Kind:**   epic
**Slices:** 6 (0/6 fully shipped; registry/index substrate implemented)

## Shipped substrate

This pass moved the first durable primitives into narrative docs and code:

- `internal/artifactjob.Store` records durable artifact-job rows with stable `job_id`, session/run attachment, status, origin, workspace handle, terminal artifact handle, visibility, and restart interruption semantics.
- `artifactjob.SQLiteStore` stores local registry, run, and artifact rows beside the session store.
- `artifactjob.MemoryStore` provides deterministic no-LLM tests and future flow fixtures.
- `artifactjob.RunIndex` indexes runs and emitted artifacts by `job_id` without rewriting trace bytes.
- `artifactjob.ReindexTrace` rebuilds index rows from immutable JSONL and existing `artifact.emitted` events.
- `artifactjob.RunURL` establishes the stable `/run/<job-id>` route contract.
- Narrative docs now live in [`../stories/artifact-driven-stories.md`](../stories/artifact-driven-stories.md) and [`../tracing/trace-artifact-service.md`](../tracing/trace-artifact-service.md).

Validation: `go test ./internal/artifactjob`.

## Why

The user-facing unit we keep wanting is broader than "a workspace" and broader than "a trace": it is a **job of work** that can run for a while, survive the operator leaving, be reopened from another session, and be shared with the artifacts that explain what happened.

Today those pieces are split across background jobs, operation handles, dev-story workspaces, trace JSONL, runstatus, and GitHub-specific run links. The shipped substrate gives those surfaces one durable identity and index to build on.

## What changes when the epic is complete

- A long-running story job has a stable `job_id` and `run_url`.
- Reopening the job from another TUI/web session attaches to the existing run record instead of minting a fresh scratch context.
- Trace JSONL remains immutable; the run/artifact index is rebuildable from disk.
- Artifact-driven stories declare workspace artifacts in `artifacts:` and use keyed instances that can be discovered, rejoined, shared, published, archived, or reclaimed.
- Dev-story uses that substrate for design and delivery jobs rather than bespoke workspace scripts.
- TUI and web expose one artifact-job console for list/resume/open/share/publish/archive actions.
- GitHub-agent jobs consume the same run/artifact substrate.

## Slices

| # | Slice | Kind | Scope | Status | File |
|---|---|---|---|---|---|
| 1 | Artifact job registry | runtime | Durable job/run identity, session attachment, local store, restart semantics, and run URL contract | Partial substrate shipped; runtime integration still open | [`artifact-job-registry.md`](artifact-job-registry.md) |
| 2 | Trace + artifact service | tracing | Persist/index many runs by `job_id`, replay completed traces, and serve artifacts by handle | Partial index substrate shipped; serving still open | [`trace-artifact-service.md`](trace-artifact-service.md) |
| 3 | Workspace instances + resume | runtime | `artifacts:` story spec, keyed workspaces, discover/rejoin, back-step, and update-mode stale handling | Draft | [`artifact-instances.md`](artifact-instances.md) |
| 4 | Share/publish lifecycle | runtime | Promote draft workspaces, publish canonical docs, apply disposition, and enumerate/reclaim archives | Draft | [`artifact-publish-lifecycle.md`](artifact-publish-lifecycle.md) |
| 5 | Dev-story adoption | story | Make dev-story design/delivery jobs use artifact jobs, persisted workspaces, terminal artifacts, and no-LLM resume/share flows | Draft | [`dev-story-artifact-jobs.md`](dev-story-artifact-jobs.md) |
| 6 | Artifact job console | tui | TUI/web list, resume, share, publish, artifact gallery, run URL, and archive cleanup controls | Draft | [`artifact-instance-console.md`](artifact-instance-console.md) |

## Remaining sequence

1. Wire the registry into session/job/operation launch paths and call `SweepInterrupted` during startup.
2. Add the multi-run runstatus provider and artifact resolver backed by `RunIndex`.
3. Add `artifacts:` loader validation and `iface.instance.*` host handlers.
4. Migrate dev-story design workspace and publish paths onto the instance/lifecycle calls.
5. Add no-LLM flow fixtures for start/leave/list/resume and publish/archive parity.
6. Build the TUI/web artifact-job console.
7. Add hosted Postgres migrations matching the SQLite logical schema.

## Shared decisions retained

1. **Artifact job is a product identity, not a replacement for `internal/jobs`.** The existing scheduler still owns goroutine lifecycle and clarification.
2. **One job gets one stable run surface.** A job has one `job_id`, one primary `session_id`, and one `run_url`.
3. **Local first, hosted compatible.** The shipped SQLite schema is the local implementation; hosted Postgres remains follow-up.
4. **Trace bytes stay immutable.** The run/artifact index is derived from trace JSONL and can be rebuilt.
5. **Workspace artifacts and run artifacts are different views of the same work.** The current pass indexes run artifacts; workspace instances remain a later slice.
6. **Share and publish are independent.** Lifecycle host calls remain future work.
7. **No real LLM or real GitHub in tests.** Current coverage is pure Go unit tests.

## Non-goals

- Not the artifact file format; [`artifact-format.md`](artifact-format.md) owns schema validation and round-trips.
- Not GitHub ingress/auth/commenting.
- Not real-time co-editing.
- No object storage abstraction in v1.
- No hidden autonomous retries.

## Dogfood notes

Friction from this MCP-only implementation run is recorded in `.context/artifact-driven-stories-dogfood-report.md`.
