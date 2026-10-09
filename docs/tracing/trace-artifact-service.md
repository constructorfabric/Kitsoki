# Trace artifact service substrate

Kitsoki traces remain append-only JSONL. The artifact-job trace service starts as a rebuildable index over those immutable traces plus the artifact files already emitted by host calls.

## What is implemented

`internal/artifactjob` provides the local substrate:

- `RunIndex` stores one run row per artifact job.
- `Artifact` rows index emitted handles by `(job_id, handle)`.
- `SQLiteStore` stores registry, run, and artifact rows in SQLite.
- `MemoryStore` provides deterministic tests and no-LLM harness fixtures.
- `ReindexTrace` rebuilds run/artifact rows from a trace JSONL file.

The index does not rewrite the trace and is not the source of truth for replay. It answers catalog questions quickly: which run belongs to this job, where is its trace, and which artifact path/mime belongs to this handle.

## Event source

The index consumes existing artifact events:

```json
{"ev":"artifact.emitted","body":{"id":"demo#1","kind":"video","mime":"video/mp4","label":"Demo","path":"/runs/demo.mp4","size_bytes":1234}}
```

It also accepts slog-style records where the event name is in `msg` and artifact fields are in `attrs` or `attrs.body`.

## URL shape

`artifactjob.RunURL(baseURL, jobID)` mints the stable route path:

```text
/run/<job-id>
```

With a hosted base URL, the path is appended under that base. The runstatus server and web console still need to consume this route; the current commit only establishes the durable data and URL contract.

## Remaining service work

The full service still needs:

- a multi-run runstatus `SessionProvider` that can stream live jobs or replay completed traces by `job_id`;
- HTTP/RPC routes for `/run/<job-id>` and by-handle artifacts backed by the index;
- `runstatus.sessions.list` rows merged with artifact jobs;
- hosted Postgres migrations matching the SQLite logical schema;
- gallery RPCs for ordered artifact metadata.

Those pieces can build on the current `Store` and `RunIndex` interfaces without changing trace bytes.
