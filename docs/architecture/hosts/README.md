# Host Handlers

This directory is the human navigation layer for built-in `host.*` handlers.
The canonical field-level reference remains [`../hosts.md`](../hosts.md), which
keeps existing anchors stable.

Hosts are grouped by the kind of boundary they cross:

| Family | Handlers | Guide |
|---|---|---|
| Local execution | `host.run`, `host.starlark.run` | [`local.md`](local.md) |
| Agent calls | `host.agent.extract`, `host.agent.ask`, `host.agent.decide`, `host.agent.codeact`, `host.agent.task`, `host.agent.converse` | [`agent.md`](agent.md) |
| Transports and artifacts | `host.transport.post`, `host.artifacts_dir`, media producers | [`transports-artifacts.md`](transports-artifacts.md) |
| Operator-facing state | jobs, chats, IDE, diffs, GitHub Issues, workspace context | [`operator.md`](operator.md) |

## Authoring Rule

Every handler must be present in the app's top-level `hosts:` allow-list before
a story can invoke it. The effect shape is documented in `kitsoki docs
app-schema`; this section documents what each built-in handler accepts and
returns.

For the Starlark-specific authoring model shared by `host.starlark.run` and
`host.agent.codeact`, see [`../starlark.md`](../starlark.md).

## Extending Hosts

To add a built-in handler, start with
[`../../guide/development/developer-guide.md` §5.2](../../guide/development/developer-guide.md#52-adding-a-new-built-in-host-handler).
For story-level host interfaces that can be rebound by importers, see
[`../../stories/imports.md`](../../stories/imports.md).
