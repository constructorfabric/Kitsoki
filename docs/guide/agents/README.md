# Agent Guide

Operational reference for launching and configuring external coding agents.

Start with [`launch.md`](launch.md) when you want `kitsoki agent launch` to
produce or run a concrete backend command.

## Launching

- [`mcp.md`](mcp.md) — use Kitsoki through Studio MCP from Codex/Claude, including
  story driving, dynamic workflows, CodeAct, and agent launch patterns.
- [`launch.md`](launch.md) — `kitsoki agent launch` use cases, dry-runs,
  interactive Codex sessions, CodeAct mode, and safety behavior.
- [`launch-policy.md`](launch-policy.md) — local preflight policy that keeps
  launched agents out of protected checkouts and unsafe branches.

## Backend Selection

- [`harness-profiles.md`](harness-profiles.md) — shared/local profile config,
  live provider/model switching, quotas, and default profile behavior.
- [`backends.md`](backends.md) — backend CLI translation for Claude, Codex,
  Copilot, and agy.
- [`providers.md`](providers.md) — provider environment retargeting for
  Anthropic/OpenAI-compatible endpoints.

## Direct Agent Calls

- [`cli.md`](cli.md) — `kitsoki agent <verb>` and JSON-RPC access to the five
  `host.agent.*` handlers.

## Architecture Contracts

- [`../../architecture/agent-plugin.md`](../../architecture/agent-plugin.md) —
  declaration and invocation contract for agent plugins.
- [`../../architecture/hosts.md`](../../architecture/hosts.md) — public
  `host.agent.*` effect contracts.
- [`../../architecture/mcp-studio.md`](../../architecture/mcp-studio.md) —
  external-agent studio MCP control plane.
