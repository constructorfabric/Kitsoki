# Use Kitsoki Through MCP

This guide is for Codex, Claude Code, and other MCP-capable coding clients. It
explains which Kitsoki MCP surface to attach, how to drive stories and dynamic
workflows from a client, and when to use `kitsoki agent launch` or CodeAct mode
instead of handing a model a shell.

For the complete tool reference, see
[`../../architecture/mcp-studio.md`](../../architecture/mcp-studio.md). This
page is the operational path.

## Choose The Surface

| Need | Use | Why |
|---|---|---|
| Drive or edit Kitsoki stories from Claude/Codex | `kitsoki mcp` | The Studio MCP exposes `story.*`, `session.*`, `workflow.*`, `render.*`, `visual.*`, `trace.*`, `host.*`, `vcs.*`, `gh.*`, and `issue.*` tools over one connection. |
| Preview the agent operating system | `kitsoki mcp --operating-profile strict` | Objective-backed managed workspaces, CodeAct, typed gates, receipts, and bounded explanation; strict is **HOLD**, not the default. |
| Let a model edit code without Bash, Python, Node, or editor tools | `kitsoki mcp-codeact` | The model calls one `codeact_eval` tool; Kitsoki runs capability-scoped Starlark snippets. |
| Launch a preconfigured Claude/Codex/Copilot agent | `kitsoki agent launch` | It resolves agent files, story `agents:`, profiles, MCP attachments, launch policy, CodeAct mode, and the backend argv. |
| Make a deterministic story call from a script | `kitsoki agent <verb>` or `agent-serve` | Direct CLI/JSON-RPC access to `host.agent.*`; see [`cli.md`](cli.md). |
| Run a story-provided deterministic script | `kitsoki starlark run` or `agent-serve` method `starlark.run` | Uses the script's `.star.yaml` sidecar and the same capability sandbox as a story effect. |

Most Codex/Claude operator work starts with the Studio MCP. Use CodeAct when
the task needs code actions but the model should not receive a general shell.
Use `kitsoki agent launch` when you want Kitsoki to assemble the right backend
command rather than hand-maintain each client's MCP flags.

## Operating-system preview (HOLD)

`legacy` is the default Studio profile and preserves the established toolbox.
Use strict only by explicit operator choice while its replay matrix remains on
hold:

```json
{
  "mcpServers": {
    "kitsoki-strict-preview": {
      "command": "kitsoki",
      "args": ["mcp", "--stories-dir", "stories", "--operating-profile", "strict"]
    }
  }
}
```

In strict, open `objective.open` before `workspace.create`, mutate only through
`workspace.*` or `workspace.codeact`, run named `gate.run` gates, preserve
receipts, then call `objective.close`. Use `studio.diagnose`, `session.explain`,
or `trace.explain` for evidence-backed diagnosis. Strict intentionally omits
raw worktree/vcs lifecycle tools, `host.patch`, and arbitrary `host.run`.
`escape` is an explicit audited exception profile; it is not a default fallback.
The current hold and its `trace-stalled-turn` failure are documented in
[MCP operating-system evaluations](../../testing/mcp-operating-system.md).

## Run Story Starlark Directly

The direct runner is useful for a deterministic helper, a shell-free CI step,
or a JSON-RPC client that needs the exact story implementation without starting
an interactive session:

```sh
kitsoki starlark run stories/scenario-qa/scripts/plan_legs.star \
  --inputs '{"run_dir":".artifacts/run"}' \
  --world @world.json \
  --capabilities '{"fs":{"read":[".artifacts/**"]}}'
```

`--inputs`, `--world`, and `--capabilities` accept inline JSON, `@file.json`,
or `-` for stdin. The runner requires the sibling `.star.yaml` sidecar and
prints the returned output object as JSON. `--function name` calls an exported
function other than `main(ctx)`.

The same request is available over the newline-delimited Unix-socket daemon:

```json
{"jsonrpc":"2.0","id":1,"method":"starlark.run","params":{"script":"/repo/scripts/plan_legs.star","inputs":{"run_dir":".artifacts/run"},"capabilities":{"fs":{"read":[".artifacts/**"]}}}}
```

This is the preferred replacement for a story helper launched through a Python
shim: move deterministic logic into a `.star` script, grant only the required
capabilities, and exercise it with the same flow/cassette path used in the
story.

## Attach Studio MCP

Studio MCP and Capsule MCP serve different roles. Studio is the story authoring
and driving surface below; Capsule MCP is the least-authority coding-agent
surface for a single project/workspace. Do not give a coding agent this Studio
configuration just to let it edit a repository. Use the Capsule-only launch
profiles in [Capsule MCP authority boundary](../../architecture/capsule-mcp.md#launch-profiles)
when it must create, edit, commit, or run CI inside a governed workspace.

Onboarding writes `.mcp.json` and installs the `kitsoki-mcp-driver` agent:

```sh
kitsoki run
# type: onboard .
```

If a project is already onboarded but the toolkit is stale, refresh it:

```sh
kitsoki project-tools upgrade --apply
```

For a source checkout or manual setup, the MCP registration has this shape:

```json
{
  "mcpServers": {
    "kitsoki": {
      "command": "kitsoki",
      "args": ["mcp", "--stories-dir", "stories"]
    }
  }
}
```

Use the generated `.mcp.json` when it exists; it is the source of truth for that
project's story root.

When the MCP server starts from a downstream project but must use stories and
managed-workspace tooling from a Kitsoki source or staging checkout, select that
checkout explicitly:

```sh
kitsoki --kitsoki-repo /absolute/path/to/Kitsoki \
  mcp --stories-dir .kitsoki/stories
```

The equivalent manual MCP registration keeps the global flag before the
subcommand:

```json
{
  "mcpServers": {
    "kitsoki": {
      "command": "kitsoki",
      "args": [
        "--kitsoki-repo", "/absolute/path/to/Kitsoki",
        "mcp", "--stories-dir", ".kitsoki/stories"
      ]
    }
  }
}
```

`--kitsoki-repo` (or `KITSOKI_REPO`) supplies both `@kitsoki/<name>` imports
and the trusted managed-workspace assets under the selected checkout:
`.capsules/workspaces` and `scripts/dev-workspace.sh`. The process still reads
the downstream project's `.kitsoki.yaml` and story paths from its own working
directory, while `workspace.*` lifecycle calls pin their `--repo` and `--root`
arguments to the selected Kitsoki checkout. Point at the exact staging capsule
that contains the changes being tested, not merely another Kitsoki checkout.
`studio.ping` keeps `working_dir` set to the downstream project but compares the
server build revision with this selected Kitsoki checkout, avoiding a false
`stale: true` result caused by the downstream project's unrelated Git HEAD.

A scoped override such as
`KITSOKI_KIT_DEV_DEV_STORY=/path/to/stories/dev-story` changes only that story's
import resolution. It is useful for CLI story validation, but it does not
provide repository-level managed-workspace assets; use `--kitsoki-repo` or
`KITSOKI_REPO` for Studio MCP startup.

Claude Code reads the repo `.mcp.json` automatically in interactive sessions:

```sh
claude --agent kitsoki-mcp-driver
```

For headless Claude runs, keep the MCP config explicit:

```sh
claude -p --agent kitsoki-mcp-driver \
  --mcp-config .mcp.json --strict-mcp-config \
  "$(cat .context/drive-brief.md)"
```

Codex uses its own MCP registration. From a Kitsoki source checkout:

```sh
codex mcp add kitsoki -- kitsoki mcp --stories-dir stories
codex mcp list
```

Then restart the Codex session; attached MCP servers are discovered at session
startup. For a whole-session Codex driver with Studio MCP attached and shell
access disabled, use Kitsoki's launcher:

```sh
kitsoki agent launch --agent kitsoki-mcp-driver --backend codex
```

For a one-shot driver task:

```sh
kitsoki agent launch \
  --agent kitsoki-mcp-driver \
  --backend codex \
  --task-file .context/drive-brief.md
```

The Studio MCP tool names are dotted in Kitsoki docs, such as
`workflow.create` and `session.status`. MCP clients often expose them as
namespaced names, such as `mcp__kitsoki__workflow_create`. Use the client name
it actually shows.

## Start Every Driver Run

Use the same opening sequence for Claude, Codex, and scripted MCP clients:

1. `studio.ping` - prove the attached server is reachable and current.
2. `studio.handles` - inspect existing workspace/session handles before opening
   another one.
3. If tools are missing or schemas are stale, restart the client or reconnect
   the MCP server before debugging the story.

The `kitsoki-mcp-driver` agent is intentionally constrained: the Studio MCP is
the mutation surface. It should use `story.*` for story files, `session.*` for
driving, `render.*` / `visual.*` for UI evidence, `trace.*` for replay artifacts,
and `issue.create` when the MCP surface itself lacks a needed operation.

## Drive A Story

Read the story before driving it:

```text
story.graph {dir:"stories/bugfix"}
story.search {dir:"stories/bugfix", pattern:"ticket_id"}
story.read {path:"stories/bugfix/app.yaml"}
```

Open the session with the full seed on the first call:

```text
session.new {
  "story_path": "stories/bugfix/app.yaml",
  "harness": "replay",
  "trace": ".artifacts/mcp/bugfix.trace.jsonl",
  "initial_world": {
    "ticket_id": "local-123",
    "test_cmd": "go test ./internal/foo -run TestBug -count=1"
  }
}
```

Important defaults:

- `session.new` defaults to `harness:"replay"`, so it does not spend LLM tokens.
- Use `harness:"live"` only when the goal is real model behavior.
- Use `profile` to select the worker model or backend; do not edit a story agent
  just for one run.
- Prefer `session.submit` for known menu choices.
- Use `session.drive` when you intentionally want free-text routing.

When a live turn returns `{running}`, poll `session.status` until `running`
disappears. Use targeted reads before full snapshots:

```text
session.status {handle:"..."}
session.world {handle:"...", key:"bug_verified"}
session.trace {handle:"...", kinds:["machine.error","agent.call.complete"]}
```

Use `session.inspect` only when you need the full state, world, jobs, inbox,
operator questions, chats, and last turns in one payload.

Use the render and visual tools for UI evidence:

```text
render.tui {handle:"..."}
render.web {handle:"...", assert_text:["Ready"]}
visual.open {kind:"web", handle:"..."}
visual.observe {visual_handle:"..."}
visual.snapshot {visual_handle:"...", region:"chat", overlay:"action_ids"}
```

After edits, keep the no-LLM gate inside MCP:

```text
story.validate {dir:"stories/bugfix"}
story.test {dir:"stories/bugfix"}
```

When a live run proves a scenario, convert it to a deterministic fixture:

```text
trace.to_flow {
  trace: ".artifacts/mcp/bugfix.trace.jsonl",
  app: "stories/bugfix/app.yaml",
  out: "stories/bugfix/flows/generated_bugfix.yaml"
}
```

Then future tests and demos should use the generated flow/cassette path instead
of another live LLM run.

## Fan Out With Dynamic Workflows

Use `workflow.*` when a Codex/Claude request is broad enough to split into
tracked work items. The MCP surface creates the draft, validates it, launches a
session over the generated `punch-list` story, and exports the run when it is
worth keeping as a reusable story.

Start with either a free-text goal:

```json
{
  "goal": "audit the bugfix story docs, add missing flow coverage, and gate it",
  "slug": "bugfix-doc-flow-audit"
}
```

Or provide explicit fan-out items:

```json
{
  "goal": "harden MCP docs and gates",
  "slug": "mcp-docs-gate",
  "items": [
    {
      "id": "docs",
      "title": "Add the MCP usage guide",
      "owner_scope": "docs/guide/agents",
      "gate": "make site"
    },
    {
      "id": "links",
      "title": "Check guide navigation and links",
      "owner_scope": "tools/site docs/guide",
      "gate": "make site"
    }
  ]
}
```

Then run the lifecycle:

```text
workflow.create {...}
workflow.validate {workflow_id:"dwf_..."}
workflow.launch {workflow_id:"dwf_..."}
studio.work {}
session.status {handle:"..."}
workflow.export {workflow_id:"dwf_...", target:"stories/mcp-docs-gate"}
```

`studio.work` is the reacquire surface. It ranks running jobs, failed jobs,
unread notifications, backgrounded chats, pending operator questions, and mining
proposals across open sessions. Follow each item's `reacquire` hint instead of
scraping frames or guessing which session needs attention.

Dynamic workflow receipts live under `.artifacts/dynamic-workflows/<id>/` and
record the manifest, validation report, lifecycle events, trace path, launch
metadata, and export report. The detailed workflow contract is in
[`../../stories/dynamic-workflows.md`](../../stories/dynamic-workflows.md).

## Use CodeAct Instead Of Bash

Use CodeAct when the model should make code changes but should not receive
`Bash`, Python, Node, or direct editor tools.

There are two related surfaces:

- `host.agent.codeact` is used inside a story room. The story launches a bounded
  agent loop, and each step emits a Starlark snippet or a final `done(payload)`.
- `kitsoki mcp-codeact` is the external-client MCP server. Claude/Codex calls
  `codeact_eval`; Kitsoki evaluates the Starlark snippet with the capability
  ceiling chosen when the server started.

The easiest way to attach CodeAct correctly is `kitsoki agent launch`:

```sh
kitsoki agent launch \
  --agent codeact-worker \
  --mode codeact \
  --backend codex \
  --task-file .context/task.md
```

Interactive Codex CodeAct mode:

```sh
kitsoki agent launch --agent codeact-worker --mode codeact --backend codex
```

On Codex, CodeAct launch disables the shell tool while keeping the
`kitsoki-codeact` MCP server attached. On Claude, it permits only
`mcp__kitsoki-codeact__codeact_eval` and denies Bash plus direct editor tools.

The default launch capability ceiling is working-directory-rooted filesystem
read/write plus read-only git probes. Override it when needed:

```sh
kitsoki agent launch \
  --agent codeact-worker \
  --mode codeact \
  --backend codex \
  --codeact-capabilities-file .context/codeact-capabilities.json \
  --task-file .context/task.md
```

Manual MCP registration is possible, but prefer the launcher when the client is
Claude or Codex:

```toml
[mcp_servers.kitsoki-codeact]
command = "kitsoki"
args = ["mcp-codeact", "--working-dir", ".", "--capabilities-json", '{"fs":true,"vcs":"read"}']
```

Do not treat `mcp-codeact` as a general terminal replacement. Use it for
capability-scoped code actions. Use `host.run` for deterministic gates and test
commands. Use `story.*` for story authoring.

## Launch Agents Deliberately

`kitsoki agent launch` resolves the same agent definitions used by stories and
the installed Codex/Claude toolkit. It emits a dry-run plan by default:

```sh
kitsoki agent launch \
  --app stories/git-ops/app.yaml \
  --agent conflict_resolver \
  --task "Resolve the listed conflicts"
```

Inspect the selected backend, working directory, model, effort, env, tool
surface, MCP config, stdin prompt, and launch policy decision. Add `--exec` only
after the plan is correct:

```sh
kitsoki agent launch \
  --app stories/prd/app.yaml \
  --agent author \
  --task-file .context/prd-task.md \
  --exec
```

For freestanding MCP driver sessions:

```sh
kitsoki agent launch --agent kitsoki-mcp-driver --backend codex
kitsoki agent launch --agent kitsoki-mcp-driver --backend codex --task-file .context/drive.md
```

Use raw interactive launch only when you intentionally want the native backend
CLI with no Kitsoki agent prompt and no MCP wrapper:

```sh
kitsoki agent launch \
  --raw --interactive \
  --backend codex \
  --working-dir /tmp/kitsoki-capsules/clean-repo
```

When [`launch-policy.md`](launch-policy.md) is enabled, launch planning rejects
protected roots, protected branches, and unsafe non-capsule workspaces before it
emits a command. The policy is a preflight guard, not a kernel sandbox.

## Operational Rules

- Keep automated tests on replay, flow, and cassette paths. Do not spend real
  LLM tokens in tests unless a human explicitly chooses a gated live run.
- Treat `session.trace` and `trace.read` as ground truth for routing, host
  calls, agent calls, and swallowed `on_error` arcs.
- Close abandoned sessions with `session.close` before reopening the same trace
  path.
- File MCP gaps with `issue.create`; local artifact tickets are the default sink.
- If the MCP server was rebuilt, restart or reconnect the Claude/Codex client.
  An attached stdio server is a running process, not a live view of the new
  binary.

For a full end-to-end dogfood runbook, use
[`../../recipes/studio-mcp-dogfood.md`](../../recipes/studio-mcp-dogfood.md).
