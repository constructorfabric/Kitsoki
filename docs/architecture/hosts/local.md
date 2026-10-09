# Local Execution Hosts

Local execution hosts run deterministic work on the machine where kitsoki is
running.

| Handler | Use it for | Reference |
|---|---|---|
| `host.run` | Shell or argv-mode commands, including background jobs. | [`../hosts.md#hostrun`](../hosts.md#hostrun) |
| `host.starlark.run` | Small deterministic glue scripts with typed inputs and replayable HTTP. | [`../hosts.md#hoststarlarkrun`](../hosts.md#hoststarlarkrun) |

Prefer `host.starlark.run` when the work is data shaping or API glue that needs
to be reviewable and replayable. Use `host.run` for ordinary process execution,
and prefer argv mode when templated values become command arguments.

The full Starlark authoring model, including the stdlib, capability sandbox,
cassettes, and its relationship to CodeAct, is [`../starlark.md`](../starlark.md).
