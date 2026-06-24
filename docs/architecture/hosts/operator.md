# Operator-Facing Hosts

These hosts connect a story to live operator context: background-job answers,
persistent chats, IDE state, diff review, GitHub Issues, and workspace context.

| Handler | Use it for | Reference |
|---|---|---|
| `host.jobs.answer_clarification` | Resume a background job waiting for human input. | [`../hosts.md#hostjobsanswer_clarification`](../hosts.md#hostjobsanswer_clarification) |
| `host.chat.*` | Resolve, list, fork, archive, rename, and drive persistent chat threads. | [`../hosts.md#hostchat--persistent-chat-threads`](../hosts.md#hostchat--persistent-chat-threads) |
| `host.ide.*` | Read editor diagnostics, selection, open files, and open files or diffs. | [`../hosts.md#hostide--editor-awareness`](../hosts.md#hostide--editor-awareness) |
| `host.diff.open` | Open a diff in the best available review surface and capture the verdict. | [`../hosts.md#hostdiffopen--review-a-change-in-the-best-surface`](../hosts.md#hostdiffopen--review-a-change-in-the-best-surface) |
| `host.gh.ticket` | File and migrate GitHub Issues-backed tickets. | [`../hosts.md#hostghticket--github-issues-backed-tracker`](../hosts.md#hostghticket--github-issues-backed-tracker) |
| `host.workspace_manager.get` | Load a typed workspace snapshot from an external workspace manager. | [`../hosts.md#hostworkspace_managerget`](../hosts.md#hostworkspace_managerget) |

For live agent questions forwarded to web/TUI, see
[`../operator-ask.md`](../operator-ask.md).
