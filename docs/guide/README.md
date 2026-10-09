# Guide

Task-oriented Kitsoki documentation: how to launch agents, configure local
harnesses, set up integrations, and work on this repository safely.

Use this section when you already know what you want to do. Use
[`../architecture/`](../architecture/README.md) when you need the system model,
runtime boundaries, or rationale behind those steps.

## Agent Operation

- [`agents/`](agents/README.md) — launch Codex/Claude/Copilot-backed agents,
  configure harness profiles, understand backend/provider selection, and keep
  coding agents out of unsafe workspaces.
- [`agents/mcp.md`](agents/mcp.md) — use Kitsoki from Codex/Claude through
  Studio MCP, dynamic workflows, stories, CodeAct, and `kitsoki agent launch`.

## Development And Local Operations

- [`development/`](development/README.md) — build, test, debug, manage
  capsules, and follow repository conventions.

## Integrations

- [`integrations/`](integrations/README.md) — operator runbooks for external
  services such as the GitHub App-backed `@kitsoki` agent.

## Related Task Sections

- [`../getting-started.md`](../getting-started.md) — install and onboard a
  project.
- [`../workflows/`](../workflows/README.md) — end-user workflow guides.
- [`../stories/`](../stories/README.md) — story authoring.
- [`../recipes/`](../recipes/README.md) — short copy-paste authoring patterns.
- [`../tracing/`](../tracing/README.md) — trace, replay, and deterministic test
  workflows.
