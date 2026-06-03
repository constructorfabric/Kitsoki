# The terminal UI

Kitsoki's interactive surface is a **single-pane, chat-style TUI**:
every event — your turns, the routing decision, the room's narrative,
inbox notifications, meta-mode replies, system notices — is a styled
**block** appended to one scrolling transcript, and you drive the
system primarily through `/commands`, mirroring Claude Code / Codex /
Gemini CLI. Typed view-elements + pongo2 remain the per-block rendering
pipeline.

This page describes the architecture as it stands in
`internal/tui/` and `internal/render/`. *Audience: contributors working
on the UI, and authors who want to understand how their views reach the
screen.*

---

## Blocks: the unit of rendering

A **block** is one styled piece of transcript. Block renderers are
pure functions of a `*Renderer` (width, theme, color knobs) returning a
string — they live in `internal/tui/blocks/`:

- `blocks.go` — the `Renderer` struct and theme wiring.
- `render.go` — the individual block renderers: `Header`, `UserTurn`,
  `RoutingResolved`, `AgentTurn`, `SystemNotice`, `Menu`, `Inbox`,
  `BackgroundComplete`, `Footer`, `Prompt`, `World`, `RoutingTrace`.
- `compose.go` — assembles full layouts from those renderers
  (`RenderChatView`, `RenderWorldView`, `RenderTraceView`).
- `themes.go` — the shipped palettes (default plus meta and off-path
  accents), each a `*Theme` of Lipgloss colors. The transcript's accent
  changes with context (e.g. meta-mode), not per room.

The split is deliberate: **pongo2 decides view content** (conditionals,
slot expressions), **blocks decide visual styling** (color, borders,
wrapping).

## The view pipeline: typed elements + pongo2

A room's `view` becomes on-screen text through `internal/render/`:

1. The room declares typed **view elements** (prose, heading, list, kv,
   code, choice…) — see [`../stories/story-style.md`](../stories/story-style.md)
   for the element vocabulary and
   [`../stories/choice-widget.md`](../stories/choice-widget.md) for the
   interactive one.
2. `renderer.go` (`AppRenderer`, a per-app `pongo2.TemplateSet`) and
   `pongo.go` (context binding, undefined-variable fallback) execute
   the templates to plain text. Note pongo2 is Django-style: filter
   args use `:` (not `()`), and there is no inline ternary — use
   `{% if %}…{% else %}…{% endif %}`.
3. The blocks layer styles that text (e.g. `AgentTurn`) and appends it
   to the transcript.

## The `/command` surface

Commands are the primary control surface. They come in a few flavours
(declared in `commands.go`): block commands that render into the
transcript, dedicated-view commands that take over the pane, and
room-switch commands. Notable families:

| Command(s) | File | Purpose |
|---|---|---|
| `/help` | `commands_help.go` | Lists commands by category |
| `/actions [n]` | `commands_actions.go` | Renders/selects the room's action menu |
| `/world` | `commands_world.go` | Hierarchical world viewer |
| `/trace` | `commands_trace.go` | The routing pipeline trace for recent turns |
| `/inbox` | `commands_inbox.go` | Inline notification list |
| `/jump` | `commands_jump.go` | Navigate to background-completion events |
| `/ide [connect\|disconnect\|status]` | `commands_ide.go` | Connect/disconnect the live editor link; ambient selection rides each turn |

## Observers: engine events → transcript

The engine runs on its own goroutines; observers bridge its events into
the bubbletea program via `tea.Program.Send`. Each is a sink the
orchestrator/host layer calls:

- `observer.go` — orchestrator outcomes, including background-turn
  completion.
- `routing_observer.go` — the semantic-routing tier events
  (deterministic / synonym / LLM / ambiguous) rendered as a live-
  updating routing-status block, with a ring buffer feeding `/trace`.
- `meta_stream_observer.go` — streams the Claude oracle subprocess's
  stdout so you see an agent's tool calls and text as they happen, not
  just the final result.
- `room_enter_observer.go` — room-entry callbacks, so the banner can
  render before `on_enter` host calls dispatch.

Each forwards on a fresh goroutine so a slow render never back-pressures
an in-flight LLM call.

## Input, menu, inbox, meta-mode

- **Prompt** (`prompt.go`) — a multi-line wrapping textarea (no
  horizontal scroll). Plain Enter submits; Alt+Enter / Ctrl+J insert a
  literal newline. A mode prefix signals context (normal `>`, meta `»`,
  off-path `#`, slot-filling `?`, awaiting-LLM `…`).
- **Menu / inbox** (`menu.go`, `inbox.go`) — the action menu and
  notification list, surfaced inline as blocks and via `/actions` /
  `/inbox`.
- **Meta-mode** (`metamode.go`, with `internal/metamode/`) — a sidebar
  agent conversation rendered into the same pane with a distinct theme
  accent; you enter with `/meta …` and return with `/meta done`. See
  [`../stories/meta-mode.md`](../stories/meta-mode.md) for authoring.

## Editor awareness: `/ide`

`/ide` connects the TUI to a running VS Code (or Cursor/Windsurf) instance
over the same lock-file + MCP-over-WebSocket mechanism Claude Code uses, so
the editor becomes an always-on context source the operator can see. The
link substrate (discovery, auth, the ws client, `host.ide.*`) is the runtime
layer — [`../hosts.md`](../hosts.md#hostide--editor-awareness) and
[`../transports.md`](../transports.md#7-the-ide-link); this is the operator
surface on top, in `commands_ide.go`.

**Commands.**

| Command | Does |
|---|---|
| `/ide` | Connect if off, else show status (convenience alias). |
| `/ide connect [n]` | Discover + connect. When several lock files match cwd it prints a picker; re-run `/ide connect <n>` to choose. Reports IDE name + workspace. |
| `/ide disconnect` | Close the link. This also stops the oracle env-scrub (the scrub is gated on a connected link) and flips the footer chip off. |
| `/ide status` | One block: connected?, IDE name, workspace, port. |

The command is dispatched inline in `handleSlashCommand` like `/help`; the
dial is async (an `ideConnectDoneMsg` carries the result back), and on
success the `*ide.Link` is held on `RootModel` and pushed onto the
orchestrator via `SetIDELink` so per-turn `host.ide.*` dispatch and the
`world.ide.connected` gate resolve it.

**Footer indicator.** While connected, a typed footer element renders an
`⧉ ide: <name> ✓` chip through the footer pongo2 template (not a hand-rolled
string) — so the operator always sees that the editor is listening.

**Ambient selection.** Before each oracle-bearing turn, if connected, the TUI
reads the editor's live selection via `host.ide.get_selection` and threads it
onto the turn ctx (`host.WithIDEAmbient`; see `internal/host/ide_ambient.go`),
then appends exactly one settled transcript line —
`⧉ Selected N lines from <file>` — as the operator's source of truth for what
rode the turn. This is the same affordance Claude Code gives. The selection is
read at submit, and the echo reflects the exact range read.

The selection then reaches the model **two ways**:

- **Always-on (no opt-in).** The operator-facing oracle verbs — `host.oracle.ask`,
  `host.oracle.ask_with_mcp`, and `host.oracle.converse` — automatically append a
  standardized `## Active editor selection (via /ide)` block to the rendered
  prompt, so a selection feeds requests like "do this idea" in *every* story
  without each prompt opting in. It is a no-op when nothing is selected (the
  prompt is byte-identical). The decision verbs (`decide`/`extract`) and the
  `task` delegation verb are intentionally **excluded** so routing/extraction and
  sub-agent context are not biased by an editor selection.
- **Explicit scope.** The same fields are also exposed as `args.ide`
  (`{{ args.ide.file }}` / `{{ args.ide.selection }}` / `{{ args.ide.range }}`)
  for a prompt that wants to place the selection precisely rather than take the
  appended block.

**Deny list.** Because kitsoki cannot read Claude Code's own `Read`
deny-rules and must not assume parity, ambient attach is gated on an explicit,
local deny list (`WithIDEDenyList`, `filepath.Match` globs against the
absolute path and base name; default empty). A deny-ruled file attaches
nothing and emits no echo.

**What we accept.** Ambient injection means the selection silently shapes
prompts — context the operator didn't type. The `⧉` echo is the mitigation
(always visible); `/ide disconnect` and the deny list are the escape hatches.
No auto-connect in v1 — `/ide` is explicit so the operator opts into ambient
injection knowingly.

## TUI as a transport

The TUI is one **transport adapter** among several. Phase templates
post narrative to a transport key; the registry dispatches to the
registered transport (the TUI buffers posts and appends them as blocks;
Jira/Bitbucket post to external threads). This keeps narrative
transport-agnostic — the same view can render to the TUI and a ticket
comment at once. See
[`../architecture/transports.md`](../architecture/transports.md).

## Testing the UI

Rendered output is regression-tested independently of state logic:

- **[`rendering-tests.md`](rendering-tests.md)** — writing TUI
  rendering regression tests with the `RenderingAnalyzer` to catch
  layout bugs, overlaps, and silent regressions. The `rendering-tests`
  skill covers the same ground with the `CapturedIO` helper.

For state-machine behaviour (not pixels), use the flow tests in
[`../tracing/testing.md`](../tracing/testing.md).
