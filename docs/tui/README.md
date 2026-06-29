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

> Prefer a browser? The same app can be driven interactively over HTTP with
> [`kitsoki web`](../web/README.md) — a multi-story story browser plus chat-style
> session surfaces beside a live trace and state diagram. It shares the
> orchestrator with this TUI. That server also hosts the
> [story editor](story-editor.md) — a per-story static inspector (rooms, world,
> agent contracts, cassettes) that needs no session.
>
> Live in the editor? The same web UI embeds as a native VS Code surface — chat in
> the sidebar, trace in a bottom panel, themed to VS Code — via the
> [VS Code extension](vscode-extension.md). (Distinct from the inverse `/ide` link
> below, where the terminal dials *out* to an editor.)

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
| `/ideas <text>` | `commands_ideas.go` | Appends a bullet to `ideas.md` at the git toplevel — jot a thought without dispatching a turn |
| `/actions [n]` | `commands_actions.go` | Renders/selects the room's action menu |
| `/world` | `commands_world.go` | Hierarchical world viewer |
| `/trace` | `commands_trace.go` | The routing pipeline trace for recent turns |
| `/inbox [n\|all\|sync-github]` | `commands_inbox.go` | Inline notification list, open item `n`, show all rows, or refresh GitHub issue/PR intake |
| `/work [--all]` | `commands_work.go` | List active async work, including queued and trace-backed proposal review; `--all` broadens jobs/chats across every session on this host |
| `/chat show <id>` | `commands_chat.go` | Focused async chat context for queued/dispatching work without attaching to tmux |
| `/provider [name\|n]` | `commands_harness.go` | List/switch the [harness profile](../architecture/harness-profiles.md) (backend/provider); takes effect next turn |
| `/model [id\|n]` | `commands_harness.go` | List/switch the active profile's model; takes effect next turn |
| `/jump` | `commands_jump.go` | Navigate to background-completion events |
| `/ide [connect\|disconnect\|status]` | `commands_ide.go` | Connect/disconnect the live editor link; ambient selection rides each turn |
| `/open <path>` | `commands_open.go` | Open an artifact (resolved against the run's cwd) in `$EDITOR` or the OS default — the terminal-agnostic fallback for `.md` links |
| `/mine [status\|pause\|resume\|now\|scope\|queue\|accept\|refine\|dismiss]` | `mine_command.go` | The operator intercom to the [ambient miner](../architecture/ambient-mining.md): read its watermark/queue/scope, pause/resume it, force a pass, or accept/refine/dismiss a queued proposal by id |

> **Reload parity.** `/reload` hot-reloads the running story's `app.yaml` in
> place (re-validate, swap the `AppDef`, re-fire `on_enter`). The web UI's
> per-session **Reload** action ([`kitsoki web`](../web/README.md#reload-parity-with-the-tui-reload))
> mirrors this exactly — same `Orchestrator.Reload` + `RerunOnEnter` path, same
> "current state removed; staying put" outcome. The mechanics are documented
> once, canonically, under the **Hot reload** bullet in
> [`docs/stories/state-machine.md`](../stories/state-machine.md#8-the-turn-loop-state-machine-of-the-orchestrator).
> `/reload --force` is a TUI authoring-only variant that bypasses `once:` caches
> for one rerun; use it when testing prompt or `on_enter` edits.

## Observers: engine events → transcript

The engine runs on its own goroutines; observers bridge its events into
the bubbletea program via `tea.Program.Send`. Each is a sink the
orchestrator/host layer calls:

- `observer.go` — orchestrator outcomes, including background-turn
  completion.
- `routing_observer.go` — the semantic-routing tier events
  (deterministic / synonym / LLM / ambiguous) rendered as a live-
  updating routing-status block, with a ring buffer feeding `/trace`.
- `meta_stream_observer.go` — streams the Claude agent subprocess's
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
  `/inbox`. When a job store is wired, the inbox poller also checks GitHub
  assigned issues and requested PR reviews every five minutes through the
  idempotent external-notification path; `/inbox sync-github [repo]` is the
  immediate manual refresh and prints inserted/skipped counts.
- **Meta-mode** (`metamode.go`, with `internal/metamode/`) — a sidebar
  agent conversation rendered into the same pane with a distinct theme
  accent; you enter with `/meta …` and return with `/meta done`. See
  [`../stories/meta-mode.md`](../stories/meta-mode.md) for authoring.
- **Proposals inbox** (`mine_command.go`) — the [ambient miner](../architecture/ambient-mining.md)
  pushes its "capture this as structure?" proposals and the agent's
  write-mode opt-ins into one queue. A non-focus-stealing `proposals: N`
  chip rides the **same footer framework row** as `inbox: N` (rendered
  through the same `render.Pongo` template path as `ideFooterChip`,
  hide-when-zero) — it never changes `m.mode` and never interrupts a turn.
  Each queued item opens in the **same operator-question card**
  (`operator_question.go`, `ModeOperatorQuestion`) the operator already
  answers, with one accept/refine/dismiss gesture; `/mine accept|refine|dismiss
  <id>` is the scriptable CLI alias for that gesture. The badge appears only
  when the miner is enabled and the queue is non-empty. `/work` also folds
  trace-backed `mining.proposal_raised` rows that have no matching
  `mining.proposal_decided` event, so a resumed or MCP-driven TUI frame can
  list proposal-review work even before the miner service snapshot is refreshed.

## Editor awareness: `/ide`

`/ide` connects the TUI to a running VS Code (or Cursor/Windsurf) instance
over the same lock-file + MCP-over-WebSocket mechanism Claude Code uses, so
the editor becomes an always-on context source the operator can see. The
link substrate (discovery, auth, the ws client, `host.ide.*`) is the runtime
layer — [`architecture/hosts.md`](../architecture/hosts.md#hostide--editor-awareness) and
[`architecture/transports.md`](../architecture/transports.md#7-the-ide-link); this is the operator
surface on top, in `commands_ide.go`.

**Commands.**

| Command | Does |
|---|---|
| `/ide` | Connect if off, else show status (convenience alias). |
| `/ide connect [n]` | Discover + connect. When several lock files match cwd it prints a picker; re-run `/ide connect <n>` to choose. Reports IDE name + workspace. |
| `/ide disconnect` | Close the link. This also stops the agent env-scrub (the scrub is gated on a connected link) and flips the footer chip off. |
| `/ide status` | One block: connected?, IDE name, workspace, port. |

The command is dispatched inline in `handleSlashCommand` like `/help`; the
dial is async (an `ideConnectDoneMsg` carries the result back), and on
success the `*ide.Link` is held on `RootModel` and pushed onto the
orchestrator via `SetIDELink` so per-turn `host.ide.*` dispatch and the
`world.ide.connected` gate resolve it.

**Footer indicator.** While connected, a typed footer element renders an
`⧉ ide: <name> ✓` chip through the footer pongo2 template (not a hand-rolled
string) — so the operator always sees that the editor is listening.

**Ambient editor context.** Before each agent-bearing turn, if connected, the
TUI reads the operator's live editor context and threads it onto the turn ctx
(`host.WithIDEAmbient`; see `internal/host/ide_ambient.go`), then appends exactly
one settled transcript line as the operator's source of truth for what rode the
turn. Two layers, in priority order:

- **Selection** (`host.ide.get_selection`): highlighted text wins. Echo:
  `⧉ Selected N lines from <file>`.
- **Active document** (`host.ide.get_selection` with empty text): with nothing
  highlighted, `getCurrentSelection` still names the focused editor's file — the
  one the cursor is in — so its *path* rides (no file read; the agent reads it
  with its own tools if needed). This is the reliable "reference the open doc"
  signal: unambiguous even with many tabs open. Echo: `⧉ Editor open on <file>`.
- **Active open tab** (`host.ide.get_open_editors`): the fallback when the editor
  reports no active text editor (e.g. focus is in the kitsoki terminal). Uses the
  tab flagged active, or the sole open tab; several tabs with none active is
  ambiguous and rides nothing (recorded as `reason: ambiguous_focus`).

Field names vary by editor: `getCurrentSelection` returns the path under
`filePath`/`file`/`fileName`/`uri`, and `getOpenEditors` keys the list as `tabs`
or `editors` with items under `fileName`/`uri` — the handlers normalise all of
these.

This is the same affordance Claude Code gives. Context is read at submit, and
the echo reflects exactly what rode the turn.

**Recorded in the trace.** Every connected turn writes an
`ide.context_captured` event to the session trace via the orchestrator
(`RecordIDEContext`), carrying `{connected, source, file, lines, range,
injected, reason}` — `source` is `selection` / `active_editor` / `none`, and
`reason` (e.g. `ambiguous_focus`, `no_open_editors`, `deny_ruled`) explains why
nothing rode when `source: none`. So "the link was connected but the model
didn't see my doc" is diagnosable from the trace alone. No selection or
diagnostic *text* is recorded — only the path, counts, and provenance.

**Inject on change only.** A selection feeds the turn (and prints the echo)
only when it differs from the one that last rode a turn. A selection the
operator holds across several turns is injected once, not silently re-shaping
every follow-up; a changed selection rides again, and deselecting resets the
tracker so re-selecting the same range later counts as new.

The selection then reaches the model **two ways**:

- **Always-on (no opt-in).** The operator-facing agent verbs — `host.agent.ask`,
  `host.agent.ask_with_mcp`, and `host.agent.converse` — automatically append a
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

## Opening markdown artifacts: OSC 8 links + `/open`

A `kv` value that names a markdown artifact (`/\S+\.md$/` — the same
`isMarkdownPath` predicate the web uses in `ViewElement.vue`, mirrored in
`internal/render/elements/links.go`) is the most important thing on screen
when reviewing a proposal or design brief. The TUI makes it **openable** two
ways, chosen by terminal capability so neither silently fails:

- **OSC 8 hyperlinks.** The kv renderer wraps a fitting `.md` path in an OSC 8
  terminal-hyperlink escape (`\x1b]8;;file://<abs>…`, built only in
  `osc8Link`) plus a lipgloss underline. Supporting terminals (iTerm2, kitty,
  WezTerm, modern xterm) render it clickable → the OS opens it; older
  terminals drop the escape and show the plain path. The escape carries zero
  visible width, so to keep the column math byte-identical to the plain render
  the **lean v1** linkifies only a single path token that already fits on the
  line — a path that would wrap stays plain and relies on `/open`.
- **`/open <path>`** (`commands_open.go`) — the universal, keyboard,
  terminal-agnostic fallback. It resolves a relative path against the run's
  working directory (where `.artifacts/…` paths are rooted, matching the
  `media` element) and opens it via `$EDITOR` when set (the only
  operator-controllable, preferred-reader path) or the OS default opener
  (`open` / `xdg-open`).

We do **not** build an in-TUI markdown pager — we hand the file to the
operator's existing reader and stop.

The same OSC 8 mechanism powers the **spatial handoff**: when an oracle wants
the operator to point at a frame, the terminal prints a clickable link to a
transient chrome-less pointing window and blocks the turn until the operator
points and sends. See [`spatial-handoff.md`](spatial-handoff.md) (the web
capture surface it hands off to is [`spatial-capture.md`](spatial-capture.md)).

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
