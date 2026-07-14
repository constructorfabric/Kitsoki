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

Kitsoki does not bet against frontier agents. It makes them more
productive by reserving their reasoning for oversight, synthesis, and
long-horizon judgment, while high-leverage tools do the repeatable
work: scripts execute checked actions, cheaper models handle narrow
classification and extraction, and structured decomposition keeps long
tasks moving through reviewable rooms, gates, artifacts, and handoffs.

For the full thesis — control inversion, narrow LLM domains,
progressive determinism, the spectrum from CLI wizards to free agent
workflows — see [`docs/architecture/concept.md`](docs/architecture/concept.md).
For a reader-specific path through the docs, start at
[`docs/README.md`](docs/README.md).

Product site: [bsacrobatix.github.io/Kitsoki](https://bsacrobatix.github.io/Kitsoki/).

Slidey is Kitsoki's sister project for evidence-first narrative decks. Kitsoki
uses Slidey wherever reasonable: demos, QA/readiness reports, bug evidence,
workflow walkthroughs, comparison studies, and product review artifacts should
become shareable decks when the evidence is visual, temporal, or comparative.
JSON and markdown remain useful for machines and quick audit, but Slidey is the
preferred human review and presentation surface when it adds clarity.

**Free-text in, deterministic transitions out.**

```sh
cd ~/code/my-project
kitsoki run
# > onboard .
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
- Higher leverage agent work: frontier models focus on reasoning,
  oversight, and long-task coordination while scripts, cassettes,
  cheaper models, and typed host calls multiply the amount of reliable
  work each model turn can supervise.
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

### 1. Install

Put the `kitsoki` binary somewhere on your `PATH`, then check it:

```sh
kitsoki version
```

Kitsoki is a single binary. It embeds the base stories, the web UI, and the
skill/agent toolkit used during onboarding.

### 2. Choose an agent backend

Kitsoki auto-selects:

| Available | Harness | What |
|---|---|---|
| `claude` CLI on `PATH` | `claude` | Uses your existing Claude Code login. |
| `ANTHROPIC_API_KEY` set | `live` | Direct Anthropic SDK calls. |
| Neither | `replay` | Deterministic replay; useful for fixtures, not fresh work. |

Force one:

```sh
kitsoki run --harness claude
kitsoki run --harness live
```

### 3. Onboard your project

Run Kitsoki from the repository you want to use it in:

```sh
cd ~/code/my-project
kitsoki run
```

Then type:

```text
onboard .
```

Kitsoki discovers your project, shows the inferred profile for review, and only
writes after you confirm. The normal path is:

```text
onboard .          # discover this repo
continue           # review the discovered profile
continue           # confirm apply
```

Onboarding writes a small checked-in setup: `.kitsoki.yaml`, a project profile,
an editable dev-story instance, readiness checks, `.mcp.json`, and the
skill/agent toolkit for your coding agent. The full walkthrough and the
detailed onboarding contract are both in
[`docs/getting-started.md`](docs/getting-started.md).

### 4. Optional: GitHub auth for bug and PR workflows

To file bugs or PRs directly to GitHub from local Kitsoki runs, use GitHub
CLI's browser/PIN login:

```sh
kitsoki gh-agent login
source ~/.config/kitsoki/github.env
```

For repo-limited permissions, set up a least-privilege GitHub App token. No
public URL is required for local use:

```sh
kitsoki gh-agent setup app --name <app-name> --local-only
kitsoki gh-agent setup attach --repo owner/name
kitsoki gh-agent token
source ~/.config/kitsoki/github.env
```

Manual fine-grained PAT fallback is also supported with
`kitsoki gh-agent token --from-env`. See
[`docs/getting-started.md`](docs/getting-started.md#3-set-up-github-auth-for-local-issuepr-work).

### 5. Verify readiness

```sh
python3 .kitsoki/check-readiness.py --list
python3 .kitsoki/check-readiness.py --json
```

The report lands at `.artifacts/kitsoki-readiness.json`. To persist a summary
back into `.kitsoki/project-profile.yaml`, run:

```sh
python3 .kitsoki/check-readiness.py --json --update-profile
```

### 5. Use it from your coding agent

Onboarding registers the Kitsoki studio MCP server in your repo's `.mcp.json`.
Claude Code can adopt the installed driver agent:

```sh
claude --agent kitsoki-mcp-driver
```

Full runbook, caveats, and the manual fallback:
[`docs/recipes/studio-mcp-dogfood.md`](docs/recipes/studio-mcp-dogfood.md).

## Where to go next

**Site:** [bsacrobatix.github.io/Kitsoki](https://bsacrobatix.github.io/Kitsoki/) —
promo landing + help docs with recorded feature demos, generated from the
[feature catalog](features/CLAUDE.md) (also served offline at `/help/` by
`kitsoki web` after `make site-embed`; pipeline: [`docs/site/README.md`](docs/site/README.md)).

| You want to… | Start here |
|---|---|
| Use Kitsoki in your project | [`docs/getting-started.md`](docs/getting-started.md), then [`docs/workflows/`](docs/workflows/README.md) |
| Understand the architecture | [`docs/architecture/concept.md`](docs/architecture/concept.md), then [`docs/architecture/overview.md`](docs/architecture/overview.md) |
| Follow operational guides | [`docs/guide/`](docs/guide/README.md), especially [`docs/guide/agents/`](docs/guide/agents/README.md) |
| Write a story | [`docs/stories/architecture.md`](docs/stories/architecture.md), then [`docs/recipes/`](docs/recipes/README.md) |
| Look up story fields | `kitsoki docs app-schema` or [`docs/embedded/app-schema.md`](docs/embedded/app-schema.md) |
| Debug or test a story | [`docs/tracing/README.md`](docs/tracing/README.md) and [`docs/tracing/testing.md`](docs/tracing/testing.md) |
| Look up host handlers | [`docs/architecture/hosts/`](docs/architecture/hosts/README.md) and [`docs/architecture/hosts.md`](docs/architecture/hosts.md) |
| Build or contribute to Kitsoki | [`docs/contributor-setup.md`](docs/contributor-setup.md), then [`CONTRIBUTING.md`](CONTRIBUTING.md) |

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
[developer guide](docs/guide/development/developer-guide.md#7-coding-conventions).

## Name and mark

**Kitsoki** (*kit-soh-kee*) is a Hopi word for a contemporary
settlement. The metaphor fits a conversational workflow engine that hosts many
surfaces as connected rooms under one architecture. The **Mesa Sun** mark carries
the same architecture-and-light theme in geometry. See
[`docs/branding/logo.md`](docs/branding/logo.md) for the full naming note,
sources, logo, palette, and usage.

## Status

Beta. The core platform is stable and dogfooded daily: orchestrator, state machine, harness
abstraction, persistent SQLite store, MCP server, multi-transport
output, background jobs with mid-flight clarifications, persistent
chat threads, virtual clock, deterministic flow tests, intent
pass-rate tests, hot-reload edit mode in the TUI. Example apps under
`testdata/apps/` have green flow tests; `go test ./...` finishes in
under 10 seconds.

Recent frontier work:

- **Agent plugin and launch system** (`docs/architecture/agent-plugin.md`,
  `docs/guide/agents/cli.md`, `docs/guide/agents/launch.md`) —
  pluggable agent transports declared under `agent_plugins:`, dispatched through
  `host.agent.<verb>` effects with schema validation, subprocess /
  MCP-over-HTTP transports, and a `kitsoki agent launch` dry-run resolver that
  turns reusable story `agents:` entries plus harness profiles into concrete
  Claude/Codex task-agent launch plans.
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

<!-- BEGIN kitsoki:launch-policy (managed by pack/launch-policy/install.sh; edits inside are overwritten on upgrade) -->
## Agent operating principles (kitsoki launch policy)

This repository is governed by the parallel-agent gitflow. The rules are
mechanical, not advisory — the shims, hooks, and capsule CI enforce them:

- **Launch through the shims.** `claude` and `codex` resolve to
  `.kitsoki/bin/` wrappers (activate with `source .kitsoki/launch-policy.sh`;
  interactive shells can hook this on `cd`). Every launch passes
  `agent_launch_policy` preflight: this repo's root and its sibling repos
  are protected roots; agent work happens in `.capsules/workspaces/`
  (or legacy `.worktrees/`) via `kitsoki agent launch --exec`, with
  `--profile pog-drive` as the sanctioned catalog-drive entry.
- **Full-permissions agents are a last resort.** Use the sanctioned escape
  hatch (the `claude superagent` / `codex superagent` aliases, or
  `kitsoki agent launch --raw --interactive`) only when the governed path
  cannot do the job — and file the gap that forced it (feedback or
  requirement node) so the workaround becomes unnecessary next time.
- **Never edit a sibling repo.** Anything this repo needs from another is
  proposed as a typed requirement/bug node into that repo's federated
  catalog via `graph_propose`; its own fleet prioritizes it.
- **CI is capsule CI.** Run `kitsoki capsule ci doctor change --workspace
  <id>` before claiming work; `kitsoki capsule ci run` produces the typed
  verdict and receipt that admit a candidate to the merge queue
  (`kitsoki queue submit`). Protected `main` is never committed to
  directly — landings are fast-forward through the queue or the repo's
  merge-to-main helper, gated on green.
- **Disk is a first-class resource.** Workspaces have owners and get
  reaped; when the doctor's disk-capacity floor trips, run
  `kitsoki capsule cleanup plan` and apply a reviewed plan before
  launching more work.
<!-- END kitsoki:launch-policy -->
