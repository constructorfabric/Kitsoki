# Epic: Story Editor View

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 3 (0/3 shipped)

## Why

Authoring a kitsoki story today is a pure text-editing exercise. The YAML
is the only view: you read `on_enter:` effects to understand what a room
does, mentally trace `on:` arcs to understand reachability, and open
cassette files in a separate tab to see what an oracle actually produces.
There is no way to ask "which rooms are even reachable from home?", test
a single oracle call without running the whole session, or see the room's
view and the room's wiring side-by-side.

This epic adds a **story editor surface** to the existing web UI — a
room-by-room, graph-ordered view of the story with inline editing, live
oracle testing, and the meta chat already present on the run surface. The
goal is to shrink the inner loop for authoring from "edit YAML → restart
session → re-play to this room → inspect" to "click the room → see its
model → edit → test the oracle → done."

## What changes

Once all three slices ship, `kitsoki web` will expose a second tab (or
panel) alongside the run surface: the **Story Editor**. It shows all
rooms ordered by average BFS distance from the entry point, with each
room expanded to its hook (on_enter effects), domain model (world keys
read/written, intents, transitions), and typed view. A meta chat runs in
the left column (the existing off-path oracle), and the room's rendered
view sits in the right column — the same component used on the run
surface. A separate **Oracle Workbench** within each room lets you browse
the cassettes for any oracle call, pick a cassette, fire the call in
isolation, and inspect the structured output. The story viewer component
used here is packaged for reuse as a half-screen column or a modal
anywhere else in the SPA.

## Impact

- **Spans:** runtime, tui (web ×2)
- **Net surface:** new backend read RPCs (graph + oracle contracts) + two
  new Vue panels in `tools/runstatus/`; no changes to the orchestrator
  hot path or the engine's state machine
- **Docs on ship:** `docs/tui/story-editor.md`; cross-link from
  `docs/tui/web-ui.md` and `docs/tui/README.md`

## Slices

| # | Slice | Kind | Scope | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Story Graph API | runtime | Read-only backend: BFS room ordering, oracle contracts per room | — | Draft | [`story-graph-api.md`](story-graph-api.md) |
| 2 | Story Editor Shell | tui | Room list + hook/domain-model display + meta-chat + inline edit | 1 | Draft | [`story-editor-shell.md`](story-editor-shell.md) |
| 3 | Oracle Workbench | tui | Cassette browser + oracle contract display + story viewer component | 1 | Draft | [`oracle-workbench.md`](oracle-workbench.md) |

## Sequencing

```
#1 (runtime: graph API) ──▶ #2 (tui: editor shell)
                        └──▶ #3 (tui: oracle workbench, parallel once API landed)
```

Slices 2 and 3 both depend on slice 1 for their backend data. They are
otherwise independent and can be built in parallel once slice 1 ships.

## Shared decisions

1. **Graph ordering is BFS from `App.InitialState()`** — average
   shortest-path distance across all paths that reach a room. Compound
   states are flattened; parallel children each contribute their own
   distance. Rooms unreachable from the entry point appear last, sorted
   by name. This is purely a display ordering; the canonical room
   identity stays the YAML state path.

2. **No YAML write path in this epic** — the editor surface is
   read-display-and-link-to-file-in-IDE in v1. Inline editing in slice 2
   means editing the room's YAML through the system editor (an IDE
   deep-link or file open), not a web form that writes YAML. The engine
   has no YAML-write path today and this epic does not add one.

3. **Story viewer component reuses existing `ViewElement.vue`** — the
   half-screen / modal viewer in slice 3 wraps the existing typed-view
   renderer, not a new one. No divergence in how view elements render
   between the run surface and the editor surface.

4. **Backend read RPCs use the same RPC layer** — new handler functions
   on the existing `internal/runstatus/server/` server; same JSON
   transport. No new server process or port.

## Cross-cutting open questions

1. **Routing: editor vs. run surface on the same `kitsoki web` instance?**
   Options: (a) separate URL path `/editor` alongside `/` run surface,
   (b) tab-switch within the SPA, (c) entirely separate `kitsoki edit`
   command. *Lean: (a) separate `/editor` path — shares the server,
   keeps concerns separate in the Vue router, easy to deep-link to a
   specific room.*

2. **Live reload on YAML change?** The editor will want to re-query the
   graph API when the story file changes on disk. Options: (a) manual
   refresh button, (b) poll on a short interval, (c) inotify/fsevents
   pushed over SSE. *Lean: (a) manual refresh for v1; (c) is a follow-on.*

## Non-goals

- **No YAML write path** — the editor does not write or validate YAML;
  that's a future slice. IDE deep-links or `$EDITOR` opens are the
  authoring mechanism.
- **Not a story runner** — the editor surface does not drive a live
  session; that's the existing run surface. The story viewer shows a
  static render of the typed view, not a live interactive session.
- **Not a graph visualiser** — the Mermaid diagram on the run surface
  already covers state topology. The editor adds ordering and per-room
  detail, not a second graph canvas.
- **Not a prompt editor** — oracle prompt templates are shown as links
  to source files, not editable in the browser in this epic.
