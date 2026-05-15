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

**Rasa** models *forms* as slot-filling containers with
`required_slots`; the form re-prompts for the first unfilled slot via
an `utter_ask_<slot>` response and deactivates when every required
slot is set
([Rasa Forms](https://legacy-docs-oss.rasa.com/docs/rasa/forms/)).
**Dialogflow CX** splits agents into *flows*, each composed of
*pages*; each page has a *form* — a list of parameters with prompts
— and *routes* (state handlers) that transition on intents,
conditions, or parameter-filled events
([Dialogflow CX Pages](https://cloud.google.com/dialogflow/cx/docs/concept/page)).
**Microsoft Bot Framework Adaptive Dialogs** are event-driven
declarative trees with triggers, actions, and `Recognizer` /
`Generator` / `Selector` components
([AdaptiveDialog class](https://learn.microsoft.com/en-us/javascript/api/botbuilder-dialogs-adaptive/adaptivedialog?view=botbuilder-ts-latest)).

**Steal:**

1. Page ≈ state with a form (Dialogflow CX). A state's slot list is
   first-class and the runtime loops until filled. Kitsoki's
   `MISSING_SLOTS` retry surface is the same shape.
2. Dynamic `required_slots` (Rasa) — the shape of the form can depend
   on previously filled values. Kitsoki's per-transition `when:`
   guards express the same kind of conditional requirement.
3. Separation of *recognizer* (turns free text into intent+entities)
   from *dialog manager* (moves through states). Kitsoki's LLM is the
   recognizer; the state machine is the dialog manager. This is the
   single most load-bearing borrowing in the design.

**Avoid:**

1. Rasa's story/rule training-data paradigm — it confuses "authoring"
   with "example data." Kitsoki is author-first, not ML-first.
2. Dialogflow's hidden built-in intents and implicit sys-parameters —
   opaque magic. Every intent in kitsoki is declared by the author.

---

## 4. LLM-specific orchestration

LangGraph's conditional edges run after a node and select the next
node deterministically, based on the node's return value
([LangGraph overview](https://docs.langchain.com/oss/python/langgraph/persistence)).
LLM-retry-with-validation-feedback is an established pattern:
libraries like Instructor
([overview](https://techsy.io/en/blog/best-llm-structured-output-libraries))
pipe the Pydantic validation error back into the next prompt,
achieving >95% recovery at small-schema sizes. MCP itself specifies
that *tool* errors should live inside the result envelope
(`isError: true`), not as JSON-RPC protocol errors, so the LLM sees
them and can self-correct
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
- Google Cloud — Dialogflow CX Pages.
  https://cloud.google.com/dialogflow/cx/docs/concept/page
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
