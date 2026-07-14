# Recipe: smoke-test async work through studio MCP

Use `kitsoki mcp-test` when you need to verify the studio MCP server without
reloading an LLM client. This smoke runs a real MCP client over stdio, keeps one
server-side session handle alive across calls, waits for a background job to
finish, captures an inbox notification id, teleports back to the task, and
re-renders the current TUI frame.

The smoke uses only replay/direct session driving and `host.run`; it does not
call a real LLM.

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" \
go run ./cmd/kitsoki mcp-test \
  --timeout 20s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/mcp-test/async-teleport.db \
  --calls '[
    {
      "tool": "session.new",
      "args": {
        "story_path": "testdata/apps/background_jobs/app.yaml",
        "key": "async-teleport"
      }
    },
    {
      "tool": "session.submit",
      "args": {
        "handle": "async-teleport",
        "intent": "enter"
      },
      "expect": {
        "structuredContent.outcome.state": "running"
      }
    },
    {
      "tool": "session.inspect",
      "args": {
        "handle": "async-teleport"
      },
      "expect": {
        "structuredContent.async.jobs_terminal": 1,
        "structuredContent.async.notifications_unread": 2
      },
      "save": {
        "notification_id": "structuredContent.notifications.0.id"
      },
      "retries": 30,
      "interval_ms": 100
    },
    {
      "tool": "studio.work",
      "expect": {
        "structuredContent.summary.notifications_unread": 2,
        "structuredContent.summary.jobs_terminal": 1,
        "structuredContent.items.0.reacquire.tool": "session.teleport"
      }
    },
    {
      "tool": "session.teleport",
      "args": {
        "handle": "async-teleport",
        "notification_id": "${notification_id}"
      },
      "expect": {
        "structuredContent.outcome.state": "running"
      }
    },
    {
      "tool": "session.inspect",
      "args": {
        "handle": "async-teleport"
      },
      "expect": {
        "structuredContent.async.notifications_unread": 1
      }
    },
    {
      "tool": "render.tui",
      "args": {
        "handle": "async-teleport"
      },
      "expect": {
        "structuredContent.frame.metadata.state": "running"
      }
    }
  ]'
```

Why the explicit `--db` matters: studio MCP opens the chat/job schema on start.
In sandboxes or read-only developer environments, the default shared
`sessions.db` may not be writable. Pointing at `.artifacts/mcp-test/*.db` keeps
the smoke self-contained and disposable.

Useful `mcp-test --calls` fields:

| Field | Purpose |
|---|---|
| `tool` | MCP tool name to call. |
| `args` | JSON object passed as tool arguments. |
| `expect` | Dot-path assertions against the MCP `CallToolResult` JSON. Array indexes are supported, for example `structuredContent.notifications.0.id`. |
| `expect_contains` | Dot-path string assertions where the actual value must contain the expected substring. Useful for rendered frames. |
| `expect_exists` | Dot-path presence assertions. Useful for binary payloads such as MCP image `data` fields where exact bytes are intentionally not stable. |
| `save` | Captures dot-path values into `${name}` variables for later calls. |
| `retries` / `interval_ms` | Repeats the tool call until expectations pass, useful for async `session.inspect` polling. |

The expected proof at the end is:

- `session.inspect.async.jobs_terminal == 1`
- `session.inspect.async.notifications_unread == 2`
- `studio.work` sees the terminal job and two unread notifications globally,
  with a `session.teleport` reacquisition hint for notification-backed job or
  notification rows
- passive `success` / `info` notifications remain visible in `studio.work` but
  report `needs_attention == false` and lower priorities than active jobs/chats
- `session.teleport` succeeds using the captured notification id
- a final `session.inspect` reports `notifications_unread == 1`
- `render.tui` reports the reacquired frame's state as `running`

## Awaiting-input clarification smoke

Use this variant to prove an intervention loop, not just background completion:
a deterministic `host.run` stub pauses mid-flight with
`host.RequestClarification`, `studio.work` prioritizes the required input,
`session.teleport` reacquires the saved context, and `session.submit` answers
the job so it can finish. The `--flow` flag installs the fixture's
`host_handlers:` stubs into every studio driving session; no LLM or shell sleep
is used for the host call.

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" \
go run ./cmd/kitsoki mcp-test \
  --list-tools=false \
  --timeout 30s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --flow --server-arg testdata/apps/background_jobs/flows/clarification.yaml \
  --server-arg --db --server-arg .artifacts/mcp-test/clarification-flow.db \
  --calls '[
    {
      "tool": "session.new",
      "args": {
        "story_path": "testdata/apps/background_jobs/app.yaml",
        "key": "clarify-smoke"
      },
      "expect": {
        "structuredContent.state": "lobby"
      }
    },
    {
      "tool": "session.submit",
      "args": {
        "handle": "clarify-smoke",
        "intent": "enter"
      },
      "expect": {
        "structuredContent.outcome.state": "running"
      }
    },
    {
      "tool": "session.inspect",
      "args": {
        "handle": "clarify-smoke"
      },
      "expect": {
        "structuredContent.async.jobs_total": 1,
        "structuredContent.async.jobs_awaiting_input": 1,
        "structuredContent.async.notifications_action_required": 1,
        "structuredContent.jobs.0.clarification_schema.prompt": "Which environment?"
      },
      "save": {
        "job_id": "structuredContent.jobs.0.id",
        "notification_id": "structuredContent.notifications.1.id"
      },
      "retries": 30,
      "interval_ms": 100
    },
    {
      "tool": "studio.work",
      "expect": {
        "structuredContent.summary.jobs_awaiting_input": 1,
        "structuredContent.summary.needs_attention": 2,
        "structuredContent.items.0.body": "Which environment?",
        "structuredContent.items.1.reacquire.args.notification_id": "${notification_id}"
      },
      "retries": 10,
      "interval_ms": 100
    },
    {
      "tool": "session.teleport",
      "args": {
        "handle": "clarify-smoke",
        "notification_id": "${notification_id}"
      },
      "expect": {
        "structuredContent.outcome.state": "running"
      }
    },
    {
      "tool": "session.submit",
      "args": {
        "handle": "clarify-smoke",
        "intent": "answer_clarification",
        "slots": {
          "job_id": "${job_id}",
          "answer": "prod"
        }
      },
      "expect": {
        "structuredContent.outcome.state": "running"
      }
    },
    {
      "tool": "session.inspect",
      "args": {
        "handle": "clarify-smoke"
      },
      "expect": {
        "structuredContent.async.jobs_total": 1,
        "structuredContent.async.jobs_terminal": 1,
        "structuredContent.async.jobs_awaiting_input": 0,
        "structuredContent.world.result": "clarification-done"
      },
      "retries": 30,
      "interval_ms": 100
    }
  ]'
```

The expected proof at the end is:

- `session.inspect.jobs[].clarification_schema.prompt` exposes the question.
- `studio.work` puts the action-required notification first and the job row
  second, both with the same prompt body.
- The job row's `session.teleport` hint points at the action-required
  notification, not the passive "job submitted" notification.
- After answering, `jobs_total` is still `1` and `jobs_awaiting_input == 0`, so
  the answer did not re-launch the background job.
- `world.result == "clarification-done"` proves the paused handler resumed and
  ran `on_complete`.

To capture the browser active-work surface too, first run `make web`, then add
this `render.web` call after `studio.work` and before `session.teleport`:

```json
{
  "tool": "render.web",
  "args": {
    "handle": "clarify-smoke",
    "query": {
      "inbox": "1"
    }
  },
  "expect": {
    "content.1.type": "image",
    "content.1.mimeType": "image/png"
  },
  "expect_exists": [
    "content.1.data"
  ]
}
```

The `?inbox=1` query opens the web inbox panel directly, so the screenshot shows
the prioritized active-work queue without a synthetic browser click.

## Chat-backed work smoke

Use this variant to prove the Claude-style subagent queue path: a state-machine
room creates a persistent chat, enqueues a non-awaited `host.chat.drive`, then
`studio.work` returns that pending drive with a `chat.show` reacquisition hint.
No dispatcher or real agent is required.

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" \
go run ./cmd/kitsoki mcp-test \
  --list-tools=false \
  --timeout 20s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/mcp-test/chat-drive-work.db \
  --calls '[
    {
      "tool": "session.new",
      "args": {
        "story_path": "testdata/apps/chat_drive_work/app.yaml",
        "key": "chat-drive-smoke"
      },
      "expect": {
        "structuredContent.state": "idle"
      }
    },
    {
      "tool": "session.submit",
      "args": {
        "handle": "chat-drive-smoke",
        "intent": "queue"
      },
      "expect": {
        "structuredContent.outcome.state": "queued"
      }
    },
    {
      "tool": "studio.work",
      "expect": {
        "structuredContent.summary.pending_drives": 1,
        "structuredContent.items.0.kind": "pending_drive",
        "structuredContent.items.0.reacquire.tool": "chat.show",
        "structuredContent.items.0.reacquire.args.handle": "chat-drive-smoke"
      },
      "save": {
        "chat_id": "structuredContent.items.0.chat_id",
        "session_id": "structuredContent.items.0.reacquire.args.session_id"
      }
    },
    {
      "tool": "chat.show",
      "args": {
        "chat_id": "${chat_id}",
        "handle": "chat-drive-smoke",
        "session_id": "${session_id}"
      },
      "expect": {
        "structuredContent.context.handle": "chat-drive-smoke",
        "structuredContent.context.session_id": "${session_id}",
        "structuredContent.chat.title": "Async MCP chat"
      }
    },
    {
      "tool": "session.command",
      "args": {
        "handle": "chat-drive-smoke",
        "command": "/work --all"
      },
      "expect_contains": {
        "structuredContent.frame.text": "/chat show ${chat_id}"
      }
    },
    {
      "tool": "session.command",
      "args": {
        "handle": "chat-drive-smoke",
        "command": "/chat show ${chat_id}"
      },
      "expect_contains": {
        "structuredContent.frame.text": "Async MCP chat"
      }
    }
  ]'
```

This smoke exercises the session-origin stamping that lets `studio.work`,
TUI `/work`, and the web inbox active-work list all find pending or failed chat
drives created by ordinary story `host.chat.drive` effects. `session.command`
runs the real TUI slash dispatcher and returns the rendered frame, so the smoke
proves the terminal active-work affordance without launching an interactive TUI.
TUI `/work` prints `/chat show <id>` for queued/dispatching/failed chat drives,
and `/chat show` gives the same focused async chat context that `chat.show`
exposes over MCP.

For the operator-ask fallback path, `session.drive` may return
`awaiting_operator` instead of settling the turn. While that turn is parked,
`studio.work` also reports an `operator_question` row with the same
`question_id`, `questions[]`, and a `reacquire.tool` of `session.answer`. This
means a client can recover a missed parked question from the global work queue
and resume it by calling the row's `{handle, question_id}` with its chosen
answers.

For backgrounded Claude PTY rows, `/work --all` also seeds the TUI's
`/sessions attach <N>` cache; studio MCP can verify the selected target with
`session.command` and `/sessions attach <N> --dry-run` without handing the
headless process to tmux.

To prove the browser surface too, first stage the embedded runstatus SPA:

```sh
make web
```

Then run a focused live-handle web render smoke. This uses the chat id captured
from `studio.work` as a `render.web` hash-query deep link, so the browser shot
lands on the same focused async chat context the web active-work panel opens:

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" \
go run ./cmd/kitsoki mcp-test \
  --list-tools=false \
  --timeout 60s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/mcp-test/render-web.db \
  --calls '[
    {
      "tool": "session.new",
      "args": {
        "story_path": "testdata/apps/chat_drive_work/app.yaml",
        "key": "web-chat-smoke"
      }
    },
    {
      "tool": "session.submit",
      "args": {
        "handle": "web-chat-smoke",
        "intent": "queue"
      }
    },
    {
      "tool": "studio.work",
      "expect": {
        "structuredContent.items.0.reacquire.tool": "chat.show",
        "structuredContent.items.0.reacquire.args.handle": "web-chat-smoke"
      },
      "save": {
        "chat_id": "structuredContent.items.0.chat_id"
      }
    },
    {
      "tool": "render.web",
      "args": {
        "handle": "web-chat-smoke",
        "query": {
          "chat": "${chat_id}"
        }
      },
      "expect": {
        "content.1.type": "image",
        "content.1.mimeType": "image/png"
      },
      "expect_exists": [
        "content.1.data"
      ]
    }
  ]'
```

This uses the same stdio MCP server, serves the open studio handle through the
runstatus web handler, and returns a `render.web` text result plus an MCP
`image/png` block when the client accepts images. The `query` object is appended
to the SPA hash route, so other web deep links can be smoke-tested the same way.
It requires the local
Playwright helper dependencies under `tools/runstatus`; story/state screenshots
without a live handle still belong to `kitsoki web-shot` with an explicit
no-LLM flow.

To prove the proposal-review browser surface without a real miner, seed the web
proposal queue through the deterministic session query seam and open the inbox
panel in the same render:

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" \
go run ./cmd/kitsoki mcp-test \
  --list-tools=false \
  --timeout 60s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/mcp-test/render-web-proposal.db \
  --calls '[
    {
      "tool": "session.new",
      "args": {
        "story_path": "testdata/apps/chat_drive_work/app.yaml",
        "key": "web-proposal-smoke"
      }
    },
    {
      "tool": "render.web",
      "args": {
        "handle": "web-proposal-smoke",
        "query": {
          "inbox": "1",
          "proposal": "{\"id\":\"demo-mcp-proposal\",\"kind\":\"write_mode\",\"title\":\"May I edit README.md?\",\"detail\":\"Proposed doc cleanup\"}"
        },
        "assert_text": [
          "Active work",
          "May I edit README.md?",
          "Proposed doc cleanup"
        ]
      },
      "expect": {
        "content.1.type": "image",
        "content.1.mimeType": "image/png"
      },
      "expect_exists": [
        "content.1.data"
      ]
    }
  ]'
```

This is a browser-surface proof for seeded web proposal rows. Real trace-backed
mining proposals are discoverable through MCP `session.inspect.mining_proposals`,
MCP `studio.work` `mining_proposal` rows, and backend web
`runstatus.work.list` active-work rows. TUI `/work` also folds the same
trace-backed mining proposal rows when session history is wired, and MCP
`session.command` can render that terminal surface headlessly. This query seam
still validates the web proposal queue specifically, not the trace-backed miner
path.
