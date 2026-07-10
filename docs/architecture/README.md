# Architecture

High-level system model for Kitsoki: the deterministic engine, the LLM
boundary, persistent sessions, and the contracts where the runtime reaches the
outside world.

Use this section to understand or change the platform. Use
[`../guide/`](../guide/README.md) for operator runbooks, CLI walkthroughs,
agent-launch instructions, harness/profile configuration, credential setup, and
contributor commands.

## Reading Paths

| Goal | Read |
|---|---|
| Understand the thesis | [`concept.md`](concept.md) -> [`overview.md`](overview.md) |
| Understand the runtime map | [`overview.md`](overview.md) -> [`hosts.md`](hosts.md) -> [`transports.md`](transports.md) |
| Understand the LLM boundary | [`semantic-routing.md`](semantic-routing.md) -> [`system-prompt.md`](system-prompt.md) -> [`agent-plugin.md`](agent-plugin.md) |
| Understand external-agent surfaces | [`mcp-studio.md`](mcp-studio.md) -> [`agent-plugin.md`](agent-plugin.md) -> [`operator-ask.md`](operator-ask.md) |
| Use or configure agent launch | [`../guide/agents/`](../guide/agents/README.md) |
| Contribute safely | [`../guide/development/developer-guide.md`](../guide/development/developer-guide.md) -> [`../tracing/testing.md`](../tracing/testing.md) |

## Start Here

- [`concept.md`](concept.md) — control inversion, progressive determinism, and
  why the LLM returns bounded results instead of driving the workflow.
- [`overview.md`](overview.md) — system layers, turn loop, LLM boundary,
  multi-surface sessions, persistence, replay, and trust model.
- [`prior-art.md`](prior-art.md) — what Kitsoki borrows from and rejects from
  interactive fiction, statecharts, workflow engines, and dialogue managers.

## Runtime Boundaries

- [`hosts.md`](hosts.md) — authoritative host-effect contracts.
  [`hosts/`](hosts/README.md) is the shorter family index.
- [`transports.md`](transports.md) — external thread transports, session keys,
  and the drive-vs-transport model.
- [`mcp-studio.md`](mcp-studio.md) — external-agent control plane for authoring,
  driving, testing, and inspecting Kitsoki sessions.
- [`operator-ask.md`](operator-ask.md) — forwarding agent questions to a live
  operator instead of using headless-broken `AskUserQuestion`.

## Runtime Concepts

- [`starlark.md`](starlark.md) — deterministic glue scripts, CodeAct snippets,
  cassettes, validation, and the narrow `ctx` capability surface.
- [`semantic-routing.md`](semantic-routing.md) — deterministic routing,
  synonyms, templates, typed slots, the turn cache, and replay tooling.
- [`system-prompt.md`](system-prompt.md) — layered, cache-friendly prompts for
  routing and `host.agent.*` calls.
- [`room-workbench.md`](room-workbench.md) — the `workbench:` primitive and its
  deterministic-boundary rule.
- [`prompt-intercept.md`](prompt-intercept.md) — pre-LLM prompt classification,
  conservative gates, and decision recording.
- [`ambient-mining.md`](ambient-mining.md) — propose/apply mining loop for
  staged story improvements.

## Contracts And Models

- [`agent-plugin.md`](agent-plugin.md) — external agent declarations and the
  `invoke: host.agent.<verb>` contract.
- [`github-agent.md`](github-agent.md) — the `@kitsoki` GitHub dispatch model.
- [`artifact-annotation.md`](artifact-annotation.md) — unified annotations for
  screenshots, videos, rrweb captures, HTML, and Slidey decks.
- [`visual-ambient.md`](visual-ambient.md) — frame/point/element context fed
  into visual oracles; generalized by artifact annotations.
- [`embeddings.md`](embeddings.md) — embedding index/store/sidecar and
  `host.agent.search`.
- [`extension-docs.md`](extension-docs.md) — source-owned docs sidecars and the
  deterministic extension library index.
- [`decomposition-graph.md`](decomposition-graph.md) and
  [`graph-grouping-taxonomy.md`](graph-grouping-taxonomy.md) — graph model,
  validation, areas, and initiatives.
- [`repo-history-training.md`](repo-history-training.md) — manifest and
  promotion rule for repo-history training data.

## Guide Handoff

- [`../guide/agents/launch.md`](../guide/agents/launch.md) — `kitsoki agent
  launch`, CodeAct mode, dry-runs, and interactive Codex launches.
- [`../guide/agents/harness-profiles.md`](../guide/agents/harness-profiles.md)
  — backend/profile/model configuration.
- [`../guide/development/developer-guide.md`](../guide/development/developer-guide.md)
  — local build/test/debug contribution workflow.
- [`../guide/development/capsules.md`](../guide/development/capsules.md) —
  hermetic repository fixtures and `kitsoki capsule` usage.
- [`../guide/integrations/github-app-setup.md`](../guide/integrations/github-app-setup.md)
  — GitHub App setup runbook.

## See Also

- [`../tracing/trace-format.md`](../tracing/trace-format.md) — the audit trail
  that makes architecture decisions replayable.
- [`../case-studies/bug-fix.md`](../case-studies/bug-fix.md) — a real workflow
  decomposed into deterministic rooms.
