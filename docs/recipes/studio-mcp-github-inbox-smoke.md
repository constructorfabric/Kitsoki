# Recipe: smoke-test GitHub inbox sync through studio MCP

Use this when you need to prove the external-intake path without touching the
network or a real GitHub account. The smoke starts a real studio MCP server over
stdio, opens a driving session, syncs fake GitHub issue/PR rows into that
session's inbox, checks the global work queue, teleports to the selected
notification, and renders the reacquired TUI frame.

The fake `gh` script lives under `.artifacts/` so it is disposable and never
committed:

```sh
mkdir -p .artifacts/mcp-test/fake-bin
cat > .artifacts/mcp-test/fake-bin/gh <<'SH'
#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi
if [ "$1" = "issue" ] && [ "$2" = "list" ]; then
  cat <<'JSON'
[{"number":7,"title":"Assigned issue","url":"https://github.com/acme/repo/issues/7","assignees":[{"login":"brad"}]}]
JSON
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  cat <<'JSON'
[{"number":42,"title":"Review this","url":"https://github.com/acme/repo/pull/42","author":{"login":"alice"}}]
JSON
  exit 0
fi
echo "unexpected fake gh command: $*" >&2
exit 1
SH
chmod +x .artifacts/mcp-test/fake-bin/gh
```

Then run the MCP smoke with that fake `gh` first on `PATH`:

```sh
PATH="$PWD/.artifacts/mcp-test/fake-bin:$PATH" \
GOCACHE="$PWD/.cache/go-build" \
go run ./cmd/kitsoki mcp-test \
  --timeout 20s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/mcp-test/github-inbox.db \
  --calls '[
    {
      "tool": "session.new",
      "args": {
        "story_path": "testdata/apps/cloak/app.yaml",
        "key": "github-inbox-smoke"
      },
      "expect": {
        "structuredContent.state": "foyer"
      }
    },
    {
      "tool": "inbox.sync_github",
      "args": {
        "handle": "github-inbox-smoke",
        "repo": "acme/repo",
        "limit": 10,
        "teleport_state": "foyer"
      },
      "expect": {
        "structuredContent.fetched": 2,
        "structuredContent.inserted": 2,
        "structuredContent.skipped": 0,
        "structuredContent.items.1.origin_ref": "github:acme/repo/pr/42",
        "structuredContent.items.1.url": "https://github.com/acme/repo/pull/42"
      },
      "save": {
        "pr_notification_id": "structuredContent.items.1.notification_id"
      }
    },
    {
      "tool": "studio.work",
      "expect": {
        "structuredContent.summary.notifications_unread": 2,
        "structuredContent.summary.notifications_action_required": 2,
        "structuredContent.items.0.origin_url": "https://github.com/acme/repo/issues/7",
        "structuredContent.items.0.reacquire.tool": "session.teleport"
      }
    },
    {
      "tool": "session.teleport",
      "args": {
        "handle": "github-inbox-smoke",
        "notification_id": "${pr_notification_id}"
      },
      "expect": {
        "structuredContent.outcome.state": "foyer"
      }
    },
    {
      "tool": "session.inspect",
      "args": {
        "handle": "github-inbox-smoke"
      },
      "expect": {
        "structuredContent.async.notifications_unread": 1,
        "structuredContent.notifications.0.origin_url": "https://github.com/acme/repo/issues/7"
      }
    },
    {
      "tool": "render.tui",
      "args": {
        "handle": "github-inbox-smoke"
      },
      "expect": {
        "structuredContent.frame.metadata.state": "foyer"
      }
    }
  ]'
```

Live web and TUI sessions also poll this same GitHub intake path every five
minutes while a session is open. This MCP smoke calls `inbox.sync_github`
explicitly so the proof is deterministic and does not wait on a wall-clock
poll interval.

Expected proof:

- `inbox.sync_github` inserts one assigned issue and one review-requested PR.
- `inbox.sync_github` returns the PR deep link and `studio.work` ranks both
  GitHub rows as unread action-required work.
- `session.teleport` reacquires the PR notification by id.
- `session.inspect` shows one unread notification left and still exposes the PR
  `origin_url` for focused context.
- `render.tui` confirms the operator-visible frame is reacquired.
