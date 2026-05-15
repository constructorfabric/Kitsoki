# Kitsoki documentation

Welcome. This is the navigation hub for the documentation tree.

For the elevator pitch and quickstart, see the top-level
[`../README.md`](../README.md). For comparative grounding against
prior art (interactive fiction, statecharts, dialogue managers, LLM
orchestration), see [`prior-art.md`](prior-art.md).

---

## Read in this order

1. **[`architecture.md`](architecture.md)** — layers, packages, data
   flow, persistence model, conversation surfaces.
2. **[`state-machine.md`](state-machine.md)** — the directed cyclic
   graph: rooms, phases, states, intents, slots, world, guards, and
   the orchestrator's turn loop.
3. **[`authoring.md`](authoring.md)** — how to write an `app.yaml`.
   Patterns, common mistakes, scaling-up features.
4. **[`developer-guide.md`](developer-guide.md)** — for contributors:
   build, test, debug, add an intent / host / transport / subcommand.
5. **[`testing.md`](testing.md)** — Mode 1 (intent pass-rate) and
   Mode 2 (deterministic flow) tests; recordings; demo capture.
6. **[`hosts.md`](hosts.md)** — every built-in `host.*` handler with
   its input/output contract.
7. **[`transports.md`](transports.md)** — TUI, Jira, Bitbucket;
   sessions keyed by external thread; phase checkpoints.
8. **[`background-jobs/`](background-jobs/README.md)** — long-running
   handlers, inbox notifications, mid-flight clarifications.
9. **[`imports.md`](imports.md)** — composing apps across files and
   repos via the `imports:` block; the `/warp` slash command and
   `kitsoki run --warp` for operator smoke testing.
10. **[`prior-art.md`](prior-art.md)** — comparative grounding: what
    kitsoki borrows from (and rejects from) Inform/TADS/Ink/Yarn,
    XState/SCXML/Temporal/LangGraph, Rasa/Dialogflow/Bot Framework,
    and the MCP tool-shape conventions.
11. **[`bugs.md`](bugs.md)** — filing story and kitsoki bug reports
    (`/meta story bug`, `/meta kitsoki bug`, `kitsoki bug create`),
    the on-disk markdown format, and the future `bug sync` design.

## Reference (embedded in the binary)

The files under [`embedded/`](embedded/) are compiled into the `kitsoki`
binary via `//go:embed` and served by `kitsoki docs <topic>`. They are
field-reference / LLM-prompt material — narrative + design rationale
lives in the top-level `docs/*.md` above.

| Topic | Where |
|---|---|
| Authoritative `app.yaml` schema | `kitsoki docs app-schema` (or [`embedded/app-schema.md`](embedded/app-schema.md)) |
| LLM-facing operator manual | `kitsoki docs llm-guide` (or [`embedded/llm-guide.md`](embedded/llm-guide.md)) |
| Implement a prose proposal against `app.yaml` | `kitsoki docs apply-proposal` (or [`embedded/apply-proposal.md`](embedded/apply-proposal.md)) |
| Markdown shape produced by `kitsoki render` | `kitsoki docs render-format` (or [`embedded/render-format.md`](embedded/render-format.md)) |

## Historical material

- [`proposals/`](proposals/) — proposal documents, some of which have
  been (partly) implemented; kept for design context.
