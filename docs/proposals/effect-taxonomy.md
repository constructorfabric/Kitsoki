# Runtime: a unified effect taxonomy for host calls and agents

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [agent-capability-model.md](agent-capability-model.md) (slice 1 — the classification substrate)

## Why

kitsoki classifies the side-effect nature of work in three disconnected,
partial places, and most of the surface isn't classified at all:

- **Agents** carry `external_side_effect: bool` (`internal/app/types.go:999`),
  read two incompatible ways — as the task **replay mode**
  (`inferReplayMode`, `internal/host/agent_task_replay.go:85`: `false` ⇒ Mode A
  "local mutations, replayable") and as the converse **read-only posture**
  (`agentIsReadOnly`, `internal/host/agents.go:208`: `false` ⇒ "deny every
  mutator"). One bit, two meanings — and it can't tell read-only from
  write-local. Of the nine in-repo agents declaring `false`, four are actually
  write-local (`stories/dev-story/app.yaml:117`, `stories/prd/app.yaml:86`,
  `stories/bugfix/app.yaml:92`, `stories/docs-review/app.yaml:87`) and carry a
  declaration identical to the read-only ones.
- **Host calls** carry **nothing**. Every builtin — `host.git`,
  `host.transport.post`, `host.chat.*`, `host.ide.*`, `host.run`
  (`internal/host/handlers.go:228`) — is dispatched live with no marker for
  whether it reads, writes, or causes an irreversible external action
  (`dispatchHostCalls`, `internal/orchestrator/host_dispatch.go:187`).
- **Replay** decides re-execute-vs-replay purely on cassette episode presence
  (`MatchEpisode`, `internal/testrunner/cassette.go:327`) — no field says "this
  call is read-only, safe to re-run" vs. "this is an external side effect, must
  be recorded and never repeated."

This blocks the rest of the [capability-model
epic](agent-capability-model.md): you cannot **enforce** a boundary
(slices 2–3) you have not **named**. It also blocks two longer-range wants.
**Trace replayability:** to replay a recorded run faithfully we must know which
calls can be re-executed (deterministic reads) and which must be served from
the recording (LLM output; external actions that must never fire twice).
**Caching (future):** a result cache needs to know which calls are memoizable
(pure/read) and which are not (write/external) — `turncache`
(`internal/turncache/cache.go`) already does exactly this for routing verdicts,
but there's no general mechanism.

The same axis underlies all of it. This slice names it once.

## What changes

Introduce a single **effect taxonomy** that classifies *any* unit of work — a
host-call operation, MCP/studio tool, or agent — by one primary mutation ladder
plus a small set of orthogonal facets. The ladder answers "what can this touch?"
while the facets answer the other operator-risk questions stories need to know:
can it ask the operator, spend metered model/API budget, or delegate work to
another worker/background loop?

> An author/handler declares **what a call touches** (effect class) and
> which non-mutation capabilities it may exercise. The engine
> derives every constraint — read-only enforcement, replay strategy,
> cacheability, write-mode grants, budget gates, operator-ask availability, and
> delegation visibility — from that contract. One promise, many consumers.

Prior art: Acronis DTS uses exactly this shape — a `deterministic_behavior`
enum `PURE | QUERY | MUTATION | SIDE_EFFECT`
(`~/code/cyberville/acronis-platform/dts/types.raml:97`), read by its engine to
gate write verbs, decide cacheability, and flag CI-safe functions, with
idempotency kept as a *separate* `is_idempotent` axis (`types.raml:125`). Our
LLM calls force the same separation: a tool-less `agent.extract` call touches
nothing (PURE) yet is non-deterministic — so effect class and determinism
cannot be one enum.

This slice owns the **classification**. The vocabulary that *grants* the tool
surface a class is computed over (named `toolboxes:`) and the *enforcement*
keyed on the class are [slice 2](toolbox-and-enforcement.md); the OS sandbox
that jails the `write`/`external` tiers is [slice 3](task-fs-sandbox.md).

## Impact

- **Code seams:** `internal/app/types.go` (effect fields on `HostInterfaceOp` and agent decl), `internal/app/loader.go:557`/`:589`/`:1559` (inference, invariants), `internal/host/agents.go:208`/`:215` (converse), `internal/host/agent_task_replay.go:85` (replay mode), `internal/host/handlers.go:228` (builtin classification table), `internal/orchestrator/host_dispatch.go:187` (record the class on the event).
- **Vocabulary:** new `effect:` + `deterministic:` + `operator:` + `spend:` +
  `delegate:` (table below); `external_side_effect: bool` becomes a deprecated
  alias.
- **Stories affected:** the nine `external_side_effect:` declarations migrate (behavior-preserving — all run via `task` today). Host-call classifications are author-invisible defaults unless a story overrides via `host_interfaces:`.
- **Backward compat:** alias kept one release with a warn-line; cassette `replay_mode` body values unchanged, so fixtures pass unmodified.
- **Docs on ship:** `docs/stories/state-machine.md` (agent decl), `docs/architecture/hosts.md` (per-verb effect table), `docs/tracing/trace-format.md` (effect class on host events).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| effect field | `effect` | `pure \| read \| write \| external` | primary mutation/data-effect ladder, on a `HostInterfaceOp` / builtin verb / MCP tool / agent (agent = join over tools) |
| effect field | `deterministic` | `bool` (default `true`) | false ⇒ re-running won't reproduce the result (every agent/LLM call; live external reads) |
| effect field | `operator` | `none \| may_ask \| requires_answer \| grants_write` | whether this unit can block on the operator or request a write/external grant |
| effect field | `spend` | `none \| metered \| budgeted` | whether the unit can spend model/API budget; `budgeted` requires a named budget/profile gate |
| effect field | `delegate` | `none \| subagent \| background \| external_worker` | whether the unit can spawn delegated work outside the current synchronous call stack |
| agent field (dep) | `external_side_effect` | `bool` | **deprecated alias** → `effect` (`true`→`external`; `false`→`write` if tools include a mutator, else `read`) |

The effect ladder (each tier a superset of the last's blast radius):

| `effect` | Touches | Reversible? | DTS analogue |
|---|---|---|---|
| `pure` | nothing — pure transform (tool-less LLM call; a formatter) | n/a | `PURE` |
| `read` | reads environment, no change (git log, `host.ide.get_*`, Read, GraphQL query) | n/a | `QUERY` |
| `write` | mutates local/in-domain state, **replayable from a diff** (Write/Edit, `host.chat.create`, git commit, `host.append_to_file`) | yes — a later edit undoes it | `MUTATION` |
| `external` | irreversible external action (`host.transport.post`, `host.gh.ticket`, a PR, an email; WebFetch) | **no** | `SIDE_EFFECT` |

The facets stay separate from the ladder because they cut across it:

| Facet | Values | Examples | Consumer |
|---|---|---|---|
| `operator` | `none`, `may_ask`, `requires_answer`, `grants_write` | plain `host.run` = `none`; `mcp__operator__ask` = `requires_answer`; write-mode gate = `grants_write` | attach/withhold the operator-ask bridge; trace blocking human decisions |
| `spend` | `none`, `metered`, `budgeted` | `host.starlark.run` replay = `none`; `host.agent.decide` = `metered`; marathon driver under a run budget = `budgeted` | cost accounting, budget enforcement, "no spend in tests" lint |
| `delegate` | `none`, `subagent`, `background`, `external_worker` | `host.agent.task` using Task = `subagent`; `background: true` = `background`; Studio MCP drive of another session = `external_worker` | inbox/work queue visibility, cancellation, trace parenting |

## The model

**Agents derive, host calls and MCP tools declare.** A host-call operation or
MCP tool declares its `effect`/`deterministic`/facets (with a sensible builtin
default per verb). An agent's primary effect class is the **join** (least upper
bound) over its tool surface — the most-privileged tool wins — and
`deterministic: false` always, because it's an LLM call:

```
agent tools []          → join → pure      (api-only; no Read/MCP)
agent tools [Read,Grep] → join → read
agent tools [...,Write] → join → write
agent tools [...,WebFetch/ext-MCP] → join → external
```

The join is computed over whatever tool surface an agent presents — an inline
`tools:` list today, or a named `toolbox:` once [slice 2](toolbox-and-enforcement.md)
lands. Facets join independently: `may_ask` + `requires_answer` becomes
`requires_answer`, any metered tool makes `spend: metered` unless the call names
a budget gate, and any nested Task/background/session-drive primitive raises the
delegate tier. These joins are pure deterministic *classification* — no
interpretive (LLM/human) decision is added, so the moat is untouched. The
taxonomy only records what a call is; it adds no new decision point.

Three consumers read the pair:

| Consumer | Rule | Replaces |
|---|---|---|
| converse permission | read-only ⟺ `effect ≤ read` → bind allowlist + deny `readOnlyDeniedTools` | `agentIsReadOnly` (`agents.go:208`) |
| task replay mode | `external`→Mode C (record-only); `write`→Mode A/B (diff/tarball); `pure`/`read`→Mode A, re-executable iff `deterministic` | `inferReplayMode` (`agent_task_replay.go:85`) |
| replay strategy (host calls) | `deterministic && effect ≤ read` ⇒ may re-execute on replay; else serve from recording; `external` ⇒ never re-run | *(new — non-agent calls had none)* |
| cache (future) | `pure`+det ⇒ memoize by input; `pure`/`read` non-det ⇒ cache by input signature (cf. `turncache`); `write`/`external` ⇒ never | *(new)* |
| operator bridge | `operator != none` ⇒ only attach/allow in live surfaces with an `OperatorPrompter`; headless tests must stub or deny | ad-hoc operator-ask gating |
| budget gate | `spend != none` ⇒ record model/profile/cost and reject in default automated tests unless a cassette/stub satisfies it | convention-only "no real LLM in tests" |
| delegation visibility | `delegate != none` ⇒ emit parent/child identifiers and show work in scheduler/inbox/studio queues | scattered background job and sub-agent tracing |

The first two consumers are rewritten in earnest by [slice
2](toolbox-and-enforcement.md), which unifies them with `ask`/`decide` into one
policy; this slice supplies the class they switch on.

## Decision recording

Host events (`HostInvoked`/`HostDispatched`/`HostReturned`,
`internal/store/event.go:50`) gain the resolved `effect`/`deterministic` and
facets on dispatch, so a trace is self-describing for replay without
re-deriving from the story. Agent events (`agent.call.start/complete/error`)
gain the same contract plus spend metadata (`profile`, `model`, opaque provider
cost if available) and delegation parent IDs when the call spawns nested work.
This is also what the [conformance check](agent-contract-eval.md) audits
against — the recorded class is the contract a run is checked to have honored.
`task.end`'s existing `replay_mode` (`internal/journal/types.go:190`) is
unchanged — its three values map 1:1 from the agent's joined effect class, so
existing cassettes stay valid. No new event kind.

## Engine seams & invariants

- `agentIsReadOnly` ⇒ `effectClass(agent) <= read`; `converseToolPolicy`
  (`agents.go:215`) and `inferReplayMode` (`agent_task_replay.go:85`) switch on
  the joined class instead of the boolean.
- `inferExternalSideEffect` (`loader.go:589`) is rewritten to compute an effect
  class from the tool surface (it currently only sees WebFetch/WebSearch — it's
  blind to Write/Edit), folding in `readOnlyDeniedTools` (`agents.go:206`).
- A builtin classification table (`internal/host/handlers.go`) assigns each
  verb its default `effect`/`deterministic`/facets; op-dispatch handlers
  (`host.git`, `host.gh`, `host.local` — `handlers.go:228`) classify **per op**
  (`git log`→read, `git push`→external), since one handler spans tiers.
- MCP/studio tool registration grows the same declaration struct, so `session.*`
  tools, `mcp__operator__ask`, validator `submit`, and future external MCP
  tools can be displayed and gated with the same vocabulary as story host calls.
- `background: true` remains an `Effect` field (`internal/app/types.go:975`),
  but dispatch records `delegate: background`; agent Task/sub-agent use records
  `delegate: subagent`; Studio MCP-driven delegated sessions record
  `delegate: external_worker`.
- Load-time hard-fails (mirroring the WebFetch contradiction at
  `loader.go:1559`): `effect: read` (or `pure`) declared on an agent/op whose
  tool surface includes a mutator/network tool → **error**. This is the teeth
  the boolean never had — it would have caught the dead `proposal_author`
  declaration. A declared `effect` disagreeing with the inferred join → warn
  (`loader.go:564`).
- Load-time/lint hard-fails: `spend != none` in a default flow fixture without a
  cassette/stub; `operator != none` in a headless-only test with no scripted
  answer; `delegate != none` without trace parent metadata.

## Backward compatibility / migration

`external_side_effect:` keeps loading one release as a deprecated alias, mapped
by tool surface (`false`+mutator→`write`; `false`+none→`read`; `true`→
`external`), with a warn-line. The nine in-repo declarations migrate in a
one-shot; each is behavior-preserving because they run via `task`, where
`write` and the old `false` both yield Mode A. Host-call classifications ship as
engine defaults — no story changes required, no cassette changes (replay-mode
values stable).

## Tasks

```
## 1. Taxonomy + classification data
- [ ] 1.1 Effect enum (pure|read|write|external) + deterministic bool + operator/spend/delegate facets; join helpers over a tool surface
- [ ] 1.2 Builtin classification table for every host verb and MCP/studio tool (per-op for host.git/gh/local); default deterministic, agent=false
- [ ] 1.3 `effect`/`deterministic`/facet fields on HostInterfaceOp + agent decl (types.go); external_side_effect alias + warn-line

## 2. Wire the present consumers
- [ ] 2.1 inferExternalSideEffect → effect-class inference (Write/Edit-aware); loader invariants (read+mutator hard-fail, disagreement warn)
- [ ] 2.2 agentIsReadOnly / converseToolPolicy / inferReplayMode read the joined class (agents.go, agent_task_replay.go)
- [ ] 2.3 Record effect/deterministic/facets on Host* and agent.call.* events (host_dispatch.go, event.go)
- [ ] 2.4 Wire operator/spend/delegate consumers: operator-ask attachment, no-real-LLM flow lint, delegation parent IDs

## 3. Verification
- [ ] 3.1 Loader unit: effect inference across pure/read/write/external; read+Write hard-fails; alias maps both branches of `false`
- [ ] 3.2 converse_tool_policy_test + agent_task_replay test driven off effect class; cassette replay_mode values stable
- [ ] 3.3 Builtin table coverage test: every registered host verb and MCP tool has a classification (fail if a new verb/tool is unclassified)
- [ ] 3.4 Flow lint: spend requires cassette/stub by default; operator requires scripted/live prompter; delegated work records a parent
- [ ] 3.5 Full story load smoke post-migration

## 4. Adopt + document
- [ ] 4.1 Migrate the nine agent declarations to `effect:`
- [ ] 4.2 Update state-machine.md, hosts.md (per-verb effect table), mcp-studio.md (tool effect table), trace-format.md; trim/delete this proposal
```

## Verification

No LLM needed. Effect-join/facet-join, loader invariants, and all derivations
are table-driven Go unit tests (`internal/app`, `internal/host`,
`internal/mcp`). The existing `converse_tool_policy_test.go` and
`agent_task_replay` tests extend directly. A coverage test asserts every
registered host verb and MCP tool is classified, so a new builtin can't slip in
unclassified. Flow lint uses cassettes/stubs for spend and operator paths;
cassette replay is unaffected (no `replay_mode` value change), so flow fixtures
pass unmodified.

## Open questions

1. Surface name — `effect` vs. `behavior` vs. DTS's `deterministic_behavior`.
   *Lean: `effect` (class) + `deterministic` (bool) as two fields — clearer than
   one bundled enum, and the tool-less-LLM = pure-but-non-deterministic case
   proves they can't merge.* (Settled at the epic level as shared decision 1.)
2. `host.ide.open_file`/`open_diff` — a benign, reversible mutation of the
   operator's IDE. `write` or a distinct "ui" tier? *Lean: `write` with a note;
   it's reversible and never repeated harmfully, so no new tier.*
3. Replay of `external` host calls — record-and-never-re-run is clear, but do we
   also need an authored "this external call is safe to repeat" escape hatch
   (idempotent POSTs)? *Lean: defer; mirror DTS's separate `is_idempotent` only
   when a real call needs it.*
4. Scope of this slice — wire converse + replay-mode now (Phases 1–2), or
   also land the host-call replay-strategy consumer? *Lean: define the taxonomy
   + record it on events now; the non-agent replay/cache consumers land as
   follow-ups once the data exists.*
5. Spend granularity — is `metered|budgeted` enough, or do we need a typed
   `budget_ref` on every spend-capable call in v1? *Lean: keep the facet small
   and require a `budget_ref` only for `spend: budgeted`; model/provider/cost
   already ride agent metadata.*
6. Delegation granularity — should `background` be an effect facet even when the
   delegated work is deterministic `host.run`? *Lean: yes; delegation is about
   scheduling/cancellation/visibility, not mutation risk.*

## Non-goals

- The toolbox vocabulary and the uniform enforcement keyed on these classes —
  [`toolbox-and-enforcement.md`](toolbox-and-enforcement.md) (slice 2).
- OS-level write confinement — [`task-fs-sandbox.md`](task-fs-sandbox.md)
  (slice 3); the taxonomy *declares* intent (`write` is exactly the class the
  sandbox jails), the sandbox *enforces* it.
- Building the result cache itself — this slice supplies the classification a
  cache would key on; the cache is a future consumer.
- Per-tool permission grading finer than the four-tier ladder.
- Changing `replay_mode` body values or adding replay modes.
- A full spend/billing system. This only records and gates spend posture; budget
  accounting remains whatever the active profile/provider exposes.
