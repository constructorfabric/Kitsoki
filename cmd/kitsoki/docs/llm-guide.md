# Kitsoki — LLM Operator Guide

This guide is written for an LLM that is driving the `kitsoki` CLI. Read it once
and you should be able to run, test, debug, and author kitsoki apps without
guessing at flags.

Companion docs (run `kitsoki docs <topic>`):

- `app-schema`     — authoritative reference for `app.yaml`
- `render-format`  — shape of the Markdown produced by `kitsoki render`
- `apply-proposal` — LLM guide for implementing a prose proposal against `app.yaml`
- `llm-guide`      — this document

## 1. What kitsoki is (one paragraph)

Kitsoki is a deterministic LLM orchestrator. An **application** is a YAML file
that declares a finite **state machine**: states, transitions, world variables,
and a finite alphabet of **intents** (the only actions the user can take). At
runtime, user free-text is sent to an LLM **harness** whose only job is to
translate the text into one valid intent + typed slots for the current state.
The state machine then applies the transition deterministically. The LLM never
picks what happens — it only picks *which legal intent* best matches the input.

Consequences you must internalise:

- The set of valid intents is **state-dependent**. A state declares what it
  accepts under `on: { <intent>: [...] }` (or inherits from the global
  `intents:` library).
- **Transitions are pure.** Guards (`when:`) are expr-lang expressions over
  `world` and `slots`; effects are a small declarative vocabulary (`set`,
  `increment`, `say`, `invoke`, `emit`).
- **Everything is replayable.** Given the same app + same recording (or
  same event log), kitsoki produces byte-identical views. This is the whole
  point.

## 2. Commands at a glance

```
kitsoki run     <app.yaml>  [--harness ...] [--trace ...] [--warp <basis>]  # interactive TUI session
kitsoki serve   <app.yaml>  [--db ...]                      # MCP server on stdio
kitsoki viz     <app.yaml>  [--out ...]                     # emit Graphviz DOT
kitsoki trace   <file.jsonl>                                # pretty-print a JSONL trace
kitsoki inspect <app.yaml>  --session-id <sid>              # JSON snapshot of a stored session
kitsoki turn    <app.yaml>  --state <S> (--intent | --input ...)  # one stateless turn → JSON
kitsoki replay  <session>                                   # (stub — not implemented)
kitsoki test flows   <app.yaml>  [--flows <glob>]           # Mode 2: deterministic, no LLM
kitsoki test intents <app.yaml>  [--harness live|static]    # Mode 1: intent pass-rate
kitsoki render  <app.yaml>       [-o <APP.md>]              # render Markdown docs from YAML
kitsoki docs    [topic]                                     # print embedded docs
kitsoki version
```

`--warp <path>` bootstraps a session directly into a primed mid-game
state from a YAML "warp basis" — same file the TUI's `/warp file:<path>`
slash command loads. Useful for smoke-testing an imported sub-story
without playing through the intro. See `docs/imports.md`
("Operator tooling: /warp and --warp") for the basis schema.

Every subcommand supports `--help`. Flags shown below are the ones you will
actually reach for.

## 3. Picking a harness (this is the most common stumble)

`kitsoki run --harness <type>` selects the LLM backend. If `--harness` is omitted,
kitsoki auto-selects using this exact precedence:

1. **`claude` binary on `PATH`** → `claude` harness (shells out to
   `claude -p --output-format json --model <model>`, uses existing Claude Code
   login — no `ANTHROPIC_API_KEY` needed).
2. **`ANTHROPIC_API_KEY` set** → `live` harness (direct Anthropic SDK with
   prompt caching, retry on 429/5xx).
3. Otherwise → `replay` — which will fail unless you also pass `--recording`.

| Harness     | Needs                        | Deterministic? | Cost    | When to use                                  |
|-------------|------------------------------|----------------|---------|----------------------------------------------|
| `claude`    | `claude` CLI on PATH         | No (LLM)       | Free*   | Default when you have Claude Code            |
| `live`      | `ANTHROPIC_API_KEY`          | No (LLM)       | Paid    | CI without `claude` CLI; explicit model pin  |
| `replay`    | recording via `--recording`     | **Yes**        | Zero    | Flow tests, demos, offline reproduction      |
| `recording` | `--recording` *or* API key      | Wraps above    | Varies  | Capture an LLM session to JSONL for replay   |

*Cost via your Claude Code plan.

`--claude-model` overrides the model for the `claude` harness. The default is
a Haiku model tuned for cheap intent routing — use `opus` for hard state
graphs or intentionally ambiguous user prompts.

`--record <path>` (with `--harness recording`) writes one JSONL object per turn
(`{state, input, intent, slots, ts, model, tokens_in, tokens_out}`). Convert to
a recording later to replay deterministically.

## 4. Typical workflows

### 4.1 Play a demo

```sh
kitsoki run testdata/apps/cloak/app.yaml
```

That's it — auto-selects the harness, opens the TUI, persists the session to
`$XDG_DATA_HOME/kitsoki/sessions.db`.

### 4.2 Run deterministically (no LLM, no cost)

```sh
kitsoki run testdata/apps/cloak/app.yaml \
    --harness replay \
    --recording testdata/apps/cloak/recording.yaml
```

### 4.3 Iterate on an app with full tracing

```sh
kitsoki run myapp.yaml --trace /tmp/myapp.jsonl --trace-pretty -
```

`--trace` writes JSONL. `--trace-pretty` writes human-readable trace; `-`
means stderr. Replay the JSONL later with `kitsoki trace /tmp/myapp.jsonl`.

### 4.4 CI: deterministic flow tests (every PR)

```sh
kitsoki test flows <app.yaml>
```

Runs every `*.yaml` under `<app-dir>/flows/` against a recording. Zero
LLM calls, fast, exits non-zero on regression. See §7 for fixture shape.

### 4.5 Expose to an external MCP client

```sh
kitsoki serve <app.yaml> --db sessions.db
```

Advertises a single `transition` tool over MCP stdio. Claude Desktop or any
MCP client calls `transition({session_id, intent, slots, confidence})` and
receives `{ok, state, view, menu, world}` or `{ok:false, error:{code,...}}`.
Without `--db`, sessions are in-memory (ephemeral).

### 4.6 Visualise the state graph

```sh
kitsoki viz <app.yaml>
dot -Tpng <appid>-viz.dot -o graph.png
```

## 5. Global flags that matter

`kitsoki run` flags you will reach for:

| Flag                    | Purpose                                                  |
|-------------------------|----------------------------------------------------------|
| `--harness <type>`      | See §3. `claude \| live \| replay \| recording`.           |
| `--claude-model <id>`   | Model for the `claude` harness (default: Haiku).         |
| `--recording <path>`       | Recording file. Required for `--harness replay`.         |
| `--record <path>`       | JSONL output for `--harness recording`.                  |
| `--db <path>`           | SQLite session DB (default `$XDG_DATA_HOME/kitsoki/`).     |
| `--trace <path>`        | JSONL trace. `-` = stderr.                               |
| `--trace-pretty <path>` | Human-readable trace in parallel. `-` = stderr.          |
| `--trace-level <lvl>`   | `debug \| info \| warn \| error` (default: debug).       |
| `--trace-redact`        | Redact API keys in trace output (default: true).         |

## 6. Understanding trace output

Trace event names are namespaced — pattern-match on the prefix:

- `turn.*`    — turn boundaries (`turn.start`, `turn.end`, `turn.commit`)
- `harness.*` — LLM calls (`harness.request`, `harness.response`, `harness.retry`)
- `machine.*` — state machine (`machine.guard`, `machine.transition`, `machine.effect`)
- `store.*`   — persistence (`store.append`, `store.snapshot`)
- `expr.*`    — expression evaluation failures

Every line is one JSON object: `{time, level, msg, session_id, turn, state_path, ...}`.
When you are debugging why the LLM picked the "wrong" intent, grep for
`harness.request` and `harness.response` — the prompt and the parsed tool call
are both logged.

`kitsoki trace <file.jsonl>` pretty-prints coloured by component. `NO_COLOR=1`
disables colour.

`turn.done` events also carry `view_rendered` — the full pre-Glamour view
text the user saw at the end of that turn. This makes a `--trace` JSONL a
complete after-the-fact transcript: when you need to know what the human
saw, grep for `turn.done` and read `view_rendered`.

## 6.1 Inspecting a live or stored session

```sh
kitsoki inspect <app.yaml> --session-id <sid> [--db <path>] [--last-turns N]
```

Read-only JSON snapshot of a session — does not lock it, so it is safe to
run alongside an active `kitsoki run`. Output shape:

```json
{
  "session_id": "...",
  "app_id": "...", "app_version": "...",
  "status": "active",
  "current_state": "terminal_result",
  "world": {...},
  "allowed_intents": ["..."],
  "last_view_bytes": 1842,
  "last_view": "Terminal › Result\n\n$ ...",
  "last_turns": [
    { "turn": 17, "input": "accept", "intent": "...",
      "from_state": "...", "to_state": "...",
      "outcome": "transitioned",
      "host_calls": ["host.run", "host.workspace_manager.get"] }
  ]
}
```

Use this when the human says "something just broke" — point it at the live
session id and read what kitsoki thinks is going on.

## 6.2 One-shot stateless turns (`kitsoki turn`)

```sh
kitsoki turn <app.yaml> --state <S> [--world @file.json] [--slots @file.json] \
           (--intent <name> | --input "<text>")
           [--harness replay --recording <path>]
```

Runs exactly one turn against an app definition without persisting anything
(no SQLite, no journey, no event log). Outputs JSON describing the turn:
`prev_state`, `next_state`, `intent`, `slots`, `world_before`, `world_after`,
`effects_applied`, `host_calls` (with args/data), `view_rendered`,
`allowed_intents`. On rejection, also `error_code` / `error_message` /
`guard_hint`.

Use cases:

- **Probe**: "what happens if I do X in state Y with world Z?" without
  spinning up a real session. `--intent` is the cheap path; `--input`
  routes through the harness (and bills accordingly).
- **CI sweep**: shell-loop over every `(state × intent)` pair to surface
  `INTENT_NOT_ALLOWED_IN_STATE` mismatches before they reach the user.
- **View regression check**: assert that for every state, a noop turn
  renders a non-empty view of bounded size.

```sh
# direct intent (no LLM)
kitsoki turn app.yaml --state cloakroom --intent hang_cloak

# routed input via replay (against a recording)
kitsoki turn app.yaml --state foyer \
    --input "go west" --harness replay --recording recording.yaml

# world override
kitsoki turn app.yaml --state cloakroom --intent look \
    --world '{"wearing_cloak": false}'
```

Both `--world` and `--slots` accept either inline JSON or `@path` to a
file. Schema defaults from `world:` are applied first; `--world` overrides
on top.

## 7. Test fixtures

### 7.1 Flow fixtures (Mode 2, deterministic)

Path: `<app-dir>/flows/*.yaml`. One fixture:

```yaml
test_kind: flow
app: ../app.yaml              # relative to this fixture
initial_state: foyer
initial_world:
  wearing_cloak: true

turns:
  - intent: { name: go, slots: { direction: south } }
    expect_state: bar
    expect_world: { wearing_cloak: true }
  - input: "hang up the cloak"            # resolved via the recording
    expect_state: cloakroom
    expect_world: { wearing_cloak: false }

expect_no_errors: true
```

A turn uses either `intent:` (skips the recording entirely — the
authoritative way to test state logic) or `input:` (requires a recording
and exercises the mapping). Mix freely.

A turn may also carry a `world_override:` map, applied to world before guard
evaluation on that turn. Use it to probe arcs that would otherwise require a
long preceding flow (e.g. cycle-budget feedback paths).

Per-turn assertions: `expect_state`, `expect_world` (partial match),
`expect_view_matches` (regex), plus a fixture-level `expect_no_errors`.

#### Background-job fixtures (orchestrator path)

When any of `host_handlers:`, `advance_clock:`, or `expect_inbox:` appear in a
fixture the test runner automatically switches to the orchestrator path, which
wires up a fake clock, an in-memory job store, and stub host handlers instead of
real infrastructure.

```yaml
test_kind: flow
app: ../app.yaml
initial_state: lobby
initial_world:
  result: ""
  last_job_id: ""

# Declare one stub per handler name used by the app.
host_handlers:
  host.run:
    data:           # returned as host.Result.Data on success
      stdout: "hello"
      exit: 0
    delay: "1s"     # virtual time to hold before completing

turns:
  - intent:
      name: enter
      slots: {}
    advance_clock: "2s"   # advance virtual time after RunIntent completes
    expect_state: running
    expect_world:
      result: "hello"
    expect_inbox:
      unread: 2
      severities: ["info", "success"]

expect_no_errors: true
```

**`host_handlers`** — map from handler name to stub behaviour:

| field | type | meaning |
|---|---|---|
| `data` | object | `host.Result.Data` returned on success |
| `delay` | duration | virtual time the stub holds before resolving |
| `error` | string | domain error string (non-fatal to job store) |
| `infra_error` | string | infrastructure error (job terminates as failed) |

**`advance_clock`** (per-turn) — duration string (`"2s"`, `"500ms"`) to advance
the fake clock after the intent fires. The runner drains the scheduler and the
orchestrator's session listener before evaluating assertions, so
`expect_world`/`expect_inbox` see the fully-resolved post-job state.

**`expect_inbox`** (per-turn) — asserts on the session notification inbox after
the turn (and any clock advance) completes:

| field | type | meaning |
|---|---|---|
| `unread` | int | exact unread notification count |
| `needs_attention` | int | exact needs-attention count |
| `severities` | []string | ordered list of severities for all unread notifications |

Note: a background job produces **two** notifications — an `info` notification
when the job is submitted and a `success` or `error` notification when it
terminates.

Run: `kitsoki test flows <app.yaml>`. Exit codes: `0` pass, `1` fail, `2` setup
error.

### 7.2 Intent fixtures (Mode 1, pass-rate)

Path: `<app-dir>/intents/*.yaml`. One fixture:

```yaml
test_kind: intents
app: cloak-of-darkness
state: foyer
defaults:
  runs: 5
  min_pass_rate: 0.8

fixtures:
  - id: go_south_plain
    intent: { name: go, slots: { direction: south } }
    inputs: ["go south", "head south", "s"]

  - id: nonsense
    expect_failure:
      any_of: [UNKNOWN_INTENT, INTENT_NOT_ALLOWED_IN_STATE]
    inputs: ["pet the goldfish", "recompile the kernel"]
```

Each `input` is run `runs` times; the fixture passes if ≥ `min_pass_rate` of
runs match the expected intent/slots (or one of the expected error codes).

Run: `kitsoki test intents <app.yaml> --harness static` (seeded from a
recording, deterministic) or `--harness live` (real LLM, costs money). Default is
`static` unless `ANTHROPIC_API_KEY` is set.

## 8. Recordings

A **recording** is a lookup table `(state, input) → {intent, slots}` used by
the `replay` harness and the `static` intent harness.

```yaml
kind: recording
app_id: cloak-of-darkness
app_version: 0.1.0
generated_at: 2026-04-22T10:00:00Z
generator: hand
min_confidence: 0.0
entries:
  - state: foyer
    input: "go south"
    intent: { name: go, slots: { direction: south } }
    confidence: 1.0
    majority_of: 1
```

Lookup is exact first, then case-insensitive. If nothing matches, `replay`
returns an unknown-intent error to the machine.

To bootstrap a recording from real LLM traffic:

```sh
kitsoki run myapp.yaml --harness recording --record /tmp/rec.jsonl
# play through desired inputs…
# (JSONL → recording YAML conversion is the intended workflow)
```

Or emit from an intent-test run:

```sh
kitsoki test intents myapp.yaml --harness live --emit-recording recording.yaml
```

## 9. Authoring apps — survival guide

**YAML is the source of truth.** `app.yaml` is the only file the engine
reads. For reviewability and LLM-assisted editing:

- `kitsoki render <app.yaml> -o APP.md` produces a human-readable Markdown
  document — overview, Mermaid state diagram, transition tables. One-way:
  the Markdown never feeds back into the engine. See `kitsoki docs
  render-format` for what's in the output.
- To change an app, the human writes a prose proposal referencing engine
  names (rooms, intents, world vars). An LLM implements the proposal
  against `app.yaml` directly, guided by `kitsoki docs apply-proposal`.
  The human re-runs `kitsoki render` to refresh the docs.
- **In-TUI Edit mode.** While playing in `kitsoki run`, press `Esc` and pick
  **Edit mode** to author a change without leaving the session. Type a
  free-text proposal; the TUI snapshots the story directory, runs
  `claude -p` (with full Read/Edit/Write tool access) inside the shadow
  copy, walks the result vs. the original to build a unified diff, and
  shows it for review. On `[a]pply` the changed files are copied back
  into place and the orchestrator hot-reloads. The scope is the **whole
  story directory** — Claude can edit `app.yaml`, included `rooms/*.yaml`
  fragments, `prompts/*.md` templates, and anything under `scripts/`,
  not just the manifest. If the user's current state still exists in
  the new graph, it is preserved; otherwise a notice tells them to
  restart. Requires the `claude` binary on `PATH`. The harness's cached
  system prompt is rebuilt as part of the reload, so the LLM router
  sees new states and intents on the very next turn.

See `kitsoki docs app-schema` for the complete YAML reference. The shortest
possible mental model:

```yaml
app: { id: myapp, version: 0.1.0, title: "My App" }

world:                         # typed, persisted variables
  counter: { type: int, default: 0 }

intents:                       # global intent library (state-scoped ok too)
  increment:
    description: "Add one to the counter."
    examples: ["add one", "increment", "++"]

root: main                     # the initial state

states:
  main:
    view: |
      Counter is {{ world.counter }}.
    on:
      increment:
        - target: main
          effects:
            - increment: { counter: 1 }
```

What to remember when writing apps:

- **Every `invoke:` host must be in the top-level `hosts:` allow-list.** The
  loader rejects `invoke: host.run` unless `hosts: [host.run]` is declared.
- **State references are dot-separated paths.** `bar.dark` means state `dark`
  nested inside compound state `bar`. Authors can use slash notation
  (`../../foyer`) in YAML; the loader resolves it.
- **Guards see `world.*`, `slots.*`, `$host_error` (when in `on_error`).**
  Bad expressions fail at runtime, not load.
- **`relevant_world: [key]` pins world keys to the TUI location indicator.**
  Those keys must exist in the global `world` schema.
- **Effects fire in order.** `set` → `increment` → `say` → `invoke` → `emit`
  is the convention inside one effect block.
- **`default: true` on a transition = catch-all.** Put it last in the list for
  an intent; the first matching guard wins.
- **Background jobs** (`background: true` on an `invoke:` effect) dispatch the
  handler asynchronously and fire `on_complete:` effects in a later synthetic
  turn. See `kitsoki docs app-schema §Background jobs` for the lifecycle,
  injected world variables (`last_job_id`, `last_job_status`, `last_job_result`),
  the same-turn race, and the mid-flight clarification flow.

## 10. Error codes you will see (and how to react)

From the intent validation pipeline — these appear in trace output and in
`kitsoki serve`'s MCP error envelope:

| Code                           | What it means                                              |
|--------------------------------|------------------------------------------------------------|
| `UNKNOWN_INTENT`               | LLM returned an intent name not in the app's library.      |
| `INTENT_NOT_ALLOWED_IN_STATE`  | Intent exists but this state does not accept it.           |
| `SLOT_MISSING_REQUIRED`        | Required slot not provided.                                |
| `SLOT_TYPE_MISMATCH`           | Slot value cannot be coerced to declared type.             |
| `SLOT_NOT_IN_ENUM`             | Slot value outside its `values:` enum.                     |
| `GUARD_FAILED`                 | No `when:` matched; catch-all absent.                      |
| `HOST_NOT_ALLOWED`             | `invoke: host.x` but `host.x` not in app's `hosts:` list.  |
| `HOST_ERROR`                   | Host handler returned an error payload.                    |

When you are debugging an intent-routing failure, the first question is
*which* error code came back: `UNKNOWN_INTENT` = the LLM made up a name (check
fixtures, recordings, system prompt); `INTENT_NOT_ALLOWED_IN_STATE` = your state
is missing an `on:` binding.

## 11. Built-in host handlers

Apps invoke host handlers through effects:

```yaml
effects:
  - invoke: host.run
    with: { cmd: "git status", cwd: "{{ world.workspace_root }}" }
    bind: { last_output: stdout, last_exit: exit_code }
    on_error: error_room
```

Built-ins (`internal/host/`):

- **`host.run`** — run a shell command. Args `cmd` (required), `cwd`.
  Returns `{stdout, exit_code, ok}`.
- **`host.oracle.ask`** — one-shot Claude call driven by a prompt template
  file. Args `prompt_path` (required, relative paths resolve against the
  app dir), `working_dir` (optional, defaults to the prompt's directory),
  and any other keys you add — those become `{{ args.X }}` inside the
  prompt. Returns `{stdout, exit_code, ok}`. See "LLM-backed effects"
  below for the common patterns.
- **`host.oracle.talk`** — conversational Claude session via `claude -p
  --session-id`. Args:
    - `question` (string, required)
    - `chat_id` (string, optional) — when set AND a `ChatStore` is wired,
      operates in **chat-aware mode**: appends the user message + assistant
      reply to the persistent transcript and reuses the chat row's
      `claude_session_id` across turns. Acquires the per-chat singleton
      lock for the duration of the turn. Returns
      `{answer, session_id, chat_id, claude_session_id, transcript_seq}`.
    - `session_id` (string, optional, legacy non-chat path) — round-tripped
      so the caller can persist it in world and resume the session.
      Ignored when `chat_id` is set.
    - `working_dir` (string, optional)
  Use this when the user is having a multi-turn conversation; use
  `host.oracle.ask` when you want a one-shot response derived from a named
  prompt file.
- **`host.oracle.ask_with_mcp`** — one-shot Claude call with optional MCP
  servers (typed-JSON validators, etc.). Same shape as `host.oracle.ask`
  plus an `mcp_servers:` map. Accepts an optional `chat_id:` arg with the
  same chat-aware semantics as `host.oracle.talk` (transcript persistence
  + Claude session reuse + singleton lock).
- **`host.chat.resolve`** — get-or-create a chat for `(app, room,
  scope_key)`. Args `app`, `room`, `scope_key` (optional), `title`
  (optional). Returns `{chat_id, title, status, is_new}`. Idempotent —
  cheap to call from `on_enter:` so a room always knows its chat.
- **`host.chat.list`** — list chats matching `(app, room, scope_key)`.
  Returns `{rendered, chats, count}` where `rendered` is a Markdown
  preformatted block suitable for inlining into a `view:`.
- **`host.chat.transcript`** — fetch a chat's transcript. Args `chat_id`,
  `since_seq` (optional), `max_turns` (optional, default 20). Returns
  `{rendered, messages, latest_seq, title}`.
- **`host.chat.fork`** — fork a chat: copy messages, set `parent_chat_id`,
  clear `claude_session_id` so the next turn starts a fresh Claude
  session. Args `chat_id`, `title` (optional).
- **`host.chat.archive`** — soft-delete a chat (status="archived"); the
  archived chat is hidden from `list` unless `--all-status` is passed.
- **`host.workspace_manager.get`** — shell out to a `workspace-manager` CLI
  and parse JSON. Args `workspace_id`. Returns the parsed object.

To call these, the app must declare them in its top-level `hosts:` list.

### 11.1 LLM-backed effects (`host.oracle.ask`)

`host.oracle.ask` is the primitive behind "draft", "refine", and "repair"
style effects. The shape is:

1. You author a prompt template file on disk (conventionally under
   `<app-dir>/prompts/<name>.md`).
2. The template uses `{{ args.X }}` placeholders — those map 1:1 to the
   extra keys you pass in the effect's `with:` block.
3. At runtime, kitsoki renders the prompt against those args and pipes it
   to `claude -p --permission-mode bypassPermissions`. Claude's final
   text message is returned as `stdout` and can be bound back into the
   world.

Example — repair a failed shell command:

```yaml
# prompts/shell_repair.md
Original command:
{{ args.failed_cmd }}

Exit: {{ args.exit_code }}
Error:
{{ args.last_error }}

Produce the corrected command. Your final message is the literal
replacement command and nothing else.
```

```yaml
# rooms/terminal.yaml — in terminal_error state
fix:
  - target: terminal_reviewing
    effects:
      - invoke: host.oracle.ask
        with:
          prompt_path: "prompts/shell_repair.md"
          failed_cmd: "{{ world.proposal_cmd }}"
          last_error: "{{ world.last_error }}"
          exit_code: 1
        bind:
          proposal_cmd: stdout
        on_error: terminal_error
      - set: { proposal_status: "reviewing" }
```

Notes:

- **Where are prompts found?** Relative `prompt_path` values resolve
  against the directory containing `app.yaml` (set internally as
  `KITSOKI_APP_DIR`). Absolute paths are used as-is.
- **What can the prompt reference?** Only `{{ args.X }}`. To surface a
  world var or slot, add it explicitly to `with:` — e.g.
  `failed_cmd: "{{ world.proposal_cmd }}"`. This keeps the prompt
  contract local to the `with:` block.
- **Tool access.** The spawned `claude` runs with
  `--permission-mode bypassPermissions`, so Bash/Read/Grep/Glob/Web
  tools are available. Your prompt should tell Claude to investigate
  (verify flags via `--help`, confirm paths with `ls`) before emitting
  the final answer.
- **Output contract.** The handler strips one trailing newline from
  stdout. Everything else is yours to define in the prompt. A common
  pattern is "final message is the literal result, no prose, no fences"
  so binding `stdout` into a world var gives a clean value.
- **Failure handling.** Non-zero exit populates `exit_code`, sets `ok`
  to false, and produces `Result.Error` (so `on_error:` fires). Missing
  binary returns a Result.Error with the install hint; missing prompt
  file returns Result.Error with the resolved path.

## 12. Common pitfalls

- **"why did it pick the wrong intent?"** — `--trace` it, find
  `harness.response`, read the parsed tool call. If the LLM is not even being
  given the right intents, check `state_path` in `harness.request` and the
  state's `on:` map.
- **"replay harness says intent not found"** — your recording does not have
  an entry for that `(state, input)` pair. Add one, or use `--harness claude/live`
  for that run.
- **"host invoke refused"** — add the host name to the app's top-level
  `hosts:` list.
- **"state transition rejected"** — the LLM returned a valid intent but your
  state's `on:` map does not bind it. Either add the binding or route it via
  a parent compound state.
- **"DB locked" error** — only one writer at a time. Do not run two
  `kitsoki run` invocations against the same `--db` concurrently.
- **Session store gotcha** — once a session is marked `completed` or
  `abandoned`, it refuses further appends (`ErrSessionClosed`). Start a new
  session.

## 13. File layout cheat-sheet

```
<app-dir>/
  app.yaml                 # the app definition (required)
  recording.yaml           # used by --harness replay + `test intents --harness static`
  flows/*.yaml             # Mode 2 flow fixtures
  intents/*.yaml           # Mode 1 intent fixtures
```

Default globs in test commands assume this layout. Override with `--flows` /
`--intents` / `--recording`.

## 14. Environment variables

| Variable            | Effect                                                        |
|---------------------|---------------------------------------------------------------|
| `ANTHROPIC_API_KEY` | Enables `--harness live`; also flips intent-test default.     |
| `XDG_DATA_HOME`     | Location of default session DB.                               |
| `NO_COLOR`          | Disables colour in `kitsoki trace` pretty-printer.              |
| `TERM=dumb`         | Also disables colour.                                         |

## 15. One-liner reference

```sh
# Discover an app's shape without reading the YAML:
kitsoki viz app.yaml && dot -Tpng $(basename app.yaml .yaml)-viz.dot -o g.png

# Print all the docs an LLM needs:
kitsoki docs llm-guide
kitsoki docs app-schema

# Test, then ship:
kitsoki test flows   app.yaml && \
kitsoki test intents app.yaml --harness static && \
echo OK

# Watch a session stream:
kitsoki run app.yaml --trace-pretty -
```
