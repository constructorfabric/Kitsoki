# Prior Art

Kitsoki sits in the overlap of four established traditions: interactive
fiction parsers, statechart frameworks, conversational-AI dialogue
managers, and LLM orchestration libraries. None of these on their own
gives the shape kitsoki wants — free-text input, an author-declared
finite intent alphabet, deterministic transitions, replayable history
— but every one of them contributes a piece. This document records
the comparison so the design rationale stays legible as the system
evolves: when a design question comes back, the answer often involves
"we already chose A over B because…".

The framing is what kitsoki *steals* from each tradition and what it
*rejects*.

---

## 1. Interactive fiction engines

**Inform 7** compiles an English-like source into an I6 program whose
parser matches input against *grammar tokens* that produce noun-phrase,
preposition, number, or unparsed-text bindings
([Writing With Inform §17.4](https://ganelson.github.io/inform-website/book/WI_17_4.html)).
Rules fire in declared precedence, and verbs can be adapted to new
syntactic forms via conjugation
([§14.3](https://ganelson.github.io/inform-website/book/WI_14_3.html)).
**TADS 3** takes a similar approach with explicit verb grammar rules
combined with `VerbProd` action maps and `singleDobj`-style slot
keywords that the parser fills
([Creating Verbs in TADS 3](http://www.tads.org/howto/t3verb.htm)).

**Ink** takes the opposite tack: knots and stitches as addressable
units, diverts (`-> london`) for flow, and choices presented as a menu
— no parser
([ink docs](https://github.com/inkle/ink/blob/master/Documentation/WritingWithInk.md)).
**Yarn Spinner** similarly uses nodes with headers/bodies and
`<<jump>>`/`<<command>>` directives
([Nodes and Lines](https://docs.yarnspinner.dev/write-yarn-scripts/scripting-fundamentals/lines-nodes-and-options),
[Commands](https://yarnspinner.dev/docs/write-yarn-scripts/scripting-fundamentals/commands)).
**Twine/Harlowe** makes passages the unit and treats everything —
including navigation — as macro calls on an interactive text surface
([Harlowe 3.3.8 manual](https://twine2.neocities.org/)).
**ChoiceScript** exposes `*choice`, `*label`, `*goto` as the whole
grammar
([Introduction to ChoiceScript](https://www.choiceofgames.com/make-your-own-games/choicescript-intro/)).

**Steal:**

1. Grammar-first *intent* declarations (Inform, TADS) — authors should
   think in verbs and object slots, not in `if`/`else`. Kitsoki's
   `intents:` block with typed `slots:` is the lineal descendant.
2. Addressable navigation units with diverts/jumps (Ink, Yarn) — a
   clean way to represent a state graph in text. Kitsoki's `states:`
   map plus `target:` is the same idea.
3. Distinction between the *narrative* surface (what the user sees)
   and the *mechanics* (state mutations) — every IF system separates
   these. Kitsoki's `view:` template vs. `effects:` enforces the same
   split.
4. Parser fallbacks that say "I didn't understand" with targeted
   nudges — kitsoki's `guard_hint:` and the structured error envelope
   from the MCP `transition` tool are the LLM-era equivalent.

**Avoid:**

1. Inform 7's natural-language rule-declaration aesthetic — readable
   to English speakers but famously hard for programmers to debug when
   precedence goes wrong. Kitsoki's DSL is structured YAML, not prose.
2. Twine's "everything is a macro in a passage" model — it leaks
   presentation into logic. Kitsoki separates view from transition.

---

## 2. Workflow and state-machine frameworks

**XState / statecharts** give us the vocabulary: hierarchical states,
parallel regions, guards as pure synchronous predicates, composable
`and()`/`or()` and the `stateIn()` predicate for parallel-region
cross-reference
([Guards](https://stately.ai/docs/guards),
[Parallel states](https://stately.ai/docs/parallel-states)).
**SCXML** is the W3C-standardized version with the same semantics in
XML, including explicit event-to-transition matching and cond
expressions ([W3C SCXML Rec](https://www.w3.org/TR/scxml/)).

**Temporal** enforces workflow determinism by replaying an event
history and comparing re-emitted commands to the recorded sequence
([Temporal Workflow Definition](https://docs.temporal.io/workflow-definition)).
**LangGraph** provides a graph API with conditional edges plus a
checkpointer (`SqliteSaver`, `PostgresSaver`) that snapshots state per
step under a thread ID
([LangGraph Persistence](https://docs.langchain.com/oss/python/langgraph/persistence)).
**BPMN** adds a vocabulary of gateway types — exclusive, parallel,
inclusive, event-based, complex — that is a useful sanity check when
designing transitions
([Camunda BPMN Reference](https://camunda.com/bpmn/reference/)).

**Steal:**

1. Compound/hierarchical states + parallel regions (XState, SCXML).
   Without these, "you're in the edit flow AND still have the inventory
   sub-state" gets expressed as a combinatorial state explosion.
2. Pure synchronous guards that evaluate against a state snapshot
   (XState, SCXML). Side-effecting guards are a nightmare to replay.
3. Event-history-as-truth (Temporal). Replay is straightforward when
   the log is the source of state — see
   [`architecture.md` §7](architecture.md#7-persistence-replay-and-auditability).
4. Event-based gateways (BPMN) — "wait for whichever event arrives
   first" is the clean way to model LLM retry timeouts.

**Avoid:**

1. Temporal-scale durability. Kitsoki does not need workers,
   activities, and long polling; it hosts one conversation per session,
   per process invocation. The persistence model (SQLite + per-session
   event log) is deliberately the small version.
2. LangGraph's implicit state-is-whatever-you-return pattern — too
   flexible, too easy to lose track of what's persisted. Kitsoki's
   `world:` is a *declared* typed schema; effects are the only writer.

---

## 3. Conversational AI / dialogue frameworks

This is the tradition with the closest match to kitsoki's full shape,
and the comparison has to be done honestly: two production-grade
frameworks already implement the recognizer/manager split that the
rest of this document treats as kitsoki's central commitment. The
question is what's left after acknowledging that, not whether the
pattern is novel.

### 3a. Rasa CALM (Conversational AI with Language Models, 2024)

CALM is Rasa's pivot away from intent-classifier dialog management
toward an LLM-as-recognizer / flows-as-manager split
([Rasa CALM overview](https://rasa.com/docs/learn/concepts/calm/)).
The pieces:

- **`CommandGenerator`** translates free text into a fixed alphabet
  of *commands*: `StartFlow`, `SetSlot`, `Cancel`, `Clarify`,
  `ChitChat`, `HumanHandoff`, `SkipQuestion`
  ([Command Reference](https://rasa.com/docs/reference/primitives/commands/)).
  This is structurally identical to kitsoki's "free text → one of the
  declared intents."
- **`flows.yml`** declares flows as ordered `steps:`; the step types
  are `collect:` (ask for a slot), `action:` (run a custom Python
  action), `link:` (jump to another flow), `set_slots:`, `noop:`, and
  `if/else:` branches with `next:`
  ([Flows reference](https://rasa.com/docs/reference/primitives/flows/)).
- **`FlowPolicy`** executes the active flow deterministically against
  the command stream; the LLM never picks the next step.
- **`Tracker`** records every event (`UserUttered`, `SlotSet`,
  `ActionExecuted`, `FlowStarted`, `FlowCompleted`, ...) with full
  replay and "interactive learning" rewind.
- **Validation actions** re-prompt on rejected slot values; same
  shape as kitsoki's `SLOT_TYPE_MISMATCH` / `SLOT_NOT_IN_ENUM`.
- **Conversation-driven development** (CDD) — annotate tracker
  exports, suggest new flow steps or training phrases from real
  conversations.

CALM is mature: production at telcos, banks, governments since the
pre-CALM era (~2017); CALM-specific tooling shipped 2024–2025;
Rasa Pro adds hosted, on-prem, SOC2.

### 3b. Dialogflow CX, modern era (Generators, Generative Fallback, Playbooks)

CX's *page-with-form + routes* structure was already in §3's earlier
draft. The modern additions close most of the gap to kitsoki:

- **Generators** are LLM prompt templates with typed input/output that
  run inside a route's fulfillment, parameterised by session state
  ([Generators](https://cloud.google.com/dialogflow/cx/docs/concept/generators)).
- **Generative fallback** uses an LLM to handle no-match events with
  a system-instruction prompt rather than a static "didn't catch
  that" ([Generative fallback](https://cloud.google.com/dialogflow/cx/docs/concept/generative-fallback)).
- **Playbooks** are LLM-driven flows defined in natural language with
  declared `Tools` (OpenAPI / Webhook / Data Store / Function), used
  for the bits that don't fit the deterministic state graph
  ([Playbooks overview](https://cloud.google.com/dialogflow/cx/docs/concept/playbook)).
- **Webhook responses** (effects, in kitsoki terms) can fully rewrite
  page parameters and session state.
- **BigQuery export** ships every turn with intent, parameters,
  matched route, latency, and conversation ID — a labelled-datapoint
  log that predates the term.
- **Versions and environments** give A/B experiments per environment
  with traffic splits.

GA since 2020; runs at conversation volumes kitsoki will not see for
years.

### 3c. Bot Framework Adaptive Dialogs

Event-driven declarative trees with triggers, actions, `Recognizer`
/ `Generator` / `Selector` components, and adaptive expressions
([AdaptiveDialog class](https://learn.microsoft.com/en-us/javascript/api/botbuilder-dialogs-adaptive/adaptivedialog?view=botbuilder-ts-latest)).
The architectural shape is the same; the design centre is Microsoft
Composer's GUI authoring, which kitsoki rejects.

### Steal

1. **Recognizer / dialog-manager separation.** The single most
   load-bearing borrowing in the design — and one kitsoki *cannot*
   claim as original. Rasa CALM and Dialogflow CX both ship it.
2. **Page ≈ state with a form** (Dialogflow CX). A state's slot list
   is first-class and the runtime loops until filled. Kitsoki's
   `MISSING_SLOTS` retry surface is the same shape.
3. **Dynamic `required_slots`** (Rasa) — the shape of the form can
   depend on previously filled values. Kitsoki's per-transition
   `when:` guards express the same kind of conditional requirement.
4. **Conversation-driven development** (CALM CDD). Kitsoki's
   `replay-routing --target` and `inspect --synonym-suggestions` are
   embryonic versions; the mature pattern is worth borrowing more
   directly.
5. **First-class repair commands** (CALM: `Cancel`, `Clarify`,
   `CorrectSlot`, `SkipQuestion`). Kitsoki has off-path and
   meta-mode for free-form repair but lacks an explicit "user
   changed their mind about slot X" pathway. Worth adopting.
6. **Flow / page natural-language descriptions** that the LLM reads
   when choosing the next flow (CALM, Playbooks). Kitsoki's per-state
   intent allowlist is similar but doesn't have an explicit
   summary-prompt for the LLM to pick from.

### Avoid

1. **Rasa's story/rule training-data paradigm** — pre-CALM Rasa
   confused "authoring" with "example data." CALM mostly fixed this
   but `nlu.yml` examples remain part of the recommended setup. Kitsoki
   is author-first, no training corpus required.
2. **Dialogflow's hidden built-in intents and implicit
   sys-parameters** — opaque magic. Every intent in kitsoki is
   declared by the author.
3. **Playbook-style "LLM-drives-the-flow-in-natural-language"** — CX
   Playbooks let the LLM pick steps from a prose description. That's
   the recognizer encroaching on the manager. Kitsoki keeps the
   manager deterministic.

---

## 4. LLM-specific orchestration

### 4a. LangGraph (the closest match in the LLM-framework tradition)

LangGraph is the framework most often cited as kitsoki-adjacent. A
careful comparison shows the resemblance is real but shallower than
it first appears.

The pieces:

- **`StateGraph`** with a typed `State` (a `TypedDict` or Pydantic
  model). Nodes are Python functions of signature `(state) -> partial
  state update` (or a `Command` that combines update + routing)
  ([LangGraph low-level reference](https://langchain-ai.github.io/langgraph/concepts/low_level/)).
- **Edges:** static (`add_edge`) and conditional (`add_conditional_edges`)
  with a routing function returning the next node name (or `END`).
- **Reducers:** declared on each state key — e.g. `Annotated[list,
  add_messages]` — control how partial updates merge. The closest
  analogue to kitsoki's effect alphabet, but typed-Python not YAML.
- **Checkpointers** (`SqliteSaver`, `PostgresSaver`,
  `AsyncRedisSaver`) snapshot the full state per super-step under a
  `thread_id`; this is the persistence layer kitsoki's
  [`architecture.md` §7](architecture.md#7-persistence-replay-and-auditability)
  cites as inspiration
  ([LangGraph Persistence](https://langchain-ai.github.io/langgraph/concepts/persistence/)).
- **Interrupts** for human-in-the-loop: static
  `interrupt_before` / `interrupt_after` on a node, or the dynamic
  `interrupt()` function that pauses execution mid-node and resumes
  via `Command(resume=...)`
  ([Human-in-the-loop](https://langchain-ai.github.io/langgraph/concepts/human_in_the_loop/)).
- **Time travel** — replay or fork from any checkpoint by
  `thread_ts`.
- **Subgraphs** with explicit state-schema mapping (or a shared
  schema) for composition.
- **Send API** for dynamic fan-out (Map-Reduce-style parallel work).
- **Streams:** state deltas, node updates, LLM tokens; all observable
  in real time.

LangGraph's bet is that *the application is a graph the engineer
writes in Python.* The agent / LLM is one node among many; the graph
routes it.

#### Where LangGraph and kitsoki overlap

| Concern | LangGraph | Kitsoki |
|---|---|---|
| State graph as the unit of control | ✓ `StateGraph` | ✓ `states:` |
| Typed state schema | ✓ `TypedDict`/Pydantic | ✓ `world:` |
| Conditional routing | ✓ conditional edges | ✓ `when:` guards |
| Per-step state mutations | ✓ partial updates + reducers | ✓ ordered effects |
| Checkpointed event history per thread | ✓ checkpointers | ✓ event log |
| Resume after restart | ✓ `thread_id` | ✓ session DB |
| Time-travel replay | ✓ from any checkpoint | ✓ from event log |
| Human-in-the-loop pause | ✓ `interrupt()` | ✓ `_awaiting_reply` states |
| Subflows | ✓ subgraphs | ✓ imports |

That's a lot of overlap. Now the gaps that matter.

#### Where LangGraph is not what kitsoki is

1. **No intent alphabet.** LangGraph has no notion of "the LLM must
   call one of these named operations." Routing happens *after* a
   node runs, based on the node's return value or the agent's
   tool-call name. Kitsoki's load-bearing constraint — *free text is
   resolved to one of the state's declared intents before any
   transition fires* — has no first-class analogue. Builders fake it
   with structured-output prompts, but the framework doesn't enforce
   it.
2. **State is whatever you return; no declared schema gate.**
   LangGraph's `TypedDict` is hint-only at runtime by default; nodes
   can shove arbitrary keys in. Kitsoki's strict loader rejects
   unknown world keys. (Per
   [feedback memory on LangGraph's implicit state model](#) — already
   recorded in this document's §2 "Avoid".)
3. **No author/operator separation.** LangGraph is for the
   *engineer* who writes Python. Kitsoki is for the *author* who
   writes YAML and never touches Python unless they're adding a new
   host handler. Story authors and runtime engineers are different
   roles with different review cadences.
4. **No semantic-routing tiers.** Every routing decision in
   LangGraph runs whatever Python the engineer wrote; there is no
   built-in "try deterministic match, then word-bag, then template,
   then cache, then LLM." Builders implement that themselves per
   project.
5. **No declarative effect alphabet.** Kitsoki's `set` / `increment`
   / `say` / `invoke` / `emit` / `emit_intent` / `background` /
   `bind` / `on_error` / `on_complete` are a small, declarative
   vocabulary the loader validates. LangGraph nodes are arbitrary
   Python; the "effect" is whatever the function does.
6. **No host-binding / capability surface.** Kitsoki imports declare
   `host_interfaces:` and parents rebind them per alias
   ([`docs/imports.md`](imports.md)). LangGraph subgraphs share a
   process-global namespace of functions and tools; rebinding is
   "pass a different function in the closure."
7. **No emit-across-import-boundary mechanism.** Kitsoki's
   `IntentAliases` resolves a bare intent name (`accept`) emitted by
   a deeply imported child to the rewriter-renamed arc
   (`bf__accept` / `core__bf__accept`) at dispatch time. Subgraphs
   in LangGraph don't have this — sub-state is either shared or
   explicitly mapped, but there's no aliasing layer.
8. **No multi-surface transport contract.** LangGraph apps are
   serving Python over LangServe / FastAPI / Streamlit. Kitsoki ships
   TUI, MCP, Jira, file-append transports against one
   transport-agnostic machine.

#### Steal

1. **The checkpointer pattern with a `thread_id`.** Kitsoki's
   per-session event log is the same design.
2. **`interrupt()` as a paradigm.** LangGraph's mid-node pause-then-
   resume is a clean way to model long-running work that needs
   user input mid-flight. Kitsoki's `host.RequestClarification` is
   the same idea; the LangGraph API is worth studying as a model
   for the eventual host-call ergonomics.
3. **Time-travel by forking from a checkpoint.** Kitsoki's
   `replay --mode file_diff` is the rough equivalent; the explicit
   fork-and-edit-a-checkpoint UX in LangGraph is more polished.

#### Avoid

1. **State-is-whatever-you-return.** Already in §2's Avoid; LangGraph
   is the canonical offender among LLM frameworks.
2. **Planner agents that pick the next graph node from a prose tool
   description.** LangGraph supports this pattern (the `Supervisor`
   and `Swarm` templates) and many teams reach for it first. It's
   the recognizer encroaching on the manager, again.
3. **Per-project semantic routing reinvention.** Every LangGraph
   project that needs deterministic routing ends up writing the same
   "try regex, then keyword, then LLM" stack by hand.

### 4b. Structured-output and retry-feedback libraries

LLM-retry-with-validation-feedback is an established pattern:
libraries like Instructor pipe the Pydantic validation error back
into the next prompt, achieving >95% recovery at small-schema sizes
([overview](https://techsy.io/en/blog/best-llm-structured-output-libraries)).
MCP itself specifies that *tool* errors should live inside the
result envelope (`isError: true`), not as JSON-RPC protocol errors,
so the LLM sees them and can self-correct
([MCP schema reference](https://modelcontextprotocol.io/specification/draft/schema)).

**Steal:**

1. Validation-feedback retry loop with a bounded budget. Kitsoki's
   harness retries on a structured error from the machine's validator
   before falling through to a clarify-the-human surface.
2. Structured tool errors in-band, always JSON, with a `suggestions`
   array the LLM can read. The error-code enum in
   [`state-machine.md` §4](state-machine.md#4-intents-and-slots) is the
   stable contract for those errors.

**Avoid:**

1. "Planner" agents that reason about what tool to call over multi-step
   plans. Kitsoki wants a one-shot extraction: free text → intent. If
   the user needs multi-step, the state graph models it, not the LLM.

---

## 5. Why one generic MCP tool, not per-state typed tools

The most consequential MCP design choice was to register a single
`transition` tool with `{intent, slots}` payload, instead of a typed
tool per intent (or per state) that the LLM picks from.

Two reasons:

1. **Tool-list churn defeats caching.** The LLM's tool list would
   change every turn. Most LLM tool-calling APIs cache the tool list
   per session; reshuffling it per turn defeats caching and adds
   latency.
2. **Per-intent tools leak the author's internal names.** Authors
   would be forced to expose their intent identifiers (`hang_cloak`,
   `restart_from`) in tool schemas the LLM sees. That couples the LLM
   prompt to the app's internals and makes refactoring fragile.

With one `transition` tool the LLM's job is uniform across apps:
given the current state's intent catalog (included in the system
prompt), call `transition` with one of them. The validator does the
shape-checking and returns a structured error envelope on mismatch.

The trade-off accepted: the LLM has slightly more freedom to call the
wrong intent name, which the validator catches on the way in. In
exchange the prompt is stable, the cache hits, and refactoring the
intent vocabulary doesn't break the tool catalog.

This decision is not theoretical-only: today `internal/mcp/server.go`
registers exactly one tool by this name, and the validator is the
gatekeeper rather than the schema.

---

## 6. Why kitsoki, given that CALM, Dialogflow CX, and LangGraph all exist

The honest reading of §§3–4 is that kitsoki's *separation of
recognizer from dialog manager*, *event-sourced replay*, and *typed
slot-filling with validation feedback* are no longer differentiators.
CALM ships them. Dialogflow CX ships them. LangGraph ships the
graph + checkpointer half. Anyone claiming "kitsoki is the first
deterministic-core conversational engine" is several years late.

This section is the worked example of what *is* differentiated. It
uses `stories/bugfix/` — the seven-room bugfix pipeline — because it
exercises every load-bearing kitsoki mechanism in one self-contained
story. If a competing framework can express the same story with the
same guarantees and at comparable cost, kitsoki is redundant. If it
can't, the gap names what kitsoki is for.

### 6.1 Judge polymorphism with one `on_enter` chain

The bugfix story has three judge modes — `human`, `llm`, and
`llm_then_human` — selected by `world.judge_mode`. The defining
contract property:

> Every `_awaiting_reply` state runs **the same `on_enter` chain** in
> all three modes. The seven checkpointed rooms have identical
> `on_enter` shapes; only `<phase>` and the next-room target vary.
> ([`stories/bugfix/README.md`](../stories/bugfix/README.md))

The judge runs a single `host.oracle.decide` call gated by `when:
judge_mode != "human"`. The verdict lands in `world.llm_verdict`; an
`emit_intent:` effect auto-fires the verdict's intent in the same
turn when `confidence >= judge_confidence_threshold`. The state
graph is identical across modes; the *operator* swaps in and out.

What this requires from the runtime:

- A pure declarative effect (`emit_intent`) that synthesises a turn
  inside the current turn, with a depth cap
  (`machine.EmitIntentMaxDepth = 8`).
- A `when:` guard language that can read both `world.*` and the
  bound result of the previous oracle call in the same `on_enter`.
- A view layer that re-renders deterministically after the auto-fire
  resolves, with no LLM call.

What it produces:

- **One story, three deployment shapes.** The same artifact is the
  fully-manual triage tool, the fully-automated CI bot, and the
  hybrid escalation pipeline.
- **Replay parity across modes.** A `judge_mode=human` flow fixture
  and a `judge_mode=llm` flow fixture exercise the same state graph
  — divergence is a runtime bug, not an authoring concern.

What it would cost in CALM: three flows or one flow with imperative
`if/else` over `slots.judge_mode` at every checkpoint, replicated
seven times. The auto-fire requires a custom action that tail-calls
into the next flow step; CALM's command stream is not
author-extensible, so the engine cannot enforce the `emit_intent`
contract. The `host.oracle.decide` verdict's typed return
(`{verdict, intent, reason, confidence}`) becomes a custom Python
action with no schema gate.

What it would cost in Dialogflow CX: a webhook on every
`_awaiting_reply` page that fans out to one of three branches based
on a session parameter, with the LLM call written by hand inside the
webhook. The branch-equivalent of `emit_intent` is a same-turn page
transition fired from the webhook response — possible but with no
framework guarantee of replay determinism.

What it would cost in LangGraph: an engineer writes a Python node
that branches on `state["judge_mode"]`, calls the LLM if needed,
and returns a `Command` with the next node name. The seven
checkpoints become seven near-identical functions with a shared
helper. The contract that "all seven have identical shape" lives in
code review, not in a loader-enforced schema.

### 6.2 Cycle budgets as a declarative pattern

The bugfix story declares per-phase budgets:

```yaml
world:
  reproducing_cycle: { type: int, default: 0 }
  reproducing_budget: { type: int, default: 3 }
  # ... same shape for proposing/testing/validating/done
```

The `refine` arc at each checkpoint is gated by
`when: <phase>_cycle < <phase>_budget`. When the counter hits the
budget, the next `refine` routes to `@exit:abandoned` with
`abandon_reason=<phase>_cycle_budget_exhausted` instead of looping.
`restart_from` rewinds and resets the target phase's counter to 0.

The phase-template version of this (
[`state-machine.md` §13](state-machine.md#13-phase-templates))
compresses the seven-room expansion into one template plus per-phase
parameters; the loader auto-synthesises the counter increments and
guards.

This is a small, declarative retry-and-budget pattern that the
loader can validate and the replay log can audit. CALM's equivalent
is a `set_slots:` step plus an `if/else:` branch at every
checkpoint, written by hand. LangGraph's equivalent is incrementing
an integer in `state` and branching on it in a routing function,
also by hand. Neither framework has a *named* concept of a
phase-scoped retry budget that an author can declare and the engine
enforces.

### 6.3 Sub-story imports with capability rebinding

The bugfix story is *importable*. The dev-story and kitsoki-dev
stories embed it under an alias:

```yaml
imports:
  bf:
    path: stories/bugfix/app.yaml
    entry: idle
    world_in:
      ticket_id: world.current_ticket.id
      workdir: world.workspace.path
    hosts: declared            # strict — child can only invoke listed hosts
    host_bindings:
      ticket: host.jira
      vcs: host.bitbucket
      ci: host.jenkins
```

What this gives:

- **Capability rebinding by alias.** The child declares
  `host_interfaces: [ticket, vcs, ci, workspace, transport]` with
  default bindings (`host.local_files.ticket`, `host.git`, etc.).
  The parent can rebind any of them per import, swapping the local
  binding for `host.jira` / `host.bitbucket` / `host.jenkins`
  without touching child YAML.
- **World isolation with explicit projection.** The child sees only
  the world keys the parent projects via `world_in:`; everything else
  is the child's private namespace. On exit, per-`@exit:` `set:`
  blocks project results back. This is the inverse of LangGraph's
  shared-namespace subgraphs and CALM's global slot bag.
- **Host gating** (`hosts: declared`) — the child cannot invoke a
  host the parent did not authorise. The loader enforces this; the
  runtime cannot escape it.
- **`emit_intent` across the import boundary.** When the LLM judge
  inside the imported `bf` alias emits the bare intent `accept`, the
  runtime walks the leaf state's `IntentAliases` map to resolve it to
  the rewriter-renamed arc (`bf__accept` or, two layers deep,
  `core__bf__accept`). The author writes `emit_intent: accept` once;
  the runtime makes it work at any import depth. See
  [`docs/imports.md`](imports.md) "emit_intent across the fold
  boundary" and `resolveEmittedIntentName` in the runtime.

This composition story has no equivalent in CALM, Dialogflow CX, or
LangGraph. CALM flows can `link:` to other flows but share a global
slot namespace and have no capability-binding mechanism — the same
`action_open_pr` runs on every parent. CX flows are not parameterised
sub-apps. LangGraph subgraphs come closest (with explicit state
mapping) but have no notion of a capability surface that the parent
rebinds per child instance.

### 6.4 Oracle-verb taxonomy with per-verb guarantees

Each oracle call in `bugfix` carries an `agent:` selecting a persona,
and uses the verb dictated by the call's blast radius
([`stories/bugfix/README.md` §Oracle-split persona table](../stories/bugfix/README.md)):

| Persona | Verb | Why this verb |
|---|---|---|
| `reproducer`, `implementer`, `test_author`, `validator` | `task` | Agentic; may read or write files |
| `proposer` | `ask` | Read-only structured analysis; carries `bash_profile: read-only` |
| `judge` | `decide` | Verdict-only; typed return `{verdict, intent, reason, confidence}`; no file access |

The verb is the framework-level guarantee. `ask` and `decide` cannot
mutate files because the host handler enforces it (read-only Bash
profile, sandboxed validator). `task` carries an acceptance loop
with replay modes A/B/C
([`docs/hosts.md`](hosts.md) §replay modes). The author picks the
verb; the runtime enforces the contract.

CALM has one `action:` step type; the read-only vs read-write
distinction lives in the Python class the engineer writes. CX
webhooks have no read-only mode at all. LangGraph nodes are
arbitrary Python.

This matters specifically for the bugfix story because the
`judge_mode=llm` path *must* be side-effect-free — a judge that
accidentally writes a file corrupts the workspace mid-pipeline. The
`decide` verb makes that a loader-enforced property, not a
code-review property.

### 6.5 Flow fixtures as deterministic state-graph tests

`stories/bugfix/flows/` contains 25 YAML fixtures, each a scripted
sequence of intents-with-slots that exercises a path through the
state graph against stub host envelopes. They are not LLM tests —
they're FSM tests with the LLM stubbed at the oracle boundary.

```
happy_human.yaml                       — accept at every checkpoint
happy_llm.yaml                         — judge_mode=llm, confident verdict
llm_uncertain_holds.yaml               — uncertain verdict holds the state
refine_budget_exhaust_reproducing.yaml — exact counter-equals-budget edge
restart_from_resets_budget.yaml        — restart_from clears the counter
jump_to_each_target.yaml               — every jump alias, including unknown
mode_switch_full_to_quick.yaml         — bugfix_mode mid-flow flip
mixed_judge_swap.yaml                  — judge_mode swap mid-run
```

Each fixture runs in milliseconds and asserts the full event-log
shape. The complete suite covers the state graph as exhaustively as
unit tests cover a function. This is feasible only because (a) the
machine is pure, (b) the host boundary is the only LLM seam, and (c)
the fixture format is identical to the runtime event log.

CALM has e2e tests with stubbed LLM responses, but they run through
the full pipeline (NLU, command generator, policy, action server)
and are minutes-not-milliseconds. CX has test cases but they target
intent recognition, not state-graph traversal. LangGraph has unit
tests on individual nodes; whole-graph deterministic tests are an
engineering exercise per project.

This is downstream of [memory: tests must be fast]: a story author
runs the full bugfix fixture suite on every YAML edit and gets
feedback in under a second.

### 6.6 The composite picture

| Mechanism | CALM | DF CX | LangGraph | Kitsoki |
|---|---|---|---|---|
| Recognizer/manager split | ✓ | ✓ | partial | ✓ |
| Event-sourced replay | ✓ | partial | ✓ | ✓ |
| Typed slot validation w/ retry | ✓ | ✓ | DIY | ✓ |
| Conversation-driven dev | ✓ mature | ✓ | DIY | embryonic |
| Per-state intent allowlist | flow-scoped | page-scoped | DIY | ✓ |
| **`emit_intent` synthesised turn** | DIY action | DIY webhook | DIY | ✓ declarative, depth-capped |
| **Declarative cycle budgets** | DIY | DIY | DIY | ✓ phase template |
| **Sub-story imports w/ capability rebinding** | flow link | none | subgraph (no rebind) | ✓ `host_bindings` |
| **World isolation + projection per import** | none | none | partial | ✓ `world_in:` / per-exit `set:` |
| **`emit_intent` resolution across import depth** | n/a | n/a | n/a | ✓ `IntentAliases` walk |
| **Oracle-verb blast-radius taxonomy** | one action type | webhook | one node | ✓ ask / decide / task / extract / converse |
| **Sandbox-enforced read-only LLM call** | DIY | none | DIY | ✓ verb-level |
| **Semantic routing tiers before LLM** | NLU adapter | route matcher | DIY | ✓ four tiers |
| **Multi-surface transport (TUI / MCP / Jira / file)** | channels DIY | CX channels | LangServe | ✓ first-class |
| **Meta-mode read-only sidebar agent** | none | none | none | ✓ |
| **Background jobs + mid-flight clarification** | none | none | `interrupt()` | ✓ `host.RequestClarification` |
| **Flow fixtures = state-graph tests (ms)** | minutes | service-level | DIY | ✓ |

The pattern: the columns to the left match kitsoki on the
*conversational* core. The rows in **bold** are where kitsoki is
either uniquely declarative, uniquely composable, or uniquely fast
to author against. They are not separate features; they are
consequences of the same architectural commitment per
[memory: kitsoki moat is architecture] — *separate interpretive
decisions from deterministic execution, with pluggable operators per
decision and every decision recorded.* CALM, CX, and LangGraph each
make that commitment at the top level but not all the way down.

### 6.7 What "worth the effort to continue" means

Kitsoki should not be sold as "deterministic conversational AI" —
that battle is over and there are three winners already. The
defensible pitch is narrower and more specific:

1. **An authoring substrate for state-machine stories that compose
   like libraries** — with capability rebinding, world isolation,
   and intent-resolution across import depth. Per
   [memory: kitsoki audience breadth] this is for any team that
   wants to share a "bugfix pipeline" or "incident triage" story
   the way they share a Python package.
2. **A verb taxonomy that makes LLM blast radius a loader-checked
   property** — not a code-review property. The judge cannot write
   files; the proposer cannot mutate state; the runtime enforces it.
3. **A semantic-routing stack that pushes most turns off the LLM
   entirely** — four deterministic tiers before LLM fallback, with
   a promotion ladder from "LLM decision in trace" → "synonym" →
   "slot template" → "deterministic edge."
4. **A test surface that runs the state graph in milliseconds** —
   so authoring loops are seconds, not minutes.

These are the things that make the bugfix story possible in 408 lines
of YAML + 25 flow fixtures, importable into a parent story without
modification, with three judge modes and seven retry-bounded
checkpoints. None of CALM, CX, or LangGraph can produce that
artifact at that cost today. That gap is what kitsoki is for.

---

## Sources

- Inform 7, *Writing With Inform* §17.4 Standard tokens of grammar.
  https://ganelson.github.io/inform-website/book/WI_17_4.html
- Inform 7, *Writing With Inform* §14.3 More on adapting verbs.
  https://ganelson.github.io/inform-website/book/WI_14_3.html
- TADS 3 — Creating Verbs. http://www.tads.org/howto/t3verb.htm
- Ink — Writing With Ink.
  https://github.com/inkle/ink/blob/master/Documentation/WritingWithInk.md
- Yarn Spinner — Nodes and Lines.
  https://docs.yarnspinner.dev/write-yarn-scripts/scripting-fundamentals/lines-nodes-and-options
- Yarn Spinner — Commands.
  https://yarnspinner.dev/docs/write-yarn-scripts/scripting-fundamentals/commands
- Twine/Harlowe 3.3.8 manual. https://twine2.neocities.org/
- ChoiceScript — Introduction.
  https://www.choiceofgames.com/make-your-own-games/choicescript-intro/
- Stately (XState) — Guards. https://stately.ai/docs/guards
- Stately (XState) — Parallel states. https://stately.ai/docs/parallel-states
- W3C — State Chart XML (SCXML) Recommendation. https://www.w3.org/TR/scxml/
- Temporal — Workflow Definition. https://docs.temporal.io/workflow-definition
- LangChain — LangGraph Persistence.
  https://docs.langchain.com/oss/python/langgraph/persistence
- Camunda — BPMN 2.0 Symbols Reference. https://camunda.com/bpmn/reference/
- Rasa — Forms. https://legacy-docs-oss.rasa.com/docs/rasa/forms/
- Rasa — CALM (Conversational AI with Language Models).
  https://rasa.com/docs/learn/concepts/calm/
- Rasa — Commands reference.
  https://rasa.com/docs/reference/primitives/commands/
- Rasa — Flows reference.
  https://rasa.com/docs/reference/primitives/flows/
- Google Cloud — Dialogflow CX Pages.
  https://cloud.google.com/dialogflow/cx/docs/concept/page
- Google Cloud — Dialogflow CX Generators.
  https://cloud.google.com/dialogflow/cx/docs/concept/generators
- Google Cloud — Dialogflow CX Generative Fallback.
  https://cloud.google.com/dialogflow/cx/docs/concept/generative-fallback
- Google Cloud — Dialogflow CX Playbooks.
  https://cloud.google.com/dialogflow/cx/docs/concept/playbook
- LangGraph — Low-level concepts.
  https://langchain-ai.github.io/langgraph/concepts/low_level/
- LangGraph — Persistence.
  https://langchain-ai.github.io/langgraph/concepts/persistence/
- LangGraph — Human-in-the-loop.
  https://langchain-ai.github.io/langgraph/concepts/human_in_the_loop/
- Microsoft — AdaptiveDialog class reference.
  https://learn.microsoft.com/en-us/javascript/api/botbuilder-dialogs-adaptive/adaptivedialog?view=botbuilder-ts-latest
- Model Context Protocol — Schema Reference.
  https://modelcontextprotocol.io/specification/draft/schema
- mirascope — LLM Validation With Retries.
  https://mirascope.com/tutorials/more_advanced/llm_validation_with_retries/
- IFWiki — Cloak of Darkness.
  https://www.ifwiki.org/index.php/Cloak_of_Darkness
- TADS Guide — Cloak of Darkness specification.
  https://users.ox.ac.uk/~manc0049/TADSGuide/cloak.htm
- ESR — Open Adventure resource page.
  http://www.catb.org/~esr/open-adventure/
- Charm — Bubble Tea framework. https://github.com/charmbracelet/bubbletea
