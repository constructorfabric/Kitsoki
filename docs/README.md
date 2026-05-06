# Hally documentation

Welcome. This is the navigation hub for the documentation tree.

For the elevator pitch and quickstart, see the top-level
[`../README.md`](../README.md). For the long-form design rationale,
see [`../design.md`](../design.md).

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
   Mode 2 (deterministic flow) tests; oracles; demo recording.
6. **[`hosts.md`](hosts.md)** — every built-in `host.*` handler with
   its input/output contract.
7. **[`transports.md`](transports.md)** — TUI, Jira, Bitbucket;
   sessions keyed by external thread; phase checkpoints.
8. **[`background-jobs/`](background-jobs/README.md)** — long-running
   handlers, inbox notifications, mid-flight clarifications.

## Quick reference

| Topic | Where |
|---|---|
| Authoritative `app.yaml` schema | `hally docs app-schema` (or [`../cmd/hally/docs/app-schema.md`](../cmd/hally/docs/app-schema.md)) |
| LLM-facing operator manual | `hally docs llm-guide` (or [`../cmd/hally/docs/llm-guide.md`](../cmd/hally/docs/llm-guide.md)) |
| Implement a prose proposal against `app.yaml` | `hally docs apply-proposal` |
| Markdown shape produced by `hally render` | `hally docs render-format` |

## Historical material

- [`proposals/`](proposals/) — proposal documents, some of which have
  been (partly) implemented; kept for design context.
- [`archive/`](archive/) — earlier brainstorms and decision documents
  preserved for history (`idea.md`, `DECISIONS.md`,
  `stack-comparison.md`, `dev-story.md`,
  `dev-story-design.md`).

The proposals are still useful as context — for example,
[`proposals/bugfix-room-proposal.md`](proposals/bugfix-room-proposal.md)
is the design for the multi-transport conversation engine that the
current code is converging on. The archive is reference-only.
