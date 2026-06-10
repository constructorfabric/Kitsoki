# TUI: Story Editor Shell

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [story-editor-view.md](story-editor-view.md)

## Why

The run surface (`kitsoki web`) shows a live session: the room's rendered
view, the composer, and the trace. What it doesn't show is the *authoring
view* of that room: what's in its hook, which world keys it touches, how
its intents wire to other rooms, and what its neighbours are in the graph.
An author switching between rooms today must hold that structure in their
head or keep the YAML file open in another window.

The story editor shell gives authors a structured, graph-ordered map of
the story alongside the same meta chat and room-view rendering they
already have on the run surface — so "navigate to this room, understand
its wiring, adjust it" becomes a single surface instead of a YAML
expedition.

## What changes

A new `/editor` route in the SPA alongside the existing `/` run surface.
The editor page is split into two columns:

- **Left column:** meta chat — the existing off-path oracle (same
  component already used on the run surface for meta-mode queries), so
  authors can ask "what does this room's prompt do?" and get an answer
  without leaving the editor.
- **Right column:** room detail — the selected room's rendered typed view
  (read-only, via `ViewElement.vue`), its hook display, and its domain
  model (world keys, intents, transitions). A room list sidebar in the
  right column orders rooms by BFS distance from the entry point (data
  from the graph API, slice 1).

"Inline editing" in this slice means an **IDE deep-link**: the room
detail header shows the source file path; clicking it opens the file at
the room's line in VS Code (or `$EDITOR` fallback). The YAML write path
is out of scope for this epic (shared decision §2).

## Impact

- **Code:** `tools/runstatus/src/` — new route + components; no Go changes beyond slice 1
- **Rendering:** reuses `ViewElement.vue` for the room view; new
  `HookDetail.vue` and `DomainModel.vue` components for on_enter effects
  and world keys / intents / transitions
- **Input:** room list click → room detail; meta chat reuses existing
  off-path RPC (`session.offpath`)
- **Docs on ship:** `docs/tui/story-editor.md`

## Mental model

The editor is a **story map + annotation surface**. The left column is
the author's scratch pad (meta chat); the right column is the authoritative
room view — what the room looks like, what it does, and where it goes.
The room list sidebar is the map; clicking a room pins it in the detail
pane. You navigate by graph proximity to the entry point, not by YAML
file order.

## Layout

```
/editor route:

┌──────────────────────┬──────────────────────────────────────────────┐
│  Meta Chat           │  [← rooms | sorted by distance]  [🔄 reload] │
│  (off-path oracle)   ├──────────────────────────────────────────────┤
│                      │  Room: clarifying          dist 1.0  [↗ open] │
│  > ask anything…     │  ┌────────────────────────────────────────┐  │
│                      │  │ Hook (on_enter)                         │  │
│  assistant: The      │  │  host.oracle.ask → prompt: intro.md    │  │
│  clarifying room     │  │  bind: world.brief ← output.summary    │  │
│  gathers the user's  │  ├────────────────────────────────────────┤  │
│  initial brief via   │  │ World keys                              │  │
│  an ask oracle…      │  │  brief          write  (from hook bind) │  │
│                      │  │  user_name      read   (view template)  │  │
│                      │  ├────────────────────────────────────────┤  │
│                      │  │ Intents → transitions                   │  │
│                      │  │  accept  → drafting                    │  │
│                      │  │  revise  → clarifying (self)           │  │
│                      │  ├────────────────────────────────────────┤  │
│                      │  │ Typed view (rendered)                   │  │
│                      │  │  [ViewElement.vue — read only]          │  │
│                      │  └────────────────────────────────────────┘  │
└──────────────────────┴──────────────────────────────────────────────┘

Room list sidebar (collapsible, inside right column header):

  ●  home              dist 0
  ●  clarifying        dist 1.0      ← selected
  ○  references        dist 2.0
  ○  drafting          dist 2.0
  ○  review            dist 3.0
     @exit             dist 4.0
  ─  orphan_room       unreachable
```

## Rendering changes

Three new Vue components, all data-driven (no hand-rolled HTML strings):

| Component | Data source | Renders |
|---|---|---|
| `EditorPage.vue` | router | two-column shell; room-list sidebar; reload button |
| `HookDetail.vue` | `/editor/rooms/{id}` → `onEnter[]` | each on_enter effect as a typed card: kind badge + key fields |
| `DomainModel.vue` | `/editor/rooms/{id}` → `worldKeys`, `intents`, `transitions` | three collapsible sections; transition targets are links that select the target room |

The room's typed view is rendered by the existing `ViewElement.vue` (no
changes). The meta chat reuses the existing off-path Vue store and
`session.offpath` RPC (no changes).

The IDE deep-link is a plain `<a href="vscode://file/{abs_path}:{line}">`.
The source path and line come from `RoomDetail.SourceRef` (a new field in
the graph API — slice 1 open question).

## Input & commands

| Interaction | Effect |
|---|---|
| Click room in sidebar | Loads `RoomDetail` for that room; updates URL to `/editor?room={id}` |
| Click `[↗ open]` | Opens source file in VS Code via `vscode://file/` deep-link |
| Click transition target in DomainModel | Selects that room in the editor |
| 🔄 reload button | Re-fetches `/editor/rooms` (manual refresh, shared decision §2 of epic) |
| Type in meta chat | Existing `session.offpath` RPC; no change |

No slash commands; the editor is mouse-driven.

## Rendering tests

The editor shell has no concurrent I/O (it's a static read surface, not
a terminal renderer), so the combined-I/O rule doesn't apply. The bar is:

- **Vitest: `EditorPage.spec.ts`** — mock the graph API responses;
  assert the room list renders in BFS order, selecting a room loads its
  detail, and the transition link selects the target room.
- **Vitest: `HookDetail.spec.ts`** — given a fixture `RoomDetail` with
  on_enter effects, assert each effect renders its kind badge and fields.
- **Vitest: `DomainModel.spec.ts`** — world-key rows, intent rows,
  transition rows render; clicking a transition target emits the correct
  room-select event.
- **Playwright: `editor.spec.ts`** — spawn `kitsoki web stories/prd/app.yaml`,
  navigate to `/editor`, assert room list appears in BFS order
  (home → clarifying → … ); click `clarifying`, assert hook + domain
  model sections appear.

## Tasks

```
## 1. Backend (slice 1 prerequisite)
- [ ] 1.0 Slice 1 (story-graph-api.md) must be complete

## 2. Router + shell
- [ ] 2.1 Add `/editor` route to Vue router (`tools/runstatus/src/router.ts`)
- [ ] 2.2 `EditorPage.vue`: two-column layout, room-list sidebar, reload button,
          URL sync (`?room=` query param)
- [ ] 2.3 Pinia store slice for editor state: selected room, room list cache

## 3. Room detail components
- [ ] 3.1 `HookDetail.vue`: on_enter effect cards (kind badge + key-value fields)
- [ ] 3.2 `DomainModel.vue`: three collapsible sections (world keys / intents /
          transitions); transition targets are clickable room selectors
- [ ] 3.3 IDE deep-link: `vscode://file/{path}:{line}` in room detail header
          (requires `SourceRef` field in slice 1's `RoomDetail`)

## 4. Meta chat integration
- [ ] 4.1 Embed existing off-path store + UI in the left column; the
          `session.offpath` RPC already exists — no new backend work

## 5. Prove + document
- [ ] 5.1 Vitest: EditorPage, HookDetail, DomainModel (see Rendering tests above)
- [ ] 5.2 Playwright: editor.spec.ts (prd fixture; BFS order; room detail visible)
- [ ] 5.3 Manual walkthrough of stories/prd; screenshot the editor for docs
- [ ] 5.4 Write `docs/tui/story-editor.md`; cross-link from web-ui.md;
          trim/delete this proposal
```

## What we lose, honestly

- **No live room hot-swap.** Changing the YAML and clicking reload
  rebuilds the room list but doesn't restart the live session on the run
  surface. Authors who want to test the change still need to restart
  `kitsoki web`. The manual-reload design (shared decision §2) makes this
  explicit.
- **No breadcrumb for compound states.** Compound states are flattened
  into the room list; the parent path is visible in the room ID but there
  is no visual nesting. Nested compound stories (rare in practice) may
  have a long ID and no visual parent-child grouping.

## Open questions

1. **SourceRef in graph API** — the IDE deep-link needs a file path and
   line number per room. The YAML loader has this at load time (via
   `yaml.Node` line info). Should `RoomDetail` carry `SourceRef{Path,
   Line}`, or is this a separate slice-1 extension? *Lean: add
   `SourceRef` to `RoomDetail` in slice 1 — it's pure read, zero
   cost.*

2. **Meta chat session scope** — the off-path RPC requires an active
   session (`WithDriver`). If the user visits `/editor` without an active
   run session, the meta chat RPC will fail. Options: (a) show meta chat
   only if a session is live, (b) start a background "editor session"
   silently. *Lean: (a) show a "start a session to enable meta chat"
   placeholder — keeps the editor read-only when no session is running.*

## Non-goals

- **No YAML write path** — IDE deep-link only; the browser does not edit
  YAML in v1.
- **Not a graph visualiser** — the Mermaid diagram on the run surface
  covers topology; this adds per-room detail, not a second canvas.
- **Not a prompt editor** — oracle prompt template `.md` files are shown
  as source links, not rendered or editable here.
- **Not a TUI feature** — this is web-only; the terminal TUI is unchanged.
