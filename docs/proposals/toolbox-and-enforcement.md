# Runtime: toolboxes + one enforcement policy for every agent

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [agent-capability-model.md](agent-capability-model.md) (slice 2 — the grant surface + tool-layer enforcement)

## Why

There is no toolbox concept in kitsoki — the word appears only as a metaphor
(`docs/architecture/concept.md:96`). An agent's tools are an inline
`Tools []string` list (`internal/host/agents.go:74`), merged per-call by
`effectiveTools` (`agents.go:144`) and forwarded as a `--allowedTools` flag
(`appendAllowedToolsFlag`, `agents.go:180`). Worse, **what that tool surface is
allowed to do is enforced three different ways depending on which agent verb
you happen to call**:

- `agent.ask` / `agent.decide` — a hardcoded `mutationTools` deny set
  (`internal/host/agent_ask.go:42`; shared into decide at
  `agent_decide.go:42`), rejected at runtime. Read-only by construction, no
  knob.
- `agent.converse` — `converseToolPolicy` (`agents.go:215`) maps a
  permission-mode string to CLI flags and, *only when* `agentIsReadOnly`
  (`agents.go:208`) is true, downgrades `bypassPermissions`→`default` and
  appends `readOnlyDeniedTools` (`agents.go:206`). Gated on the overloaded
  `external_side_effect: false` boolean.
- `agent.task` — `buildBaseCLIArgs` sets `bypassPermissions`
  (`agent_task.go:191`) and forwards every declared tool unrestricted
  (`agent_task.go:193`). Nothing reins it in — the YOLO that motivated the
  whole epic.

Three mechanisms, three vocabularies, three blast radii, no shared model.
[Slice 1](effect-taxonomy.md) gives us the missing model — an `effect` class
per tool surface. This slice makes the **grant** reusable (a toolbox) and the
**enforcement** uniform (one policy keyed on the class), so every agent is
restricted the same way for the same reason.

## What changes

Two coupled additions:

1. A **toolbox** — a named, reusable capability grant. A top-level
   `toolboxes:` map names a tool set (and, optionally, an asserted `effect:`);
   any agent references it by name with `toolbox:`, and may specialize with
   `tools_add:` / `tools_remove:`. The agent's effect class is the slice-1
   join over the *resolved* surface.

2. **One enforcement policy** — `enforceToolbox(agent) → {cliMode, allowed[],
   denied[]}` — derived purely from the resolved toolbox's effect class, and
   applied by **all four** agent handlers (`ask`, `decide`, `converse`,
   `task`) at the tool layer. The three ad-hoc paths
   (`mutationTools`-rejection, `converseToolPolicy`'s read-only branch, task's
   unrestricted spawn) collapse into this one function.

> One sentence: an agent's tools come from a named toolbox; the engine
> classifies that toolbox once (slice 1) and enforces the same allowlist +
> mutator-deny on every agent kind — no verb gets a private rulebook.

The kernel layer beneath this — confining the `write`/`external` tiers so a
`Bash`-holding agent can't `python -c 'open(...).write()'` past the
allowlist — is [slice 3](task-fs-sandbox.md). This slice is the tool-layer
boundary; slice 3 is the OS boundary under it.

## Impact

- **Code seams:** `internal/app/types.go` (`Toolboxes` top-level map; `Toolbox`/`ToolsAdd`/`ToolsRemove` on the agent decl), `internal/app/loader.go` (resolve `toolbox:` → tools, validate references, compute the join), `internal/host/agents.go:144`/`:180`/`:206`/`:215` (`effectiveTools` resolves the toolbox; `converseToolPolicy` becomes `enforceToolbox`), `internal/host/agent_ask.go:42`/`agent_decide.go:42` (drop the private `mutationTools` path → call `enforceToolbox`), `internal/host/agent_task.go:191` (stop hardcoding `bypassPermissions`; honor `enforceToolbox`).
- **Vocabulary:** `toolboxes:` (top-level), `toolbox:` / `tools_add:` / `tools_remove:` (agent) — table below. No new effects (slice 1 owns those).
- **Stories affected:** none forced — inline `tools:` keeps working. Stories may *opt in* to toolboxes to dedupe (e.g. the read-only judge/extract agents across `dev-story`, `prd`, `bugfix`, `docs-review` collapse to one `read_only` box).
- **Backward compat:** additive. An agent with no `toolbox:` behaves exactly as today; the only behavior change is that `ask`/`decide`/`task` now route through the *same* policy as converse — which is behavior-preserving because the policy reproduces each verb's current effective allowlist for the classes those verbs already imply (see Migration).
- **Docs on ship:** `docs/stories/state-machine.md` (agent + toolbox decl), `docs/architecture/hosts.md` (the one enforcement policy, per agent kind).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| top-level | `toolboxes` | `{ <name>: { tools: [..], effect?: <class> } }` | named, reusable tool set; `effect:` (optional) asserts the class → load-time check vs. the join |
| agent field | `toolbox` | `<name>` | reference a named box; the agent's surface = the box's tools |
| agent field | `tools_add` | `[..]` | specialize: union onto the referenced box (may raise the effect class — e.g. `+WebFetch` → `external`) |
| agent field | `tools_remove` | `[..]` | specialize: subtract from the box (may lower the class) |
| agent field (existing) | `tools` | `[..]` | unchanged; inline surface when no `toolbox:` is named. `toolbox:` + `tools:` on the same agent → load error (pick one base) |

```yaml
toolboxes:
  read_only:   { tools: [Read, Grep, Glob],            effect: read }
  repo_writer: { tools: [Read, Grep, Glob, Edit, Write, Bash], effect: write }

agents:
  brief_judge:  { toolbox: read_only }                       # → read   (enforced read-only on every kind)
  researcher:   { toolbox: read_only, tools_add: [WebFetch] } # → external
  impl_writer:  { toolbox: repo_writer, sandbox: {...} }      # → write  (slice 3 confines it)
```

## The model

`enforceToolbox` is the single policy. It resolves the agent's toolbox to a
tool surface, asks slice 1 for the effect class, and returns the CLI posture —
identically for every agent handler:

```
agent ─▶ resolve toolbox (named + add/remove) ─▶ tool surface
                                                      │ slice-1 join
                                                 effect class
                                                      │
                              ┌───────────────────────┴───────────────────────┐
                       effect ≤ read                                   effect ≥ write
                              │                                                │
              cliMode=default                                   cliMode per permission_mode
              allowed = surface                                 allowed = surface
              denied  = readOnlyDeniedTools                     (kernel confinement is slice 3)
                              │                                                │
              same for ask / decide / converse / task  ◀──────────────────────┘
```

- `pure`/`read` → `default` mode, the toolbox is the allowlist, mutators
  (`readOnlyDeniedTools`: Write/Edit/MultiEdit/NotebookEdit/Bash, `agents.go:206`)
  hard-denied. This is exactly today's `ask`/`decide` posture and today's
  converse read-only branch — now reached by one path, and now also the
  *default* for a `task` whose toolbox is read-only.
- `write`/`external` → the allowlist is the toolbox; the operator's
  `permission_mode` still governs prompting. Confinement of the actual writes
  is slice 3's OS sandbox; without it, a `write` toolbox is tool-allowlisted
  but not kernel-jailed (a loud load-time note when `write`+no `sandbox:`).

No interpretive decision is added — the policy is a deterministic function of
the resolved class. The moat is untouched.

## Decision recording

No new event kind. The resolved toolbox name + effect class already ride on the
host event via slice 1 (`effect`/`deterministic` recorded at
`host_dispatch.go:187`); this slice adds the **toolbox name** and the
**resolved allowed/denied sets** to the existing agent-invoke event so a trace
shows *which box governed the call and what it permitted* — the exact data the
[conformance check](agent-contract-eval.md) reads to prove no tool use
exceeded the box.

## Engine seams & invariants

- `effectiveTools` (`agents.go:144`) gains toolbox resolution: `toolbox:` →
  named box's tools, then `tools_add`/`tools_remove`, then per-call `tools:`
  override (unchanged precedence).
- `converseToolPolicy` (`agents.go:215`) is generalized to `enforceToolbox`
  and called from `agent_ask.go`, `agent_decide.go`, `agent_converse.go`,
  and `agent_task.go` — the per-verb branches deleted.
- Load-time invariants: `toolbox:` references a declared box (else
  `ValidationError`); `toolbox:` + `tools:` on one agent → error; a box's
  asserted `effect:` disagreeing with its tool-join → error (this is the same
  teeth slice 1 adds for agents, applied to the reusable box); `write`/`external`
  toolbox on a `task` with no `sandbox:` → warn (points at slice 3).

## Backward compatibility / migration

Additive and behavior-preserving. Inline `tools:` agents are untouched. The
risk surface is routing `ask`/`decide`/`task` through `enforceToolbox`:

- `ask`/`decide` are read-only today via `mutationTools`; their inferred class
  is `read` (no mutator in surface), so `enforceToolbox` yields the same deny
  set — verified by extending `converse_tool_policy_test.go` to cover all four
  kinds.
- `task` today is `bypassPermissions`-unrestricted; its class is `write` (it
  holds Write/Bash), so `enforceToolbox` keeps the allowlist permissive at the
  tool layer (the *new* confinement is slice 3, opt-in) — no existing task
  changes behavior until it adds `sandbox:`.

Optional follow-up (not required to ship): dedupe the four read-only
judge/extract agents onto a shared `read_only` box.

## Tasks

```
## 1. Toolbox vocabulary (deps: slice 1)
- [ ] 1.1 `Toolboxes` top-level map + `Toolbox`/`ToolsAdd`/`ToolsRemove` agent fields (types.go)
- [ ] 1.2 Resolve toolbox in effectiveTools (named + add/remove + per-call override precedence)
- [ ] 1.3 Load invariants: undeclared-box ref, toolbox+tools clash, box effect-assertion vs join, write-task-without-sandbox warn

## 2. One enforcement policy
- [ ] 2.1 converseToolPolicy → enforceToolbox(agent) keyed on slice-1 effect class
- [ ] 2.2 Route ask/decide/converse/task through enforceToolbox; delete the private mutationTools path and task's hardcoded bypassPermissions
- [ ] 2.3 Record toolbox name + resolved allowed/denied on the agent-invoke event

## 3. Verification
- [ ] 3.1 enforceToolbox table test: pure/read/write/external → expected cliMode+allow+deny, for each of the four agent kinds
- [ ] 3.2 Behavior-preserving proof: ask/decide deny set unchanged; task allowlist unchanged pre-sandbox (extend converse_tool_policy_test)
- [ ] 3.3 Load test: every invalid toolbox shape fails with a clear error
- [ ] 3.4 Flow smoke: existing stories (inline tools) unchanged; one story migrated to a shared box stays green

## 4. Adopt + document
- [ ] 4.1 Migrate the read-only judge/extract agents onto a shared `read_only` box (optional dedupe)
- [ ] 4.2 Update state-machine.md (toolbox decl) + hosts.md (the one enforcement policy); trim/delete this proposal
```

## Verification

No LLM. `enforceToolbox` is a pure function — a table test drives
`{class} × {agent kind}` → expected `{cliMode, allowed, denied}` and asserts
the four kinds now agree. The behavior-preserving claim is proved by snapshotting
each verb's current effective allowlist and showing the unified policy
reproduces it. Toolbox resolution + load invariants are `internal/app` loader
unit tests. Flow fixtures (inline-tools stories) must stay green unmodified.

## Open questions

1. **Per-call `tools:` precedence over a named box** — does a call-site
   `tools:` *replace* or *union* the agent's toolbox? *Lean: replace (matches
   today's `effectiveTools` "per-call wins"), but re-run the join so the class
   can't be silently escalated unaudited.*
2. **A box asserting a *lower* effect than its tools** (e.g. `effect: read` on
   a box containing `Write`) — hard error (slice-1 invariant) or allowed as an
   author override that *also tightens* enforcement? *Lean: hard error — an
   asserted class below the join is a bug, not an intent.*
3. **Should `toolboxes:` be sharable across stories** (a stdlib of boxes) or
   strictly per-app? *Lean: per-app now; a shared library is a later concern
   once boxes prove useful.*

## Non-goals

- The effect taxonomy itself — [`effect-taxonomy.md`](effect-taxonomy.md)
  (slice 1) owns the classes; this slice consumes them.
- Kernel-level confinement of the `write`/`external` tiers —
  [`task-fs-sandbox.md`](task-fs-sandbox.md) (slice 3); this slice stops at the
  tool allowlist.
- Per-tool argument-level policy (e.g. "Bash but only `git`") — coarser than
  the four-tier ladder; out of scope.
- A cross-app toolbox registry (open question 3).
