# Kitsoki documentation

Welcome. This is the navigation hub for the documentation tree.

For the elevator pitch and user quickstart, see the top-level
[`../README.md`](../README.md).

## Pick your path

| You are | Read in order |
|---|---|
| Deciding whether Kitsoki is worth it | [`evaluate-kitsoki.md`](evaluate-kitsoki.md) -> [`architecture/concept.md`](architecture/concept.md) -> [`case-studies/bug-fix.md`](case-studies/bug-fix.md) -> [`case-studies/bugfix-bakeoff.md`](case-studies/bugfix-bakeoff.md) |
| Using Kitsoki in your own project | [`getting-started.md`](getting-started.md) -> [`workflows/`](workflows/README.md) |
| Writing a story | [`stories/architecture.md`](stories/architecture.md) -> [`stories/authoring.md`](stories/authoring.md) -> [`tracing/testing.md`](tracing/testing.md) -> [`recipes/`](recipes/README.md) -> [`embedded/app-schema.md`](embedded/app-schema.md) |
| Contributing to Kitsoki | [`contributor-setup.md`](contributor-setup.md) (developer environment setup, including Playwright/Chromium visual QA deps) -> [`../CONTRIBUTING.md`](../CONTRIBUTING.md) -> [`guide/development/developer-guide.md`](guide/development/developer-guide.md) |
| Debugging a session | [`tracing/README.md`](tracing/README.md) -> [`tracing/testing.md`](tracing/testing.md) -> [`tracing/trace-format.md`](tracing/trace-format.md) |
| Working on UI | [`web/README.md`](web/README.md) or [`tui/README.md`](tui/README.md) -> [`tui/rendering-tests.md`](tui/rendering-tests.md) |
| Managing local development workspaces | [`dev-workspaces.md`](dev-workspaces.md) -> [`architecture/hosts.md#hostgit_worktree-workspace-interface`](architecture/hosts.md#hostgit_worktree-workspace-interface) -> [`guide/development/capsules.md`](guide/development/capsules.md) |
| Checking whether the product actually works for a user | [`persona-qa.md`](persona-qa.md) -> [`testing/README.md`](testing/README.md) |

The tree is organised by task and area. Each section has its own `README.md`
index. The proposal tree is design history and work in progress, not the
product manual.

---

## Sections

### [`architecture/`](architecture/README.md) — the engine and its boundaries

How kitsoki works under the hood: control inversion, progressive
determinism, the package map and turn loop, persistence, and the runtime
contracts where Kitsoki reaches the outside world (hosts, transports, agent
plugins, routing, prompts, MCP Studio). *Audience: architects and people
changing the platform model.*

Start with [`architecture/concept.md`](architecture/concept.md) for the thesis,
then [`architecture/overview.md`](architecture/overview.md) for the system map.

### [`guide/`](guide/README.md) — operational and contributor guides

How to use the platform surfaces: launch agents, configure harness profiles,
set local launch policy, use `kitsoki agent`, set up GitHub App credentials,
manage capsules, and follow the repository contribution workflow. *Audience:
operators, contributors, and anyone following a concrete task.*

Start with [`guide/agents/launch.md`](guide/agents/launch.md) for agent launch
and [`guide/development/developer-guide.md`](guide/development/developer-guide.md)
for source work.

### [`stories/`](stories/README.md) — the authoring model

How to write a story: the `app.yaml` state-machine vocabulary (rooms,
phases, states, intents, slots, world, guards, transitions, effects),
the authoring loop, composition via imports, the visual/narrative
style guide, the choice widget, sidebar meta-mode agents, background
jobs, and bug filing. *Audience: story authors.*

### [`tracing/`](tracing/README.md) — trace, test, debug, replay

The session trace is the authoritative state; everything else derives
from it. This section covers the trace format, the two test modes,
host cassettes, the `kitsoki turn` probe, and how to replay and debug
a session. *Audience: anyone testing, debugging, or developing a story.*

### [`testing/`](testing/README.md) — evaluating Kitsoki against real use

Story-local agent benchmarks, routing-fixture tuning, upgraded-story CI
checks, model/harness comparison, and **Persona QA** — checking a product
scenario across transports or running a full persona x scenario matrix sweep
against real repos, with an independent judge and a report + Slidey deck.
*Audience: anyone evaluating whether a story or the product itself actually
works.*

Start with [`persona-qa.md`](persona-qa.md) for the 5-minute path.

### [`recipes/`](recipes/README.md) — copy-paste patterns

Short, task-oriented recipes for common authoring patterns: add an
intent, gate a destructive effect, branch on a host call, collect a
form, write a flow test, run a background job. Each links back to the
reference docs. *Audience: authors who know what they want to do and
want the shortest correct path.*

Use recipes after the authoring model and test/replay loop are clear.

### [`tui/`](tui/README.md) — the terminal UI

The single-pane chat TUI: the block rendering pipeline, typed
view-elements + pongo2, the `/command` surface, engine-event observers,
and how to write TUI rendering regression tests. *Audience: contributors
working on the UI; authors wanting to understand how their views render.*

---

## Reference (embedded in the binary)

The files under [`embedded/`](embedded/) are compiled into the `kitsoki`
binary via `//go:embed` and served by `kitsoki docs <topic>`. They are
field-reference / LLM-prompt material — narrative and design rationale
live in the sections above.

| Topic | Where |
|---|---|
| Authoritative `app.yaml` schema | `kitsoki docs app-schema` (or [`embedded/app-schema.md`](embedded/app-schema.md)) |
| LLM-facing operator manual | `kitsoki docs llm-guide` (or [`embedded/llm-guide.md`](embedded/llm-guide.md)) |
| Implement a prose proposal against `app.yaml` | `kitsoki docs apply-proposal` (or [`embedded/apply-proposal.md`](embedded/apply-proposal.md)) |
| Markdown shape produced by `kitsoki render` | `kitsoki docs render-format` (or [`embedded/render-format.md`](embedded/render-format.md)) |

## Worked examples and per-story references

- [`getting-started.md`](getting-started.md) — install the binary and onboard
  your own repo: a committed, working kitsoki environment (instance + studio
  MCP + skill/agent toolkit), backed by the dev-story
  [`init` pipeline](stories/dev-story-onboarding.md).
- [`workflows/`](workflows/README.md) — canonical "how a user does this" doc
  per core developer workflow (PRD/design, decompose→implement, file a bug,
  fix a bug), one per surface (TUI/web/VS Code/gh-agent) where the steps
  actually differ.
- [`contributor-setup.md`](contributor-setup.md) — build Kitsoki from source and
  set up this checkout for development, including browser dependencies for
  runstatus and TUI visual QA.
- [`guide/`](guide/README.md) — task-oriented reference for agent launch,
  harness profiles, local development, capsules, credentials, and integration
  setup.
- [`dev-workspaces.md`](dev-workspaces.md) — create, bootstrap, commit, land,
  and remove managed clone-backed capsule workspaces for Codex/Claude and
  dev-story runs.
- [`case-studies/`](case-studies/README.md) — worked examples of
  progressive determinism applied to real workflows. Start with
  [`case-studies/bug-fix.md`](case-studies/bug-fix.md): how a
  prompt-driven agent loop became the multi-room `bugfix` pipeline.
- **Per-story READMEs** — each story under `../stories/` ships its own
  authoritative reference. Notable ones:
  [`../.kitsoki/stories/kitsoki-dev/README.md`](../.kitsoki/stories/kitsoki-dev/README.md)
  (dogfood operator walkthrough),
  [`../stories/bugfix/README.md`](../stories/bugfix/README.md)
  (the bugfix pipeline),
  [`../stories/pr-refinement/README.md`](../stories/pr-refinement/README.md),
  [`../stories/docs-review/README.md`](../stories/docs-review/README.md)
  (the meta-story that audits these docs against the code at HEAD).

## Historical material

- [`proposals/`](proposals/README.md) — proposal documents still in
  design or partially shipped; kept for design context. As a proposal
  ships, its user-facing reference moves into one of the sections above
  (e.g. semantic routing now lives at
  [`architecture/semantic-routing.md`](architecture/semantic-routing.md))
  and the fully-shipped proposal is deleted.
- [`features/mvp.md`](features/mvp.md) — the MVP scope list.
- [`competitive-analysis/`](competitive-analysis/README.md) — market,
  domain, and technical research. Business/positioning material, not part
  of the product manual.
