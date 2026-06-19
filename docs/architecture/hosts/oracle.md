# Oracle Hosts

Oracle hosts are the LLM boundary. They should be used only when deterministic
routing, guards, templates, or host calls cannot answer the question directly.

| Handler | Use it for | Reference |
|---|---|---|
| `host.oracle.extract` | Tiered structured extraction: synonyms, slot templates, then LLM. | [`../hosts.md#hostoracleextract`](../hosts.md#hostoracleextract) |
| `host.oracle.ask` | Read-only inspection that returns prose and optional typed JSON. | [`../hosts.md#hostoracleask`](../hosts.md#hostoracleask) |
| `host.oracle.decide` | Schema-bounded verdicts and gates. | [`../hosts.md#hostoracledecide`](../hosts.md#hostoracledecide) |
| `host.oracle.task` | Focused agent work with replay artifacts and acceptance modes. | [`../hosts.md#hostoracletask`](../hosts.md#hostoracletask) |
| `host.oracle.converse` | Free-form conversational sessions with permission controls. | [`../hosts.md#hostoracleconverse`](../hosts.md#hostoracleconverse) |

For declaring alternate oracle transports and providers, see
[`../oracle-plugin.md`](../oracle-plugin.md), [`../oracle-providers.md`](../oracle-providers.md),
and [`../oracle-backends.md`](../oracle-backends.md).
