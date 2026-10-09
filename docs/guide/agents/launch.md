# Agent Launch CLI

`kitsoki agent launch` turns an existing Kitsoki agent definition into a
concrete Claude, Codex, or Copilot CLI command. It is a resolver over story
`agents:`, freestanding `.codex/agents/*.toml` files, and harness profiles. It
is not a second agent schema.

Task-backed launches are no-provider dry runs by default. They print the exact
argv, working directory, backend, model, tool surface, redacted env, and launch
policy decision that would be used. Add `--exec` only when you want to run a
task-backed backend CLI. Interactive launches are different: a freestanding
launch with no task, or `--raw --interactive`, opens the native backend CLI.

## Use Cases

Dry-run a story agent from a story `app.yaml`:

```sh
kitsoki agent launch --app stories/git-ops/app.yaml --agent conflict_resolver --task "Resolve the listed conflicts"
```

Actually run a task-backed story agent after inspecting the plan:

```sh
kitsoki agent launch --app stories/prd/app.yaml --agent author --task-file .context/prd-task.md --exec
```

Launch a freestanding agent task from `.codex/agents/<name>.toml`:

```sh
kitsoki agent launch --agent kitsoki-mcp-driver --backend codex --task-file .context/drive.md
```

For `kitsoki-mcp-driver`, Codex launch automatically passes
`--disable=shell_tool`; the driver keeps the kitsoki studio MCP attached but
does not receive a shell.

Open an interactive Codex TUI with the same freestanding agent instructions and
MCP attachments:

```sh
kitsoki agent launch --agent kitsoki-mcp-driver --backend codex
```

Run a CodeAct task agent whose only code-action surface is `mcp-codeact`:

```sh
kitsoki agent launch --agent codeact-worker --mode codeact --backend codex --task-file .context/task.md
```

Open an interactive Codex TUI in CodeAct mode:

```sh
kitsoki agent launch --agent codeact-worker --mode codeact --backend codex
```

Open a raw backend CLI without any app, agent file, MCP wrapper, or Kitsoki
replacement prompt:

```sh
kitsoki agent launch --raw --interactive --backend codex --working-dir /tmp/kitsoki-capsules/clean-repo
```

## Agent Sources

Story-backed launch uses `--app` plus `--agent`. The agent comes from that
story's top-level `agents:` block and supplies the persona, cwd, tools, model,
effort, and provider name.

Freestanding launch omits `--app` and resolves `.codex/agents/<name>.toml` from
the current project, or the file passed with `--agent-file`. The file supplies
developer instructions, model, effort, and optional `[mcp_servers.*]` blocks.

For freestanding Codex agents, `[mcp_servers.*]` blocks are materialized into
the same `--mcp-config` shape Claude uses, then translated into Codex
`-c mcp_servers...` overrides. This is the Codex analogue of launching a Claude
Code agent with the studio MCP attached.

Freestanding task launch uses `codex exec`. Freestanding launch with no task,
or with `--interactive`, uses top-level `codex [OPTIONS] [PROMPT]`, so the
terminal opens the Codex TUI with the agent instructions as the initial prompt.
The built-in `kitsoki-mcp-driver` is special-cased because its contract is
studio-MCP-only: Codex launch disables `shell_tool` for that agent while keeping
`mcp_servers.kitsoki` available.

Raw interactive launch also uses the backend's top-level interactive CLI, but
passes no app/agent prompt and no MCP config. It is intended for native
logged-in host CLI sessions, especially macOS subscription workflows.

## Backends And Profiles

Use `--backend codex`, `--backend claude`, or `--backend copilot` to select a
backend explicitly. Use `--profile <name>` to select a harness profile from
`.kitsoki.yaml` / `.kitsoki.local.yaml`.

For story-backed launch, resolution composes three layers:

1. Story `agents:` supplies the persona, default cwd, tools, model, effort, and
   provider name.
2. Harness profiles supply backend, model, effort, and provider env. The config
   `default_profile` is used when present unless an explicit backend changes the
   backend family.
3. Task flags supply per-launch overrides such as `--working-dir`, `--model`,
   `--effort`, `--backend`, `--add-dir`, and `--env KEY=VALUE`.

Profile model and effort override story-local defaults so a Codex or
synthetic.new profile does not inherit a Claude-only model id from a story
agent. When `--backend` explicitly selects a different backend than the
implicit default profile, that default profile's model, effort, and env are not
applied. Pass an explicit matching `--profile` when you want profile settings.
An explicit `--profile` whose backend conflicts with `--backend` is rejected.
Extra env values are merged last and are redacted in dry-run output.

For freestanding launch, the harness profile still wins for
backend/model/effort/env so operators can reuse the same profile names as story
sessions. Interactive freestanding launch currently supports Codex only.

## CodeAct Mode

`--mode codeact` is a launch mode, not an agent source. You still pass
`--agent` or `--agent-file`; the mode replaces the launched tool surface with
the standalone `kitsoki mcp-codeact` server.

Keep the two committed Codex profiles separate:

| Profile | Intended MCP surface |
|---|---|
| `kitsoki-mcp-driver` | Kitsoki Studio MCP (`kitsoki mcp`) for story/session orchestration. |
| `kitsoki-codeact-driver` | Kitsoki CodeAct MCP (`kitsoki mcp-codeact`) for direct repository edits through `codeact_eval`. |

`kitsoki-mcp-driver` proves the orchestrator stayed inside Kitsoki Studio MCP;
it does not prove the implementation worker used CodeAct. For direct
limited-permission benchmark cells, launch `kitsoki-codeact-driver` with
`--mode codeact`.

CodeAct mode is for agents that should edit code without receiving Bash,
Python, Node, a shell, or direct editor tools. The launched model supplies
Starlark snippets to `codeact_eval`; the server enforces the capability ceiling
from its startup args.

Supported CodeAct launches:

- Codex one-shot task launch.
- Codex interactive freestanding launch.
- Claude one-shot task launch.

Unsupported CodeAct launches fail during planning:

- Raw interactive launch, because raw mode intentionally skips the agent/MCP
  wrapper.
- Interactive Claude freestanding launch, because Kitsoki's interactive
  freestanding path is Codex-only today.
- Backends that cannot hard-remove shell access.

On Codex, CodeAct launch passes `--disable=shell_tool`, so the shell tool is
not present, while the CodeAct MCP server remains attached. Codex one-shot
launch still uses `--dangerously-bypass-approvals-and-sandbox` because Codex
currently requires that flag for non-interactive MCP calls; CodeAct's no-shell
posture comes from disabling the shell tool and routing edits through
`mcp-codeact`.

On Claude, CodeAct launch permits only
`mcp__kitsoki-codeact__codeact_eval` and hard-denies `Bash` plus direct editor
tools.

Without an explicit `--backend` or `--profile`, CodeAct mode defaults to Codex
and drops an implicit non-Codex default profile. If an explicit `--backend` or
`--profile` selects an unsupported backend, planning fails instead of relying
on prompt instructions.

The default CodeAct capability ceiling is working-directory-rooted filesystem
read/write through `ctx.fs` plus read-only git probes through `ctx.probe`. It is
passed to `mcp-codeact` as `{"fs":true,"vcs":"read"}`. Override it with
`--codeact-capabilities-json` or `--codeact-capabilities-file`.

For clients outside `kitsoki agent launch`, attach the same server manually:

```toml
[mcp_servers.kitsoki-codeact]
command = "kitsoki"
args = ["mcp-codeact", "--working-dir", ".", "--capabilities-json", '{"fs":true,"vcs":"read"}']
```

## Delegation

`run_as_user` delegation is currently disabled at runtime. Kitsoki still parses
existing `.kitsoki.local.yaml` `agent_user_delegation:` blocks so local config
files keep loading, but `kitsoki agent launch` ignores `wrapper_bin`, launches
the normal backend binary, and omits `run_as_user` from the launch plan:

```yaml
agent_user_delegation:
  enabled: true
  run_as_user: kitsoki-agent
  wrapper_bin: /Users/Shared/kitsoki/agent-bin
```

The setup story and receipt shape remain in the tree for a future re-enable,
but missing or incomplete delegation config does not produce a macOS setup
warning while runtime delegation is disabled.

## Dry Run Output

The launch plan includes:

- the selected backend and binary,
- the concrete argv after backend translation,
- the resolved working directory,
- the effective model and effort,
- the resolved tool surface,
- redacted provider environment keys,
- the stdin prompt that will be sent to the backend.
- the `launch_policy` decision when `agent_launch_policy:` is enabled.

Tests and normal dry-run inspection do not call live LLMs.

## Backends

The command builds the same neutral Claude-shaped invocation used by host agent
handlers, then asks the host backend translator for the concrete invocation.
That means Codex dry-runs show the real `codex exec ...` argv, including model
and working-directory translation, while Claude dry-runs show the direct Claude
argv.

`--exec` uses the same host runner as the existing harness path, with the
selected backend installed on context and provider env applied to the child
process.

## Safety

Read-only agents (`external_side_effect: false`) launch with Claude's enforcing
`default` permission mode and a hard deny-list for mutation tools. All launches
deny headless escape tools such as `AskUserQuestion`, `Agent`, and `Task`.

When `.kitsoki.yaml` / `.kitsoki.local.yaml` enables
[`agent_launch_policy:`](./launch-policy.md), launch planning rejects
protected roots, protected branches, and non-capsule workspaces before emitting
a command plan. The same guard applies to raw interactive sessions.

Launch policy is a preflight guard, not a kernel/filesystem sandbox. Use it to
keep agents out of the protected checkout and inside opened capsules. The
macOS `run_as_user` wrapper path is temporarily disabled, so write-capable
backend CLIs currently run as the invoking user. Use `with.sandbox` on hosted
calls when a story also needs runtime supervision.
