# hally

A deterministic conversation engine. The user (or an external
orchestrator) drives a finite-state application with free text; an LLM
is used only to translate that text into one of a finite alphabet of
intents declared by the application author. Every transition, every
guard, every world mutation is in YAML. No hallucinated flags. No
out-of-state actions.

**Free-text in, deterministic transitions out.**

```sh
go build -o hally ./cmd/hally
./hally run testdata/apps/cloak/app.yaml
```

## What hally is good for

- Building a structured CLI/TUI that accepts natural language without
  giving up on determinism.
- Hosting one conversation per session across many surfaces — local
  TUI, Jira ticket comments, Bitbucket PR comments — with a shared
  state machine driving all of them.
- Long-running background work (LLM calls, builds) that pauses for a
  human reply and resumes, all from declarative YAML.
- Replayable, testable, demo-able LLM-driven flows. Mode 2 flow tests
  run with zero LLM cost and exit non-zero on regression.

It is **not** a chat agent. The LLM has no latitude to invent actions
outside the intent alphabet you declare.

## Quickstart

### 1. Build

```sh
go build -o hally ./cmd/hally
```

Requires Go 1.25+. Single static binary; no CGO, no system libraries.

### 2. Pick a harness

`hally run` auto-selects:

| Available | Harness | What |
|---|---|---|
| `claude` CLI on `PATH` | `claude` | Shells out to `claude -p` using your existing Claude Code login. **Default.** |
| `ANTHROPIC_API_KEY` set | `live` | Direct Anthropic SDK calls. |
| Neither | `replay` | Deterministic; needs an `--oracle` YAML. |

Force one:

```sh
./hally run testdata/apps/cloak/app.yaml --harness claude
./hally run testdata/apps/cloak/app.yaml --harness live
./hally run testdata/apps/cloak/app.yaml \
    --harness replay --oracle testdata/apps/cloak/oracle.yaml
```

### 3. Play

```sh
./hally run testdata/apps/cloak/app.yaml
```

The TUI opens with a transcript pane, action menu, and inbox panel.
Type free text or pick an action. Sessions persist in
`$XDG_DATA_HOME/hally/sessions.db`.

### 4. Test

```sh
./hally test flows testdata/apps/cloak/app.yaml          # deterministic, no LLM
./hally test intents testdata/apps/cloak/app.yaml \      # intent pass-rate (free w/ Claude Code)
    --harness static
```

### 5. Visualise

```sh
./hally viz testdata/apps/cloak/app.yaml | dot -Tpng -o /tmp/cloak.png
./hally viz testdata/apps/cloak/app.yaml --mermaid > /tmp/cloak.mmd
```

## Documentation

| Doc | What |
|---|---|
| **[`docs/architecture.md`](docs/architecture.md)** | Layers, packages, data flow, persistence model, conversation surfaces. |
| **[`docs/state-machine.md`](docs/state-machine.md)** | Rooms, phases, states, intents, slots, world, guards, the turn loop. The directed cyclic graph in detail. |
| **[`docs/authoring.md`](docs/authoring.md)** | How to write an `app.yaml`. Patterns, scaling-up, pitfalls. |
| **[`docs/developer-guide.md`](docs/developer-guide.md)** | For contributors: build, test, debug, add features. |
| **[`docs/testing.md`](docs/testing.md)** | Mode 1 (intent pass-rate) and Mode 2 (deterministic flow) tests. |
| **[`docs/hosts.md`](docs/hosts.md)** | Every built-in `host.*` handler with input/output contracts. |
| **[`docs/transports.md`](docs/transports.md)** | TUI / Jira / Bitbucket transports; sessions keyed by external thread. |
| **[`docs/background-jobs/`](docs/background-jobs/README.md)** | Long-running handlers, notifications, clarifications. |
| `hally docs llm-guide` | Embedded operator manual aimed at an LLM driving hally. |
| `hally docs app-schema` | Authoritative `app.yaml` schema reference. |
| **[`design.md`](design.md)** | Long-form design rationale (~2000 lines). |

## Project layout

```
hally/
├── cmd/hally/             CLI: run, serve, viz, trace, replay, test,
│                          record, session, chat, inspect, turn, render,
│                          mcp-validator, docs, version
├── internal/              platform packages — see docs/architecture.md
├── pkg/hallytest/         public testing helpers for app authors
├── docs/                  narrative documentation
├── testdata/apps/         example apps: cloak, dev-story,
│                          background_jobs, proposal_smoke
├── demo/                  VHS tapes and recorded GIFs
├── design.md              long-form design
├── ideas.md               working notes / backlog
└── README.md              you are here
```

## Status

PoC. The core platform is stable: orchestrator, state machine, harness
abstraction, persistent SQLite store, MCP server, multi-transport
output, background jobs with mid-flight clarifications, persistent
chat threads, virtual clock, deterministic flow tests, intent
pass-rate tests, hot-reload edit mode in the TUI. All four example
apps under `testdata/apps/` have green flow tests; `go test ./...`
finishes in under 10 seconds.

The current frontier is multi-transport sessions driven from external
orchestrators (Jira, Bitbucket); see
[`docs/proposals/bugfix-room-proposal.md`](docs/proposals/bugfix-room-proposal.md).

## License

TBD.
