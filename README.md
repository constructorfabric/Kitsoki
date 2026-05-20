# kitsoki

A conversation engine built on one commitment: **make workflows as
deterministic as possible, and confine the LLM to narrow, identified,
traceable decision points.**

Most LLM systems put the LLM in charge — it plans, it reasons, it
calls tools, the runtime executes. Kitsoki inverts that. The runtime
is a YAML state machine, written by the application author. When it
needs help with something it cannot resolve deterministically, it
*calls the LLM* for that narrow sub-task — routing a turn onto a
declared intent, extracting structured fields from free text, or
running focused agent work inside a sandboxed phase — and then takes
the result and resumes deterministic execution.

This lets a workflow start as an LLM-heavy sketch and grow, one
decision point at a time, into something predictable: prompts become
flows, free-form tool calls become typed host invocations,
interpretation becomes slot templates. The trace records every
decision, which is what makes the conversion incremental and
auditable.

For the full thesis — control inversion, narrow LLM domains,
progressive determinism, the spectrum from CLI wizards to free agent
workflows — see [`docs/concept.md`](docs/concept.md).

**Free-text in, deterministic transitions out.**

```sh
go build -o kitsoki ./cmd/kitsoki
./kitsoki run testdata/apps/cloak/app.yaml
```

## What kitsoki is good for

- Building a structured CLI/TUI that accepts natural language without
  giving up on determinism.
- Hosting one conversation per session across many surfaces — local
  TUI, Jira ticket comments, Bitbucket PR comments — with a shared
  state machine driving all of them.
- Long-running background work (LLM calls, builds) that pauses for a
  human reply and resumes, all from declarative YAML.
- Replayable, testable, demo-able LLM-driven flows. Mode 2 flow tests
  run with zero LLM cost and exit non-zero on regression.
- Fast on the common case: a four-tier semantic-routing stack
  (synonyms, slot templates, a turncache, and the LLM) resolves
  most user input in microseconds without calling the LLM. On the
  Oregon Trail story ~78% of recorded turns route deterministically
  or via author-declared synonyms — the LLM only fires on the genuinely
  open-ended ones. See [`docs/semantic-routing.md`](docs/semantic-routing.md).

It is **not** a chat agent. The LLM has no latitude to invent actions
outside the intent alphabet you declare.

## Quickstart

### 1. Build

```sh
go build -o kitsoki ./cmd/kitsoki
```

Requires Go 1.25+. Single static binary; no CGO, no system libraries.

### 2. Pick a harness

`kitsoki run` auto-selects:

| Available | Harness | What |
|---|---|---|
| `claude` CLI on `PATH` | `claude` | Shells out to `claude -p` using your existing Claude Code login. **Default.** |
| `ANTHROPIC_API_KEY` set | `live` | Direct Anthropic SDK calls. |
| Neither | `replay` | Deterministic; needs a recording (passed via `--recording`). |

Force one:

```sh
./kitsoki run testdata/apps/cloak/app.yaml --harness claude
./kitsoki run testdata/apps/cloak/app.yaml --harness live
./kitsoki run testdata/apps/cloak/app.yaml \
    --harness replay --recording testdata/apps/cloak/recording.yaml
```

### 3. Play

```sh
./kitsoki run testdata/apps/cloak/app.yaml
```

The TUI opens with a transcript pane, action menu, and inbox panel.
Type free text or pick an action. Sessions persist in
`$XDG_DATA_HOME/kitsoki/sessions.db`.

### 4. Test

```sh
./kitsoki test flows testdata/apps/cloak/app.yaml          # deterministic, no LLM
./kitsoki test intents testdata/apps/cloak/app.yaml \      # intent pass-rate (free w/ Claude Code)
    --harness static
```

### 5. Visualise

```sh
./kitsoki viz testdata/apps/cloak/app.yaml | dot -Tpng -o /tmp/cloak.png
./kitsoki viz testdata/apps/cloak/app.yaml --mermaid > /tmp/cloak.mmd
```

## Dogfood mode — fixing kitsoki with kitsoki

`stories/kitsoki-dev/` is the dogfood instance: kitsoki working on
kitsoki itself (and on each of its stories) through its own UI, with
the bug file as both ticket and conversation log.

```sh
./kitsoki run stories/kitsoki-dev/app.yaml
```

Lands at the engineer's-day landing room. From there: `tickets` to
search `issues/bugs/`, `pick <id>` to pick a bug, `bugfix` to walk
the supervised 8-room pipeline (reproduce → propose → implement →
test → review → validate → done → PR refinement → merge). Every
checkpoint appends a `## Comment <iso> by <author>` block to the bug
file, so the file itself is the conversation log + audit trail.

Autonomous variant (LLM-judge auto-fires confident verdicts, bails
to human only on uncertainty):

```sh
./kitsoki run stories/kitsoki-dev/app.yaml \
    --warp scenarios/autonomous_ready.yaml
```

See **[`stories/kitsoki-dev/README.md`](stories/kitsoki-dev/README.md)**
for the full operator walkthrough, the
**[`docs/proposals/dev-story-bugfix-unify-proposal.md`](docs/proposals/dev-story-bugfix-unify-proposal.md)**
proposal for the architecture, and
**[`issues/README.md`](issues/README.md)** for the on-disk bug
schema. The dogfood multi-glob covers both kitsoki-self bugs
(`issues/bugs/*.md`) and per-story bugs
(`stories/*/issues/bugs/*.md`) in one pipeline.

## Documentation

| Doc | What |
|---|---|
| **[`docs/concept.md`](docs/concept.md)** | The thesis: control inversion, narrow LLM domains, progressive determinism, the spectrum of stories. **Start here.** |
| **[`docs/architecture.md`](docs/architecture.md)** | Layers, packages, data flow, persistence model, conversation surfaces. |
| **[`docs/state-machine.md`](docs/state-machine.md)** | Rooms, phases, states, intents, slots, world, guards, the turn loop. The directed cyclic graph in detail. |
| **[`docs/authoring.md`](docs/authoring.md)** | How to write an `app.yaml`. Patterns, scaling-up, pitfalls. |
| **[`docs/developer-guide.md`](docs/developer-guide.md)** | For contributors: build, test, debug, add features. |
| **[`docs/testing.md`](docs/testing.md)** | Mode 1 (intent pass-rate) and Mode 2 (deterministic flow) tests. |
| **[`docs/hosts.md`](docs/hosts.md)** | Every built-in `host.*` handler with input/output contracts. |
| **[`docs/transports.md`](docs/transports.md)** | TUI / Jira / Bitbucket transports; sessions keyed by external thread. |
| **[`docs/background-jobs/`](docs/background-jobs/README.md)** | Long-running handlers, notifications, clarifications. |
| **[`docs/embedded/llm-guide.md`](docs/embedded/llm-guide.md)** | Operator manual aimed at an LLM driving kitsoki. Also `kitsoki docs llm-guide`. |
| **[`docs/embedded/app-schema.md`](docs/embedded/app-schema.md)** | Authoritative `app.yaml` schema reference. Also `kitsoki docs app-schema`. |
| **[`docs/embedded/apply-proposal.md`](docs/embedded/apply-proposal.md)** | LLM guide for implementing a prose proposal against `app.yaml`. |
| **[`docs/embedded/render-format.md`](docs/embedded/render-format.md)** | Shape of the Markdown produced by `kitsoki render`. |
| **[`docs/prior-art.md`](docs/prior-art.md)** | Comparative grounding: what kitsoki borrows from interactive fiction, statecharts, dialogue managers, and LLM orchestration. |

## Project layout

```
kitsoki/
├── cmd/kitsoki/           CLI: run, serve, viz, trace, replay, test,
│                          record, session, chat, inspect, turn, render,
│                          mcp-validator, docs, version
├── internal/              platform packages — see docs/architecture.md
├── docs/                  narrative documentation
├── docs/embedded/         CLI-embedded reference docs (//go:embed)
├── testdata/apps/         example apps: cloak, dev-story,
│                          background_jobs, proposal_smoke
├── demo/                  VHS tapes and recorded GIFs
├── ideas.md               working notes / backlog
└── README.md              you are here
```

## About the name

**Kitsoki** (*kit-soh-kee*) is a Hopi word for a contemporary
settlement — a collection of houses, ceremonial chambers, and public
plazas arranged into one living whole. The metaphor fits a
conversation engine that hosts many surfaces (TUI, daemon, Jira,
Bitbucket) as connected rooms under one architecture.

Greek mythology is exhausted as a source of software names. Every
other tool is *Hermes*, *Hydra*, *Apollo*, *Athena*, *Pythia*, or some
flavor of *Oracle*. The Hopi word is a small reminder that other
civilizations were doing serious intellectual work too — and that the
Western canon is not the only well to draw from.

The Chacoan ancestors of today's Pueblo peoples were practicing
astronomy at a level modern archaeologists still find striking:

- Great houses at Chaco Canyon are oriented to the cardinal directions
  and to the 18.6-year lunar standstill cycle — an astronomical
  pattern subtle enough that detecting it requires sustained
  observation across more than a human generation.
- The Sun Dagger on Fajada Butte uses three rock slabs to cast
  light-and-shadow markers onto a spiral petroglyph at the solstices
  and equinoxes.
- The Great North Road runs almost exactly due north from Chaco
  for about sixty kilometers across broken terrain — a deliberate
  engineering project that required sustained surveying.

This is pre-Columbian scientific work, encoded into the built
landscape. The name is a small acknowledgment.

Sources for the term and the architectural vocabulary it sits in:

- Whiteley, Peter. *[Chacoan Kinship](https://www.amnh.org/content/download/67776/1174292/file/chacoan-kinship.pdf)*. American Museum of Natural History.
- Kuwanwisiwma, Leigh J., T. J. Ferguson, and Chip Colwell, eds. (2018). *[Footprints of Hopi History: Hopihiniwtiput Kukveni'at](https://uapress.arizona.edu/book/footprints-of-hopi-history)*. University of Arizona Press.

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
