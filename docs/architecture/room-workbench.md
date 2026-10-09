# The room workbench primitive (`workbench:`)

A room may declare a `workbench:` block instead of hand-rolling `write_mode`
+ `agent_off_ramp` + an `on_enter host.agent.task` + a free-text capture
intent. The loader **desugars** the block at load time into exactly those
four already-shipped primitives, bound to the named agent's WS
`toolbox:`/`effect:` declaration. It is a macro over existing mechanisms —
not a new execution engine, not a new permission system.

`stories/dev-story/rooms/landing.yaml` is the reference consumer: the whole
free-form "Claude-Code-like" floor described in
[`state-machine.md` §11](../stories/state-machine.md#the-agent-off-ramp--the-automatic-no-match-door)
and in
[dev-story's own README](../../stories/dev-story/README.md#the-free-form-workbench-landing)
is one `workbench:` block. Read those two first if you want the
worked example before the abstract shape below.

## Why this exists

Before this primitive, "governed free-form floor" meant hand-authoring four
things every time: a `write_mode: read_only` gate, an `agent_off_ramp:` Q&A
door, an `on_enter` effect invoking `host.agent.task` with a guard/`once:`/
bind, and a catch-all intent to capture the utterance into world. dev-story's
`landing.yaml` did all four by hand (~150 lines) using the legacy
`tools:`/`bash_profile:`/`external_side_effect:` agent vocabulary rather than
the shipped WS `toolboxes:`/`effect:` vocabulary. Any second story wanting
the same floor had to copy the recipe by hand. `workbench:` is that recipe,
generalized, bound to WS, and enforced by load-time invariants so a
misconfigured workbench fails at `kitsoki test flows` / load time instead of
silently behaving like a read-only advisor or a write-anything agent.

## The model

```
free text ──▶ [routing: free_form_fallback / room default_intent]
                        │ (deterministic — no LLM; semantic-routing.md §1.6)
                        ▼
              workbench room's synthesized <room>_capture intent
              (sets <room>_request in world, self-targets the room)
                        │
                        ▼
              on_enter: host.agent.task(agent, prompt, acceptance_schema)
                guarded on world.<room>_request != '', once: true
                bound to the agent's WS toolbox + effect class
                (enforceToolbox, internal/host/agents.go:731)
                        │
              write_mode: read_only gate on every mutating tool call
                (internal/host/write_mode_gate.go — operator grant,
                or a headless deny with no operator attached)
                        │
                        ▼
              close-out note bound to <room>_note ─┬─▶ plain summary (done)
                                                    └─▶ hand-authored route-out:
                                                        an ordinary `on:` arc's
                                                        set:/emit_intent: effects
                                                        commit world BEFORE the
                                                        target room's on_enter
                                                        runs — never an
                                                        agent-constructed call

pure Q&A that never reaches the capture intent (rare — the capture sink
claims almost everything) ──▶ agent_off_ramp (read-only converse, unchanged)
```

## The declaration

```yaml
states:
  bench:
    workbench:
      agent: builder                # required — must resolve to WS effect write|external
      prompt: prompts/bench.md      # required — with.context.prompt template
      acceptance_schema: schemas/bench-note.json   # required — with.acceptance.schema
      capture_slot: bench_request   # optional, default "<room>_request"
      off_ramp_agent: qa_agent      # optional, default: same as agent
      context_args: {prior_summary: "{{ world.bench_note.summary }}"}  # optional
      plan: false                   # optional — see "The plan: true contract" below
```

`WorkbenchDecl` lives on `State.Workbench` (`internal/app/types.go`); see the
type's doc comment for the field-by-field contract. A state with no
`workbench:` block is byte-for-byte untouched — this is opt-in, additive
syntax, not a new default.

## The desugaring contract

`expandWorkbenches` (`internal/app/workbench.go`) walks every state in the
loaded tree and, for each non-nil `Workbench`, sets:

1. **`write_mode: read_only`** — the room now dispatches an agent via its
   own synthesized `on_enter`, so every mutating tool call that agent
   attempts holds for an operator write-mode grant
   (`internal/host/write_mode_gate.go`).
2. **`agent_off_ramp: {agent: <off_ramp_agent or agent>}`** — genuine Q&A
   that never reaches the capture intent falls through to a read-only
   converse turn instead of a hard rejection.
3. **One `on_enter` effect appended** invoking `host.agent.task` — guarded
   on `world.<capture_slot> != ''`, `once: true`, `working_dir:
   "{{ world.workdir }}"`, `acceptance.schema: <acceptance_schema>`,
   `context.prompt: <prompt>`, `context.args` seeded with `request` (the
   captured utterance) plus any `context_args` entries, `bind:
   {<room>_note: submitted}`, `on_error: <the room's own state path>`.
4. **A synthesized top-level intent `<room>_capture`** (registered in
   `def.Intents` so it resolves through the same lookup `default_intent`
   validation uses) with one required string slot (`request`), and one
   `on:` arc on the room that stores the utterance into `<capture_slot>`,
   resets `<room>_note`, and self-targets — so the fresh `on_enter` guard
   re-fires on the next turn. This intent is set as the room's
   `default_intent`.

This pass runs in `runLoadPipeline`
(`internal/app/loader.go`) **after `expandPhases` and before
`injectBuiltinMetaModes`** — after imports have folded (so an imported
room's `workbench:` block is visible in the merged tree) and before the
`roomDispatchesAgent` / write-mode precondition pass and agent
effect-taxonomy resolution run, so those existing checks validate the
*desugared* shape for free. This mirrors how `expandPhases`
(`internal/app/phases.go`) precedes the same passes for `phases:` — a
declarative block expanding into concrete state fields before validation,
not a new validation path of its own.

Names are always synthesized from the room, never author-chosen
(`<room>_note`, default `<room>_request`, `<room>_capture`) — this avoids
collisions across import aliasing the same way bare intent names already
must not collide, and means an author adopting `workbench:` never has to
invent naming.

## Load-time invariants

Enforced by `expandOneWorkbench` before any field is mutated, alongside the
required-field checks (`agent`, `prompt`, `acceptance_schema`):

- **Agent capability.** The named `agent:` must be declared in `agents:`,
  must use the WS `toolbox:` vocabulary (not the legacy
  `tools:`/`bash_profile:`/`external_side_effect:` triplet), and must
  resolve to `effect: write` or `effect: external`. A workbench dispatches
  work that makes changes; a read-only agent belongs behind a hand-authored
  `agent_off_ramp:` instead of `workbench:`. This feeds the *existing*
  agent-effect-taxonomy machinery (`resolveAgentEffect`,
  `internal/app/loader.go`) rather than adding a parallel check.
- **Mutual exclusion.** A state may not combine `workbench:` with a
  hand-authored `write_mode`, `agent_off_ramp`, or `default_intent` — the
  macro sets all three itself, so a hand-authored value alongside it is an
  unresolvable ambiguity and fails the load rather than silently picking a
  winner.
- **The `plan: true` contract.** When set, `acceptance_schema` must declare
  a top-level `plan` object property whose `required` list is a superset of
  `{goal, step, verify}` — the shape
  [`stories/dev-story/schemas/plan.json`](../../stories/dev-story/schemas/plan.json)
  declares. This checks only the *contract* a planner and the hand-authored
  propose/accept/apply/verify rooms must agree on
  (see [`ad-hoc-plan.md`](../stories/ad-hoc-plan.md)); it does not
  code-generate those rooms — one real consumer is not enough evidence to
  freeze that shape into the macro (open question, tracked in the now-mostly-
  landed proposal history).

Violating any of these is a load error (`kitsoki test flows` / any loader
entry point), not a runtime surprise.

## The deterministic-seam rule (non-negotiable)

**A route-out to another authored pipeline is always an ordinary `on:` arc's
`set:`/`emit_intent:` effects — never an agent-constructed call.** When the
workbench agent's close-out note names a target (e.g.
`landing_note.route.intent`), the transition that fires is hand-authored:
the agent *names* an intent in its structured note, the engine *sets the
world and fires the transition*, deterministically and recorded in the
trace. The agent never constructs the `host.*` call or the target room's
`initial_world` itself.

This is the GU (generalized-usage) cycle-6–8 lesson: three independent
failures at exactly this seam, all because an LLM operator was trusted to
pass `initial_world` reliably. It didn't generalize, and `workbench:`
consumers don't get to skip it — the macro synthesizes the *entry* into the
agent dispatch, but every *exit* to a downstream authored room stays a real
transition with real effects, full stop.

Note also that the effect used for a route-out is the singular
**`emit_intent:`** (one templated intent string —
`internal/app/types.go`'s `Effect.EmitIntent string` field), not a plural
`emit_intents:` array; see `stories/dev-story/rooms/landing.yaml`'s
`take_route` transition for the worked shape.

When a workbench models an abandonable task, put the dispatching room in
`operation:` scope and make the route-out transition commit only the values the
downstream authored room should treat as facts. Intermediate notes, host
results, and retry state should stay in the operation overlay, be persisted with
`persist_draft`, or be abandoned automatically on exit. That keeps this
deterministic route-out rule intact without leaking scratch state into durable
world.

## Dispatch and permissions

The synthesized `on_enter` call is an ordinary `host.agent.task` invocation.
It goes through `enforceToolbox` (`internal/host/agents.go:731`) exactly
like any hand-authored dispatch. **`workbench:` adds zero new permission
surface** — WS's `toolbox:`/`effect:` declaration on the named agent is the
only thing that governs what the synthesized dispatch may touch. The
off-ramp and any route-out likewise ride existing, unmodified mechanisms:
`maybeOffRamp` (`internal/orchestrator/offpath.go`) and the normal
transition/effect pipeline, respectively.

## Decision recording

No new trace event kind. The synthesized `on_enter host.agent.task` records
the ordinary `agent.call.*` events (state path identifies which room's floor
produced the dispatch); the write-mode gate records
`machine.write_mode_granted` (or a headless deny) exactly as it does for a
hand-authored workbench; a route-out records the ordinary
`TransitionApplied` + intent-emission provenance.

One additional signal rides the existing `turn.end` payload rather than a
new event: `usable_kitsoki_gate` (`internal/orchestrator/workbench_gate_signal.go`),
the S6 usable-kitsoki-gate producer contract
([`docs/tracing/usable-kitsoki-gate.md`](../tracing/usable-kitsoki-gate.md)).
It carries `candidate_completed` / `silent_bounce` / `misroute_adjacent` /
`evidence_refs`, derived from the dispatching state's on-error outcome. When
the dispatching room's world carries a scenario's `expected_effects` list
(S4's scenario IR) under the `<noteKey>_expected_effects` convention key,
`candidate_completed` is a real join against those effects (substring match
over the workbench's own bound close-out note); otherwise it falls back to
the narrower dispatch-success proxy. `misroute_adjacent` remains hard-false —
see that file's own doc comment for the full contract and its one remaining
honest gap.

## Testing without an LLM

- **Stateless unit** (`internal/app/workbench_test.go`, testdata under
  `internal/app/testdata/workbench/`): a minimal `workbench:` block desugars
  into the expected `State` shape; each invariant violation produces the
  expected load error.
- **Flow fixture** (`internal/testrunner/flows_workbench_smoke_test.go`,
  app + flows under `testdata/apps/workbench_smoke/`): a synthetic
  single-room story exercising free-text capture, a stubbed
  `host.agent.task` dispatch via a flow cassette, the write-mode hold, the
  off-ramp's residual Q&A path, and a hand-authored route-out whose
  `set:` effect commits to world before the target room's `on_enter` reads
  it — no LLM anywhere in the loop.
- **Decision-recording proof**
  (`internal/testrunner/flows_workbench_smoke_trace_test.go`): asserts the
  synthesized dispatch's `AgentCalled` event carries the dispatching room's
  state path, using `host_cassette:` rather than `host_handlers:` (the
  latter replaces the handler wholesale and never writes `AgentCalled`).
- **dev-story's flow-fixture suite** is the regression bar for any story
  migrating an existing hand-rolled floor onto `workbench:` — run via
  `go run ./cmd/kitsoki test flows stories/dev-story/app.yaml`.

## The conversational lane (`agent_off_ramp.capture_free_text`)

The workbench above dispatches **write work**. Its sibling is the
**conversational lane**: one persistent, engine-owned conversation per room
for everything the room's deterministic commands don't cover — follow-ups,
"what happened?", "what should I do next?". A room opts in with one line:

```yaml
states:
  main_ops:
    agent_off_ramp: { agent: git_assistant, capture_free_text: true }
```

`stories/git-ops/rooms/main_ops.yaml` (and its two sibling hubs) is the
reference consumer — it replaced a hand-rolled per-room `discuss` intent +
`host.chat.resolve` + `host.agent.converse` arc duplicated across three
rooms, five world vars, and a view-injected answer.

What the engine owns, so the story carries **zero** conversation plumbing:

- **Deterministic entry.** The loader (`expandOffRampCaptures`,
  `internal/app/offramp_capture.go`) synthesizes a `<room>_discuss` intent
  (one required `message` slot) and sets it as the room's `default_intent`,
  so the deterministic free-text tier (`routeViaDefaultIntent`,
  [`semantic-routing.md` §1.6](semantic-routing.md)) sinks unmatched prose
  into it with **no main-turn LLM classification** — commands the operator
  actually names still win in the earlier tiers. Combining the flag with a
  hand-authored `default_intent`, or colliding with a declared
  `<room>_discuss` intent, is a load error.
- **No transition, no re-render.** The orchestrator diverts the synthesized
  intent BEFORE the state machine runs (`maybeConversationDivert`,
  `internal/orchestrator/offramp_conversation.go`, hooked into
  `submitDirect` so every live entry path — routed free text, menu submit,
  MCP drive — is covered) and returns the answer as a `ModeOffPath` outcome:
  resting state, menu, and world are untouched, and the room's `on_enter`
  chain never re-fires mid-conversation. A synthesized no-op self-arc keeps
  the intent legal for validation, menus, and bare-machine flow fixtures.
- **One conversation per room.** The chat thread is resolved per
  `(session, room)` (`offRampScope`, `internal/orchestrator/offpath.go`), so
  follow-up turns `--resume` the same warm Claude session; the transcript
  lives in the ChatStore, never in world. The typed `/freeform` door keeps
  its separate session-wide `off_path` thread.
- **Engine-composed context.** Every converse call carries a `room_context`
  block (`composeRoomContext`) appended to the agent's system prompt: the
  room's purpose (state description), the commands available this turn (with
  their intent descriptions, framed as "recommend these, don't perform their
  work yourself"), and the room's `relevant_world` values. Composed fresh
  per call, so a resumed conversation always sees the CURRENT room state.
- **Permissions unchanged.** The converse dispatch rides the same
  `enforceToolbox` as every agent call — the named agent's declaration is
  the whole permission surface, and the lane's advisory posture means a
  read-only agent is the right shape here (contrast the `workbench:`
  agent-capability invariant above, which *requires* write/external).

The clarify-triggered off-ramp (a plain `agent_off_ramp:` without the flag)
still exists and now shares the per-room thread and composed context; the
flag only adds the deterministic capture entry. Trace-wise the lane records
`OffPathEntered` with `reason: "conversation"` plus the ordinary
`OffPathQuestion`/`OffPathAnswer` pair — additive fields, replay-safe.

Testing without an LLM: loader desugaring + invariants in
`internal/app/offramp_capture_test.go`; the divert, per-room scope, and
context composition in
`internal/orchestrator/offramp_conversation_test.go`; the machine-level
no-op floor in `stories/git-ops/flows/workbench_discuss.yaml`; the
command-vs-prose routing split in
`stories/git-ops/intents/workbench_conversation.yaml`.

## What this is not

- Not a new execution engine — every synthesized primitive already existed
  and is unchanged by this pass.
- Not a new permission model — WS `toolbox:`/`effect:` remains the only
  mechanism `workbench:` honors.
- Not code-generation of the propose/accept/apply/verify sub-loop — `plan:
  true` only checks the schema contract; those rooms stay hand-authored.
- Not a bypass of the deterministic-seam rule above, under any
  circumstance.
