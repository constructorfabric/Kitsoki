# Architecture

How kitsoki works under the hood — the engine, its design commitments,
and the boundaries where it talks to the outside world. *Audience:
architects evaluating the approach, and contributors changing the
kitsoki codebase itself.*

If you are here to **write a story**, you want [`../stories/`](../stories/README.md)
instead. If you are here to **test or debug** one, see
[`../tracing/`](../tracing/README.md).

---

## Start here

- **[`concept.md`](concept.md)** — the thesis. Control inversion (the
  runtime drives the state machine; the LLM only handles narrow
  sub-tasks), progressive determinism, and the spectrum from CLI
  wizards to free agent workflows. Read this first.
- **[`overview.md`](overview.md)** — the system design: layers, the
  package map, the turn loop, the LLM boundary, multi-surface
  conversations, the persistence/replay model, and the trust model.
- **[`prior-art.md`](prior-art.md)** — comparative grounding: what
  kitsoki borrows from and rejects from interactive fiction
  (Inform/TADS/Ink/Yarn), statecharts and orchestration
  (XState/SCXML/Temporal/LangGraph), and dialogue managers
  (Rasa/Dialogflow/Bot Framework).

## The external-world boundary

These docs describe how the deterministic engine reaches outside
itself — every interpretive or side-effecting call crosses one of
these surfaces.

- **[`hosts.md`](hosts.md)** — every built-in `host.*` handler with its
  input/output contract. The effect surface authors invoke from YAML.
  Use [`hosts/`](hosts/README.md) as the shorter family index.
- **[`agent-plugin.md`](agent-plugin.md)** — the Agent plugin
  contract: declaring external agents under `agent_plugins:`, the
  `invoke: host.agent.<verb>` + `agent:` effect shape, the
  subprocess / MCP-over-HTTP transports, schema validation, and
  sub-events.
- **[`agent-providers.md`](agent-providers.md)** — the `providers:`
  block: retargeting the `claude` subprocess at an alternate
  Anthropic-compatible backend (model + env overrides) per invocation,
  selected by an agent's `provider:` or an effect's `with: { provider }`.
- **[`agent-backends.md`](agent-backends.md)** — the `--agent
  claude|copilot` switch: which coding-agent CLI kitsoki forks behind
  every agent verb + routing, the claude→copilot flag translation, and
  the interface-compliance conformance suite.
- **[`agent-cli.md`](agent-cli.md)** — the `host.agent.*` verb
  surface as a standalone CLI (`kitsoki agent …`, `agent-serve`) for
  validators and CI outside a running state machine.
- **[`mcp-studio.md`](mcp-studio.md)** — the `kitsoki mcp` studio server
  an external coding agent attaches to: the handle model and the
  `story.*` / `session.*` / `render.*` tool surface that let it author a
  story, drive a live session (no-LLM by default), and *see* the terminal
  and browser views — plus the MCP-client operator surface.
- **[`operator-ask.md`](operator-ask.md)** — forwarding a dispatched
  agent's clarifying question back to the live operator (web + TUI + the
  MCP studio client) via the `mcp__operator__ask` bridge, replacing the
  headless-broken built-in `AskUserQuestion` tool; the DI
  `OperatorPrompter` seam, interactivity gating, the wire schema, and the
  trace events.
- **[`artifact-annotation.md`](artifact-annotation.md)** — viewing and
  annotating a room artifact (png / mp4 / rrweb / html / slidey deck) with
  location-tied feedback: the discriminated `AnnotationAnchor` union, the
  producer-agnostic semantic-sidecar plugin contract, the `ArtifactAnnotator`
  surface, and the companion-serving RPC/route. Generalises
  [`visual-ambient.md`](visual-ambient.md).
- **[`system-prompt.md`](system-prompt.md)** — the layered, cache-friendly
  system prompt (kitsoki → project → task) composed for every claude
  invocation, the replace-vs-append model, and the per-verb dynamic-sections
  policy.
- **[`transports.md`](transports.md)** — output adapters (TUI, Jira,
  Bitbucket) and how sessions are keyed by external thread.
- **[`semantic-routing.md`](semantic-routing.md)** — the routing stack
  that sits between the deterministic match and the LLM: synonyms,
  templates, typed slot parsers, and the turncache, plus
  `kitsoki replay-routing` and `kitsoki inspect --synonym-suggestions`.
- **[`ambient-mining.md`](ambient-mining.md)** — the propose → apply
  loop: a scored recipe is deduped against the live inventory, drafted
  into a staged YAML delta, and on an explicit accept applied via the
  meta-mode reload path **only when the no-LLM flow suite stays green**,
  with the surface-and-verdict recorded in the trace.

## Contributing

- **[`developer-guide.md`](developer-guide.md)** — build, test, debug;
  how to add an intent, host handler, transport, or subcommand; coding
  conventions and the repository layout.

## See also

- The session trace and replay guarantees that make all of this
  auditable: [`../tracing/trace-format.md`](../tracing/trace-format.md).
- How a real workflow was decomposed into deterministic rooms:
  [`../case-studies/bug-fix.md`](../case-studies/bug-fix.md).
