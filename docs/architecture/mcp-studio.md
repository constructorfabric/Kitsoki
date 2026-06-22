# MCP studio — author, drive & see kitsoki from an external agent

`kitsoki mcp` is a stdio [MCP](https://modelcontextprotocol.io) server an
**external** LLM client (Claude Code, Claude Desktop, an IDE agent) attaches
to. It gives that agent the three things it needs to build a kitsoki story:
**author** it, **drive** a live session of it, and **see** the rendered
result — terminal *and* browser — over one connection.

It is distinct from the narrow per-app server: [`kitsoki serve`](../../internal/mcp/server.go)
exposes a single `transition` tool that drives one app's state machine.
`kitsoki mcp` is the authoring/introspection *control plane* — its state is an
authoring **workspace** plus zero-or-more live **driving sessions**, exposed as
the `studio.*`, `story.*`, `session.*`, `chat.*`, `render.*`, and `issue.*`
tool families. It is a sibling of
`kitsoki serve` and the [operator-ask bridge](operator-ask.md): same
`github.com/modelcontextprotocol/go-sdk`, same `StdioTransport`, same
`mcp__<server>__<tool>` naming.

Implementation: [`internal/mcp/studio/`](../../internal/mcp/studio/) (server,
handle model, tools, MCP-client operator prompter) and
[`cmd/kitsoki/mcp.go`](../../cmd/kitsoki/mcp.go) (the subcommand).

## The handle model

An MCP connection is **one studio session** = the server process's in-memory
state ([`StudioSession`](../../internal/mcp/studio/handles.go)). It owns named
handles:

- **At most one workspace handle** — a story directory under authoring, with
  the last `app.Load` result (the cached `AppDef` + `ValidationError`s) on it.
  `story.*` tools operate on the workspace.
- **Zero or more driving-session handles** — each a keyed, trace-backed kitsoki
  session over its own orchestrator + harness, with a harness mode
  (`replay`/`live`) and a JSONL trace path. `session.*` tools take a session
  handle; `render.*` tools take a session handle **or** an explicit
  `{story_path, state, world?}` spec.

```
StudioSession (one MCP connection)
├── workspace: WorkspaceHandle?          // a story dir under authoring (≤1)
│     └── dir, cached app.Load result (AppDef + ValidationError[])
└── sessions: map[handle]SessionHandle    // keyed driving sessions (0..n)
      └── kitsoki SessionID, orchestrator, harness mode (replay|live), trace path

   client ──(stdio MCP)──▶ kitsoki mcp ──▶ tool dispatch ──▶ handle
        story.*   → app.Load / graph.* / testrunner.RunFlows   (no LLM)
        session.* → orch.Turn / SubmitDirect / ContinueTurn    (replay default)
        render.*  → ComposeFrame / shot raster / webshot.Shot  (read-only)
```

The server core records **nothing new**: each driving handle writes through the
same JSONL event sink as [`kitsoki turn --trace`](developer-guide.md#61-the-trace-is-your-transcript),
so routed intents, `agent.call.*`, and transitions land in that session's
trace and replay unchanged. The studio session itself is ephemeral process
state; its handles point at durable traces. Handle resolution is **fail-fast** —
an unknown session handle, or a `story.*` call with no workspace bound, returns
a structured [`ToolError`](../../internal/mcp/studio/server.go) (`ok:false` +
a stable `code`: `UNKNOWN_HANDLE`, `NO_WORKSPACE`, `BAD_REQUEST`, …), mirroring
`serve`'s `TransitionError` — never a panic, never a silent no-op.

## No-LLM by default

Every driving session defaults to the **replay** harness, so the studio never
hits a real LLM unless explicitly told to — the project-wide testing rule
(automated tooling never incurs LLM cost). The harness is built behind an
injectable seam ([`HarnessBuilder`](../../internal/mcp/studio/handles.go)) so a
test can supply a *failing* live harness and assert a default-mode session never
reaches it (`TestServer_NoLiveFallthrough`). A session opts into `live` (or VCR
`record:`) explicitly on `session.new`, and the mode rides the handle's metadata
so the agent — and the human watching it — knows when an LLM is in the loop. A
replay miss is a **hard error**, never a silent live fallthrough.

`story.validate` and `story.test` are deterministic by construction.

## Tool surface

Tools keep the dotted `family.verb` name; the SDK exposes each to the client as
`mcp__kitsoki__<name>`. Two liveness tools — `studio.ping` (`→ {ok, version}`)
and `studio.handles` (`→ {sessions[], workspace?}`) — prove transport and attach
before any domain tool runs. `studio.work` adds the global async queue across
open handles, so an external agent can ask "what needs attention now?" without
inspecting every session one by one.

### `studio.*` — attach & reacquire globally

| Tool | Shape | Purpose |
|---|---|---|
| `studio.ping` | `{}` → `{ok, version}` | liveness probe |
| `studio.handles` | `{}` → `{sessions[], workspace?}` | list open handles |
| `studio.work` | `{include_quiet?, limit?}` → `{summary, sessions[], items[]}` | prioritized async work queue across all open driving handles |

`studio.work.items[]` includes unread inbox notifications, running or
awaiting-input jobs, failed jobs, pending/dispatching chat drives, and
backgrounded tmux chats. Each item carries the source `handle`, session/story
metadata, stable IDs, a priority, and a `reacquire` hint naming the next MCP
tool call (`session.teleport`, `session.inspect`, or `chat.show`). By default it
omits read notifications and quiet terminal jobs; pass `include_quiet:true` when
you need the full non-dismissed history. The queue is sorted by intervention
priority: passive `success` / `info` notifications stay visible and
reacquirable, but rank below active jobs/chats and do not increase
`summary.needs_attention`.

When a job row has a matching unread job-origin notification, `studio.work`
returns `reacquire.tool: "session.teleport"` with that notification id so the
client can jump directly to the saved origin context. Job rows without a
matching unread notification keep the broader `session.inspect` fallback.

### `story.*` — author (deterministic, LLM-free)

The agent is the author; these are its compiler, linter, and test runner. Each
wraps a shipped Go API — the same code the human's `kitsoki run` / `/editor`
uses, so the MCP surface can never disagree with them.
([`story_tools.go`](../../internal/mcp/studio/story_tools.go).)

| Tool | Shape | Wraps |
|---|---|---|
| `story.read` | `{path} → {content}` | workspace-scoped file read |
| `story.write` | `{path, content} → {written, validation}` | write, then **auto-validate** in one round-trip; path-escape rejected |
| `story.validate` | `{dir?} → {ok, errors[]}` | `app.Load` → `[]ValidationError{File, Line, Column, Message}` — the full load-time invariant set |
| `story.graph` | `{dir?, room?} → {rooms[] \| detail \| agents[]}` | `graph.RoomList` / `Detail` / `AgentContracts` (the pure functions behind `/editor`) |
| `story.test` | `{dir?, flows?} → {report}` | `testrunner.RunFlows` (no LLM; honours `--recording`/`--host-cassette`) |

### `session.*` — drive & introspect

`session.drive` is the **one interpretive seam**: it submits free text through
the orchestrator turn loop (live or replay), and that routing decision is
recorded to the trace exactly as a TUI turn is. Everything else is a
deterministic direct path or a read.
([`session_tools.go`](../../internal/mcp/studio/session_tools.go),
[`session_runtime.go`](../../internal/mcp/studio/session_runtime.go).)

| Tool | Shape | Wraps |
|---|---|---|
| `session.new` | `{story_path, harness?, cassette?, trace?} → {handle, state}` | open a driving handle (default `harness:replay`) |
| `session.attach` | `{story_path, key, …} → {handle, state}` | co-drive an existing keyed session via the external-attach bridge |
| `session.drive` | `{handle, input} → {outcome, frame}` | **free text** → `orch.Turn` (interpretive route) |
| `session.submit` | `{handle, intent, slots?} → {outcome, frame}` | `SubmitDirect` — pick a menu intent |
| `session.continue` | `{handle, slots} → {outcome, frame}` | `ContinueTurn` — supply missing slots |
| `session.answer` | `{handle, question_id, answers} → {outcome, frame} \| {awaiting_operator}` | resume a parked operator-ask (see below) |
| `session.teleport` | `{handle, notification_id} → {outcome, frame}` | jump to an inbox notification's saved target and mark it read |
| `session.inspect` | `{handle} → {state, world, allowed_intents, last_view, async, jobs[], notifications[], pending_drives[], backgrounded_chats[], last_turns[]}` | `buildInspectOutput` + session JobStore / ChatStore (read-only) |
| `session.command` | `{handle, command, cols?, rows?} → {frame}` | run a deterministic TUI slash command such as `/work --all` against the handle |
| `session.trace` | `{handle, since?, until?, limit?} → {events[], last_turn}` | the session's JSONL trace (read-only) |
| `chat.show` | `{chat_id, handle?, session_id?, since_seq?} → {context?, chat, pty?, messages[]}` | read-only focused context for a selected async chat/subagent; `chat.display_scope_key` is the operator-facing scope label |

Every drive/submit/continue returns **both** the structured `TurnOutcome` (mode,
new state, allowed intents, slots needed) **and** the rendered `Frame` — so the
agent reasons on metadata and *sees* the screen in one call.

`session.command` exists for TUI-only operator surfaces that are not
orchestrator turns, especially smoke-testing `/work --all` and `/chat show
<id>` through MCP. It uses the live TUI slash dispatcher and rejects commands
that return an asynchronous terminal side effect, such as attaching to tmux.

`session.inspect` also carries compact per-handle background-job and inbox projections.
`async` summarizes running, awaiting-input, terminal, unread, and unread
action-required counts; `jobs[]` shows the session's job IDs, kinds, statuses,
origin states, errors, and timestamps; `notifications[]` shows active inbox
rows, including `action_required` items and teleport job/state fields. When a
chat store is wired, `pending_drives[]` shows pending/dispatching
chat-input-queue rows owned by the session, and `backgrounded_chats[]` shows
tmux-hosted chats left in `pty_background` mode. This is the structured MCP
surface for an external agent to inspect the chosen handle after `studio.work`
has ranked the global queue, notice required operator input, and reacquire or
switch to the task through `session.teleport` without scraping the TUI frame or
decoding trace events.

Story-authored `host.chat.drive` effects are stamped with the originating
session and state before the host handler enqueues the drive, so ordinary
state-machine chat work appears in these same `pending_drives[]` and
`studio.work.items[]` surfaces without fixture-only store seeding.

When the selected async item is chat-backed, `chat.show` drills into the
focused context: chat metadata, the transcript slice, and any recorded tmux PTY
state. That gives an MCP client the same "switch attention to this subagent"
context that `session.inspect.backgrounded_chats[]` points at, without shelling
out to `kitsoki chat show`. When the client follows a `studio.work` reacquire
hint, it can pass the hint's `handle` and `session_id` through unchanged;
`chat.show` validates that the chat belongs to that session and echoes the
focused context back in `context`.

For a copy-paste smoke test of the async path, including background completion,
inbox notification capture, and `session.teleport`, see
[`../recipes/studio-mcp-async-smoke.md`](../recipes/studio-mcp-async-smoke.md).

### `render.*` — see (read-only)

`render.*` re-render a state the agent already reached, or an explicit
`{story_path, state, world?}` spec — and **never advance a session** (least
surprise: "look at this" can't mutate the machine). None of them invent a layout
path: they capture the existing TUI and web renderers.

| Tool | Returns | Source |
|---|---|---|
| `render.tui` | the `Frame` `{text, ansi, metadata}` at any width | `tui.ComposeFrame` |
| `render.tui_png` | the `Frame.text` **+** an MCP image block of the terminal | `internal/tui/shot` raster (ANSI→PNG) |
| `render.web` | text **+** an MCP image block of the **real** browser view | `internal/webshot` (headless `kitsoki web`) |

The **`Frame`** is the unit of fidelity — "the full screen a human sees" as a
value, captured once by the TUI's own composer and read by every headless
consumer so a screenshot and a real terminal can never drift. Its composition,
the `kitsoki drive` / `kitsoki shot` / `kitsoki web-shot` substrate commands, and
the `webshot` seam are documented in
[`docs/tui/frame-composition.md`](../tui/frame-composition.md).

Image blocks are gated on client capability: `render.tui_png` / `render.web`
attach an image block when the client advertises image support and **always**
include the textual frame, so a text-only client still gets something.
`kitsoki mcp` wires `render.web` for live handles by serving the open studio
session through the same runstatus web handler and screenshotting it with the
Node/Playwright [`web-shot.ts`](../../tools/runstatus/web-shot.ts) invoker.
That path needs a staged runstatus SPA (`make web`) and local Playwright
dependencies. Story/state spec screenshots still belong to `kitsoki web-shot`,
where a no-LLM flow or host cassette can define the deterministic web session.

### `issue.*` — file a gap (with evidence bundled)

The agent that drives kitsoki through this MCP has no shell and no write tools,
so when the *studio surface itself* can't do something needed to develop, test,
run, introspect, trace, or debug a story, it can't reach for `gh`. `issue.create`
closes that gap from inside the MCP — and, because the studio already produces
the evidence, it bundles it in.
([`issue_tools.go`](../../internal/mcp/studio/issue_tools.go).)

| Tool | Shape | Wraps |
|---|---|---|
| `issue.create` | `{title, body?, labels?, repo?, handle?, include_trace?, trace_limit?, include_inspect?, assets?} → {url, number, labels[], assets[]}` | render assets → `.artifacts`, bundle a handle's trace + inspect, then the injectable `IssueFiler` (prod: `gh`) |

Three things happen server-side so the agent never handles bytes:

- **assets** — each `assets[]` entry (`kind: tui_png | web | tui_text`, targeting
  a handle or a `{story_path, state, world}` spec) is rendered through the same
  `composeRenderFrame` / `shot.RenderPNG` / `webShot` seams `render.*` use,
  written under the artifacts dir (`.artifacts/mcp-issues/<slug>/`), and
  referenced in the body **by relative path**. Asset *upload* isn't wired yet —
  the path is a stopgap reference, flagged with an HTML comment; `IssueResult`
  already carries the asset list so the upgrade is localized to the filer.
- **context** — with a `handle` and `include_trace` / `include_inspect`, the
  session's trace tail (the same `session.trace` returns) and inspect snapshot
  are folded into the body, so a gap report is reproducible by construction.
- **file** — the composed `{repo, title, body, labels}` goes to the injected
  [`IssueFiler`](../../internal/mcp/studio/issue_tools.go) seam. The
  `source-autonomous` label is always applied (first) so agent-filed issues are
  filterable. Production (`cmd/kitsoki`) shells to `gh issue create` (and
  best-effort `gh label create --force source-autonomous`); a test injects a fake
  that records the request and returns a canned URL — no network, no LLM. With no
  filer wired the tool returns a structured `ISSUE_UNAVAILABLE`. `issue.create`
  is allowed in `--read-only` mode (it mutates `.artifacts` + GitHub, not the
  story tree).

## Operator-ask — the MCP client *is* the operator

A driven turn can dispatch a kitsoki agent sub-agent (`host.agent.ask/decide/
task`) that asks the operator a clarifying question via `mcp__operator__ask`. In
a TUI/web run a live surface answers it; a plain headless session has no operator
and the sub-agent takes the "proceed on your own" path
([operator-ask.md](operator-ask.md)) — so the one story behaviour a headless
session can't reach is the interactive one.

The studio closes that gap by making the **driving MCP client the operator**. It
adds a third [`OperatorPrompter`](operator-ask.md#interactivity-gating--the-tool-is-attached-only-when-answerable)
implementation (alongside the TUI and web ones) whose surface is the MCP
connection, injected with `WithOperatorPrompter` before each driven turn — so
`mcp__operator__ask` auto-attaches to the dispatched sub-agent and the round-trip
runs end to end. Everything else is reused verbatim: the per-call socket, the
attach gate, the wire/answer schema, the bounded wait, and the
`operator.question.*` trace events. See
[operator-ask.md → MCP client surface](operator-ask.md#mcp-client-surface) for
the prompter's two transports (MCP elicitation primary, `session.answer`
suspend/resume fallback) and the no-LLM test posture.

## Attach config

The server drops into a client's `.mcp.json` the same way the bash/operator
servers attach
([`writeMCPConfigTempfile`](../../internal/host/agent_helpers.go) shape):

```json
{ "mcpServers": { "kitsoki": {
    "command": "kitsoki", "args": ["mcp", "--stories-dir", "<dir>"] } } }
```

`kitsoki mcp` flags: `--stories-dir` (workspace resolution root), `--db` (the
session store for driving handles), `--harness replay|live` (default `replay`),
`--workspace` (an initial authoring workspace bound on boot).

## Demo → QA

The studio surface ships a **tour-driven demo video** of an external coding agent
driving it end to end — `tools/mcp-demo/` (Claude Code TUI is the POC; codex/copilot
slot in by swapping a cassette). It generalizes the VS Code demo→QA pipeline
(`tools/vscode-kitsoki`) to a *terminal* surface: an xterm.js terminal **replays a
committed `termcast` cassette**, filmed through the shared demo machinery (camera
1600×900, `ChapterRecorder` sidecar, 25s floor) and gated by the `kitsoki-ui-qa`
review (`mcp-feature.md` / `mcp-scenarios.yaml`).

It is **no-LLM by construction** — the replay plays a static cassette and never
spawns a model (enforced by `tools/mcp-demo/scripts/lint-no-llm.mjs`). Authenticity
comes from *record once, replay forever*: a single **gated** live `claude` ↔
`kitsoki mcp` capture (`claude -p --output-format stream-json` →
`scripts/streamjson-to-termcast.mjs`) becomes the cassette, then replays for free,
identically, on every render. Two cassettes ship: the synthetic `claude-code`
(the CI-safe no-LLM default + QA fixture) and the captured-live `claude-code-live`
(the authentic Claude-Code session — driving a deterministic `barista` story via
direct `session.submit`, rendering the real `render.tui` frame). Both pass the
cast-agnostic `mcp-scenarios.yaml` gate (7/7, 0 visual issues).

```
make mcp-demo-fast   # no-LLM validate (CI-safe: lint + PACE=0 assert)
make mcp-demo        # watch-speed record → .artifacts/mcp-demo/claude-code.mp4
make mcp-qa          # vision QA (GATED: local claude CLI)
```

See [`tools/mcp-demo/README.md`](../../tools/mcp-demo/README.md).

## Non-goals

- **A second view renderer or a hand-rolled web view.** `render.*` capture the
  existing TUI and SPA renderers; readability work is
  [view-rendering-readability](../proposals/view-rendering-readability.md).
- **Cross-process / durable session sharing.** One stdio subprocess per client
  holds its handles in-process; co-driving across processes is the
  [hybrid-session-driving](../proposals/hybrid-session-driving.md) concern.
- **Replacing `kitsoki serve`** (the narrow per-app `transition` server) or the
  TUI/web operator surfaces. This is an additive, agent-facing control plane.
</content>
