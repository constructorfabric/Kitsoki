# kitsoki

A conversational workflow engine built on one commitment: **make workflows as
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
workflows — see [`docs/architecture/concept.md`](docs/architecture/concept.md).

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
  open-ended ones. See [`docs/architecture/semantic-routing.md`](docs/architecture/semantic-routing.md).

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
make test                                                  # full suite — what CI runs

./kitsoki test flows testdata/apps/cloak/app.yaml          # deterministic, no LLM
./kitsoki test intents testdata/apps/cloak/app.yaml \      # intent pass-rate (free w/ Claude Code)
    --harness static
```

`make test` runs `go test ./...` plus every story's deterministic flow fixtures —
it's the suite CI runs and the [pre-PR gate](CONTRIBUTING.md) runs. Open PRs with
`make pr` (local gate) or `make pr-ci` (gate on real CI).

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
the supervised 8-room pipeline (idle → reproducing → proposing →
implementing → testing → reviewing → validating → done). PR
refinement is a separate story under `stories/pr-refinement/`.
Every checkpoint appends a `## Comment <iso> by <author>` block to
the bug file, so the file itself is the conversation log + audit
trail.

Autonomous variant (LLM-judge auto-fires confident verdicts, bails
to human only on uncertainty):

```sh
./kitsoki run stories/kitsoki-dev/app.yaml \
    --warp scenarios/autonomous_ready.yaml
```

See **[`stories/kitsoki-dev/README.md`](stories/kitsoki-dev/README.md)**
for the full operator walkthrough, the
**[`docs/case-studies/bug-fix.md`](docs/case-studies/bug-fix.md)**
case study for the architecture, and
**[`issues/README.md`](issues/README.md)** for the on-disk bug
schema. The dogfood multi-glob covers both kitsoki-self bugs
(`issues/bugs/*.md`) and per-story bugs
(`stories/*/issues/bugs/*.md`) in one pipeline.

## Documentation

| Doc | What |
|---|---|
| **[`docs/architecture/concept.md`](docs/architecture/concept.md)** | The thesis: control inversion, narrow LLM domains, progressive determinism, the spectrum of stories. **Start here.** |
| **[`docs/architecture/overview.md`](docs/architecture/overview.md)** | Layers, packages, data flow, persistence model, conversation surfaces. |
| **[`docs/stories/state-machine.md`](docs/stories/state-machine.md)** | Rooms, phases, states, intents, slots, world, guards, the turn loop. The directed cyclic graph in detail. |
| **[`docs/stories/authoring.md`](docs/stories/authoring.md)** | How to write an `app.yaml`. Patterns, scaling-up, pitfalls. |
| **[`docs/stories/choice-widget.md`](docs/stories/choice-widget.md)** | Author cookbook for `choice:` view elements (single / multi / form picker). |
| **[`docs/stories/story-style.md`](docs/stories/story-style.md)** | Story style guide — typed view elements, narration voice, choice-widget conventions. |
| **[`CONTRIBUTING.md`](CONTRIBUTING.md)** | Start here to contribute: `make test`, the pre-PR gate (`make pr` / `make pr-ci`), CI. |
| **[`docs/architecture/developer-guide.md`](docs/architecture/developer-guide.md)** | For contributors: build, test (incl. CI + cross-platform gotchas), debug, add features. |
| **[`docs/tracing/testing.md`](docs/tracing/testing.md)** | Mode 1 (intent pass-rate) and Mode 2 (deterministic flow) tests. |
| **[`docs/architecture/hosts.md`](docs/architecture/hosts.md)** | Every built-in `host.*` handler with input/output contracts. |
| **[`docs/architecture/oracle-plugin.md`](docs/architecture/oracle-plugin.md)** | Oracle plugin contract: `oracle_plugins:`, `host.oracle.<verb>` effects, subprocess / MCP-over-HTTP transports, schema validation. |
| **[`docs/architecture/oracle-cli.md`](docs/architecture/oracle-cli.md)** | The five-verb `host.oracle.*` surface. |
| **[`docs/tracing/trace-format.md`](docs/tracing/trace-format.md)** | The JSONL trace schema — event vocabulary, `EventSink` contract, `call_id` derivation. The trace is the session's authoritative state. |
| **[`docs/tracing/cassettes.md`](docs/tracing/cassettes.md)** | Host cassette file-format reference: episode matching, `!include`, record mode, CI safety. |
| **[`docs/architecture/transports.md`](docs/architecture/transports.md)** | TUI / Jira / Bitbucket transports; sessions keyed by external thread. |
| **[`docs/stories/background-jobs/`](docs/stories/background-jobs/README.md)** | Long-running handlers, notifications, clarifications. |
| **[`docs/embedded/llm-guide.md`](docs/embedded/llm-guide.md)** | Operator manual aimed at an LLM driving kitsoki. Also `kitsoki docs llm-guide`. |
| **[`docs/embedded/app-schema.md`](docs/embedded/app-schema.md)** | Authoritative `app.yaml` schema reference. Also `kitsoki docs app-schema`. |
| **[`docs/embedded/apply-proposal.md`](docs/embedded/apply-proposal.md)** | LLM guide for implementing a prose proposal against `app.yaml`. |
| **[`docs/embedded/render-format.md`](docs/embedded/render-format.md)** | Shape of the Markdown produced by `kitsoki render`. |
| **[`docs/architecture/prior-art.md`](docs/architecture/prior-art.md)** | Comparative grounding: what kitsoki borrows from interactive fiction, statecharts, dialogue managers, and LLM orchestration. |

## Project layout

```
kitsoki/
├── cmd/kitsoki/           CLI: run, serve, viz, trace, replay,
│                          replay-routing, test, record, session, chat,
│                          inspect, turn, render, docs, bug, cassette,
│                          extract, journal, oracle, oracle-serve,
│                          migrate-oracle, mcp-bash, mcp-validator,
│                          export-status, ui, version
├── internal/              platform packages — see docs/architecture/overview.md
├── docs/                  narrative documentation
├── docs/embedded/         CLI-embedded reference docs (//go:embed)
├── stories/               first-party story state machines (kitsoki-dev,
│                          bugfix, pr-refinement, docs-review, code-review,
│                          dev-story, oregon-trail, …)
├── tools/                 first-party companion tooling (runstatus SPA,
│                          pellicule video pipeline, loopy, …)
├── testdata/apps/         example apps: background_jobs, choice_smoke,
│                          cloak, dev-story, imports_prompt_rebase,
│                          imports_smoke, parallel_smoke, proposal_smoke,
│                          timeout
├── demo/                  VHS tapes and recorded GIFs
├── ideas.md               working notes / backlog
└── README.md              you are here
```

## About the name

**Kitsoki** (*kit-soh-kee*) is a Hopi word for a contemporary
settlement — a collection of houses, ceremonial chambers, and public
plazas arranged into one living whole. The metaphor fits a
conversational workflow engine that hosts many surfaces (TUI, daemon, Jira,
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
pass-rate tests, hot-reload edit mode in the TUI. Example apps under
`testdata/apps/` have green flow tests; `go test ./...` finishes in
under 10 seconds.

Recent frontier work:

- **Oracle plugin system** (`docs/architecture/oracle-plugin.md`,
  `docs/architecture/oracle-cli.md`) — pluggable oracle transports declared under
  `oracle_plugins:`, dispatched through `host.oracle.<verb>` effects
  with schema validation, subprocess / MCP-over-HTTP transports, and
  a registry/dispatch seam audited end-to-end.
- **JSONL trace as authoritative state**
  (`docs/tracing/trace-format.md`) — the unified event log (`oracle.call.start`
  / `.complete` / `.error`, `EventSink`, deterministic `call_id`) is
  now the session's source of truth, with replay guarantees layered
  on top.
- **`runstatus` inspection UI** (`tools/runstatus/`) — Vue 3 SPA +
  Playwright fixtures for inspecting live and recorded sessions
  against the JSONL trace.
- **`docs-review` story** (`stories/docs-review/`) — meta-story that
  audits the docs against the code at HEAD and writes back surgical
  fixes.

## License

TBD.
