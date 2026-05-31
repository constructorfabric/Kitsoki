# Run-status web UI

**Status:** Phase 1 (artifact mode) ~90% shipped; Phases 2–3 remain.
Shipped: `FlowchartWithMap()`/`NodeRef` (`internal/viz/nodemap.go`),
the `Snapshot` type (`internal/runstatus/`), `kitsoki export-status
--from-trace`, and the full Vue 3 SPA with fixtures and Vitest +
Playwright tests (`tools/runstatus/`). Remaining:
- **Phase 1 step 6** — `export-status -o status.html` single-file
  build from the in-process ring buffer (stubbed in
  `cmd/kitsoki/export_status.go`).
- **Phase 2** — timeline virtualization for large traces and
  URL-persisted expand/collapse state.
- **Phase 3** — live mode: an HTTP+SSE listener folded into `kitsoki
  oracle serve` with the `runstatus.*` JSON-RPC handlers the SPA's
  `live-source.ts` already expects.

The shipped artifact surface is not yet covered in narrative docs;
fold it into `docs/tracing/` when Phase 1 closes. The sections below
are the actionable agenda for the three remaining items.

## Goal

A Vue 3 single-page app that shows a kitsoki run as:

1. An **interactive state-machine diagram** — the same flowchart
   `internal/viz` already emits, but clickable.
2. A **trace timeline** — every event from the ring buffer,
   grouped by turn, filterable, with full payloads for LLM and host
   calls.
3. A **detail drawer** that, for any node or event, surfaces the
   exact YAML, the resolved prompt or `with:` template, and the
   recorded inputs / outputs.

One Vite build, two delivery modes:

- **Self-contained HTML artifact** — `kitsoki export-status` writes
  a single file with the snapshot inlined; opens with `file://`.
- **Live** — the existing oracle daemon gains an HTTP listener;
  the SPA connects via JSON-RPC + SSE and updates as the run
  progresses.

Read-only. No editing, no drag/drop. Complements the TUI; does not
replace it.

## Motivation

Today the only ways to inspect a run are:

- The TUI, which renders the current turn but does not expose the
  state graph or the resolved templates.
- `kitsoki viz --flowchart`, which emits a Mermaid diagram but
  cannot show *where the run is* or *why it went there*.
- Tailing the JSONL trace file by hand.

For dogfood debugging and for showing the system to non-authors,
these aren't enough. The proposal in `docs/architecture/concept.md` calls out
"every decision is a labelled datapoint" as the core architectural
commitment — without a UI that lets a human walk that record, the
commitment is invisible. The pitch video pipeline
(`tools/pellicule/`) renders pre-recorded scenes; this is the
matching surface for live and replayable inspection.

The artifact mode also gives us a portable bug-report format:
"attach the status.html" reproduces the exact run state in any
browser without a kitsoki install.

## Non-goals

- Authoring. Story editing stays in YAML +
  [`kitsoki-story-authoring`](../../CLAUDE.md).
- TUI replacement. The single-pane TUI proposal owns that surface.
- Multi-tenant auth. Assume localhost or a trusted internal network.
- Multi-session hosting in v1 (see "Open questions").

## Mental model

Three panels, one screen:

```
┌─ kitsoki run · cypilot · turn 14 · proposing/await_review ──┐
│ ┌──────────────────────────────┐ ┌─ trace ───────────────┐  │
│ │                              │ │ turn 14               │  │
│ │   [state diagram —           │ │  · TurnStarted        │  │
│ │    mermaid LR flowchart,     │ │  · LLMCalled  ▸       │  │
│ │    current state highlit]    │ │  · HostInvoked  ▸     │  │
│ │                              │ │  · TransitionApplied  │  │
│ │                              │ │ turn 13               │  │
│ │                              │ │  · …                  │  │
│ └──────────────────────────────┘ └───────────────────────┘  │
│ ┌─ detail (drawer, opens on click) ────────────────────────┐ │
│ │ state: proposing/await_review                            │ │
│ │ description: …                                           │ │
│ │ view template: …                                         │ │
│ │ on_enter:                                                │ │
│ │   - invoke: host.cypilot.fetch_pr   with: { … }          │ │
│ │ transitions: …                                           │ │
│ └──────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

Clicking a diagram node opens the drawer for that state / effect /
transition. Clicking a trace event opens the drawer for that
event and highlights the matching node. The drawer is the
single place where "what is this?" gets answered.

## Architecture

```
┌──────────────────────────────┐
│ kitsoki oracle serve         │  same dispatcher as oracle.*,
│   --http :7777               │  one extra namespace.
│   (existing unix socket +    │
│    new HTTP listener)        │
└──────────────┬───────────────┘
               │
   POST /rpc   │   JSON-RPC 2.0 control
   GET  /rpc/events   text/event-stream notifications
               │
       ┌───────▼────────────────┐
       │ Vue 3 SPA — live mode  │
       └────────────────────────┘

┌──────────────────────────────┐         ┌────────────────────────┐
│ kitsoki export-status        │ writes  │ status-<ts>.html        │
│   -o status.html             │ ──────▶ │ same SPA, snapshot      │
│                              │         │ inlined as <script>     │
└──────────────────────────────┘         └────────────────────────┘
```

The same Vite build runs in both modes. At boot the SPA checks for
`window.__KITSOKI_SNAPSHOT__`; if present it uses the
`SnapshotSource` data layer, otherwise it uses the `LiveSource`.
Components never know which mode they're in.

## Transport

### Reuse

The oracle-split work (merged in `5b71629`) gives us a working
JSON-RPC 2.0 dispatcher with interleaved server-side notifications
(`cmd/kitsoki/oracle_serve.go:309-341`, `:346-373`). The framing,
the `parent_session_id` threading, and the per-call timeout pattern
are reusable as-is.

### Browser surface

A unix socket isn't reachable from a browser. The daemon gains an
HTTP listener alongside the existing unix-socket listener. Both
feed **one** dispatcher.

- **Control: `POST /rpc`.** Single endpoint. Request body is one
  JSON-RPC frame (or batch). Response body is the matching
  frame(s). Idempotent reads, no special framing.
- **Streaming: `GET /rpc/events?subscription_id=<id>`.** Returns
  `text/event-stream`. Each `data:` line is a full JSON-RPC
  notification frame — the exact shape the unix-socket transport
  already emits. `EventSource` handles reconnect.

Subscribe flow:

1. Client `POST /rpc` → `runstatus.session.subscribe`, receives
   `{subscription_id}`.
2. Client opens `EventSource('/rpc/events?subscription_id=…')`.
3. Server pushes `runstatus.event` notifications until the client
   closes or calls `runstatus.session.unsubscribe`.
4. On reconnect, client calls `runstatus.session.trace` with
   `since_turn=<last>` to backfill, then resumes the stream.

The unix-socket transport is untouched; CLI clients and the
validator keep using it.

### JSON-RPC method inventory

New `runstatus.*` namespace, all read-only.

| Method | Returns | Purpose |
|---|---|---|
| `runstatus.sessions.list` | `[{session_id, app_id, current_state, turn, started_at, terminal}]` | Workflow list for navigation. v1 returns 0 or 1 entry. |
| `runstatus.session.get` | header snapshot | Single-session header. |
| `runstatus.session.app` | serialized `AppDef` | States, transitions, effects, view templates, prompts, menus — everything the drawer needs. |
| `runstatus.session.trace` | `{events, last_turn}` | Ring-buffer slice; params `{since_turn?, until_turn?, limit?}`. |
| `runstatus.session.mermaid` | `{source, node_map}` | Delegates to `internal/viz`; `node_map: nodeId → {kind, ref}`. |
| `runstatus.session.subscribe` | `{subscription_id}` | Opens a stream slot. |
| `runstatus.session.unsubscribe` | `{ok:true}` | Tear down. |

Notification shape mirrors `oracle.event`:

```
{"jsonrpc":"2.0","method":"runstatus.event",
 "params":{"subscription_id":"…","event":{<trace record>}}}
```

## Graph library

**Mermaid.js**, rendering the Mermaid source produced by
`internal/viz`.

Why:
- `internal/viz/mermaid.go` already emits LR flowcharts at four
  detail levels (`DetailRooms`, `DetailStates`, `DetailSteps`,
  `DetailFull`). The SPA gets the existing semantics for free.
- Mermaid supports `click NODE_ID call jsCallback(...)` — enough
  for "click a node, open its detail."
- One library, no node/edge conversion, no layout to maintain.
- Renders fine inside a single-file artifact.

Trade-off considered: **Vue Flow** (xyflow) gives richer
interaction — hover tooltips, custom node components, viewport
control. The cost is converting `AppDef.States` → nodes/edges
ourselves and recreating the four detail levels. Defer to a Phase 4
if Mermaid's click model proves too limiting.

Other libraries (Cytoscape, d3-dagre, elkjs) are overkill for
read-only.

### Node-ID sidecar

To translate a Mermaid click into an `AppDef` lookup,
`internal/viz` gains a parallel emitter `FlowchartWithMap()` that
returns both the Mermaid source and a sidecar:

```go
type NodeMap = map[string]NodeRef
type NodeRef struct {
    Kind string // "state" | "effect" | "transition" | "world"
    Ref  string // state path; or "<state>:<effect-index>"; etc.
}
```

The existing `Flowchart()` stays unchanged for CLI users.

## Frontend (Vue 3)

Stack: Vue 3 + Vite + TS + Pinia + vue-router + Mermaid.js +
`vite-plugin-singlefile` for the artifact build. Tailwind or
hand-rolled CSS; kept light so the inline artifact stays small.

### Routes

- `/` — session list. v1 auto-navigates when there's exactly one.
- `/s/:sessionId` — main view.

### Components

- `SessionList.vue` — table of sessions.
- `RunView.vue` — three-panel layout for `/s/:id`.
- `StateDiagram.vue` — Mermaid render. Props: `mermaidSource`,
  `nodeMap`, `currentStatePath`. Post-render DOM patch adds a
  `current` class to the current state's node. Emits `click(nodeId)`.
- `TraceTimeline.vue` — virtualized list grouped by turn. Filter
  chips: subsystem, event type, state path. Rows collapse; expanding
  shows the full JSON payload with copy buttons.
- `DetailDrawer.vue` — context-aware sections:
  - State node → description, view template (rendered + raw),
    `on_enter` chain, transitions table, menu, timeout.
  - Effect → invoke handler, resolved `with`, `set`/`bind` writes,
    `on_error` target.
  - LLM event → resolved prompt, system message, response, tool
    calls, token counts.
  - Host event → handler name, resolved `with`, `return` payload,
    duration.
  - World-var node → name, current value, recent writes.

### Data layer

`src/transport/jsonrpc.ts`:

- `post(method, params): Promise<result>` over `fetch`.
- `subscribe(method, params, onEvent): unsubscribe` opens
  `EventSource`. Reconnect with exponential backoff; on reconnect,
  call `trace?since_turn=<last>` to backfill before resuming.

`src/data/source.ts` — `DataSource` interface, two impls:

- `LiveSource` — JSON-RPC client.
- `SnapshotSource` — reads `window.__KITSOKI_SNAPSHOT__`, answers
  the same method names from the embedded blob.

`src/stores/run.ts` (Pinia) — `appDef`, `events`, `currentStatePath`,
`selectedNode`. Hydrated through `DataSource`.

### Cross-panel linking

- Click diagram node → drawer opens, timeline scrolls to first
  event involving that state path, matching events highlight.
- Click trace event → diagram highlights the relevant node,
  drawer opens to the event detail.
- "Follow live" toggle (live mode) — auto-track most recent
  event's `state_path` and auto-scroll the timeline.

## Self-contained artifact

New command `kitsoki export-status -o status.html`:

- Reads the in-memory ring buffer + the loaded `AppDef`, or accepts
  `--trace-file <jsonl> --app <id>` for finished runs.
- Inlines the Vite single-file build.
- Injects the snapshot JSON as
  `<script type="application/json" id="kitsoki-snapshot">…</script>`
  before the SPA boot script.

The snapshot JSON has **the same shape** that the JSON-RPC methods
return — one canonical contract used three ways (live mode,
artifact mode, test fixtures).

## Fixtures and static example

Fixtures are checked-in static examples that double as Playwright
inputs and as the source of truth for the snapshot shape.

A new flag `kitsoki export-status --from-trace <jsonl> --app <id>
-o fixture.snapshot.json` produces fixture files deterministically
from a recorded run. Three fixtures land under
`tools/runstatus/fixtures/`:

1. `in-progress.snapshot.json` — mid-run, current state set, ~50
   events.
2. `completed.snapshot.json` — terminal state, ~200 events.
3. `edge-cases.snapshot.json` — host error, off-path arc,
   world-write chain, long LLM response.

A dev page `tools/runstatus/dev.html` (Vite-served via `pnpm dev`)
ships a fixture picker so a human can switch between them without
rebuilding.

## Testing

Per [`feedback_fast_tests`](../../../../.claude/projects/-home-cloud-user-code-kitsoki/memory/feedback_fast_tests.md):
every suite runs in seconds, not minutes. No real-LLM dependencies
anywhere in the test path.

### Unit — Go

- `internal/runstatus/` — table-driven tests for each method
  handler against fixture `AppDef`s.
- SSE sink — subscribe, push N events, unsubscribe; slow-client
  drop policy with a counter; concurrent subscribers don't see each
  other's traffic.
- `POST /rpc` listener — request validation, error frames, batch
  dispatch, `Content-Type` enforcement.
- `internal/viz` `FlowchartWithMap()` — golden tests across the
  cloak, oregon-trail, bugfix, frontier_event stories.

### Unit — frontend (Vitest + `@vue/test-utils`)

- `transport/jsonrpc.ts` — mock `fetch` and `EventSource`. Cover:
  id correlation, batch frames, error-frame surfacing,
  subscribe→event→unsubscribe lifecycle, reconnect with backfill.
- `stores/run.ts` — snapshot vs. live hydration, event append
  ordering, current-state tracking, idempotent re-subscribe.
- `nodemap` lookups — every `NodeRef.Kind` resolves.
- `TraceTimeline` filter logic — pure functions tested directly.
- `DetailDrawer` snapshot tests per event kind.

Test budget: whole suite under 5 seconds. No real network, no real
`EventSource`.

### End-to-end (Playwright)

Primary target: **artifact mode**. Playwright loads the built HTML
from `file://` with each fixture inlined. No server, no startup
ordering, sub-second per test.

Secondary suite: boots `kitsoki oracle serve --http :0` against a
recorded trace replay and validates live-mode behaviors (SSE,
reconnect backfill, subscribe lifecycle). Smaller; runs in CI, not
on every dev iteration.

Coverage checklist (artifact unless noted):

- Page loads; header shows session id / app / current state / turn.
- Diagram renders; current state node has the `current` class;
  expected node count for each detail level.
- Click each node kind → drawer opens with the right section and
  the right content.
- Click LLM event → drawer shows prompt + response, both
  copyable; long content collapsed with expand control working.
- Click host event → drawer shows resolved `with` and `return`
  payloads.
- Timeline filter chips narrow the list; clear restores it.
- Cross-panel linking both directions.
- "Follow live" toggle (live suite): no scroll jump when off;
  auto-scrolls when on.
- Reconnect backfill (live suite): drop SSE, server emits 3 more
  events, client reconnects, all 3 land in order with no
  duplicates.
- Edge-cases fixture: error event renders distinct, off-path arc
  visible, world-write chain expandable.

Playwright config: `workers: 4`, no `webServer` for artifact tests
(just `file://`), screenshots on failure only.

## Files

**New:**

- `internal/runstatus/` — method handlers, session registry, SSE
  sink wrapping the ring buffer.
- `internal/http/` (or extension of the oracle-serve package) —
  HTTP listener, `POST /rpc`, `GET /rpc/events`, static asset
  handler.
- `internal/viz/nodemap.go` — `FlowchartWithMap()` and `NodeRef`.
- `cmd/kitsoki/export_status.go` — both run-mode and `--from-trace`
  variants.
- `tools/runstatus/` — Vue project. `dist/` embedded via Go
  `embed.FS`. `fixtures/` checked in. `tests/playwright/` and
  `tests/unit/`.

**Modified:**

- `cmd/kitsoki/oracle_serve.go` — register `runstatus.*` namespace;
  add HTTP listener behind `--http`.
- `internal/viz/flowchart.go` — extracted helper used by both
  `Flowchart()` and `FlowchartWithMap()`.

## Phasing

### Phase 1 — Static example + tests

Goal: a working artifact you can open in a browser, with the test
suites that protect it.

1. `FlowchartWithMap()` in `internal/viz` + goldens for four
   stories.
2. `kitsoki export-status --from-trace` generator.
3. Three checked-in fixtures.
4. Vue scaffold; `SnapshotSource`; Mermaid render; click → drawer.
5. `TraceTimeline` + `DetailDrawer` for all event kinds.
6. `kitsoki export-status -o status.html` (single-file build,
   inlined snapshot).
7. Vitest unit suite. Playwright artifact suite.

Exit criteria: opening any of the three fixtures via
`pnpm dev` or via `status.html` produces the documented
behavior, and CI passes.

### Phase 2 — Trace polish

Filter chips, virtualized timeline at 5000+ events, copy buttons,
expand/collapse states persisted in the URL, prompt-response
rendering polished.

### Phase 3 — Live mode

`--http` listener on `kitsoki oracle serve`. `runstatus.*` methods.
SSE stream. Single-session auto-nav. Backfill on reconnect.
Live-mode Playwright suite.

### Phase 4 (optional)

Vue Flow swap-in if Mermaid's interaction ceiling becomes a
bottleneck. World-state inspector tab. Multi-session hosting once
the runner supports it.

## Open questions

1. **Slow-client policy for SSE.** Drop or block? Lean drop with a
   `dropped_count` field on the next delivered event so the client
   knows to refetch from `since_turn`.
2. **Detail-level toggle in the UI.** Mermaid emits four levels.
   Persist the choice in the URL? Default to `DetailStates` (today's
   `kitsoki viz` default).
3. **Snapshot redaction.** Real traces can contain secrets in host
   inputs / outputs (API keys, customer data). v1 ships no
   redaction; consumers must export from sanitized runs. Open: do
   we want a `--redact <jq-expr>` flag on `export-status` before
   v1, or is that a Phase 2 follow-up?

## Decisions already locked

- Transport: **HTTP+SSE** (not WebSocket). JSON-RPC for control,
  `text/event-stream` for updates.
- Server: **fold into `kitsoki oracle serve`** behind `--http`; one
  daemon, two namespaces.
- Scope: **single session** for v1; the list endpoint exists so the
  UI's picker code path is real, but it returns 0 or 1 entries.
