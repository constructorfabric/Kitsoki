# Runtime: secure agent runtime and sandbox backends

**Status:** Draft v2. Re-scoped from a bwrap-first filesystem sandbox into a
pluggable agent-runtime boundary. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [agent-capability-model.md](agent-capability-model.md) (slice 3 — the OS/runtime enforcement layer)

## Why

Kitsoki can already reduce agent damage with story/runtime policy:
`write_mode: read_only` boots agents into a read-only posture and routes
mutating Bash attempts through the operator gate
([`docs/stories/state-machine.md`](../stories/state-machine.md#write_mode),
[`docs/architecture/hosts.md`](../architecture/hosts.md#write-mode-gate)).
That is necessary, but it is not enough.

The remaining problem is below the tool layer. A `host.agent.task` or
write-capable `host.agent.converse` ultimately forks a coding-agent CLI. If the
process can run arbitrary Bash, Python, package managers, helper binaries, or
backend-specific tool calls, then `working_dir` is not a jail. The existing docs
say the same in several places:

- `host.agent.task` can run under `bypassPermissions` and mutate from turn one
  unless a room opts into `write_mode: read_only`.
- `bash_profile: sandboxed_write` is useful policy, but network denial is a
  best-effort `HTTP_PROXY` trick; raw TCP is not blocked
  ([`docs/stories/authoring.md`](../stories/authoring.md#bash-profiles)).
- The Codex backend bypasses Codex's own sandbox so MCP submit tools work
  ([`docs/guide/agents/backends.md`](../guide/agents/backends.md));
  therefore Kitsoki must own the process boundary.

The product requirement is also sharper than "run Docker." Default local users
should be able to download Kitsoki and go, especially on macOS and Linux. Docker
Desktop is too heavy and too externally controlled to be the default dependency.
Hosted Linux can use stronger isolation, and the proprietary side may ship
Firecracker/Kubernetes/cloud backends, but the OSS core needs a stable runtime
interface plus useful local backends.

One sentence: *every write-capable agent subprocess runs through a Kitsoki-owned
runtime boundary that records the actual confinement applied; the first local
Linux boundary is Landlock/supervision, not Docker.*

## What changes

Add an `AgentRuntime` layer under `host.agent.*`. It wraps the backend CLI
launch (`claude`, `codex`, `copilot`, or future plugins), applies filesystem and
resource policy, supervises the process tree, and returns a machine-readable
`AppliedPolicy` recorded in the trace.

The runtime is selected by capability, not by a single hard-coded backend:

1. `none` — explicit unsafe mode for development only.
2. `supervised` — all platforms: timeout, process-group cleanup, environment
   filtering, temporary HOME/XDG dirs, rlimits where available, final diff
   capture.
3. `fs_confined` — kernel-enforced filesystem policy: repo read-only, explicit
   read-write workspace/worktree/scratch paths. Linux Landlock is the first
   no-daemon backend; macOS native sandboxing is best-effort.
4. `os_confined` — namespace/jail-level isolation: mount/PID/network/seccomp and
   stronger resource controls via bubblewrap, nsjail, FreeBSD jails, or similar.
5. `vm_confined` — microVM/VM/orchestrated isolation: Firecracker, Lima, WSL2,
   Kubernetes jobs.

The story-facing `sandbox:` vocabulary remains opt-in and asks for minimum
strength and policy. The runtime chooses the best available backend that
satisfies the request, or fails/degrades according to explicit policy. A
degraded run must never look like a confined run in the trace.

## Impact

- **Code seams:** `internal/host/agent_runner.go` and `internal/host/agent_task.go`
  for launch wrapping; `internal/host/agent_converse.go` for write-capable
  conversations; a new `internal/host/agentruntime/` package; loader/types
  changes in `internal/app/types.go` and `internal/app/loader.go`.
- **Vocabulary:** extend the existing proposed `sandbox:` block with
  `min_strength`, `rw`, `network`, `resources`, `degrade`, and optional
  `persist` policy.
- **Stories affected:** none by default. Existing stories keep current behavior
  unless they declare `sandbox:` or an app/session config requires a runtime
  minimum. First adopters should be write-capable tasks such as design authoring
  and implementation worktrees.
- **Backward compat:** opt-in. A `sandbox:` task with no acceptable backend
  either fails closed or degrades loudly depending on `degrade`.
- **Docs on ship:** `docs/architecture/hosts.md`,
  `docs/guide/agents/backends.md`,
  `docs/stories/state-machine.md`, `docs/stories/authoring.md`, and the
  `kitsoki-story-authoring` skill.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| config | `sandbox.min_strength` | `supervised \| fs_confined \| os_confined \| vm_confined` | The minimum acceptable runtime guarantee. |
| config | `sandbox.repo` | `read_only \| none` | `read_only` is the normal write-agent posture. |
| config | `sandbox.rw` | list of templated paths | Explicit read-write doors: worktree, workspace, scratch, cache. |
| config | `sandbox.hidden` | list of templated paths | Paths that must not be visible or writable, such as credential dirs. |
| config | `sandbox.network` | `inherit \| deny \| model_only \| allowlist` | `model_only` is a future policy; v1 may treat it as `inherit` with a warning. |
| config | `sandbox.resources` | `{ timeout, memory, cpus, pids, file_size, fds }` | Backends apply what they can and record gaps. |
| config | `sandbox.degrade` | `fail \| warn` | Whether falling below `min_strength` aborts or records degraded execution. |
| event | `agent.runtime.start` | `{ backend, strength, policy, degraded[] }` | Emitted before launch. |
| event | `agent.runtime.end` | `{ exit_code, signal, resources, killed }` | Emitted after process-tree cleanup. |
| event | `TaskPersist` | `{ paths }` | Deferred persist layer: files copied out of a confined workspace. |
| event | `TaskPersistOverride` | `{ path, rationale, verdict, confidence }` | Deferred override gate for out-of-allowlist persist. |

Example:

```yaml
agents:
  impl_writer:
    system_prompt_path: prompts/implement.md
    tools: [Read, Grep, Glob, Edit, Write, Bash]
    external_side_effect: false

on_enter:
  - invoke: host.agent.task
    with:
      agent: impl_writer
      sandbox:
        min_strength: fs_confined
        repo: read_only
        rw:
          - "{{ world.worktree_path }}"
          - "{{ world.scratch_dir }}"
        hidden:
          - "{{ env.HOME }}/.ssh"
          - "{{ env.HOME }}/.aws"
        network: inherit
        resources:
          timeout: 20m
          memory: 8GiB
          cpus: 2
          pids: 256
        degrade: fail
      acceptance:
        schema: schemas/implementation_result.json
```

## The model

```
host.agent.task / write-capable converse
       │
       ▼
resolve backend CLI invocation
       │
       ▼
AgentRuntime.Probe + select backend by requested min_strength
       │
       ├─ supervised       all platforms: env, temp HOME, timeout, rlimits
       ├─ linux-landlock   fs_confined: repo RO, explicit RW paths
       ├─ darwin-local     fs_confined-ish best effort, recorded as such
       ├─ bwrap/nsjail     os_confined optional stronger Linux backend
       └─ firecracker/k8s  vm_confined hosted/proprietary plugin
       │
       ▼
agent process tree runs with applied policy
       │
       ▼
engine captures diff/artifacts, validates acceptance, optionally persists
```

The interpretive step is still the agent. The deterministic runtime does not
decide whether a change is good; it only controls what the process can touch and
records the boundary applied. The existing moat stays intact:

- agent output crosses into Kitsoki through schema validation and acceptance;
- write-mode grants remain operator decisions, not LLM decisions;
- future persist overrides are explicit `agent.decide`/operator decisions and
  are recorded.

## Backend choices

### `supervised` — all platforms, OSS core

This backend is the always-available floor:

- run the agent in its own process group;
- kill the whole tree on timeout/cancel;
- set a temporary HOME/XDG/cache layout by default;
- pass an environment allowlist instead of ambient shell state;
- apply Unix `setrlimit` for file size, descriptors, processes, CPU/wall
  where useful;
- capture final diff and artifacts.

This is not a sandbox, but it removes many accidental damage channels and gives
every platform the same runtime event shape.

### `linux-landlock` — first no-daemon Linux confinement

Landlock is the preferred first Linux backend because it is unprivileged,
Go-friendly, and does not require Docker, an image store, or a daemon. The
backend should run a small supervisor/child that applies Landlock rules before
`exec`:

- repo root read-only;
- declared `rw` paths read-write;
- credential and sibling-repo paths unavailable or at least unwritable;
- inherited network in v1 unless a supported Landlock ABI/network policy is
  detected;
- optional cgroup-v2 resource manager if the current user/session has delegated
  cgroup control.

Sources for implementation research:

- <https://www.kernel.org/doc/html/latest/userspace-api/landlock.html>
- <https://github.com/landlock-lsm/go-landlock>

### `darwin-local` — useful local Mac damage prevention

macOS should have a local backend because Mac developers are a first-priority
audience. The target is honest best-effort behavior:

- native filesystem sandboxing where available;
- temp HOME/XDG/cache;
- process group and timeout cleanup;
- `setrlimit` where supported;
- trace `strength=fs_confined` only for policies the backend can actually
  enforce, otherwise `strength=supervised` with `degraded[]`.

This backend should not pretend to provide Linux cgroup-level isolation. Strong
local Mac isolation is a separate VM runner.

### Optional local/hosted backends

- `linux-bwrap`: stronger local Linux filesystem/process/network shape via
  bubblewrap. Good optional backend, but not the zero-setup default.
- `linux-nsjail`: stronger hosted/CI isolation through namespaces, cgroups,
  rlimits, and seccomp-bpf. Good advanced/proprietary backend.
- `lima-linux`: optional strong local Mac runner that uses the Linux backend
  inside a Lima VM without Docker Desktop.
- `wsl2-linux`: supported Windows path; run Kitsoki under WSL2 and use the
  Linux backend available there.
- `firecracker`: hosted/proprietary microVM backend.
- `k8s`: enterprise/proprietary orchestration backend using pods, volumes,
  service accounts, resource limits, and NetworkPolicy.
- `runc/libcontainer`: possible future OCI backend, but not the starting point;
  rootless/userns, OCI bundle/rootfs, and image lifecycle are too much surface
  for the first process sandbox.

## Decision recording

Every live agent launch emits `agent.runtime.start` before the subprocess runs:

```json
{
  "backend": "linux-landlock",
  "strength": "fs_confined",
  "repo_ro": true,
  "rw_paths": [".../.worktrees/item", ".../.artifacts/session/scratch"],
  "network": "inherit",
  "resources": {
    "timeout": "20m",
    "memory": "8GiB",
    "cpus": 2
  },
  "degraded": ["cgroup_memory_unavailable"]
}
```

If `sandbox.min_strength: fs_confined` and the selected backend only provides
`supervised`, the run either fails before launch or emits a degraded start event
depending on `sandbox.degrade`. The trace is the contract: no unconfined run may
look equivalent to a confined run.

`agent.runtime.end` records exit status, whether Kitsoki killed the process
tree, and best-effort resource usage. If the backend cannot provide usage, it
records that gap.

## Engine seams & invariants

- `internal/host/agentruntime` owns:
  - `Runtime` interface;
  - `Probe(ctx) Capabilities`;
  - `Launch(ctx, LaunchSpec) (*Running, AppliedPolicy, error)`;
  - backend registry and selection by `min_strength`;
  - fake backend for tests.
- `agent_runner.go` remains the backend-CLI normalization point. Runtime wrapping
  happens after backend translation, before process start.
- `agent_task.go` builds `LaunchSpec` from `with.sandbox`, `working_dir`,
  repo root, acceptance/post-command needs, and app/session runtime config.
- `agent_converse.go` only uses the runtime for write-capable conversations; a
  read-only conversation remains tool-policy-only.
- Loader invariants:
  - `sandbox.min_strength` is known;
  - every `rw`/`hidden` path is non-empty after templating or rejected at runtime
    before launch;
  - `sandbox.degrade` is `fail` or `warn`;
  - `sandbox.network: model_only` warns until a backend can prove it;
  - `write`/`external` agent tasks without `sandbox:` continue to load, but the
    future toolbox slice may warn.
- Replay/cassette mode does not re-run live sandboxes. It replays recorded
  outputs and asserts the trace contains the runtime policy that existed in the
  live run.

## Backward compatibility / migration

Default-off. Existing stories, cassettes, and agent declarations keep working.

Migration is incremental:

1. Add the runtime package and `supervised` backend with no story vocabulary
   change.
2. Add `sandbox:` parsing and trace events, still default-off.
3. Adopt `sandbox:` in one or two risky write-agent call sites.
4. Move shipped semantics into architecture/story docs and trim this proposal.

Hosted deployments can set a session/app-level minimum later, e.g. "every
write-capable agent must be at least `fs_confined`." Local developer defaults
should remain pragmatic and visible rather than surprising.

## Tasks

```
## Phase 0 — Probe and policy spike
- [ ] 0.1 Define `Strength`, `NetworkPolicy`, `ResourcePolicy`,
        `PersistPolicy`, and `AppliedPolicy`; validate names against this proposal.
- [ ] 0.2 Linux spike: run a fake agent under Landlock, prove repo writes fail
        and declared workspace writes succeed.
- [ ] 0.3 macOS spike: determine the native filesystem sandbox shape Kitsoki can
        invoke reliably; document exactly what is enforceable.
- [ ] 0.4 Resource spike: process-group kill + rlimit behavior on macOS/Linux;
        cgroup-v2 detection on Linux.

## Phase 1 — Runtime substrate
- [x] 1.1 Add `internal/host/agentruntime` interface, registry, capability probe,
        and fake backend.
- [x] 1.2 Implement `supervised` backend: env allowlist, temp HOME/XDG/cache,
        process group, timeout/cancel, and final diff capture. Rlimits remain
        a deferred backend-strength enhancement.
- [x] 1.3 Emit `agent.runtime.start/end` from the sandboxed agent path.
- [x] 1.4 Unit tests with fake agent scripts; no LLM and no external services.

## Phase 2 — Linux filesystem confinement
- [ ] 2.1 Implement `linux-landlock` backend behind build tags.
- [ ] 2.2 Tests: fake agent attempts repo write, sibling-repo write, HOME write,
        workspace write, temp write; only declared writes succeed.
- [ ] 2.3 Add optional cgroup-v2 probe/application where available; record
        degradation when unavailable.

## Phase 3 — Schema and host wiring
- [x] 3.1 Add `sandbox:` schema/types/load validation.
- [x] 3.2 Wrap `host.agent.task` launches after backend translation.
- [x] 3.3 Wrap write-capable `host.agent.converse` launches where applicable.
- [x] 3.4 Replay/cassette assertions: sandbox metadata replays; no live sandbox
        or LLM is invoked.

## Phase 4 — Local platform backends
- [ ] 4.1 Implement `darwin-local` best-effort backend or explicitly document
        the unsupported pieces found in Phase 0.
- [ ] 4.2 Add optional `linux-bwrap` backend if the `bwrap` binary is present.
- [ ] 4.3 Document WSL2 and Lima as strong-local Linux runtime paths, without
        making either required for normal startup.

## Phase 5 — Adopt and prove
- [x] 5.1 Adopt `sandbox:` in a high-risk write-agent path such as design
        authoring or implementation worktrees.
- [x] 5.2 Add flow fixtures with fake agents proving denied repo writes and
        accepted workspace writes.
- [ ] 5.3 Run one explicitly requested live e2e only after the no-LLM gates pass.

## Phase 6 — Persist/override and hosted plugins
- [ ] 6.1 Add optional persist allowlist from confined workspace to repo plus
        `TaskPersist` trace events.
- [ ] 6.2 Add out-of-allowlist persist override gate with recorded decision.
- [ ] 6.3 Define external plugin contract for Firecracker/Kubernetes/nsjail
        runtimes; proprietary implementations can live outside OSS core.
- [ ] 6.4 Migrate shipped runtime semantics to docs and trim/delete this proposal.
```

## Verification

All required tests are no-LLM:

- fake agent binary attempts writes to repo, sibling repo, HOME, `/tmp`, and
  declared workspace;
- fake agent forks a child and sleeps; cancellation kills the whole process tree;
- fake agent exceeds file-size/process/timeout limits where the backend supports
  them;
- loader tests reject malformed `sandbox:` blocks;
- replay tests assert recorded runtime policy without invoking a live backend;
- flow fixtures cover the adopted story call site with cassettes/fakes.

Live LLM verification is optional and gated. It should only run after the fake
agent tests prove the boundary and should be explicitly requested because it
incurs cost.

## Open questions

1. **macOS native backend shape** — is filesystem sandboxing reliable enough to
   advertise `fs_confined`, or should macOS default to `supervised` plus an
   optional Lima runner for real confinement? *Lean: ship best-effort with honest
   trace strength; recommend Lima for strong local isolation.*
2. **Network policy v1** — can `model_only` be enforced locally without proxying
   model calls through Kitsoki? *Lean: v1 supports `inherit` and `deny`; treat
   `model_only` as future unless a backend proves it.*
3. **Resource limits on developer machines** — should `memory`/`cpus` be hard
   requirements or best-effort local policy? *Lean: hard when
   `min_strength >= os_confined`; recorded best-effort for `supervised` and
   `fs_confined`.*
4. **Plugin boundary** — Go plugin, subprocess, or RPC for proprietary runtimes?
   *Lean: subprocess/RPC contract so OSS binaries do not need Go plugin ABI
   coupling.*
5. **Default posture once stable** — should write-capable tasks warn when no
   `sandbox:` is declared? *Lean: warn after toolbox/effect taxonomy lands, but
   do not break existing stories immediately.*

## Non-goals

- Docker as the default local runtime.
- A general sandbox for the Kitsoki server process; this proposal scopes only
  spawned agent subprocesses and their descendants.
- Replacing `write_mode: read_only`; this proposal sits below it.
- Full network egress policy in v1.
- Shipping Firecracker/Kubernetes in OSS core.
- Making Windows native a first-class backend; Windows support is through WSL2.
