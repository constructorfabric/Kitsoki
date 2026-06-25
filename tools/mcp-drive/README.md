# mcp-drive — headless kitsoki-MCP delegation primitive

`drive.sh` launches a **headless `claude -p`** with the kitsoki **studio MCP**
attached, so a delegated agent can author/validate stories and drive live
sessions entirely through the studio facade — from a script, a cron job, or
another agent — with no interactive client.

## The bug it fixes

Dispatching a kitsoki-driving agent through an **in-process subagent** (the
Agent tool / `Task`) does **not** attach the studio MCP. An in-process subagent
inherits the *parent* session's MCP set; a parent started without the kitsoki
server has none to hand down, so the subagent boots with **“No MCP servers
configured”** and can call nothing (`session.new`, `story.read`, … all absent).

The fix is to delegate to a **raw `claude -p`** with:

- `--mcp-config tools/mcp-drive/kitsoki-mcp.json` — attach the studio server fresh, and
- `--strict-mcp-config` — use *only* that file, so a stray worktree/project
  `.mcp.json` can't shadow or drop it (see MEMORY `maker-submit-strict-mcp`).

`drive.sh` encapsulates exactly that, plus the studio-tool allowlist.

## Use it

```sh
# inline prompt; orchestrator defaults to sonnet (cheap — it only clicks)
tools/mcp-drive/drive.sh "Call studio_ping and report the result."

# a real driving task from a file
tools/mcp-drive/drive.sh --prompt-file my-drive.md

# pin the orchestrator model / restrict the toolset
MCP_DRIVE_MODEL=opus MCP_DRIVE_TOOLS=mcp__kitsoki__studio_ping \
  tools/mcp-drive/drive.sh "ping"
```

It prints `claude -p`'s JSON result envelope on stdout (`result`,
`total_cost_usd`, `usage`, `session_id`). Run it in the background (`&` or a
background Bash) for long drives.

## Orchestrator model vs worker model

The **orchestrator** (`claude -p`) only *drives* the studio — it clicks
`session.new` / `session.drive` / `session.submit`. The model that actually does
the work runs **inside** the kitsoki session and is chosen per session:

```
session.new { story_path, harness: "live", profile: "codex-native" }   # → GPT-5.5
session.new { story_path, harness: "live", profile: "synthetic-claude" } # → GLM-5.2
```

So *“drive with a cheap Claude, do the work with GPT-5.5 / GLM-5.2”* is the
intended split — the orchestrator never generates the deliverable.

## Cost

`drive.sh` spends real tokens on the **orchestrator** turns (a `studio_ping`
round-trip is ~$0.10 on sonnet). Live worker sessions additionally spend on their
own profile/provider. It is operator-run, never in CI.

See MEMORY `mcp-first-delegation-runbook` for the end-to-end delegation playbook.
