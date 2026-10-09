# Agent Hosts

Agent hosts are the LLM boundary. They should be used only when deterministic
routing, guards, templates, or host calls cannot answer the question directly.

| Handler | Use it for | Reference |
|---|---|---|
| `host.agent.extract` | Tiered structured extraction: synonyms, slot templates, then LLM. | [`../hosts.md#hostagentextract`](../hosts.md#hostagentextract) |
| `host.agent.ask` | Read-only inspection that returns prose and optional typed JSON. | [`../hosts.md#hostagentask`](../hosts.md#hostagentask) |
| `host.agent.decide` | Schema-bounded verdicts and gates. | [`../hosts.md#hostagentdecide`](../hosts.md#hostagentdecide) |
| `host.agent.codeact` | Bounded agent loop that emits capability-scoped Starlark snippets, then `done(payload)`. | [`../hosts.md#hostagentcodeact`](../hosts.md#hostagentcodeact) |
| `host.agent.task` | Focused agent work with replay artifacts and acceptance modes. | [`../hosts.md#hostagenttask`](../hosts.md#hostagenttask) |
| `host.agent.converse` | Free-form conversational sessions with permission controls. | [`../hosts.md#hostagentconverse`](../hosts.md#hostagentconverse) |

For declaring alternate agent transports and providers, see
[`../agent-plugin.md`](../agent-plugin.md), [`../../guide/agents/providers.md`](../../guide/agents/providers.md),
and [`../../guide/agents/backends.md`](../../guide/agents/backends.md).

For the shared Starlark sandbox used by `host.agent.codeact`, see
[`../starlark.md`](../starlark.md).
