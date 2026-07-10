# Agent Launch Policy

`agent_launch_policy:` is a machine-local preflight guard for external coding
agent launches. It rejects unsafe working directories before Kitsoki forks
`claude`, `codex`, `copilot`, `agy`, or an agent plugin path.

The policy is not a filesystem sandbox. It is the first auditable boundary:
protect the operator-owned checkout, keep delegated agents in prepared
workspaces, and record the launch decision. `sandbox:` runtime policy can still
add subprocess supervision for calls that opt into it.

## Configuration

Put machine-specific policy in `.kitsoki.local.yaml` so every operator can map
their own workspace layout without breaking the shared checkout:

```yaml
agent_launch_policy:
  enabled: true
  require_capsule: true

  # Defaults to the directory containing .kitsoki.yaml when omitted.
  protected_roots:
    - .

  # Explicit workspaces agents may use. These are also carve-outs from
  # protected_roots, so capsules under .worktrees can be allowed while the
  # primary checkout stays protected.
  allowed_roots:
    - ./.worktrees/capsules
    - /tmp/kitsoki-capsules

  # Defaults when omitted: main, master, trunk, integration/*, staging/*.
  protected_branches:
    - main
    - master
    - trunk
    - integration/*
    - staging/*
```

`protected_roots` and `allowed_roots` are resolved relative to the config file
and normalized to absolute paths at load time. `protected_branches` are git
branch patterns, not paths.

## Semantics

When enabled, a launch is allowed only when all checks pass:

1. The resolved `working_dir` exists and is a directory.
2. If `allowed_roots` is non-empty, `working_dir` must be inside one of them.
3. `working_dir` and its git root must not be inside `protected_roots`, unless
   an explicit `allowed_roots` entry carves that workspace out.
4. A non-capsule git checkout must not be on a protected branch.
5. If `require_capsule` is true, `working_dir` must be inside an opened Kitsoki
   capsule, identified by `.kitsoki-capsule` plus `capsule-manifest.json`.

Opened capsules may contain normal fixture branches such as `main`. The branch
guard is intended to protect real worktrees and integration branches, not the
throwaway git repositories inside capsule workspaces.

Use [`kitsoki capsule open`](../development/capsules.md) to create an opened capsule
workspace, then pass that path as the agent `working_dir`.

## Enforcement Surface

The same policy is installed for:

- `host.agent.task`, before subprocess or plugin dispatch;
- `host.agent.converse`, before subprocess or plugin dispatch;
- `host.agent.codeact`, before the codeact runner starts;
- sessions created by `kitsoki run`, `kitsoki web`, and `kitsoki mcp`;
- `kitsoki agent launch`, including dry-run planning and raw interactive
  launches.

For host calls, allowed and denied decisions emit an `agent.launch.policy` event
when a trace/event sink is attached. For `kitsoki agent launch`, an allowed
decision appears in the dry-run JSON plan as `launch_policy`; denied launches
return an error before a command plan is emitted.

Decision records include the verb, agent name, working directory, git root,
branch, matching protected root/branch, capsule name/root/spec path, and the
effective policy lists. They never include provider secrets.

## Raw Interactive Launches

Use `kitsoki agent launch --raw --interactive` to start a normal interactive
backend CLI without an app, agent file, MCP wrapper, or Kitsoki replacement
system prompt:

```sh
kitsoki agent launch --raw --interactive --backend codex --working-dir /tmp/kitsoki-capsules/clean-repo
```

This path is useful on macOS, where operators often have Claude Code or Codex
subscription auth in the native host CLI. It still runs the launch policy
preflight first, so a raw session cannot accidentally start in the protected
main checkout when policy is enabled.

Raw interactive launch supports `codex` and `claude` backends. Harness profiles
may still supply backend, model, effort, and environment retargeting; they do
not supply any app or agent prompt.

## Delegated macOS User Setup

`run_as_user` delegation is currently disabled at runtime. The setup story and
config shape remain documented here for later re-enablement, but Kitsoki now
parses existing `agent_user_delegation:` blocks without using the wrappers,
without recording `run_as_user` in launch plans, and without surfacing the
macOS setup warning.

On macOS, `agent_launch_policy:` should be paired with a separate Standard user
for coding-agent backends. The policy rejects unsafe launch locations, but the
OS user boundary is what makes the protected checkout unwritable after the
backend starts.

Run the setup story for the guided no-LLM setup flow:

```sh
kitsoki run @kitsoki/run-as-user-setup
```

The story can show the generated `.kitsoki.local.yaml` blocks, root-owned
backend wrappers, sudoers snippet, capsule-assignment commands, and validation
probes before applying anything. When the operator chooses `apply`, it uses
non-interactive `sudo -n` to create or reuse the local account/group, install
the wrappers and sudoers file, set up the sample capsule permissions, and run
the delegated write/write-deny probes. If macOS needs a password, the story
stops in an authorization screen and asks the operator to run `sudo -v`, then
retry.

The local receipt block is:

```yaml
agent_user_delegation:
  enabled: true
  run_as_user: kitsoki-agent
  wrapper_bin: /Users/Shared/kitsoki/agent-bin
  capsule_root: /Users/Shared/kitsoki/capsules
```

This block is a local receipt for the OS-user delegation setup. While runtime
delegation is disabled, it is only parsed and path-resolved; it does not affect
launch binaries, launch plans, TUI startup notices, or web setup warnings.

When runtime delegation is re-enabled, live agent surfaces that do not consume
`agent_user_delegation.wrapper_bin` directly will still need the wrapper
directory first in `PATH`:

```sh
PATH=/Users/Shared/kitsoki/agent-bin:$PATH kitsoki run @kitsoki/dev-story
```

## Relationship To Sandboxing

`agent_launch_policy:` is fail-fast placement control. It answers "may this
agent start here?" before the backend runs. It does not constrain the child
process after launch.

`with.sandbox` on a host call is runtime control. Today the open-source
`supervised` backend records requested repo/rw/hidden/network policy, uses a
temporary HOME/XDG, controls process lifetime, and captures final diff evidence.
It records degradation when stronger filesystem/network controls are requested
but unavailable.

Together they provide the first practical step toward full sandboxing:

- policy prevents obvious unsafe launch locations;
- capsules provide reproducible, disposable workspaces;
- runtime events prove what was requested and what was actually applied;
- future macOS local-user, Linux namespace, Docker, VM, or SSH backends can
  enforce stronger confinement without changing story authoring vocabulary.
