# Single-pane chat-style TUI

**Status:** Draft v1. Nothing implemented yet.

## Goal

Replace the multi-pane mouse-driven TUI with a single-pane chat
transcript where every event (turns, menu, inbox, routing, meta) is
a styled block in the same stream, and the user controls the
system primarily through `/commands` — mirroring Claude Code,
Codex, and Gemini CLI. Keep typed view elements + pongo2 as the
per-block rendering pipeline.

## Motivation

Bugs and friction in the current TUI accumulate around the
multi-pane / mouse design (see `ideas.md`):

- Input doesn't wrap; long lines scroll off the edge (line 58, 66, 72).
- Numbers can't be typed as the first character because `1`–`9`
  quick-select the menu (line 59).
- The right column (menu + inbox) is too narrow to be useful
  (line 57), and the inbox panel competes with the transcript for
  vertical space.
- Mouse wheel capture breaks normal terminal text selection,
  requiring a `/mouse off` workaround.
- The world dump in the header keeps creeping back (line 56, 69).
- Features keep silently regressing (line 74) — the multi-pane
  surface has too many states for the test suite as it stands.

The redesign trades dashboard density for a model that's
predictable, testable, and familiar to anyone who's used a modern
terminal coding agent.

## Mental model

The UI is a **single pane** at any moment — no multi-pane layout,
no overlays, no mouse capture. But the *content* of that pane is
not always a chat transcript. Three kinds of view occupy the pane:

1. **Chat view** (the default). Everything that happens in a room
   — turns, system notices, inbox notifications, menus, routing
   chips, agent thinking — is a styled block in an append-only
   transcript.
2. **Dedicated views** invoked by certain `/` commands. Example:
   `/world` opens a **hierarchical object viewer/editor** for the
   world state — not a printed text block. `/viz` and `/trace`
   may be plain text today but are free to grow their own
   presentation later. These views fully replace the pane until
   dismissed (Esc / `q` / a defined exit).
3. **Room transcripts** (one per room). Switching rooms swaps the
   active transcript. Meta mode is a specific case: a parallel
   "meta room" with its own color theme that runs alongside the
   on-path room.

Common to all three: a two-line footer (mode, room/state, queue
depth, unread; line 2 driven by a story/room pongo template) and
a single prompt with a mode-specific prefix (`>`, `»`, `#`). No
mouse, no overlays, no side panes.

```
┌─ kitsoki ────────────────────────────────────────────────┐
│ [transcript: turns, system blocks, menus printed inline, │
│  inbox notifications printed inline, oracle live stream  │
│  printed inline, routing trace printed inline]           │
│                                                          │
│ │ proposing · cypilot · 2 queued · 3 unread              │  ← footer line 1 (framework)
│ │ PR #4821 · CI: passing · PLTFRM-90014                  │  ← footer line 2 (story/room)
│ > _                                                      │  ← prompt
└──────────────────────────────────────────────────────────┘
```

## What stays

- **Rendering pipeline**: `app.View` → `elements.RenderAll` →
  Glamour fallback (`internal/tui/transcript.go:355-384`). The
  whole typed-view + pongo2 stack is renderer-agnostic and works
  inside a single pane just as well.
- **Observer pattern**: orchestrator → `OnBackgroundTurn` →
  transcript block. `observer.go`, `meta_stream_observer.go`,
  `routing_observer.go` keep doing their jobs; they just write to
  the transcript instead of side panels.
- **Slash dispatcher** in `internal/tui/tui.go:1377-1443` — expand
  it, don't rewrite it.
- **Meta mode**, **off-path mode**, **slot-filling**,
  **disambiguation** as *concepts* — but visualized inline, not
  as overlays.
- **Semantic routing** (synonyms + slot parsers, commit `6b3caa0`)
  — leaned on harder, since users will mostly type action names
  rather than press numbers.

## What goes

| Component | Why |
|---|---|
| Right column (`menu.go` pane + `inbox.go` panel) | Crowds the screen; selecting by number hijacks input. |
| `/mouse on/off` + wheel capture | Mouse support is the source of input-fighting bugs and breaks normal text selection. |
| Routing-trace **overlay** (Ctrl+R full-screen) | Becomes a transcript block printed on demand by `/trace`. |
| Sessions-panel **overlay** (`sessions_panel.go`) | Becomes `/meta list` output printed inline; `/meta resume <id>` is just a command. |
| Clarify **modal** (`clarify.go`) | Becomes an inline "Clarification needed" block; user replies in the prompt. |
| Disambiguation **modal** (`disambiguation.go`) | Becomes an inline numbered list with the same rules as `/actions`. |
| System menu on Esc (`menu_system.go`) | Replaced by `/help` and explicit commands. |
| Routing chip (in-flight resolution badge near the input) | Replaced by an **inline routing-status block** in the transcript, attached to the user turn that triggered it (see Input pipeline). |
| Banner line + scroll-hint line | Collapse into the two-line footer. |

## New layout

### Chat view (default)

```
location/room                                     ← header line (one line max)
─────────────────────────────────────────────────
transcript stream — fills all available height,
mixed blocks rendered through the existing
typed-view + pongo2 pipeline:

  ▸ user turn      (> indented, printed the
                    instant Enter is pressed)
  ▸ routing status (attached under the user turn,
                    live-updated in place as the
                    pipeline progresses, then
                    settles into the resolved
                    intent line)
  ▸ agent turn     (markdown / typed view)
  ▸ system notice  (dim · prefix)
  ▸ menu block     (numbered list rendered as a
                    typed view; numbers select
                    via /actions <n> or by
                    re-typing the action name)
  ▸ inbox block    (printed when a new job
                    notification arrives — no
                    polling-driven panel)
  ▸ routing trace  (printed on /trace; full
                    pipeline trace including
                    candidates considered)
─────────────────────────────────────────────────
proposing · cypilot · 2 queued · 3 unread         ← footer line 1 (framework)
PR #4821 · CI: passing · PLTFRM-90014             ← footer line 2 (story/room template)
> _                                               ← prompt (single textarea)
```

### Dedicated views (e.g. `/world`)

A `/` command can open a view that fully replaces the pane. The
header and footer stay (so the user always knows where they are
and how to leave), but the body is owned by the view, not the
transcript:

```
world · cypilot                                   ← header (view name · room)
─────────────────────────────────────────────────
▾ session
  ▸ id: "sess_42"
  ▾ user
      name: "brad"
      role: "dev"
  ▾ tickets [3]
    ▸ [0] PLTFRM-89912
    ▸ [1] PLTFRM-90001
    ▸ [2] PLTFRM-90014
▸ flags
▸ providers
                                                  ← body owned by /world
─────────────────────────────────────────────────
view: world  ·  ↑/↓ navigate  ·  enter expand  ·  e edit  ·  q close
```

Dedicated views are not append-only and do not scroll the chat.
They have their own keybindings (described in a small footer
hint) and exit back to the chat view via `q`, `Esc`, or their
own dismiss command. Today's candidates:

- `/world` — hierarchical viewer/editor of the world object.
- `/viz` — could grow into an interactive FSM browser.
- `/trace` — could grow into a step-through routing inspector.

For v1, `/trace` and `/viz` can remain text dumps printed into
the transcript; `/world` is the one that genuinely needs a
dedicated view from day one.

### Room view (parallel transcripts)

Each room has its own transcript. The chat view above is "the
current room's transcript." Switching rooms swaps the active
transcript:

- **On navigation between on-path rooms**, the new room's
  transcript becomes active. If the old transcript should be
  cleared on entry, the TUI **down-scrolls past the old content**
  (rather than wiping it) so it stays available by scrolling up.
  This mirrors `ideas.md:65`: "the screen clears and the new room
  is near the top." Persistent vs transient is per-room author
  choice — persistent rooms keep history visible across re-entry;
  transient rooms down-scroll on every entry.
- **On entering `/meta`**, the TUI enters a different room with
  its own parallel transcript and a different color theme. The
  on-path transcript is preserved (scroll up to see it on return,
  or `/meta done` to come back). Each meta mode can declare its
  own accent color so they're visually distinct
  (`ideas.md:68`).

## Slash commands

Treat slash commands as the primary control plane. Extend the
existing dispatcher:

| Command | Status | Behavior |
|---|---|---|
| `/help` | **new** | Chat block. List all commands grouped by category (mirrors Claude Code's `/help`). |
| `/actions` | **new** | Chat block. Print the current room's available actions. Rendering is **room-provided**: each room may declare its own pongo2 template for the actions block (e.g. a card grid, a numbered list, a categorised tree). The default template ships with the framework; rooms override when they want something specific. Optional `<n>` arg dispatches by index. |
| `/actions auto on\|off` | **new** | Toggle: when on, the room's actions block is auto-printed at the end of every turn, just before the prompt re-shows. Default off. Persists for the session; rooms may declare a default in YAML. |
| `/inbox` | rework | Chat block. List recent notifications; `--all` for full list, `<n>` to open. Drop the expand/compact modes. |
| `/world` | rework | **Dedicated view.** Hierarchical viewer/editor of the world object. Owns the pane until dismissed. Not a printed text block. |
| `/trace` | rework | Chat block (v1) — print the last turn's routing trace. May grow into a dedicated step-through view later. |
| `/meta`, `/meta list`, `/meta new`, `/meta resume <id>`, `/meta done` | rework | **Room switch.** `/meta` enters a parallel meta room with its own transcript and accent color; `/meta done` returns. `/meta list` is a chat block in whichever room you're in. |
| `/warp <state> [k=v...]` | keep | Dev teleport unchanged. Chat block confirming the jump. |
| `/viz` | keep | Export DOT (chat block confirming the file path). May grow into an interactive view later. |
| `/jump`, `/jump <n>` | **new** | **Room switch.** Navigate to a recent background-completion event. `/jump` and `/jump 0` go to the latest; `/jump 1` is the one before that, etc. (0-indexed, newest-first). Scrolls the destination room's transcript to the completion point. |
| `/quit`, `/q` | keep | |
| `/clear`, `/compact` | **not included** | The transcript is append-only. No clear, no compaction. Scrollback and per-room transcripts handle "where did that go." |
| `/mouse` | **remove** | |

The "Chat block" / "Dedicated view" / "Room switch" column above
is load-bearing: it distinguishes commands that print into the
transcript from commands that take over the pane, and from
commands that swap which room (and therefore which transcript) is
active. Implementations should reflect this distinction in code
— not every `/` is a `fmt.Fprintln` into the transcript.

Convention: unknown command echoes `(unknown command: X) — try /help`.

## Input fixes

From `ideas.md` lines 58, 59, 66, 72:

- **Wrapping**: prompt textarea must visually wrap, not
  horizontally scroll. The current `internal/tui/prompt.go:24-80`
  claims wrap but tests in `internal/tui/tui_test.go` don't cover
  long single-paragraph input — add a golden test for ≥3 wrap
  lines on a textarea ≥ width.
- **Numbers at start of input**: today `1`–`9` quick-select the
  menu, so you can't type `1.5` or "10 PRs". Fix: drop numeric
  quick-select entirely. Numbers are normal text. Action selection
  is always `/actions <n>` or typing the action name (semantic
  routing handles synonyms — see `docs/semantic-routing.md`).
- **Input history**: already wired via ↑/↓ (`tui.go:1110-1200`,
  `inputHistory` / `historyPrev` / `historyNext`). Keep this
  binding. Shift+↑/↓ or PageUp/PageDown reserved for transcript
  scroll.
- **Esc cancels queue**: when there are queued in-room items
  (see **Input pipeline**), Esc drops them all from the queue
  and appends each to `inputHistory` so the user can recover them
  with ↑. The in-flight turn is not aborted — Esc is "stop
  feeding it more," not "stop what's running." Esc with an empty
  queue does nothing (or clears the prompt, matching current
  behavior).
- **Enter submits, Alt+Enter newlines**: keep the current binding
  (`internal/tui/tui.go:1033-1044`) — it's already correct.

## Input feedback: echo + live routing in the transcript

Today, the user's submitted input and the in-flight routing
indicator both live **near the input area**: the textarea blanks
on submit and a small chip appears beside the prompt while
routing runs. This proposal moves both into the transcript so
the chat is always the source of truth for "what just happened."

The moment the user presses Enter:

1. **Echo the user's text immediately** as a `user turn` block
   at the bottom of the transcript — even before routing starts.
   The prompt clears in the same frame. This matches
   `ideas.md:36` ("when user presses enter, immediately add
   their input into the chat window and show thinking there").
2. **Attach a live routing-status block** directly beneath the
   user turn. The block updates **in place** as the pipeline
   progresses through its phases:

   ```
   > back to the proposal
     routing: deterministic…
   ```

   becomes

   ```
   > back to the proposal
     routing: synonyms…
   ```

   becomes

   ```
   > back to the proposal
     routing: LLM…
   ```

   and finally settles into the resolved-intent line:

   ```
   > back to the proposal
     → nav: back   (deterministic · 1.00)
   ```

3. **The settled line stays in the transcript** as a permanent
   record of how that input was interpreted. The user turn + its
   resolution form a stable pair: scrolling up shows you exactly
   which intent fired for every prior message and via which
   pipeline phase, which is the readable form of `ideas.md:37`
   (visually distinguish deterministic vs LLM resolutions, show
   filled intent + confidence).

4. **Block input is non-blocking for the rest of the UI.** The
   user can keep typing the next message while routing finishes
   on the previous one; subsequent submits queue or pre-empt per
   the rules below.

### Settled-line format

Different cases render differently — same template family, but
the prefix and detail trailer vary:

| Source | Settled line |
|---|---|
| Deterministic match | `→ <kind>: <intent> (deterministic · 1.00)` |
| Synonym hit | `→ <kind>: <intent> (synonym · 1.00)` |
| Slot parser | `→ <kind>: <intent> (slot-parser · slots: …)` |
| Turn-result cache hit | `→ <kind>: <intent> (cached)` |
| LLM | `→ <kind>: <intent> (LLM · 0.84)` `slots: …` |
| Ambiguous (multiple candidates) | `? need clarification:` (followed by the inline disambiguation list — see below) |
| Unknown command | `(unknown command: /foo) — try /help` |
| Free-text in off-path | `→ off-path message` (no resolution; goes straight to the agent) |

`<kind>` is one of `nav`, `view`, `system`, `in-room`,
`off-path` — the same classification used by the queue-vs-immediate
fork. Showing the kind inline tells the user *why* the next thing
happens (or doesn't happen) the way it does: a `nav` line means
they're about to switch rooms; an `in-room` line means it
queued.

### Implications

- The Ctrl+R "routing trace" overlay disappears (already in
  "What goes") because every input now carries its routing
  resolution inline. `/trace` becomes the way to expand the
  *full* pipeline trace for the last input — candidates
  considered, scores, why others were rejected — which is more
  detail than the always-on settled line.
- The footer no longer needs to host an in-flight routing
  indicator — the transcript shows it next to the input that
  caused it. The footer is for *persistent* status (room,
  mode, queue depth, story fields), not per-turn state.
- For Phase 0 (UI preview CLI), the routing-status block is one
  of the block kinds the preview must render, including its
  live-update intermediate frames as separate static samples so
  authors can iterate on the visual.

## Input pipeline: queue vs. immediate

Every user input — whether it's `1`, "let's open the proposal",
or `/world` — passes through the same first stage: the **semantic
routing pipeline** (deterministic match → synonyms → slot parsers
→ LLM fallback as configured). Routing produces a *resolved
intent* before the TUI decides what to do with it. Only after the
intent is known does the dispatch path fork:

```
user text
   │
   ▼
semantic routing  ──► resolved intent (with kind: nav | in-room | view | system)
   │
   ▼
classify by kind ───┬─► nav        ─► execute immediately, do NOT queue
                    ├─► view       ─► execute immediately, do NOT queue
                    ├─► system     ─► execute immediately, do NOT queue
                    └─► in-room    ─► append to the current room's queue
```

### What "immediate" means

- **Navigation** (back, warp, room switch, on-path action whose
  type is navigational): pre-empts. Switches rooms without
  waiting for the current room's in-flight turn or queued items.
- **Dedicated views** (`/world`, future viewers): take over the
  pane immediately. The chat keeps running underneath; exit the
  view to see what happened.
- **System commands** (`/help`, `/actions`, `/quit`, `/inbox`,
  `/jump`): instant. Render their block, toggle a flag, or
  navigate. Never queued.

### What gets queued

In-room actions and free-text turns — anything that would invoke
the oracle/LLM or trigger an in-room state transition — go into
the current room's queue. The queue is FIFO and processes one
item at a time. While an item is in flight, subsequent in-room
inputs append to the queue and the user sees a "queued"
acknowledgement block in the transcript so they know it was
captured.

### Queueing across navigation

Each room owns its own queue. When the user navigates away (back,
warp, `/meta`), the rooms they leave keep processing their
queues in the background:

- The in-flight turn for room A continues running after the user
  warps to room B.
- Items queued in room A stay in room A's queue and process in
  order.
- When background work in room A finishes, an inline block is
  printed in whichever room the user is currently in:
  `✓ <room> · <summary>` (one line; the summary comes from the
  room's completion template). The user is **not auto-jumped**;
  they keep their current context. To visit the result, they
  type `/jump` (latest event) or `/jump <n>` (n-th most recent,
  0-indexed, so `/jump` == `/jump 0`).
- Coming back to room A shows the transcript with the
  background-completed turns already appended, scrolled to the
  completion point.

This reuses the existing background-jobs notification
mechanism; we extend it to cover "left a room with chat still in
flight."

### Esc clears the queue

Pressing Esc while items are queued removes them from the queue
and appends each — in submission order — to the user's
`inputHistory`. The user can then ↑-arrow through them to
recover, edit, or re-submit. The in-flight item is unaffected;
Esc is "stop feeding the queue," not "abort what's running."

This gives a non-destructive cancel: you can change your mind
about three queued follow-ups without losing the text you typed.

### Why route *before* enqueue

The routing pipeline classifies the intent. Without routing
first, we couldn't tell `back` (nav, immediate) from `pick the
backup branch` (in-room, queue). LLM-assisted routing has cost,
so this matters:

- Deterministic and synonym matches are essentially free — the
  classification happens before any LLM call.
- Where routing falls through to the LLM, the input *waits* on
  the routing decision before being enqueued. The user sees a
  brief "routing…" indicator in the prompt. Once classified, the
  intent either dispatches immediately (nav/view/system) or
  appends to the queue.
- The turn-result cache (commit `6b3caa0`) covers common
  phrasings, so this overhead is small in practice.

The user-visible promise: **`back` is always instant; `do the
thing` queues if the room is busy.** That's only true because
routing happens before the queue decision.

## Mode visualization and footer

Replace overlays with mode prefixes + a **two-line footer** + per-room
theming.

### Mode prefixes

| Mode | Prefix | Theme |
|---|---|---|
| Normal | `> ` | default |
| Meta | `» ` | meta-mode accent (per agent) |
| Off-path | `# ` | default + dim header |
| Slot-filling | `? ` | default |
| Awaiting LLM | `… ` (dim) | default |

Each meta mode declares its own accent color so when you're in a
parallel meta room the whole transcript reads visibly different
from on-path (`ideas.md:68`). Pongo2 templates already control
colors per block; the room itself picks the palette the templates
render against. Switching rooms is therefore a *theme swap* as
well as a *transcript swap*, which is exactly what makes it
unmistakable that you've changed contexts.

### Two-line footer (pongo-extensible)

The footer is **two lines tall** and its content is driven by
pongo2 templates declared at the **story** and **room** level.
The framework ships a default story-level template that covers
the common case; rooms override either line when they want to
surface domain-specific state.

```
─────────────────────────────────────────────────
proposing · cypilot · meta · 2 queued · 3 unread     ← line 1: framework defaults
PR #4821 · CI: passing · ticket PLTFRM-90014         ← line 2: story/room template
> _
```

Inputs available to the footer templates:

- `room` (id, label), `state` (current state path), `mode`
  (normal/meta/off-path/...), `queue_depth`, `unread_count`,
  `theme` — provided by the framework.
- Anything the story declares via `relevant_world` paths and
  custom expressions — same expression env as the rest of the
  view system.

Defaults:

- **Line 1** (framework): `room · state · mode · queue · unread`.
  Mode and queue-depth are framework concerns; the rest comes
  from the room.
- **Line 2** (story): empty by default. Stories opt in with their
  own template — e.g. dev-story can show PR/CI/ticket; cypilot
  can show current proposal title; oregon-trail can show date
  and party health.

Why pongo here: the rest of the UI already uses pongo2 templates
for views; the footer following the same model means authors get
the full expression language, includes/extends, and theming
without a new mechanism.

## Room transitions

Rooms own transcripts. The TUI's job on a room transition is:

1. Save the outgoing room's transcript (so it survives a return
   visit if the room is persistent).
2. Activate the incoming room's transcript and theme.
3. If the incoming room is configured **transient** or the author
   asked for a clean entry, **down-scroll past the existing
   content** so the new room's first lines start near the top of
   the viewport. Don't truncate — scrolling up still shows the
   prior content. This matches the model in `ideas.md:65`.
4. If the incoming room is **persistent**, leave the scroll
   position at the bottom of the prior transcript so the new
   content appends in place — the room feels like a continuing
   conversation.

Meta entry (`/meta`) is always a room switch with a theme swap.
On-path navigation between rooms is a room switch whose
clear-vs-append behavior is per-room author choice (declared in
the room's YAML; new field, see Open Questions).

## Migration plan

Phased so the tree stays runnable. Each phase ends with a working
TUI.

### Phase 0 — UI preview CLI

Before touching the live TUI, build a tiny CLI mode that renders
the redesign's blocks to stdout using the same renderer the TUI
will use. This is the design feedback loop and the golden-test
fixture for every later phase.

- Shape: `kitsoki ui preview [flags]` (subcommand on the
  existing `cmd/kitsoki` binary; no bubble tea event loop).
- Default output: a representative chat view showing one of each
  block kind in order — header, user turn, agent turn, system
  notice, menu block, inbox notification, routing trace, footer
  chip, prompt — exactly the sketch in the **New layout · Chat
  view** section above.
- Flags:
  - `--block <kind>` print just one block kind (for tight
    iteration on a single template).
  - `--view chat|world|trace` switch which view to preview.
    `--view world` renders the `/world` hierarchical viewer
    against a sample world object so the dedicated-view layout
    has the same golden treatment as chat blocks.
  - `--theme <name>` apply a room theme (default / meta-blue /
    meta-amber / off-path) so themes can be compared side by
    side.
  - `--width <n>` force a terminal width (for deterministic
    golden tests; defaults to detected width).
  - `--fixture <path>` render a YAML/JSON file describing a
    sequence of blocks, so authors can sketch a transcript and
    see how it renders without running the real app.
- Constraints:
  - Pure renderer — no orchestrator, no machine, no MCP. The
    inputs are static fixtures.
  - Same code path as the live TUI's renderer
    (`elements.RenderAll`, pongo2, Glamour fallback). If the
    preview drifts from the live UI, that's a bug in the
    renderer being split across two callers.
  - Output is byte-for-byte stable given the same width + theme
    + fixture, so `go test` can pin it as a golden.

Use cases:

1. **Design iteration**: tweak a template, run
   `kitsoki ui preview --block menu`, see the diff. No state
   machine to spin up.
2. **Golden tests**: a `tui_render_test.go` invokes the same
   render function with a fixed fixture + width and asserts on
   the rendered string. Catches regressions in formatting
   (`ideas.md:74`).
3. **Documentation**: paste the preview output into proposals
   and docs without hand-drawing ASCII (this proposal's sketches
   become real once the preview exists).
4. **Theme bake-off**: render the same fixture across themes to
   pick accent colors for meta modes.

### Phase 1 — additive commands + inline routing feedback

- Add `/help`, `/actions`, `/actions auto on|off`.
- Move the user-input echo into the transcript (immediate on
  Enter) and replace the input-area routing chip with the
  inline routing-status block under each user turn.
- Rework `/inbox` to print inline; keep the existing panel for now.
- When a new inbox notification arrives, also print a transcript
  line (additive — panel still updates too).
- Establish the three-flavored command model in code: introduce
  a `Command` interface (or similar) with implementations for
  *chat-block*, *dedicated-view*, *room-switch* so future commands
  pick the right shape from day one. `/world` is the proof — see
  Phase 2.
- No removals. Validates the chat-first feel before tearing out
  the right column.

### Phase 1.5 — `/world` dedicated view

- Build the hierarchical viewer for the world object: collapsible
  nodes, navigation, expand/collapse, copy-path.
- Editor comes later — read-only is fine for v1 of the view.
- This is the first non-chat-block command and validates the
  dedicated-view plumbing (pane takeover, exit back to chat,
  footer hint line).

### Phase 2 — inline overlays

- Routing trace (Ctrl+R) → `/trace` command, prints a transcript
  block.
- Sessions panel (`sessions_panel.go`) → `/meta list`, prints
  inline. `/meta resume <id>` becomes plain dispatch.
- Clarify modal (`clarify.go`) → inline "Clarification needed"
  block; user replies in the normal prompt.
- Disambiguation modal (`disambiguation.go`) → inline numbered
  list using the same rules as `/actions`.
- Delete the overlay paths once their inline replacements are
  green.

### Phase 3 — drop the right column

- Remove menu pane + inbox panel from
  `internal/tui/tui.go:3114-3115` layout. Transcript fills full
  width.
- Delete `menu.go` (data still flows; rendering moves into
  `/actions`).
- Delete inbox panel modes from `inbox.go`; keep the store and
  notification model — the data layer survives, only the panel
  rendering goes away.

### Phase 4 — input fixes + queue

- Wrap, numeric-start-input (drop quick-select).
- Wire the per-room input queue and the route-then-dispatch
  classifier. In-room actions enqueue; nav/view/system pre-empt.
- Esc clears the queue and stashes items into `inputHistory`.
- `/jump` and `/jump <n>` for background-completion navigation.
- Golden tests: wrap at width-1, leading numerals, Esc-stashes,
  queue ordering with mixed nav/in-room input.

### Phase 5 — drop mouse

- Remove `tea.MouseMsg` handler, `/mouse` command, `MouseOn` test
  helper. Terminal handles text selection natively.

### Phase 6 — two-line footer, mode prefixes, room transcripts

- Replace banner line + scroll-hint line + routing chip line with
  the two-line footer described above. Line 1 is the framework
  default (room · state · mode · queue · unread); line 2 is a
  story/room pongo2 template that defaults to empty.
- Wire mode-specific prompt prefixes.
- Implement per-room transcripts: one transcript buffer per room,
  swapped on navigation, with the down-scroll-on-transient-entry
  behavior. Persistent-vs-transient declared in the room YAML.
- Wire per-room theme so `/meta` rooms render with their declared
  accent color.

### Phase 7 — cleanup

- Delete `menu_system.go` (Esc → menu), `routing_chip.go`, dead
  overlay code.
- Update `tui_test.go` — most pane-coordinate assertions will
  break; replace with transcript-content assertions, which is
  healthier.

## What we lose, honestly

- **Persistent menu visibility**: today the menu is always
  on-screen. After the redesign you type `/actions` (or scroll up
  to the last menu block) to re-see it. The footer mitigates
  this by showing the room/state, and `/actions auto on` brings
  it back as a per-turn affordance.
- **Polling-driven inbox panel**: today the inbox updates every
  2s in a fixed location. After: it appears as a transcript line
  when new items land, and `/inbox` reprints the latest. For
  users who want a "dashboard," that's a regression — but it
  matches the chat-first model and stops the panel from competing
  for vertical space.

## Decisions

Settled during initial review:

- **Action selection when off-screen**: `/actions <n>` works
  regardless of scroll position because the latest action set is
  held in state. No selection window; no quick-select.
- **No `/clear`, no `/compact`**: the transcript is append-only.
  Scrollback and per-room transcripts cover "where did that go."
- **Two-line footer, pongo-driven**: line 1 framework defaults
  (room · state · mode · queue · unread), line 2 story/room
  template (empty by default). Pongo2, same as the rest of the
  view system.
- **No numeric quick-select**: drop `1`–`9` action-select
  entirely. Selection is `/actions <n>` or by name.
- **`/world` is a full viewer/editor**: editing world state from
  the UI is allowed. It breaks determinism *in the abstract*,
  but every edit is captured in the trace, so a recorded session
  containing manual edits replays deterministically. Editor in
  scope from v1.
- **Dedicated views are flat (v1)**: a dedicated view cannot
  open another dedicated view. Nestable later if a real use case
  appears.
- **Background completion = print + `/jump`**: a one-line
  completion summary prints in the user's current room
  (`✓ <room> · <summary>`). No auto-teleport. The user navigates
  to the event with `/jump` (latest) or `/jump <n>` (0-indexed
  newest-first).
- **Footer shows queue depth**: yes. Part of the line-1
  defaults.
- **No `/cancel` command**: Esc clears the whole queue and
  stashes items into `inputHistory` for ↑-recovery. That covers
  the common case without adding a per-item cancel surface.
- **Room YAML field for transcript persistence**: ship as
  `transcript: persistent | transient` on the room. Defaults:
  `persistent` for on-path rooms, `transient` for meta-style
  parallel rooms. Threads through `app.Room` → loader → state
  machine; the TUI reads it to decide whether to down-scroll on
  entry.
- **`/jump` scope is global within a TUI session**: not
  per-story. The user model is "the last thing that finished,"
  not "the last thing in room X." If we ever need per-story
  later, we can add a flag.
- **Input is blocked during a nav transition**: the prompt is
  not accepted between the moment a nav intent is dispatched and
  the moment the new room is active. This sidesteps the
  old-room-vs-new-room queue ambiguity entirely — by the time
  the user's next keystroke is processed, they're in the new
  room, and any in-room action they type lands in the new
  room's queue. Transitions are fast (room swap is local; no
  LLM in the loop), so this is a sub-frame block.
- **Two-line footer overflow**: truncate-with-ellipsis on the
  right. Story authors use pongo conditionals to elide
  low-priority fields at narrow widths.

## Out of scope

- Localization / theming beyond what pongo2 templates already
  support (`ideas.md:21`). The redesign doesn't help or hurt
  these; they ride on the same rendering pipeline.
- React UI / VS Code integration (`ideas.md:49-50`). Different
  surface, different proposal.
- Background-job dashboard. If we ever want one, it can be a
  pane *opt-in* via `/inbox watch` or similar — not the default.
