# Epic: a unified capability model for every agent

**Status:** Draft v1. No slices implemented as proposed. Adjacent safety work
has shipped (`write_mode: read_only`, bash profiles including sandboxed-write,
validator sandboxing, converse/read-only tool policy, and load-time
`external_side_effect` cross-checks), but the proposed `effect:` taxonomy,
named `toolboxes:`, `tools_add:`/`tools_remove:`, unified enforcement surface,
and task filesystem sandbox are not present. `external_side_effect` remains the
real story vocabulary.
**Kind:**   epic
**Slices:** 3 (0/3 shipped) + a conformance check folded into `agent-contract-eval.md`

## Why

kitsoki restricts what its interpretive steps may *do* with three
disconnected, ad-hoc mechanisms, and classifies the side-effect nature of
work with one overloaded boolean read two incompatible ways. The result: a
`host.agent.task` can write anywhere its tools reach (`working_dir` is not a
jail — `internal/host/agent_task.go:284`), and the real incident behind
[`task-fs-sandbox.md`](task-fs-sandbox.md) was `proposal_author` *implementing*
the idea (`cmd/kitsoki/web.go` + a `/actions→/intents` rename) when asked only
to *propose* it.

Today the three restrictions don't share a model:

- `agent.ask` / `agent.decide` — a hardcoded `mutationTools` deny set
  (`internal/host/agent_ask.go:42`, shared into decide at
  `agent_decide.go:42`); read-only by construction.
- `agent.converse` — a `readOnlyDeniedTools` backstop
  (`internal/host/agents.go:206`) applied by `converseToolPolicy`
  (`agents.go:215`), gated on the overloaded `external_side_effect: false`
  boolean via `agentIsReadOnly` (`agents.go:208`).
- `agent.task` — fully unrestricted `bypassPermissions`
  (`agent_task.go:191`); nothing reins it in.

And `external_side_effect` (`internal/app/types.go:999`) is one bit meaning
two things: the task **replay mode** (`inferReplayMode`,
`agent_task_replay.go:85`) and the converse **read-only posture**
(`agentIsReadOnly`). It can't tell read-only from write-local — four in-repo
agents declare `false` while actually writing files. There is **no toolbox
concept** (the word appears only as a metaphor in
`docs/architecture/concept.md:96`) and host builtins carry **no** read/write
marker at all (`host_dispatch.go:187` dispatches with no effect metadata).

The same axis underlies all of it. We should name it once, restrict every
agent through it, enforce it in layers, and audit that the boundary held.

## What changes

Once every slice has shipped, **every agent — `decide`, `ask`, `converse`,
`task` — is governed by one capability model** built from four cooperating
layers, each feeding the next:

```
TOOLBOX        a named, reusable capability grant — the tools an agent may use
   │ join over tools (most-privileged tool wins)
EFFECT CLASS   pure | read | write | external   (+ orthogonal `deterministic` bit)   ← the declared contract
   │ derives one uniform enforcement policy
ENFORCEMENT    tool-layer allowlist + mutator-deny (pure/read)  →  OS sandbox (write/external)
   │ audited offline from the trace by
CONTRACT EVAL  schema conformance + "stayed inside its toolbox / honored its effect" + correctness
```

- The **toolbox** is the single thing that restricts an agent: a named set
  of tools, referenced by any agent, with an inline override
  (`tools_add:`) for specialization.
- The toolbox's tool-join yields the **effect class** — the deterministic
  classification (`pure|read|write|external` + `deterministic`) that replaces
  the overloaded boolean and that every consumer (converse posture, replay
  mode, future cache) reads.
- **Enforcement is derived from the effect class and applied uniformly** to
  all four agent kinds: the tool-layer allowlist + mutator-deny for
  `pure`/`read`, and the OS sandbox (the kernel boundary `working_dir` never
  was) for `write`/`external`.
- **Conformance** is audited offline from the recorded trace: a call's actual
  tool uses never exceeded its toolbox/effect, alongside the existing schema +
  correctness checks.

The throughline: an author declares **one toolbox** per agent; the engine
derives the class, picks the enforcement layer, and the trace proves the box
held. The three ad-hoc mechanisms collapse into one.

## Impact

- **Spans:** runtime (3 slices) + tracing (the conformance check folded into
  the standalone `agent-contract-eval.md`).
- **Net surface:** one new vocabulary block (`toolboxes:` + `effect:` /
  `deterministic:` / `toolbox:` / `tools_add:`), the deprecation of
  `external_side_effect`, a builtin host-call classification table, one
  uniform tool-policy path replacing three, an opt-in OS `sandbox:` enforcer,
  and a new offline conformance lint. Behavior-preserving for every existing
  story (the nine `external_side_effect:` agents map mechanically; toolboxes
  are opt-in).
- **Docs on ship:** `docs/architecture/hosts.md` (per-verb effect table +
  toolboxes), `docs/stories/state-machine.md` (agent/toolbox decl),
  `docs/tracing/trace-format.md` (effect class on host events),
  `docs/tracing/cassettes.md` (conformance lint).

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | effect taxonomy | runtime | `effect: pure\|read\|write\|external` + `deterministic`; classify host calls **and** agents; replace the overloaded boolean | — | Draft | [`effect-taxonomy.md`](effect-taxonomy.md) |
| 2 | toolbox + uniform enforcement | runtime | named `toolboxes:` + `tools_add:`; one effect-derived tool-layer policy for **all four** agent kinds, replacing the three ad-hoc mechanisms | 1 | Draft | [`toolbox-and-enforcement.md`](toolbox-and-enforcement.md) |
| 3 | OS sandbox | runtime | `sandbox:` confines the `write`/`external` tiers at the kernel (bwrap/Landlock); engine validates + persists the workspace diff | 1, 2 | Draft | [`task-fs-sandbox.md`](task-fs-sandbox.md) |
| — | effect/toolbox conformance | tracing | offline check that a recorded call's tool uses never exceeded its toolbox/effect (a new Layer-1 sibling) | 1 | Draft | [`agent-contract-eval.md`](agent-contract-eval.md) (§Conformance) |

## Sequencing

Slice 1 is the substrate — the classification every other slice keys on.
Slice 2 adds the toolbox vocabulary and folds the three enforcement
mechanisms into one effect-derived policy at the tool layer. Slice 3 adds the
kernel layer beneath the tools for the `write`/`external` tiers. The
conformance check rides on slice 1's recorded effect class and can land in
parallel with 2/3 once events carry the class.

```
#1 effect-taxonomy ──▶ #2 toolbox + enforcement ──▶ #3 OS sandbox
        └──────────────────────────────────────────▶ conformance (parallel once #1 records effect)
```

The fix for the headline incident (`proposal_author`'s YOLO) ships at the end
of slice 3, but is *already* tightened by slice 2: once the author's toolbox
is `read` and enforcement is uniform, its Write/Bash are denied at the tool
layer; slice 3 makes the same boundary kernel-hard against `python -c
'open(...).write()'`.

## Shared decisions

1. **Two axes, not one enum.** `effect` (class) and `deterministic` (bool)
   stay separate because a tool-less `agent.extract` touches nothing (`pure`)
   yet is non-deterministic — they can't merge. Owned by slice 1; every other
   slice defers. (Mirrors Acronis DTS's separate `is_idempotent` axis.)
2. **The effect ladder names** — `pure | read | write | external` — are fixed
   in slice 1. Slices 2 and 3 reference them, never redefine them. `write` is
   exactly the class the OS sandbox jails; `read`/`pure` is exactly the class
   the tool-layer allowlist permits unsandboxed.
3. **The toolbox is the only grant surface.** An agent's tools come from a
   named `toolbox:` (with optional `tools_add:`) or an inline `tools:` list;
   the engine computes the effect class as the join over that surface. Owned
   by slice 2. `external_side_effect:` becomes a deprecated alias for one
   release (owned by slice 1's migration).
4. **Classification and enforcement are deterministic — the moat is
   untouched.** The taxonomy, the toolbox join, the tool-deny, and the kernel
   confinement add **no** interpretive decision. The single exception is the
   sandbox's out-of-allowlist *persist override* (slice 3), which is a
   recorded `agent.decide` — a labeled datapoint, the moat applied to agent
   output.

## Cross-cutting open questions

1. **Where the effect class is authoritative for a host call** — declared on
   the `HostInterfaceOp` vs. a fixed builtin table vs. both with override.
   *Lean: builtin table is the default, an `host_interfaces:` override is the
   escape hatch (slice 1).* 
2. **Does conformance belong in `agent-contract-eval` or this epic?** Decided:
   folded into `agent-contract-eval.md` as a new Layer-1 sibling (schema
   conformance gets an effect/toolbox-conformance neighbour), kept standalone
   and cross-referenced — the correctness-eval scope stays clean.
3. **macOS / no-kernel-backend posture** — when no OS sandbox backend exists,
   slice 3 degrades to slice 2's tool-layer enforcement + a loud warning. Is
   tool-layer-only ever acceptable for a `write` agent in CI? *Lean:
   warn-and-degrade by default, with a hard-fail knob (slice 3).* 

## Non-goals

- A general OS sandbox for all of kitsoki — only agent subprocesses, and only
  the `write`/`external` tiers, opt in (slice 3).
- Building the result cache — the taxonomy supplies the key a cache would use;
  the cache is a future consumer (slice 1 names it, nothing implements it).
- Per-tool permission grading finer than the four-tier ladder.
- The correctness-eval / backend-routing machinery itself — that stays in
  [`agent-contract-eval.md`](agent-contract-eval.md) and
  [`local-model-agent.md`](local-model-agent.md); this epic only adds the
  conformance sibling.
