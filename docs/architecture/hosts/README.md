# Host Handlers

This directory is the human navigation layer for built-in `host.*` handlers.
The canonical field-level reference remains [`../hosts.md`](../hosts.md), which
keeps existing anchors stable.

Hosts are grouped by the kind of boundary they cross:

| Family | Handlers | Guide |
|---|---|---|
| Local execution | `host.run`, `host.starlark.run` | [`local.md`](local.md) |
| Oracle calls | `host.oracle.extract`, `host.oracle.ask`, `host.oracle.decide`, `host.oracle.task`, `host.oracle.converse` | [`oracle.md`](oracle.md) |
| Transports and artifacts | `host.transport.post`, `host.artifacts_dir`, media producers | [`transports-artifacts.md`](transports-artifacts.md) |
| Operator-facing state | jobs, chats, IDE, diffs, GitHub Issues, workspace context | [`operator.md`](operator.md) |

## Authoring Rule

Every handler must be present in the app's top-level `hosts:` allow-list before
a story can invoke it. The effect shape is documented in `kitsoki docs
app-schema`; this section documents what each built-in handler accepts and
returns.

## Extending Hosts

To add a built-in handler, start with
[`../developer-guide.md` §5.2](../developer-guide.md#52-adding-a-new-built-in-host-handler).
For story-level host interfaces that can be rebound by importers, see
[`../../stories/imports.md`](../../stories/imports.md).
