# Runtime: contextual room routing and persistent room chats

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   — standalone
**Relation:** builds on [`ad-hoc-structured-plan.md`](ad-hoc-structured-plan.md)
and the shipped meta-mode / agent-off-ramp model. The structured-plan proposal
makes an ad-hoc plan executable; this proposal decides which conversation or
intent the user's next utterance belongs to before that plan can be proposed,
accepted, refined, edited, or rewound.

## Why

Kitsoki has two conversation surfaces that are becoming the same shape:

- **In-room ad hoc work** — a room-local agent handles free-form requests, can
  propose a structured plan, and should treat "ok", "change the verify gate",
  or "do it" as continuation of that same room conversation.
- **Meta mode** — a persistent sidebar conversation edits or explains the story
  itself, scoped to the active room and resumable later.

Today the boundary is too implicit. Deterministic routing already tries exact
intents, semantic matches, turn cache, and `default_intent` before the LLM
router (`docs/architecture/semantic-routing.md`). Once the LLM is involved,
however, the question is no longer only "which declared intent is this?" The
utterance might be an explicit intent with slots, a question about how to use
the story or kitsoki, an in-room free-form request that should go to the room's
agent, or a meta edit request that should enter the room's story-edit chat. The
operator needs to see that routing choice, correct it, and rewind to the fork
point when the wrong conversation took the turn.

## What changes

When deterministic tiers do not resolve a turn, the LLM tier becomes a
**contextual router** with four allowed verdict classes:

1. **`intent`** — one explicitly listed intent, with slots if needed. This is the
   only class that advances the state machine directly.
2. **`help`** — a question about how to use this story, the current room, or
   kitsoki itself. It enters the room's read-only explanation chat.
3. **`room_request`** — free-form work that belongs inside the current room's
   operating context. It enters the room's active in-room agent conversation,
   where skills, world context, and plan acceptance/refinement apply.
4. **`meta_edit`** — a request to edit the story, room, prompts, routing,
   schemas, docs, or kitsoki behavior itself. It enters the room's editable meta
   chat.

Every non-deterministic verdict is recorded and shown to the operator as a
small routing receipt: what was chosen, why, what context/chat received it, and
which alternatives were considered. The receipt has a "rewind routing" action
that returns to the turn before the verdict and lets the operator choose a
different class without losing the prior transcript.

This keeps the current moat: deterministic state transitions stay pure and
replayable; interpretive routing is explicit, recorded, and correctable.

## Impact

- **Code seams:** `internal/orchestrator` routing fall-through, off-ramp /
  converse dispatch, chat-thread resolution, trace emission, and submit-direct
  rewind hooks.
- **Vocabulary:** one contextual route verdict, room chat declarations, route
  receipts, and a rewind target that points at a prior routing decision.
- **Stories affected:** `stories/dev-story/rooms/landing.yaml` is the first
  adopter because the in-progress structured-plan flow already needs "ok" and
  refinement to land in the active room conversation.
- **Backward compat:** opt-in per room. Existing deterministic routing,
  `default_intent`, `agent_off_ramp`, and `/meta` continue to behave unchanged
  until a room declares contextual routing.
- **Docs on ship:** `docs/architecture/semantic-routing.md`,
  `docs/stories/meta-mode.md`, `docs/stories/state-machine.md`,
  `docs/tracing/trace-format.md`, and the dev-story README.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| room field | `contextual_routing` | `{enabled, help_chat, room_chat, meta_chat}` | opt-in; declares the chats the contextual router may target |
| route verdict | `context_route` | `{class, intent?, slots?, chat_id?, confidence, reason, alternatives[]}` | emitted only after deterministic tiers miss |
| chat scope | `room_chat` | `(app_id, room_path, kind, mode)` | persistent per room; one active chat per kind for v1 |
| chat mode | `readonly \| edit` | enum | read-only chats use `host.agent.ask`; edit chats use write-capable converse/task under write-mode gates |
| event | `turn.context_route_decided` | verdict + deterministic miss summary | records the LLM routing choice |
| event | `turn.context_route_overridden` | `{from_decision_id, old_class, new_class, reason}` | records operator correction |
| action | `rewind_route` | `{decision_id, new_class?}` | restores state/world/chat pointer to before the routed turn |

## The model

```
user input
  ├─ deterministic exact/example/synonym/template/cache/default_intent
  │    └─ hit: submit declared intent as today
  │
  └─ miss: contextual router (LLM, schema-bound, recorded)
        ├─ intent       ─▶ validate intent + slots ─▶ machine turn
        ├─ help         ─▶ room readonly chat
        ├─ room_request ─▶ room active agent chat
        └─ meta_edit    ─▶ room edit meta chat

receipt shown:
  "Routed as room_request to landing/workbench because ..."
  alternatives: intent:go_idea, help, meta_edit
  actions: accept · switch route · rewind
```

The deterministic tiers stay first. `default_intent` remains a deterministic
sink for rooms that want all unmatched prose to become one declared intent. A
room opts into contextual routing when it needs the richer distinction between
"continue the room agent", "ask for help", and "edit the story". If both
`default_intent` and `contextual_routing` are present, `default_intent` still
wins unless the room explicitly sets `contextual_routing.after_default: true` in
a later design; v1 should avoid that mixed posture unless a real story demands
it.

## Room chat model

Each room may expose three persistent chat lanes:

| Lane | Class | Mode | Agent verb | Scope |
|---|---|---|---|---|
| help | `help` | `readonly` | `host.agent.ask` | app + room |
| work | `room_request` | `readonly` or `edit` by room policy | `host.agent.converse` / `task` | app + room |
| meta | `meta_edit` | `readonly` or `edit` | existing meta-mode agent | app + room, or `kitsoki-self` for kitsoki edits |

Only one lane is active at a time in v1. A route verdict selects the active
lane, appends the user's utterance to that lane, and renders that lane's latest
assistant response in the room transcript. The operator can start a new chat,
resume an older chat, or switch lanes, but no background chat advances while
another lane is active.

The existing meta-mode persistence key
`(AppID, "meta:<modeName>", scopeKey=state_path)` becomes the model, not a
special case. Room work chats use the same chat store and the same state-path
scope, with a different kind (`room:<lane>`). Builtin `story.ask`,
`story.edit`, `kitsoki.ask`, and `kitsoki.edit` remain valid explicit meta
commands; contextual routing simply chooses one of those lanes when the operator
types natural language.

## In-room agent and plan acceptance

The in-room agent follows the same conversation protocol as the meta chat:

1. A `room_request` starts or resumes the room work chat.
2. The agent may return normal prose, a structured plan from
   `ad-hoc-structured-plan.md`, or a route suggestion.
3. "Ok", "apply it", "dry-run first", or "change the verify gate" is routed
   against the active chat before the router treats it as a fresh request.
4. If the active chat has a pending structured plan, affirmations become plan
   acceptance and follow-up content becomes plan refinement unless the operator
   overrides the route receipt.

That means the plan proposal, operator acceptance, agent application, verify
gate, and red-path refinement all happen in one persistent room conversation
instead of being reinterpreted as unrelated turns.

## Decision recording

The trace must make every interpretive choice reconstructable:

| Event | When emitted | Key fields |
|---|---|---|
| `turn.context_route_requested` | deterministic tiers missed and LLM routing starts | state path, allowed intents, declared lanes, active chat summary id |
| `turn.context_route_decided` | router returns a schema-valid verdict | class, intent, slots, chat kind, chat id, confidence, reason, alternatives |
| `turn.context_route_applied` | the verdict is actually dispatched | decision id, dispatch kind, resulting state/chat id |
| `turn.context_route_overridden` | operator rewinds or switches the verdict | prior decision id, replacement class, replacement chat/intent, operator reason |

Receipts in TUI/web read from these events. Replay uses the recorded verdict
instead of calling the router again, just like existing LLM intent routing and
turn cache behavior.

## Engine seams & invariants

- Contextual routing is a new final-tier router, not a replacement for the
  deterministic tiers in `docs/architecture/semantic-routing.md`.
- A room cannot enable a verdict class without declaring the backing lane or
  intent surface. Load-time validation should fail on a `help` lane without a
  read-only agent, a `meta_edit` lane without a meta mode, or a `room_request`
  lane without an agent.
- A read-only lane must dispatch through `host.agent.ask` or a read-only
  `converse` profile. An edit lane must use the existing write-mode gate and
  operator-ask bridge; headless tests deny writes.
- Router prompts are schema-bound and receive only the allowed classes,
  currently valid intents, intent slot schemas, active chat summaries, and room
  context. They must not receive permission to invent a fifth class.
- Rewind restores the machine state/world and the active chat pointer to the
  pre-dispatch snapshot. It does not delete chat rows; the superseded assistant
  turn remains archived and linked to the overridden decision.

## User feedback and rewind

Every LLM-routed turn surfaces a compact receipt:

```text
Routed as: room request -> landing work chat
Why: continuation of the pending migration plan ("ok go ahead")
Other plausible routes: intent go_idea, meta edit
Actions: switch route · rewind
```

`switch route` applies immediately when the target can consume the same text
without changing prior state. `rewind` is the stronger operation: return to the
pre-turn snapshot, mark the original decision overridden, and re-dispatch the
same utterance under the operator's chosen class.

For v1, rewind is one-decision deep and foreground-only. It does not try to
rewrite background jobs or multiple concurrent chat lanes because those do not
exist in this proposal.

## Backward compatibility / migration

Default behavior stays unchanged. Existing stories can continue to use:

- declared intents only;
- `default_intent` as a deterministic free-text sink;
- `agent_off_ramp` for no-match read-only answers;
- explicit `/meta story ask`, `/meta story edit`, `/meta kitsoki ask`, and
  `/meta kitsoki edit`.

Migration is opt-in. The first migration should be dev-story landing after the
structured-plan work is far enough along to need active-plan continuation. The
mechanical migration is:

1. declare `contextual_routing` lanes on `landing`;
2. remove any prompt-only workaround that asks the workbench agent to infer
   "ok go ahead" without a recorded route decision;
3. add flow fixtures that stub the contextual router verdicts and assert the
   chosen chat/intent dispatch.

## Verification

All automated coverage uses cassettes, stubs, or flow fixtures; no test should
call a real LLM.

- **Deterministic-first unit:** exact intent, synonym, template, cache, and
  `default_intent` hits never call the contextual router.
- **Router schema unit:** a miss with four allowed classes accepts only
  `intent`, `help`, `room_request`, or `meta_edit`; malformed fifth-class output
  fails loud and falls back to a clarification card.
- **Intent verdict flow:** stubbed contextual verdict `{class:intent}` validates
  slots and advances the machine exactly like current LLM routing.
- **Help verdict flow:** stubbed `{class:help}` appends to the room read-only
  chat and leaves state/world unchanged.
- **Room request flow:** stubbed `{class:room_request}` resumes the active room
  chat, preserves pending plan context, and routes "ok" to the plan accept path.
- **Meta edit flow:** stubbed `{class:meta_edit}` enters the room edit chat with
  the current state/view/world context and write-mode posture intact.
- **Rewind flow:** route a turn incorrectly, rewind to `meta_edit`, assert the
  state/world snapshot restored, the old decision is marked overridden, and the
  replacement chat receives the original text.
- **Trace replay:** replay a trace with recorded `turn.context_route_decided`
  events and assert no contextual router host call occurs.

## Tasks

```
## 1. Runtime router
- [ ] 1.1 Define the contextual route verdict schema and route classes
- [ ] 1.2 Add the final-tier contextual router after deterministic miss, behind an opt-in room field
- [ ] 1.3 Validate verdicts against current allowed intents, slot schemas, and declared chat lanes
- [ ] 1.4 Replay recorded verdicts without invoking an LLM

## 2. Room chat substrate
- [ ] 2.1 Generalize meta-mode chat keying into room-scoped chat lanes
- [ ] 2.2 Enforce readonly/edit lane posture through agent verb selection and write-mode gates
- [ ] 2.3 Track one active lane per room; support start new / resume previous / switch lane

## 3. Plan continuation
- [ ] 3.1 Attach pending structured-plan context to the room work chat
- [ ] 3.2 Route affirmations to accept/apply and follow-up content to refine before fresh routing
- [ ] 3.3 Migrate dev-story landing onto the contextual router once ad-hoc structured plan is present

## 4. Feedback, rewind, and surfaces
- [ ] 4.1 Emit route receipt data for TUI/web
- [ ] 4.2 Implement one-decision foreground rewind and route override
- [ ] 4.3 Add TUI/web controls for receipt, switch route, start/resume chat, and rewind

## 5. Trace, flows, and docs
- [ ] 5.1 Add contextual route trace events and replay fixtures
- [ ] 5.2 Add no-LLM flow fixtures for all four verdict classes and rewind
- [ ] 5.3 Update semantic-routing, meta-mode, state-machine, trace docs, and dev-story README; trim/delete this proposal
```

## Open questions

1. **Should `default_intent` always preempt contextual routing?** *Lean: yes for
   v1.* It preserves current stories and keeps contextual routing opt-in for
   rooms that intentionally remove the deterministic sink.
2. **Are `help` and `meta_edit` separate lanes or both meta modes?** *Lean:
   separate classes, shared substrate.* Help is read-only and should never imply
   edit permission; meta edit carries write posture and reload semantics.
3. **How much active-chat summary enters the router prompt?** *Lean: a small
   typed summary: lane kind, pending plan id, last user/assistant summary, and
   available actions.* Do not paste full transcripts into the router.
4. **Can the operator switch route without full rewind?** *Lean: yes when the
   original dispatch did not mutate state/world or write files; otherwise require
   rewind so the correction has a clean snapshot.*
5. **Should contextual routing replace `agent_off_ramp` eventually?** *Lean:
   not now.* Agent off-ramp remains the small read-only no-match tool; contextual
   routing is for rooms that need multiple conversation classes and receipts.

## Non-goals

- Background or concurrent room conversations. V1 has one active lane at a time.
- Multi-turn undo across several state transitions. Rewind is one route decision
  deep.
- Changing deterministic routing semantics, synonym matching, slot parsing, or
  turn cache behavior.
- Allowing the router to invent actions outside declared intents and lanes.
- Running real LLM tests in CI.
