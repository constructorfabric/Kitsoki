# Hally — LLM Operator Guide

This guide is written for an LLM that is driving the `hally` CLI. Read it once
and you should be able to run, test, debug, and author hally apps without
guessing at flags.

Companion docs (run `hally docs <topic>`):

- `app-schema`     — authoritative reference for `app.yaml`
- `render-format`  — shape of the Markdown produced by `hally render`
- `apply-proposal` — LLM guide for implementing a prose proposal against `app.yaml`
- `llm-guide`      — this document

## 1. What hally is (one paragraph)

Hally is a deterministic LLM orchestrator. An **application** is a YAML file
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
- **Everything is replayable.** Given the same app + same oracle (or same
  event log), hally produces byte-identical views. This is the whole point.

## 2. Commands at a glance

```
hally run     <app.yaml>  [--harness ...] [--trace ...]   # interactive TUI session
hally serve   <app.yaml>  [--db ...]                      # MCP server on stdio
hally viz     <app.yaml>  [--out ...]                     # emit Graphviz DOT
hally trace   <file.jsonl>                                # pretty-print a JSONL trace
hally inspect <app.yaml>  --session-id <sid>              # JSON snapshot of a stored session
hally turn    <app.yaml>  --state <S> (--intent | --input ...)  # one stateless turn → JSON
hally replay  <session>                                   # (stub — not implemented)
hally test flows   <app.yaml>  [--flows <glob>]           # Mode 2: deterministic, no LLM
hally test intents <app.yaml>  [--harness live|static]    # Mode 1: intent pass-rate
hally render  <app.yaml>       [-o <APP.md>]              # render Markdown docs from YAML
hally docs    [topic]                                     # print embedded docs
hally version
```

Every subcommand supports `--help`. Flags shown below are the ones you will
actually reach for.

## 3. Picking a harness (this is the most common stumble)

`hally run --harness <type>` selects the LLM backend. If `--harness` is omitted,
hally auto-selects using this exact precedence:

1. **`claude` binary on `PATH`** → `claude` harness (shells out to
   `claude -p --output-format json --model <model>`, uses existing Claude Code
   login — no `ANTHROPIC_API_KEY` needed).
2. **`ANTHROPIC_API_KEY` set** → `live` harness (direct Anthropic SDK with
   prompt caching, retry on 429/5xx).
3. Otherwise → `replay` — which will fail unless you also pass `--oracle`.

| Harness     | Needs                        | Deterministic? | Cost    | When to use                                  |
|-------------|------------------------------|----------------|---------|----------------------------------------------|
| `claude`    | `claude` CLI on PATH         | No (LLM)       | Free*   | Default when you have Claude Code            |
| `live`      | `ANTHROPIC_API_KEY`          | No (LLM)       | Paid    | CI without `claude` CLI; explicit model pin  |
| `replay`    | `--oracle oracle.yaml`       | **Yes**        | Zero    | Flow tests, demos, offline reproduction      |
| `recording` | `--oracle` *or* API key      | Wraps above    | Varies  | Capture an LLM session to JSONL for replay   |

*Cost via your Claude Code plan.

`--claude-model` overrides the model for the `claude` harness. The default is
a Haiku model tuned for cheap intent routing — use `opus` for hard state
graphs or intentionally ambiguous user prompts.

`--record <path>` (with `--harness recording`) writes one JSONL object per turn
(`{state, input, intent, slots, ts, model, tokens_in, tokens_out}`). Convert to
an oracle later to replay deterministically.

## 4. Typical workflows

### 4.1 Play a demo

```sh
hally run testdata/apps/cloak/app.yaml
```

That's it — auto-selects the harness, opens the TUI, persists the session to
`$XDG_DATA_HOME/hally/sessions.db`.

### 4.2 Run deterministically (no LLM, no cost)

```sh
hally run testdata/apps/cloak/app.yaml \
    --harness replay \
    --oracle testdata/apps/cloak/oracle.yaml
```

### 4.3 Iterate on an app with full tracing

```sh
hally run myapp.yaml --trace /tmp/myapp.jsonl --trace-pretty -
```

`--trace` writes JSONL. `--trace-pretty` writes human-readable trace; `-`
means stderr. Replay the JSONL later with `hally trace /tmp/myapp.jsonl`.

### 4.4 CI: deterministic flow tests (every PR)

```sh
hally test flows <app.yaml>
```

Runs every `*.yaml` under `<app-dir>/flows/` against a replay oracle. Zero
LLM calls, fast, exits non-zero on regression. See §7 for fixture shape.

### 4.5 Expose to an external MCP client

```sh
hally serve <app.yaml> --db sessions.db
```

Advertises a single `transition` tool over MCP stdio. Claude Desktop or any
MCP client calls `transition({session_id, intent, slots, confidence})` and
receives `{ok, state, view, menu, world}` or `{ok:false, error:{code,...}}`.
Without `--db`, sessions are in-memory (ephemeral).

### 4.6 Visualise the state graph

```sh
hally viz <app.yaml>
dot -Tpng <appid>-viz.dot -o graph.png
```

## 5. Global flags that matter

`hally run` flags you will reach for:

| Flag                    | Purpose                                                  |
|-------------------------|----------------------------------------------------------|
| `--harness <type>`      | See §3. `claude \| live \| replay \| recording`.           |
| `--claude-model <id>`   | Model for the `claude` harness (default: Haiku).         |
| `--oracle <path>`       | Required for `--harness replay`.                         |
| `--record <path>`       | JSONL output for `--harness recording`.                  |
| `--db <path>`           | SQLite session DB (default `$XDG_DATA_HOME/hally/`).     |
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

`hally trace <file.jsonl>` pretty-prints coloured by component. `NO_COLOR=1`
disables colour.

`turn.done` events also carry `view_rendered` — the full pre-Glamour view
text the user saw at the end of that turn. This makes a `--trace` JSONL a
complete after-the-fact transcript: when you need to know what the human
saw, grep for `turn.done` and read `view_rendered`.

## 6.1 Inspecting a live or stored session

```sh
hally inspect <app.yaml> --session-id <sid> [--db <path>] [--last-turns N]
```

Read-only JSON snapshot of a session — does not lock it, so it is safe to
run alongside an active `hally run`. Output shape:

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
session id and read what hally thinks is going on.

## 6.2 One-shot stateless turns (`hally turn`)

```sh
hally turn <app.yaml> --state <S> [--world @file.json] [--slots @file.json] \
           (--intent <name> | --input "<text>")
           [--harness replay --oracle <path>]
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
hally turn app.yaml --state cloakroom --intent hang_cloak

# routed input via replay oracle
hally turn app.yaml --state foyer \
    --input "go west" --harness replay --oracle oracle.yaml

# world override
hally turn app.yaml --state cloakroom --intent look \
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
  - input: "hang up the cloak"            # resolved via oracle
    expect_state: cloakroom
    expect_world: { wearing_cloak: false }

expect_no_errors: true
```

A turn uses either `intent:` (skips the oracle entirely — the authoritative
way to test state logic) or `input:` (requires the oracle and exercises the
mapping). Mix freely.

Per-turn assertions: `expect_state`, `expect_world` (partial match),
`expect_view_matches` (regex), plus a fixture-level `expect_no_errors`.

Run: `hally test flows <app.yaml>`. Exit codes: `0` pass, `1` fail, `2` setup
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

Run: `hally test intents <app.yaml> --harness static` (seeded from oracle,
deterministic) or `--harness live` (real LLM, costs money). Default is
`static` unless `ANTHROPIC_API_KEY` is set.

## 8. Oracle files

An oracle is a lookup table `(state, input) → {intent, slots}` used by the
replay harness and the static intent harness.

```yaml
kind: oracle
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

To bootstrap an oracle from real LLM traffic:

```sh
hally run myapp.yaml --harness recording --record /tmp/rec.jsonl
# play through desired inputs…
# (stage-7 conversion JSONL → oracle.yaml is the intended workflow)
```

Or emit from an intent-test run:

```sh
hally test intents myapp.yaml --harness live --emit-oracle oracle.yaml
```

## 9. Authoring apps — survival guide

**YAML is the source of truth.** `app.yaml` is the only file the engine
reads. For reviewability and LLM-assisted editing:

- `hally render <app.yaml> -o APP.md` produces a human-readable Markdown
  document — overview, Mermaid state diagram, transition tables. One-way:
  the Markdown never feeds back into the engine. See `hally docs
  render-format` for what's in the output.
- To change an app, the human writes a prose proposal referencing engine
  names (rooms, intents, world vars). An LLM implements the proposal
  against `app.yaml` directly, guided by `hally docs apply-proposal`.
  The human re-runs `hally render` to refresh the docs.
- **In-TUI Edit mode.** While playing in `hally run`, press `Esc` and pick
  **Edit mode** to author a change without leaving the session. Type a
  free-text proposal; the TUI shells out to `claude -p` (same prompt
  surface as `apply-proposal`), shows a unified diff for review, and
  on `[a]pply` writes the new YAML to `app.yaml` and hot-reloads the
  orchestrator. If the user's current state still exists in the new
  graph, it is preserved; otherwise a notice tells them to restart.
  Requires the `claude` binary on `PATH`. The harness's cached system
  prompt is rebuilt as part of the reload, so the LLM router sees the
  new states and intents on the very next turn.

See `hally docs app-schema` for the complete YAML reference. The shortest
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

## 10. Error codes you will see (and how to react)

From the intent validation pipeline — these appear in trace output and in
`hally serve`'s MCP error envelope:

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
fixtures, oracles, system prompt); `INTENT_NOT_ALLOWED_IN_STATE` = your state
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
  --session-id`. Args `question` (required), `session_id` (optional;
  round-tripped so the caller can persist it in world and resume the
  session), `working_dir`. Returns `{answer, session_id}`. Use this when
  the user is having a multi-turn conversation; use `host.oracle.ask`
  when you want a one-shot response derived from a named prompt file.
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
3. At runtime, hally renders the prompt against those args and pipes it
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
  `HALLY_APP_DIR`). Absolute paths are used as-is.
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
- **"replay harness says intent not found"** — your oracle does not have an
  entry for that `(state, input)` pair. Add one, or use `--harness claude/live`
  for that run.
- **"host invoke refused"** — add the host name to the app's top-level
  `hosts:` list.
- **"state transition rejected"** — the LLM returned a valid intent but your
  state's `on:` map does not bind it. Either add the binding or route it via
  a parent compound state.
- **"DB locked" error** — only one writer at a time. Do not run two
  `hally run` invocations against the same `--db` concurrently.
- **Session store gotcha** — once a session is marked `completed` or
  `abandoned`, it refuses further appends (`ErrSessionClosed`). Start a new
  session.

## 13. File layout cheat-sheet

```
<app-dir>/
  app.yaml                 # the app definition (required)
  oracle.yaml              # optional; used by --harness replay + `test intents --harness static`
  flows/*.yaml             # Mode 2 flow fixtures
  intents/*.yaml           # Mode 1 intent fixtures
```

Default globs in test commands assume this layout. Override with `--flows` /
`--intents` / `--oracle`.

## 14. Environment variables

| Variable            | Effect                                                        |
|---------------------|---------------------------------------------------------------|
| `ANTHROPIC_API_KEY` | Enables `--harness live`; also flips intent-test default.     |
| `XDG_DATA_HOME`     | Location of default session DB.                               |
| `NO_COLOR`          | Disables colour in `hally trace` pretty-printer.              |
| `TERM=dumb`         | Also disables colour.                                         |

## 15. One-liner reference

```sh
# Discover an app's shape without reading the YAML:
hally viz app.yaml && dot -Tpng $(basename app.yaml .yaml)-viz.dot -o g.png

# Print all the docs an LLM needs:
hally docs llm-guide
hally docs app-schema

# Test, then ship:
hally test flows   app.yaml && \
hally test intents app.yaml --harness static && \
echo OK

# Watch a session stream:
hally run app.yaml --trace-pretty -
```
