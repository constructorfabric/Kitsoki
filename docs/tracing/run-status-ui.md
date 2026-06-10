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

This page covers the **read-only** viewer. For an **interactive** browser
surface — drive a live session by chat, beside this same trace and diagram —
see [the web UI](../web/README.md) (`kitsoki web`), built on the same SPA and
RPC/SSE contract plus a write layer.

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

## State diagram: path & horizon views

The state diagram answers **"where am I in the machine"** — not "what is the
whole machine". It offers four views, switched by tabs; the route-centric three
appear whenever the run has a resolvable current room, and **Metro** is the
default (a mid-stream trace with no landed state opens in **Full**, so nothing
regresses):

- **Metro** — a vertical interchange line: the traveled leg, the current stop
  with its live exits as pills, and the road ahead.
- **Graph** — the same 1-hop neighbourhood as an SVG node-link diagram:
  came-from → you-are-here → the rooms each live move leads to, with directed
  elbow connectors.
- **Path** — a breadcrumb of the traveled path (each room + the intent that
  entered it) + the current room as a hero card + the live exits as chips.
- **Full** — the whole static graph, every phase/room (the right view for the
  "what is the entire machine" question). The "+N elsewhere" chip routes here.

All three route views draw the **same three truth tiers**, styled deliberately
distinct so the view never implies something the trace doesn't ([the trace is
the source of truth](../../tools/runstatus/CLAUDE.md)):

| Tier | Shows | Source | Truth status |
|---|---|---|---|
| **Traveled leg** (bright/solid) | rooms behind the current one, each tagged with the intent that entered it | ordered `machine.state_entered` → room, joined to `machine.transition` `attrs.intent` for provenance | **trace** — the run went there |
| **Current station** (amber/solid) + horizon **pills** | the current room + the live next moves | `currentView.intents` × the room's parsed outgoing edges | **live** — what you can do now |
| **Road ahead** (muted/**dashed**) | forward rooms not yet reached | the static graph: a greedy forward room walk (`roomSpineAhead`) | **projection** — declared, not run |

Two rules are non-negotiable: the stations are **rooms** (a whole pipeline can
live in one authored phase — dev-story's `proposal_*` rooms do — so the spine is
walked room-by-room, not phase-by-phase); and **projection is never styled as
traveled** (the road ahead is dashed/muted; even a terminal stop stays a dashed
ring, never a solid traveled dot). The forward walk takes the deepest unvisited
non-escape edge at each step and stops on a cycle, so a linear-with-shortcuts
pipeline projects its canonical route.

**Phase banners** (e.g. `INTAKE` / `SEARCHING` / `DRAFTING`) ride each station.
They are declared graph metadata — a room's static `banner` view element —
surfaced through the web viz path as a `%% banner <state> <text>` comment on the
flowchart source (`viz.FlowchartOptions.Banners`, parsed into `Room.banner`).
Templated banners are skipped (a runtime render is not declared metadata).

Each horizon pill is `intent → target room`; forward pills are solid, self-loop
and exit pills outlined. Clicking a pill (or any room/node) highlights its target
room via the same `select` emit / `highlightedStatePaths` path a full-graph room
click uses. A pill whose intent has no declared outgoing edge still appears, but
does not navigate.

The derivations are pure functions in `tools/runstatus/src/diagram/horizon.ts`
(`matchRoomId`, `traveledPath`, `horizon`, `roomSpineAhead`, `spineAhead`,
`enteringIntents`) — no Vue/DOM coupling, unit-tested in
`tests/unit/diagram-path-horizon.test.ts`. A Playwright gate
(`tests/playwright/diagram-overflow.spec.ts`) asserts nothing overflows in any
view — DOM elements by `scrollWidth`, and the Graph view's SVG node labels by
`getBBox` against their rect (long ids are compressed with `textLength`, not
clipped). The showcase walkthrough is
`tests/playwright/diagram-showcase.spec.ts`.

## Agent actions drawer

When an oracle event carries a
[`transcript_ref`](trace-format.md#agent-action-transcript-sidecar), its detail
pane shows an **"Agent actions (N)"** affordance (N = the captured event count).
Opening it lazily fetches the per-call sidecar
(`runstatus.session.transcript {session_id, call_id}` on the live server; the
inlined `attrs.transcript` on a static export) and renders the call's native
execution stream as **typed rows** — the same per-tool-call fidelity a dedicated
agent-observability tool gives you, kept beside the decision trace that produced
it. Every verb that ran on the claude CLI has one (not just `task`).

The drawer normalizes each backend's native events
(`tools/runstatus/src/data/transcript.ts`) into one render model and shows:

- **Typed rows** — assistant reasoning/`thinking`; each **Tool**/**MCP** call's
  full input and output, collapsible (the `Read` file, the `Edit` diff, the
  `Bash` command + stdout); and the terminal **result** with tokens/cost.
- **Guardrail rows** — a `decide`'s `mcp__validator__submit` is typed as a
  **Guardrail** (PASS / REJECTED + the verdict), not a generic tool call, and
  the full submit → **rejected** (reason) → host **nudge** → re-submit →
  **accepted** arc — including the host-injected nudge row the raw stream omits —
  reads as one sequence with iteration boundaries.
- **Waterfall** — per-step latency bars from the `.timings` sidecar, so the
  wall-clock bottleneck stands out.
- **Running cost accrual** — input/output tokens and cost accrued across the
  whole call, not only the terminal total.
- **Session rollup** — the **Actions** view mode groups every transcript-bearing
  call across the run under its turn/room, each expandable into its own drawer.
- **Cassette-vs-live diff** — because the transcript replays byte-identically
  from the cassette, a fresh live run can be diffed against the recorded one to
  flag tool-path drift. Under pure replay there is no live run to compare, and
  the control says so honestly ("byte-identical to the cassette") rather than
  fabricating a diff.

Per [the trace must always be correct](../../tools/runstatus/CLAUDE.md), the
drawer only ever *projects* the recorded sidecar; it never reconstructs or
edits it.

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
