# The story editor

The **story editor** is a per-story static inspector served by
[`kitsoki web`](../web/README.md). It answers "what does this story *do*" —
its rooms, the world they read and write, the oracle calls they make, and the
cassettes that back those calls — **without starting a session**. Where the
chat surfaces ([RunView / InteractiveView](../web/README.md)) drive a live
orchestrator, the editor reads a story definition straight off disk and
recompiles it on every request, so it always reflects the on-disk YAML.

*Audience: authors inspecting a story's shape, and contributors working on the
editor surface.*

> The editor is read-only. It never mutates a story, never starts an
> orchestrator, and never calls an LLM. Cassette replay (below) reads recorded
> output only.

---

## The route

The editor is a hash route in the SPA router
([`tools/runstatus/src/router.ts`](../../tools/runstatus/src/router.ts)):

```
/editor?story=<id|path>&room=<id>[&session=<id>]
```

- `story` — story id or absolute `app.yaml` path; resolved against the registry
  catalogue.
- `room` — selected room (top-level state id); drives the detail pane.
- `session` — optional live session id. Present only to enable **meta chat**
  (below); the rest of the editor needs no session.

The page is [`views/EditorPage.vue`](../../tools/runstatus/src/views/EditorPage.vue):
meta chat on the left, the BFS room list + selected-room detail on the right.

## The room list (BFS ordering)

The left rail lists every top-level room ordered by **breadth-first distance
from the initial room**. The initial room (`idle` in `stories/prd`) is distance
`0`; each transition hop adds one. Ties break by id; unreachable/orphan rooms
sort last.

The list field is named `distance` and typed `float64` because the contract is
the *mean of all shortest-path distances* to a room. BFS yields a single
shortest distance per node, so today that mean reduces to the BFS depth — the
float type keeps room for future edge weighting without an API change. See
[`internal/app/graph/graph.go`](../../internal/app/graph/graph.go) (`RoomList`,
`roomDistances`).

For `stories/prd` this orders the five rooms:

```
idle (0) → clarifying (1) → brief (2) → references (3) → drafting (4)
```

## Room detail

Selecting a room calls `runstatus.editor.room` and renders four facets:

- **Hooks** — the room's `on_enter` effects, each flattened to a coarse `kind`
  (`invoke` / `set` / `say` / `emit_intent` / `increment` / `other`) plus the
  world keys it `bind`s and `set`s. Oracle invokes link into the
  [Oracle Workbench](#the-oracle-workbench).
- **Domain model** — the world variables the room references, each with a type
  (from the world schema) and a conservative `read` / `write` / `readwrite`
  direction.
- **Typed view** — the room's view rendered through the shared
  [StoryViewer](#storyviewer) component (the same typed-element pipeline the
  TUI and chat surfaces use).
- **IDE deep-link** — a `vscode://file/<path>:<line>` link built from the
  room's `source_ref`. Line info is best-effort (`app.Load` does not retain
  per-node YAML line numbers, so it falls back to line `1` of the manifest) —
  enough to open the right file.

See [`internal/app/graph/detail.go`](../../internal/app/graph/detail.go).

## Meta chat

The left rail hosts a **meta chat** — the read-only meta agent that observes and
advises on a story. This reuses the existing meta surface
([`stores/meta.ts`](../../tools/runstatus/src/stores/meta.ts),
[`components/meta/MetaOverlay.vue`](../../tools/runstatus/src/components/meta/MetaOverlay.vue),
the `runstatus.meta.*` RPC family), **not** a separate off-path channel.

Meta chat requires an active session. The editor has none of its own, so when no
`?session=` query param is present the rail shows a placeholder
(`Start a session to enable meta chat.`) rather than crashing. With a session
present it loads the `story`-group meta mode and opens the overlay.

## The Oracle Workbench

The workbench
([`components/editor/OracleWorkbench.vue`](../../tools/runstatus/src/components/editor/OracleWorkbench.vue))
inspects a room's LLM contracts and the cassettes that back them.

- **Contracts** (`runstatus.editor.oracles`) — one entry per `host.oracle.*`
  invoke the room makes, carrying the verb (`kind`), prompt path, output schema,
  and the **cassette key** an episode must match to back it. The key mirrors the
  runtime's [cassette match logic](../tracing/testing.md): `handler` (the verb),
  `phase` (the room id), `schema_name` (basename of the schema), and the
  author-assigned call `id` when present. See
  [`internal/app/graph/oracle.go`](../../internal/app/graph/oracle.go).
- **Cassette browser** (`runstatus.editor.cassettes`) — every episode under the
  story's cassette globs (`cassettes/*.yaml`, `flows/*.cassette.yaml`,
  `flows/cassettes/*`) whose `match:` is consistent with the contract's key,
  with an input digest and output preview.
- **Replay** (`runstatus.editor.replay`) — loads one cassette episode's recorded
  output and shows it plus a **bind-directive world snapshot** (what the call
  *would* write to the world, derived from the effect's `bind:` map), rendered
  through [StoryViewer](#storyviewer).

Two safety rules are enforced server-side
([`internal/runstatus/server/editor.go`](../../internal/runstatus/server/editor.go)):

- **Cassette-only.** Live replay (a real oracle round-trip) requires a session
  and operator, which the per-story editor has not. A replay request without a
  `cassette_file` returns `codeReadOnly`. **Task oracles** (`*.task`) are
  rejected outright even with a cassette — running an agent has side effects.
- **Isolation caveat.** Replay does **not** advance state. It reads recorded
  output and computes the would-be world snapshot; it never mutates a session,
  never re-runs `on_enter`, and never persists anything.

### Editor RPC surface

All editor RPCs are dispatched in
[`internal/runstatus/server/editor.go`](../../internal/runstatus/server/editor.go)
and take a `story_path` (absolute `app.yaml`). They are backed by the optional
`EditorProvider` capability on the session provider — the multi-story registry
implements it; a single-entry read-only adapter does not, in which case every
editor RPC returns `codeReadOnly`.

| Method | Params | Result |
|---|---|---|
| `runstatus.editor.rooms` | `{story_path}` | `{rooms: [{id, label, distance, has_oracle}]}` |
| `runstatus.editor.room` | `{story_path, room_id}` | `{id, label, distance, on_enter[], world_keys[], intents[], transitions[], view[], source_ref?}` |
| `runstatus.editor.oracles` | `{story_path, room_id}` | `{contracts: [{kind, prompt_path, output_schema, cassette_key, effect_index}], cassette_globs: []}` |
| `runstatus.editor.cassettes` | `{story_path, cassette_key?}` | `{episodes: [{cassette_file, episode_id, handler, phase, schema_name, input_digest, output_preview}]}` |
| `runstatus.editor.replay` | `{story_path, room_id, oracle_index, cassette_file?}` | `{output, world_snapshot, source: "cassette", cassette_file, episode_id, note}` |

Graph computation is delegated to the pure
[`internal/app/graph`](../../internal/app/graph) package (no I/O, no LLM);
`editor.go` is the JSON-RPC adapter plus the cassette read/replay file paths.

## StoryViewer

[`components/editor/StoryViewer.vue`](../../tools/runstatus/src/components/editor/StoryViewer.vue)
is a self-contained, reusable read-only renderer for a room's typed view plus an
optional world snapshot. It takes its data entirely through props — no Pinia
store, no DataSource, no session — so the same component renders a static editor
preview, a cassette-replay snapshot, or any `View` + world map.

Props:

| Prop | Type | Notes |
|---|---|---|
| `view` | `View \| null` | Typed view elements (rendered via [`ViewElement.vue`](../../tools/runstatus/src/components/ViewElement.vue)). |
| `worldSnapshot` | `Record<string, unknown> \| null` | Post-bind / current world values, shown as a key/value grid. |
| `mode` | `"column" \| "modal"` | `column` (default): inline 50%-width panel; `modal`: centred overlay that emits `close`. |
| `title` | `string` | Panel heading. |

---

## Pointers

- Route + page: [`router.ts`](../../tools/runstatus/src/router.ts) ·
  [`views/EditorPage.vue`](../../tools/runstatus/src/views/EditorPage.vue)
- Components: [`components/editor/`](../../tools/runstatus/src/components/editor/)
  (`OracleWorkbench`, `CassetteBrowser`, `DomainModel`, `HookDetail`, `StoryViewer`)
- RPC client: [`data/live-source.ts`](../../tools/runstatus/src/data/live-source.ts)
  (`editorRooms` / `editorRoom` / `editorOracles` / `editorCassettes` / `editorReplay`)
- Backend: [`internal/runstatus/server/editor.go`](../../internal/runstatus/server/editor.go) ·
  pure graph: [`internal/app/graph/`](../../internal/app/graph/)
- Tests: `tests/unit/{editor-page,story-viewer}.test.ts`,
  `tests/playwright/editor.spec.ts`, `internal/app/graph/graph_test.go`
- Siblings: the live [web UI](../web/README.md) · the [TUI](README.md)
