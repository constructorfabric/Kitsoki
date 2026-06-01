# Run-status web UI

A read-only web view of a run as an **interactive state diagram**, a
**filterable trace timeline**, and a **detail drawer** that shows, for any
node or event, the resolved YAML/prompt and the recorded inputs/outputs. It
complements the TUI and `kitsoki viz` — it shows *where the run is*, *why it
went there*, and the full payload of every LLM and host call.

It is built from the [session trace](trace-format.md), the authoritative
record. The UI never mutates state; it only projects the trace.

The viewer is a Vue 3 single-page app under `tools/runstatus/`. It is
**bundled into the `kitsoki` binary** — there is no separate Node step to
generate or serve it. The SPA is built once by `make build` (which runs
`pnpm build` under `tools/runstatus/` and embeds the result); `kitsoki` then
produces artifacts and serves the live UI on its own.

There are two ways to view a run, from one bundle.

## Self-contained HTML artifact

A single `.html` file with the run's snapshot inlined — opens in any browser
over `file://`, no server, no kitsoki install. The portable bug-report format:
"attach the status.html".

From a recorded JSONL trace:

```
kitsoki export-status --from-trace run.jsonl --app myapp.yaml -o run.html
```

From an already-built snapshot JSON (wraps it in the UI — the Go replacement
for the former `scripts/build-artifact.mjs`):

```
kitsoki export-status --from-snapshot run.snapshot.json -o run.html
```

The same command emits raw Snapshot JSON instead when `-o` does not end in
`.html`. The fixtures under `tools/runstatus/fixtures/` are regenerated to
HTML with `make -C tools/runstatus/fixtures artifacts` (after `make build` has
staged the SPA).

`--from-snapshot` inlines any oracle-prompt sidecars (`prompt_file` /
`system_prompt_file`) referenced by events, resolving them relative to the
snapshot's directory, so the artifact is fully self-contained under `file://`.

## Live UI

A live, updating view of an in-progress (or finished) run, served over HTTP:

```
kitsoki run myapp.yaml --trace run.jsonl          # terminal 1
kitsoki status serve myapp.yaml --trace run.jsonl # terminal 2 → http://127.0.0.1:7777
```

The browser connects over JSON-RPC (`POST /rpc`) and Server-Sent Events
(`GET /rpc/events`); the server re-reads the trace and streams newly-appended
events as the run grows the file. The trace file need not exist yet when
serving starts — the UI shows an empty run until the first events are written.
Read-only; assumes a trusted localhost/internal network (no auth).

### Why `--trace`, not the session store

The live server reads the **JSONL trace**, not the SQLite session store,
because the trace is the full-fidelity record. The SQLite store persists only
`turn/seq/ts/kind/payload`; it does **not** persist per-event `state_path`,
`call_id`, or `parent_turn`. Those survive only in the JSONL trace, and the UI
needs them — `call_id` pairs `oracle.call.start`/`.complete`, `state_path`
groups events by state. Sourcing the live view from the store would silently
drop them, contradicting the rule that [the trace must always be
correct](../../tools/runstatus/CLAUDE.md). The artifact and live paths build
the snapshot from the same code (`internal/runstatus.SnapshotFromTrace`), so
the two views cannot drift.

## Where the pieces live

- `internal/runstatus/` — the `Snapshot` type and its builders
  (`FromHistory` from a store history; `ParseTrace` + `SnapshotFromTrace` from
  a JSONL trace), plus `RenderArtifact` (snapshot + bundled SPA → HTML).
- `internal/runstatus/web/` — the embedded SPA (`//go:embed`); built and
  staged by `make build`, gitignored otherwise.
- `internal/runstatus/server/` — the live HTTP/JSON-RPC/SSE surface.
- `cmd/kitsoki/export_status.go`, `cmd/kitsoki/status_serve.go` — the
  `export-status` and `status serve` commands.
- `tools/runstatus/` — the Vue 3 SPA source, fixtures, and Vitest/Playwright
  tests.
