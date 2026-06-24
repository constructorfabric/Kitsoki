# Runtime: confine a write/external agent's filesystem writes (OS sandbox)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [agent-capability-model.md](agent-capability-model.md) (slice 3 — the OS enforcement layer)

## Why

An agent whose toolbox resolves to the `write` or `external` class — chiefly
`host.agent.task`, but any write-capable `converse` too — can write anywhere
its tools reach. `working_dir` is not a jail (`internal/host/agent_task.go:284`
just sets the subprocess CWD). Real incident: `design_author`, asked for a
*proposal document*, implemented the idea instead — wrote `cmd/kitsoki/web.go` +
`internal/runstatus/server/live.go` + a `/actions→/intents` rename alongside
`docs/proposals/web-ui.md`.

This slice is the **kernel layer of the [capability
model](agent-capability-model.md)**: [slice 1](effect-taxonomy.md) names the
`write`/`external` class, [slice 2](toolbox-and-enforcement.md) enforces a tool
allowlist on it — but the allowlist only governs *named tools*. Three weaker
fixes were considered and rejected as *the* boundary:
- **Prompt-hardening** (shipped) lowers the odds but a capable model still
  goes off-lane.
- **Mediating the `Read`/`Write`/`Edit` tools** (earlier draft of this
  proposal) only covers *those* tools — a task with `Bash` can
  `python -c 'open(...).write()'` or `sed -i` straight past them.
- **The slice-2 tool allowlist** stops a *denied* tool, but a `write`-class
  agent legitimately *holds* `Bash`/`Write` — so the allowlist can't be the
  jail for the writes it's meant to permit-yet-confine.

The boundary has to sit **below the tools**, at the OS, so *no* tool —
present or future, built-in or shelled-out — can write outside the agent's
declared output. Proven feasible on this host (kernel 5.14, RHEL 9.4):
`bwrap` is installed and unprivileged user namespaces work; a PoC bound the
repo read-only and a scratch dir read-write, and python/sed/bash writes to
the repo all failed with `EROFS` while the scratch write succeeded.

## What changes

A `write`/`external`-class agent declared with **`sandbox:`** (in practice a
`task`, the first adopter) runs its claude-cli subprocess inside an OS sandbox:
the repo is **read-only**, a single per-task **workspace is read-write**, and
everything else is unavailable. The agent
(and anything it spawns) can only write into the workspace; the engine then
**validates and persists** that workspace diff into the repo. Writing outside
the workspace is denied by the kernel, not by a prompt or a tool wrapper.

One sentence: *the interpretive step runs in a box whose only writable door
is the workspace, and the engine decides what leaves the box.*

## Impact

- **Code seams:** `internal/host/agent_task.go` (the claude-cli spawn at
  ~:284 — wrap argv in the sandbox launcher); a new
  `internal/host/tasksandbox/` with a **pluggable backend** (bwrap → landlock
  → degraded) and the mount/ruleset assembly; `internal/app/types.go` (a
  `Sandbox` field on the task invoke); load-time validation.
- **Vocabulary:** one config block (`sandbox:`), the persist/override events
  (table below). No change to `set:`/`bind:`/`once:`/the acceptance loop.
- **Stories affected:** none forced. `design_author` is the first adopter
  (workspace = `docs/proposals/.workspace/<slug>`; it already writes only
  there and `publish_design.py` moves the result out — so confinement is a
  natural fit). bugfix/implementation/cypilot writers can adopt to confine to
  their worktree.
- **Backward compat:** opt-in, default off. No `sandbox:` → today's behavior.
  When no kernel backend is available (no userns / no Landlock), the engine
  **degrades** to tool-minimization + a loud warning rather than silently
  running unconfined — see Backward compat.
- **Docs on ship:** `docs/architecture/hosts.md`, the authoring skill (§5),
  `docs/stories/state-machine.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| config | `sandbox` | on a `task` invoke `with:` (templated): `{ workspace: <path>, repo_ro: true, allow_net: true, persist: <allowlist globs>, override_decider: <agent?> }` | Presence → run the subprocess confined; `workspace` is the sole RW path. |
| event | `TaskSandboxStart` | `{ backend, workspace, repo_ro }` | Which backend confined the task (bwrap/landlock/degraded). |
| event | `TaskPersist` | `{ paths }` | The workspace files the engine copied into the repo. |
| event | `TaskPersistOverride` | `{ path, rationale, verdict, confidence }` | An out-of-allowlist persist gated by `agent.decide` (recorded — the moat). |

## The model

```
host.agent.task (sandbox:)  ─spawn─▶  bwrap/landlock:
                                          repo            → READ-ONLY
                                          workspace       → READ-WRITE   (the only door)
                                          ~/.claude,/tmp  → READ-WRITE   (claude-cli needs them)
                                          claude bin+rt   → READ-ONLY
                                          net             → per allow_net
                                              │
                                  agent uses ANY tool (Write/Bash/python/…)
                                  kernel denies every write outside workspace
                                              │
                              task ends ─▶ engine VALIDATES workspace diff vs `persist:` allowlist
                                          ─▶ in-allowlist  → copy into repo (TaskPersist)
                                          ─▶ out-of-allowlist → reject; agent/operator may supply an
                                             override_rationale → agent.decide(override_decider)
                                             → allow (TaskPersistOverride, recorded) | deny
```

Everything INTERPRETIVE (what the agent writes in the box) is unconstrained
and cheap; everything that crosses the DETERMINISTIC boundary (what gets
persisted into the repo) is allowlisted, and any exception is a recorded
`agent.decide` — the moat, applied to agent output.

## Decision recording

`TaskSandboxStart` (which backend actually confined the run — so a degraded,
unconfined run is visible in the trace, never silent), `TaskPersist` (the
exact files that landed — the file-level audit the trace lacks today), and
`TaskPersistOverride` (rationale + verdict for any out-of-allowlist persist —
a labeled datapoint: how often agents try to escape, with what justification,
and whether the judge agreed).

## Engine seams & invariants

- Wrap the claude-cli argv (`agent_task.go` spawn) in the backend launcher.
  bwrap: `--ro-bind <repo> <repo> --bind <workspace> <workspace> --bind
  ~/.claude ~/.claude --tmpfs /tmp --ro-bind <claude-rt> … --proc /proc --dev
  /dev --chdir <repo>` (+ net unless `allow_net:false`). The workspace dir must
  exist before the bind (the brief room already creates it).
- **Backend probe at startup**: detect userns (`unshare -Ur`) / Landlock LSM /
  `bwrap`; pick the strongest; record it. `sandbox:` + no backend → load/run
  WARNING and degrade (do not fail the run, do not silently run unconfined).
- Load-time invariant: `sandbox.workspace` non-empty; `override_decider` (if
  named) resolves to a declared `decide` agent.
- Persist is atomic under the session lock; on task error nothing persists.

## Backward compatibility / migration

Opt-in; absent `sandbox:` nothing changes. First adopter `design_author`
(its YOLO becomes kernel-impossible). Degraded mode (no userns/Landlock — e.g.
some CI/containers) keeps tool-minimization (no `Bash` for doc agents) +
prompt guidance + the persist allowlist, and emits a `TaskSandboxStart{backend:
degraded}` so the weaker posture is auditable. macOS would add a `sandbox-exec`
backend.

## Tasks

Dependency-ordered. **The fix ships at the end of Phase 4** (confinement alone
makes `design_author`'s YOLO kernel-impossible); the persist/override layer
(Phase 5) is a deferred slice for *worktree-mutating* tasks and could be its
own proposal. Sizes: S ≈ <½ day, M ≈ 1–2 days, L ≈ 3+ days. Critical path runs
through **0.1**.

```
## Phase 0 — De-risk spike (throwaway code; gates the schema)
- [ ] 0.1 (M, CRITICAL PATH) Enumerate claude-cli's minimal confined bind set.
        Wrap one real `host.agent.task` (or a bare `claude -p`) in bwrap and
        iteratively add binds until a real task completes confined: reads the
        repo, writes only the workspace, reaches the model.
        WHY: the single biggest unknown — claude-cli needs ~/.claude (creds/
        session), /tmp, its node runtime + binary, TLS certs, and network.
        ACCEPT: a documented, reproducible bwrap argv where a real confined
        task succeeds AND a repo write fails (EROFS). Output: the bind list.
- [ ] 0.2 (S) Capability probe matrix: detection for userns (`unshare -Ur`),
        `bwrap` presence, Landlock LSM. Decide the degraded-fallback policy
        (warn-and-degrade default + a hard-fail knob).
        ACCEPT: a `Probe() -> {backend, capabilities}` design + decision note.
- [ ] 0.3 (S) Finalize the `sandbox:` YAML schema from 0.1/0.2 learnings
        (workspace, repo_ro, allow_net, extra_rw[], persist[], override_decider).
        ACCEPT: frozen `Sandbox` struct shape + example block.

## Phase 1 — tasksandbox backend package  (deps: 0.1, 0.2, 0.3)
- [ ] 1.1 (M) `internal/host/tasksandbox`: backend interface
        `Confine(ctx, Spec) (Wrapper, cleanup, error)` + `Probe()`. Spec =
        {repoRoot, workspace, extraRW[], allowNet, claudeRuntime paths}.
- [ ] 1.2 (M) bwrap backend: assemble argv from Spec using the 0.1 bind set;
        temp/cleanup handling; chdir; net toggle.
- [ ] 1.3 (S) Degraded backend: no-op confinement that reports
        capability=degraded (so callers can WARN + record it).
- [ ] 1.4 (M) Unit tests (no LLM): confine `/bin/sh -c` that (a) writes the repo
        → denied (EROFS), (b) writes the workspace → ok, (c) python + sed
        variants → denied; degraded backend returns unconfined + flag.

## Phase 2 — schema + load validation  (deps: 0.3; parallel with Phase 1)
- [ ] 2.1 (S) Add `Sandbox` field to the task invoke in `internal/app/types.go`
        (yaml `sandbox`), documented like `once`/`background`.
- [ ] 2.2 (S) Loader invariants: `sandbox.workspace` non-empty; `override_decider`
        (if named) resolves to a declared `decide` agent. Clear `ValidationError`
        + `loader_sandbox_test.go`.
- [ ] 2.3 (S) Schema docs: `docs/embedded/app-schema.md` invoke table row +
        types.go comment.

## Phase 3 — agent_task wiring (confinement only)  (deps: 1.x, 2.x)
- [ ] 3.1 (M) In `agent_task.go` spawn (~:284): when `sandbox:` set, build the
        Spec (templated workspace/extra_rw from args), `Confine`, and wrap the
        claude-cli argv — composing with the existing `--mcp-config` validator
        and `--allowedTools` flags.
- [ ] 3.2 (S) Emit `TaskSandboxStart{backend, workspace, repo_ro}` (store event
        + trace); WARN + degrade when no backend (never silently unconfined).
- [ ] 3.3 (S) Replay/cassette: sandbox is a NO-OP in replay mode
        (`agent_task_replay.go`) — confinement only on live runs; assert it.
- [ ] 3.4 (M) Hermetic integration test (no real LLM): a FAKE `claude` binary
        (a script) that attempts to write `cmd/kitsoki/web.go` → denied; a
        workspace write → ok. Drives the real `agent_task` confinement path.

## Phase 4 — adopt design_author  (deps: Phase 3)  ← THE FIX SHIPS HERE
- [ ] 4.1 (S) Add `sandbox:` to the `design_draft` task invoke
        (workspace = `{{ world.design_workspace }}`, repo_ro, allow_net).
        publish_design.py (outside the sandbox) still moves the result out —
        unchanged.
- [ ] 4.2 (S) Flow/integration: confirm the existing proposal flows stay green
        and the sandboxed author cannot reach tracked source.
- [ ] 4.3 (S, ONE LLM RUN) Live e2e: run the proposal flow with a model that
        previously YOLO'd; confirm the attempt to implement is denied at the
        kernel and only `005-proposal.md` is produced in the workspace.
        Keep the prompt-hardening as belt-and-suspenders.

## Phase 5 — persist + override gate (DEFERRED slice; worktree-mutating tasks)
- [ ] 5.1 (M) Post-task persist: diff the workspace, copy in-allowlist
        (`persist:` globs) files into the repo; `TaskPersist{paths}` event.
        (Not needed by design_author — its workspace is the gitignored scratch
        and publish handles the move; this is for tasks whose output lands in
        tracked paths.)
- [ ] 5.2 (M) Reject-with-override: an out-of-allowlist persist is rejected;
        a supplied `override_rationale` is judged by `agent.decide(override_
        decider)` (default-deny, capped); `TaskPersistOverride{rationale,
        verdict, confidence}` recorded.
- [ ] 5.3 (M) Adopt for a worktree-mutating writer (bugfix implementer / impl
        write_code) to confine it to its worktree. Likely spun into its own
        proposal once Phase 4 proves the substrate.

## Phase 6 — backends, docs, lifecycle  (deps: Phase 4)
- [ ] 6.1 (M) Additional backends behind the interface: Landlock (kernels with
        the LSM enabled — lighter, in-process) and macOS `sandbox-exec`.
- [ ] 6.2 (S) Docs: `docs/architecture/hosts.md` (agent.task sandbox), the
        `kitsoki-story-authoring` skill (§5 + a sandbox note), state-machine.md.
- [ ] 6.3 (S) Migrate the durable mechanism description into docs/; trim this
        proposal to any remaining deferred slice (Phase 5) or delete it.
```

**Cross-cutting**

- **Test strategy:** everything except 4.3 is hermetic — a fake `claude` script
  stands in for the model so confinement is exercised with zero LLM spend; 4.3
  is the single real-model run that proves the end-to-end fix.
- **Degraded policy:** default warn-and-degrade (don't break unconfined CI/
  containers) with a config knob to hard-fail a `sandbox:` task where
  confinement is mandatory (2.2/3.2).
- **Who implements:** a non-sandboxed engineer/agent builds this — do NOT route
  it through the YOLO-prone `design_author` (it can't implement anyway once
  it has no Bash + is sandboxed; noted to avoid the obvious irony).
- **Sequencing recap:** `0.1 → {1, 2} → 3 → 4` ships the fix; `5` (needs 1+3)
  and `6` follow. 0.1 is the only real unknown; if its bind set proves
  intractable, fall back to tool-minimization + the degraded posture and
  re-scope.

## Verification

Core needs no LLM: drive a subprocess inside the backend and assert the
repo-write fails (EROFS) while the workspace-write succeeds — exactly the PoC
already run by hand (python + sed + redirect). The override-judge is the one
LLM touch; stub it. The headline test: a stubbed sandboxed `design_author`
that tries to write `cmd/kitsoki/web.go` is denied and only `005-proposal.md`
persists.

## Open questions

1. **claude-cli bind set.** The exact RO/RW paths + network claude-cli needs
   to run confined (node runtime location, `~/.claude`, `/tmp`, TLS certs).
   This is the main implementation risk. *Action: enumerate empirically with a
   minimal bwrap wrapper around a trivial task before building the schema.*
2. **Backend portability + fallback severity.** When no kernel backend exists,
   degrade-with-warning (lean) vs hard-fail a `sandbox:`-declared task (safer
   but breaks unconfined CI)? *Lean: degrade + WARN + a config knob to make it
   hard-fail in environments that require confinement.*
3. **Mediated FS tools — still wanted?** With the kernel boundary *and* slice
   2's tool allowlist, the earlier `mcp__taskfs__*` overlay is redundant. Keep
   it only if we want the per-write recording / read-overlay UX; otherwise the
   allowlist + kernel + persist step suffice. *Lean: drop it; revisit if
   per-write telemetry is wanted.*
4. **`host.run` from a sandboxed task** inherits the confinement (good), but a
   task that needs to run tests writes build output — fold that into the
   workspace or grant an explicit extra RW path? *Lean: explicit extra RW
   paths in `sandbox:`.*

## Non-goals

- A general OS sandbox for all of kitsoki — this scopes only the
  `write`/`external`-class agent subprocesses that opt in via `sandbox:`.
- Network / secret isolation beyond an `allow_net` on/off (a task that needs
  the model API keeps net).
- Confining `pure`/`read`-class agents — slice 2's tool allowlist already
  denies them every mutator, so they need no kernel jail. (Most `decide`/`ask`
  agents are `read`; a write-capable `converse` would adopt `sandbox:` here.)
- The classification (slice 1) and the tool-layer allowlist (slice 2) — this
  slice consumes both and adds only the kernel boundary beneath them.
