# TUI: Oracle Workbench

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [story-editor-view.md](story-editor-view.md)

## Why

Testing an oracle today means running the whole session to reach the room
that invokes it, hoping the cassette loads correctly, and inspecting the
trace after the fact. There is no way to:

- browse the cassettes available for a given oracle call,
- fire a single oracle call in isolation with a chosen cassette,
- see the oracle's declared input/output contract alongside its actual
  outputs, or
- watch the room's typed view update as if the oracle had returned a
  specific cassette response.

This makes oracle authoring and debugging slow and indirect. The oracle
workbench puts those operations on one surface, reachable from the story
editor shell (slice 2) without starting a full session.

## What changes

Within the story editor's room detail panel (slice 2), a third collapsible
section — **Oracle Workbench** — appears below the domain model for any
room that has oracle calls in its hook or intent arcs. It shows:

1. **Oracle contract list** — one card per oracle call in the room, from
   the `/editor/rooms/{id}/oracles` API (slice 1). Each card shows the
   call kind, prompt path, output schema (if declared), and cassette key.

2. **Cassette browser** — for the selected oracle card, a list of matching
   cassette files on disk (from a new `/editor/cassettes/{cassette_key}`
   endpoint). Each cassette entry shows its recorded input digest and a
   preview of the structured output.

3. **Replay button** — fire the selected oracle call against the selected
   cassette (or live, if a session is running). The call is isolated: it
   does not advance the story state. The structured output is displayed
   inline. If a session is active, the call is also recorded to the trace.

4. **Story viewer** — a reusable component (packaged for use as a
   half-screen column or a modal) that renders the room's typed view
   updated as if the oracle had returned the selected cassette response.
   This component is `StoryViewer.vue`, wrapping `ViewElement.vue` with a
   local world snapshot derived from the cassette's output bindings.

The story viewer component is designed for reuse: it accepts a `View` prop
and a `WorldSnapshot` prop, renders the typed view elements, and can be
mounted as a column (inline, takes 50% width) or as a modal (overlay).
Other parts of the SPA can import and compose it.

## Impact

- **Code:** `tools/runstatus/src/components/oracle/` — new components;
  `internal/runstatus/server/editor.go` — new cassette-list endpoint (slice 1's file)
- **Rendering:** new `OracleWorkbench.vue`, `CassetteBrowser.vue`,
  `StoryViewer.vue`; reuses `ViewElement.vue` and existing
  `oracle/OracleDetail.vue` style for cassette previews
- **Input:** card click → cassette detail; replay button → isolated oracle call RPC
- **Docs on ship:** `docs/tui/story-editor.md` (oracle workbench section);
  `docs/tui/web-ui.md` (story viewer component reference)

## Mental model

The workbench is a **test harness for one oracle at a time**. You select
the oracle contract (what the call declares), select the cassette (what
it recorded), and press replay (what it does now). The story viewer shows
what the room would look like if that cassette were the live response. The
author's loop is: pick cassette → view room → adjust prompt or schema →
save → reload.

## Layout

```
Within the right column of the editor shell (slice 2):

┌─ Room: clarifying ──────────────────────────────── dist 1.0  [↗ open] ┐
│ Hook        [▼]                                                         │
│ Domain model[▼]                                                         │
│ Oracle Workbench [▼]                                                    │
│  ┌──────────────────────────────────────────────┐                      │
│  │ ask · intro.md · schema: BriefSummary         │  ← oracle card      │
│  │ cassette key: clarifying__ask__intro          │                      │
│  └──────────────────────────────────────────────┘                      │
│                                                                         │
│  Cassettes for clarifying__ask__intro:                                  │
│  ┌────────────────────────────────────────────────────────────┐        │
│  │ ● happy_path   in: "I need a data pipeline…"   [preview ▼] │  ← selected │
│  │ ○ edge_empty   in: ""                          [preview ▼] │        │
│  └────────────────────────────────────────────────────────────┘        │
│                                                                         │
│  Output: { summary: "Data pipeline for …", confidence: 0.92 }          │
│                                                                         │
│  [▶ Replay against cassette]   [▶ Replay live (requires session)]       │
│                                                                         │
│  ┌─ Story viewer (room view with cassette output applied) ─────┐       │
│  │  [ViewElement.vue rendering world + cassette bindings]       │       │
│  └──────────────────────────────────────────────────────────────┘      │
└─────────────────────────────────────────────────────────────────────────┘

Story viewer as modal (triggered from any surface):

┌──────────────────────────────────────────────────────────┐
│ Room: clarifying — preview with cassette: happy_path   ✕ │
│                                                          │
│  [ViewElement.vue — full-width modal body]               │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

## Rendering changes

New components — all data-driven, no hand-rolled HTML:

| Component | Data source | Renders |
|---|---|---|
| `OracleWorkbench.vue` | `/editor/rooms/{id}/oracles` | oracle contract cards; cassette browser; replay buttons |
| `CassetteBrowser.vue` | `/editor/cassettes/{key}` | cassette list; selected cassette output preview |
| `StoryViewer.vue` | `View` + `WorldSnapshot` props | typed view rendered with local world; column or modal mode |

`StoryViewer.vue` is the only component in this slice designed for reuse
outside the editor. It must be self-contained: no dependency on the run
surface's Pinia store or session state. It accepts props, renders, and
emits nothing. Other components that want a "preview this view" surface
import and compose it.

Replay output is displayed via the existing `oracle/OracleDetail.vue`
(already handles structured output rendering for trace events) — no new
output renderer.

## Input & commands

| Interaction | Effect |
|---|---|
| Click oracle contract card | Expands cassette browser for that call |
| Click cassette entry | Selects that cassette; shows output preview; updates story viewer |
| `[▶ Replay against cassette]` | Calls new `/editor/oracle/replay` RPC with cassette override; shows output inline |
| `[▶ Replay live]` | Same RPC without cassette override; requires active session; records to trace |
| `[preview ▼]` on cassette | Inline expand of raw cassette JSON |
| Story viewer "half-screen" mode | Mounts as a 50% column in the editor's right column |
| Story viewer "modal" mode | Overlays full-width; close button emits `close` event |

New backend endpoints (in `internal/runstatus/server/editor.go`, slice 1's file):

| Endpoint | Notes |
|---|---|
| `GET /editor/cassettes/{key}` | Lists cassette files matching the key; returns input digest + output preview |
| `POST /editor/oracle/replay` | Body: `{room_id, oracle_index, cassette_file?}`; returns structured output; if `cassette_file` absent and session active, calls oracle live |

The replay RPC is the only write-path operation in this slice. It does
not advance story state. If no session is active and no cassette is
supplied, it returns an error (`code=noSession`).

## Rendering tests

No concurrent I/O (static + one RPC); the combined-I/O rule doesn't
apply. The bar is:

- **Vitest: `OracleWorkbench.spec.ts`** — mock `/editor/rooms/{id}/oracles`
  and `/editor/cassettes/{key}`; assert oracle cards render; selecting a
  cassette updates the story viewer's `WorldSnapshot` prop.
- **Vitest: `StoryViewer.spec.ts`** — given a `View` fixture and a
  `WorldSnapshot`, assert typed view elements render correctly in both
  column and modal modes; assert the component is self-contained (no
  store imports).
- **Vitest: `CassetteBrowser.spec.ts`** — given a cassette list fixture,
  assert each entry renders input digest and output preview; clicking
  selects it.
- **Playwright: `oracle-workbench.spec.ts`** — spawn `kitsoki web
  stories/prd/app.yaml --flow …/happy_path.yaml`; navigate to
  `/editor?room=clarifying`; expand Oracle Workbench; assert cassette
  list appears; click happy_path cassette; assert story viewer renders the
  expected view content.

## Tasks

```
## 1. Backend (slice 1 prerequisite + extensions)
- [ ] 1.0 Slice 1 (story-graph-api.md) must be complete
- [ ] 1.1 `GET /editor/cassettes/{key}` — list matching cassette files;
          return input digest (hash of oracle input) + output preview (first 200 chars)
- [ ] 1.2 `POST /editor/oracle/replay` — isolated oracle call; cassette-override path
          (no session needed); live path (session required); returns structured output

## 2. Workbench components
- [ ] 2.1 `OracleWorkbench.vue`: oracle contract cards; cassette browser integration;
          replay buttons; output display via OracleDetail.vue
- [ ] 2.2 `CassetteBrowser.vue`: cassette list from backend; input digest + preview;
          select-and-expand flow
- [ ] 2.3 Wire OracleWorkbench into the editor room detail panel (slice 2's EditorPage.vue)

## 3. Story viewer component
- [ ] 3.1 `StoryViewer.vue`: accepts `View` + `WorldSnapshot` props; renders via
          ViewElement.vue; column mode (50% width) + modal mode (overlay);
          self-contained — no store dependency
- [ ] 3.2 Document StoryViewer props + usage in a brief `docs/tui/story-viewer.md`;
          cross-link from `docs/tui/web-ui.md`

## 4. Prove + document
- [ ] 4.1 Vitest: OracleWorkbench, CassetteBrowser, StoryViewer (see Rendering tests)
- [ ] 4.2 Playwright: oracle-workbench.spec.ts (prd fixture; cassette browser visible;
          story viewer renders)
- [ ] 4.3 Manual walkthrough with stories/prd clarifying room; screenshot workbench
- [ ] 4.4 Add Oracle Workbench section to docs/tui/story-editor.md;
          trim/delete this proposal
```

## What we lose, honestly

- **Cassette replay does not advance story state.** The workbench is
  deliberately isolated: you can see what an oracle produces, but you
  can't trigger the downstream `bind:` or `emit_intent:` effects from
  the workbench replay. Testing the full on_enter chain still requires
  driving the session through the run surface.
- **Live replay requires a running session.** Authors who just want to
  test an oracle prompt offline must always use a cassette. There is no
  "live oracle without a session" path because oracle prompts reference
  `world` context that only exists inside a session.

## Open questions

1. **Cassette matching by key** — the `CassetteKey` from the graph API
   (slice 1) must match the files on disk. The current cassette lookup
   in `internal/oracle/` uses a deterministic hash of the call
   parameters. The workbench needs the same key to list files correctly.
   Should slice 1 expose the raw cassette directory path instead of just
   the key? *Lean: expose both — the key (for display) and the absolute
   path pattern (for the backend's glob search).*

2. **WorldSnapshot derivation** — to render the story viewer with
   cassette output applied, we need to simulate the `bind:` directives
   for the oracle's on_enter effect. Options: (a) replicate the bind
   logic in the frontend (fragile), (b) have the replay RPC return the
   post-bind world snapshot alongside the output (clean). *Lean: (b) —
   the replay RPC already runs on the Go side where bind logic lives.*

3. **Replay RPC auth / safety** — the live replay path calls an oracle.
   If the oracle is an `oracle.task` with `Write`/`Edit` tools (see
   memory: task-agents-must-not-implement), a carelessly-triggered replay
   could write files. Options: (a) disallow live replay for task oracles,
   (b) require explicit confirmation, (c) sandbox via worktree. *Lean:
   (a) for v1 — reject task oracle live replay with a clear error; the
   workbench shows cassette-only for task oracles.*

## Non-goals

- **No YAML write path** — the workbench does not edit cassette files or
  prompt templates; those open in the IDE via deep-link.
- **No full session replay** — the story viewer shows a static render
  derived from one oracle's cassette output, not a replayed session.
- **No cassette recording** — creating new cassettes is done by running
  the session normally; the workbench only reads existing cassettes.
- **Not a TUI feature** — web-only; the terminal TUI is unchanged.
