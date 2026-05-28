# Developer Guide

For people working *on* kitsoki — not just authoring apps that run on it.
If you want to write a kitsoki app, see [`authoring.md`](authoring.md) and
the embedded `kitsoki docs app-schema`.

---

## 1. Repository tour

```
kitsoki/
├── cmd/kitsoki/           single CLI entrypoint, one Go file per subcommand
├── internal/              all platform packages (see architecture.md §3)
├── docs/                  this directory — narrative documentation
├── testdata/apps/         runnable example apps (cloak, dev-story, …)
├── demo/                  VHS tapes and recorded GIFs
├── ideas.md               working notes / backlog
└── README.md              top-level entry point
```

Everything under `internal/` is private to the binary by Go's
visibility rules; that's deliberate. Stable user surfaces are the
`kitsoki` CLI, the `app.yaml` schema, the MCP `transition` tool,
and the JSONL trace format.

---

## 2. Toolchain

| Requirement | Why |
|---|---|
| Go 1.25+ | Generics in the orchestrator; `slog` everywhere |
| `claude` CLI | Default LLM harness (recommended; optional if you set `ANTHROPIC_API_KEY`) |
| SQLite | Embedded via `modernc.org/sqlite` (pure Go; no system library needed) |
| `dot` | Optional; only for rendering DOT output of `kitsoki viz` |
| `vhs` | Optional; only for re-recording the demo GIF |

No CGO, no managed services, no Docker required to develop.

---

## 3. Build, vet, test

```sh
go build ./...          # build every package
go build -o kitsoki ./cmd/kitsoki   # build the CLI
go vet ./...            # vet every package
go test ./...           # run every test
go test -race ./...     # plus the race detector (recommended in CI)
go mod tidy             # keep go.mod / go.sum honest
```

The full test suite is fast — under 10 seconds on a modern laptop —
because almost everything that matters runs against an in-memory
SQLite or a fake clock.

---

## 4. Run kitsoki locally

```sh
# Default: claude CLI harness if found, else live SDK if API key, else replay
./kitsoki run testdata/apps/cloak/app.yaml

# Force the deterministic replay path (no LLM at all)
./kitsoki run testdata/apps/cloak/app.yaml \
    --harness replay \
    --recording testdata/apps/cloak/recording.yaml

# A JSONL trace is always written automatically to .kitsoki/sessions/
# View it with:
kitsoki trace .kitsoki/sessions/20260115T090000Z-cloak.jsonl
# or:
jq '.kind,.state_path' .kitsoki/sessions/20260115T090000Z-cloak.jsonl

# One-shot headless turn (no TUI, no LLM):
./kitsoki turn --app testdata/apps/cloak/app.yaml \
    --trace /tmp/cloak-turn.jsonl --intent go --slot direction=west

# Visualise the state graph
./kitsoki viz testdata/apps/cloak/app.yaml | dot -Tpng -o /tmp/cloak.png
./kitsoki viz testdata/apps/cloak/app.yaml --mermaid > /tmp/cloak.mmd
```

For the embedded LLM-operator manual (a complete tour of every flag and
workflow), `kitsoki docs llm-guide`.

---

## 5. Workflows for common changes

### 5.1 Adding a new intent or transition

Almost always purely a YAML edit — no code change.

1. Declare the intent in the app's `intents:` block (or a state's local
   `intents:` map). Add slot types if it carries arguments.
2. Bind it from one or more states' `on:` map. Add `when:` guards or a
   `default: true` catch-all as needed.
3. Run `./kitsoki inspect testdata/apps/<app>/app.yaml --session-id <id>`
   on a stored session to check the new intent appears in
   `allowed_intents`.
4. Add a flow fixture under `<app>/flows/` exercising the new path.
5. `./kitsoki test flows testdata/apps/<app>/app.yaml`.

If you want the new intent to be reachable via natural language and not
just structured `intent:` blocks, also add a Mode 1 fixture under
`<app>/intents/` and (for replay) a recording entry.

### 5.2 Adding a new built-in host handler

Code changes; live in `internal/host/`.

1. Implement a `host.Handler` (`func(ctx, args, store) (Result, error)`)
   in a new or existing file under `internal/host/`.
2. Register it inside `Registry.RegisterBuiltins` in
   `internal/host/handlers.go`. The name **must** start with `host.`
   and be dot-separated (`host.git.commit`, `host.k8s.apply`, …).
3. Document the input/output contract in the handler's Go doc comment
   *and* in [`hosts.md`](hosts.md). The contract is part of the public
   surface — apps depend on the field names in `with:` and `bind:`.
4. Add a unit test in `internal/host/<name>_test.go`. Tests should
   cover at least: happy path, missing required arg, on_error path.
5. If the handler can be long-running, gate it behind `background:
   true` in the example so authors aren't surprised when it blocks the
   turn.

### 5.3 Adding a new transport

Transports are output adapters. See `internal/transport/`.

1. Implement the `Transport` interface (`ID`, `Post`, `Close`) in a new
   file under `internal/transport/`.
2. Wire it into the orchestrator's transport registry in
   `cmd/kitsoki/main.go` (read the relevant config from environment or
   flags; transports are registered at process start).
3. Convert kitsoki's intermediate Markdown to the surface's native markup
   if needed. The Jira transport's `jira_markdown.go` is a worked
   example of "Markdown in, Jira wiki out".
4. Add a session-key parser if your transport invents a new
   `(transport:thread)` shape — see `internal/store/external_keys.go`.
5. End-to-end test: drive a session with `kitsoki session continue
   --key <transport>:<thread>` and assert the transport's `Post` was
   called with the expected message.

### 5.4 Adding a new CLI subcommand

1. Drop a new file under `cmd/kitsoki/` named after the subcommand.
2. Build a `cobra.Command` and register it in `cmd/kitsoki/main.go`'s
   root command.
3. If the command needs read-only session access, use `kitsoki inspect`
   as the worked example — it deliberately does **not** acquire the
   writer lock.
4. If the command must mutate, acquire the writer lock and respect
   `EX_TEMPFAIL` (75) on conflict, like `kitsoki session continue`.

### 5.5 Changing the event log

The `EventKind` enum in `internal/store/event.go` is part of the
implicit public surface — flow tests, the trace pretty-printer, and
`kitsoki inspect` all key off it.

- Adding a new kind: append to the iota, never insert in the middle.
  Stored databases are forward-compatible.
- Removing or renumbering a kind: don't. Add a deprecation comment and
  let it ride.
- Adding a new payload field on an existing kind: safe; payloads are
  JSON.

---

## 6. Debugging

### 6.1 The trace is your transcript

`kitsoki run` always writes a JSONL trace into the nearest `.kitsoki/sessions/`
folder (walking up from cwd; falling back to cwd if none exists), named
`<utc-timestamp>-<app-id>.jsonl`. No extra flag is needed. Add
`.kitsoki/sessions/` (but not the whole `.kitsoki/` directory) to your
project's `.gitignore`; this repo's `.gitignore` already does so.

For `kitsoki turn --trace <path>`, the trace is at the path you supply.

**Viewing a trace:**

```sh
kitsoki trace .kitsoki/sessions/20260115T090000Z-cloak.jsonl   # pretty-print
jq 'select(.kind=="machine.state_entered") | .state_path' trace.jsonl  # jq
```

**Trace format:** one JSON object per line in the `store.Event` shape
(`turn`, `seq`, `ts`, `kind`, `state_path`, `payload`). The full schema and
event-kind vocabulary are in [`docs/trace-format.md`](trace-format.md).

**Path schemes:**

| Entry point          | Path                                                         |
|----------------------|--------------------------------------------------------------|
| `kitsoki run` (TUI)  | `~/.kitsoki/sessions/<app>/<sha8>-tui-<sid>.jsonl`          |
| `kitsoki turn`       | Caller-supplied `--trace <path>`; explicit                  |
| `session continue`   | `~/.kitsoki/sessions/<app>/<sha8>-<slug>.jsonl`             |

The TUI uses the home-anchored scheme so resumed sessions always append to the
same file. `kitsoki run` for one-off interactive sessions uses the repo-anchored
scheme so traces land next to the story sources.

**Diagnostic events to look for:**

- `oracle.ask.start` / `oracle.tool_call` — what the LLM was given and what
  came back.
- `machine.guard_rejected` — every `when:` evaluation that failed.
- `harness.called` / `harness.returned` — host call boundaries with args and
  result data.
- `machine.state_exited` / `machine.state_entered` — state transition with
  the exact FROM and TO paths stamped per-event.
- `turn.end` — carries the rendered view, so the trace is a complete transcript.

### 6.2 `kitsoki inspect`

A read-only JSON snapshot of a stored session. Safe to run alongside an
active `kitsoki run`:

```sh
kitsoki inspect path/to/app.yaml --session-id <id> [--last-turns 5]
```

Use it when the human says "something just broke" — point at the live
session and read what kitsoki thinks is going on (current state, world,
allowed intents, last view, last N turn summaries).

### 6.3 `kitsoki turn`

One-shot stateless turn against an app, no SQLite, no journey. Great
for "what would happen if I did X in state Y with world Z?":

```sh
kitsoki turn app.yaml --state cloakroom --intent hang_cloak
kitsoki turn app.yaml --state foyer \
    --input "head south" --harness replay --recording recording.yaml
kitsoki turn app.yaml --state cloakroom --intent look \
    --world '{"wearing_cloak": false}'
```

Output is a JSON diff (prev/next state, intent, slots, effects,
host calls, view) and never touches the session DB.

### 6.4 Replaying a stored session

```sh
kitsoki replay <session-id> [--db <path>]
```

Replays the event log into a fresh state machine and diffs every
checkpoint against the recorded snapshot. Used to catch silent
regressions in the machine after a code change.

### 6.5 The MCP validator

```sh
kitsoki mcp-validator --schema schema.json
```

A standalone stdio MCP server that validates a JSON payload against a
JSON Schema and returns a structured error envelope. The same code
powers the typed-JSON submit side-channel that oracle handlers
(`host.oracle.decide`, `host.oracle.task`, `host.oracle.ask` with
`schema:`) attach to Claude. Run it directly when debugging a
schema-shaped prompt.

---

## 7. Coding conventions

- **One responsibility per package.** The package map in
  [`architecture.md`](architecture.md#3-package-map) is the contract.
  If a feature spans two packages, the higher-level one calls the
  lower; never the reverse.
- **Effects belong in the orchestrator.** The machine is pure and the
  expr evaluator is pure. Anything that touches the network, the
  filesystem, or wall-clock time goes through `host.Handler` or
  `clock.Clock`.
- **Errors use the `intent.ErrorCode` enum at the boundary.** Inside a
  package, ordinary `error` is fine; at the harness/MCP boundary,
  every failure must be one of the documented codes (see
  [`state-machine.md`](state-machine.md#4-intents-and-slots)).
- **No silent defaults in `app.yaml`.** YAML parsing is strict
  (`KnownFields`); unknown keys are errors. Add a default in the type
  itself, not by skipping a missing field.
- **Tests are the spec.** When adding behaviour, add the flow fixture
  under `testdata/apps/<app>/flows/` first or alongside. The Mode 2
  runner is fast enough to make TDD pleasant.
- **Determinism is non-negotiable for the machine.** If your change
  introduces a `map` iteration order dependency or a `time.Now()` call
  inside the pure path, that's a bug.
- **No comments unless the *why* is non-obvious.** A subtle invariant
  earns a comment; restating the code does not.

---

## 8. Hot-reload (edit-mode)

While running `kitsoki run`, press `Esc` to open the action menu, then
pick **Edit mode**. The TUI:

1. Snapshots the app directory into a shadow copy.
2. Spawns `claude -p` inside the shadow copy with full Read/Edit/Write
   tool access and the user's free-text proposal as the prompt.
3. Diffs the result against the original to build a unified diff.
4. Displays the diff for review.

Hitting `[a]pply` copies the changed files back into place; the
orchestrator hot-reloads the `AppDef`, rebuilds the harness's cached
system prompt, and rebinds the current state if it still exists in the
new graph.

This is the easiest way to evolve an app — and a good debugging tool
when you can't tell whether a behaviour change is a YAML or a code
problem.

---

## 9. Releases & versioning

There is currently no formal release process. The CLI's `kitsoki
version` reads from `cmd/kitsoki/main.go`'s `Version` constant. Once a
release process exists, this section will document it; until then,
binaries are built ad hoc with `go build -o kitsoki ./cmd/kitsoki`.

---

## 10. Pointers

- **Architecture overview** → [`architecture.md`](architecture.md)
- **State machine** → [`state-machine.md`](state-machine.md)
- **Authoring an app** → [`authoring.md`](authoring.md)
- **Background jobs** → [`background-jobs/`](background-jobs/README.md)
- **Hosts and transports** → [`hosts.md`](hosts.md), [`transports.md`](transports.md)
- **Testing** → [`testing.md`](testing.md)
- **Embedded LLM operator manual** → `kitsoki docs llm-guide`
- **Authoritative `app.yaml` schema** → `kitsoki docs app-schema`
- **Prior art and comparative grounding** → [`prior-art.md`](prior-art.md)
