<img src="docs/branding/assets/mesa-sun-wordmark.svg" width="200" alt="kitsoki — the Mesa Sun wordmark">

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
For a reader-specific path through the docs, start at
[`docs/start-here.md`](docs/start-here.md).

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

Prebuilt downloads are published on
[GitHub Releases](https://github.com/bsacrobatix/Kitsoki/releases/latest) for
macOS, Linux, and Windows. The product site has the platform list at
[Download Kitsoki](https://bsacrobatix.github.io/Kitsoki/download.html).

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

`.kitsoki/stories/kitsoki-dev/` is the dogfood instance: kitsoki working on
kitsoki itself (and on each of its stories) through its own UI, with
the bug file as both ticket and conversation log.

```sh
./kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml
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
./kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml \
    --warp scenarios/autonomous_ready.yaml
```

See **[`.kitsoki/stories/kitsoki-dev/README.md`](.kitsoki/stories/kitsoki-dev/README.md)**
for the full operator walkthrough, the
**[`docs/case-studies/bug-fix.md`](docs/case-studies/bug-fix.md)**
case study for the architecture, and
**[`issues/README.md`](issues/README.md)** for the on-disk bug
schema. The dogfood multi-glob covers both kitsoki-self bugs
(`issues/bugs/*.md`) and per-story bugs
(`stories/*/issues/bugs/*.md`) in one pipeline.

## Where to go next

**Site:** [bsacrobatix.github.io/Kitsoki](https://bsacrobatix.github.io/Kitsoki/) —
promo landing + help docs with recorded feature demos, generated from the
[feature catalog](features/CLAUDE.md) (also served offline at `/help/` by
`kitsoki web` after `make site-embed`; pipeline: [`docs/site/README.md`](docs/site/README.md)).

| You want to… | Start here |
|---|---|
| Pick the right docs path | [`docs/start-here.md`](docs/start-here.md) |
| Understand the architecture | [`docs/architecture/concept.md`](docs/architecture/concept.md), then [`docs/architecture/overview.md`](docs/architecture/overview.md) |
| Write a story | [`docs/stories/architecture.md`](docs/stories/architecture.md), then [`docs/recipes/`](docs/recipes/README.md) |
| Look up story fields | `kitsoki docs app-schema` or [`docs/embedded/app-schema.md`](docs/embedded/app-schema.md) |
| Debug or test a story | [`docs/tracing/README.md`](docs/tracing/README.md) and [`docs/tracing/testing.md`](docs/tracing/testing.md) |
| Look up host handlers | [`docs/architecture/hosts/`](docs/architecture/hosts/README.md) and [`docs/architecture/hosts.md`](docs/architecture/hosts.md) |
| Contribute code | [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`docs/architecture/developer-guide.md`](docs/architecture/developer-guide.md) |

## Project layout

```
kitsoki/
├── cmd/kitsoki/           CLI: run, serve, viz, trace, replay,
│                          replay-routing, test, record, session, chat,
│                          inspect, turn, render, docs, bug, cassette,
│                          extract, journal, agent, agent-serve,
│                          migrate-agent, mcp-bash, mcp-validator,
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
├── .context/              scratch: transient proposals, summaries, plans
│                          (gitignored)
├── .artifacts/            generated review output: renders, test reports,
│                          videos (gitignored)
└── README.md              you are here
```

`.context/` and `.artifacts/` are gitignored scratch spaces — put
transient markdown (proposals, summaries) in `.context/` and any
generated artifact for review in `.artifacts/`, so neither clutters the
tracked tree. See the
[developer guide](docs/architecture/developer-guide.md#7-coding-conventions).

## Name and mark

**Kitsoki** (*kit-soh-kee*) is a Hopi word for a contemporary
settlement. The metaphor fits a conversational workflow engine that hosts many
surfaces as connected rooms under one architecture. The **Mesa Sun** mark carries
the same architecture-and-light theme in geometry. See
[`docs/branding/logo.md`](docs/branding/logo.md) for the full naming note,
sources, logo, palette, and usage.

## Status

PoC. The core platform is stable: orchestrator, state machine, harness
abstraction, persistent SQLite store, MCP server, multi-transport
output, background jobs with mid-flight clarifications, persistent
chat threads, virtual clock, deterministic flow tests, intent
pass-rate tests, hot-reload edit mode in the TUI. Example apps under
`testdata/apps/` have green flow tests; `go test ./...` finishes in
under 10 seconds.

Recent frontier work:

- **Agent plugin system** (`docs/architecture/agent-plugin.md`,
  `docs/architecture/agent-cli.md`) — pluggable agent transports declared under
  `agent_plugins:`, dispatched through `host.agent.<verb>` effects
  with schema validation, subprocess / MCP-over-HTTP transports, and
  a registry/dispatch seam audited end-to-end.
- **JSONL trace as authoritative state**
  (`docs/tracing/trace-format.md`) — the unified event log (`agent.call.start`
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

See [`LICENSE`](LICENSE).
