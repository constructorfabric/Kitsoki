# The Architecture of a Story

This is the **front door** to how a kitsoki story is built and how it
runs. It walks the whole shape end-to-end — **rooms**, **phases**,
**intents**, **turns**, **room hooks**, **views**, and how the
**agent** plugs into **intent routing** and the **agent rooms** like
`/meta` — and points at the deeper reference for each piece.

A story is a **deterministic directed cyclic graph** with a scoped,
typed context (the *world*). YAML is only the authoring surface; the
runtime drives the graph and the LLM is called for narrow interpretive
sub-tasks only. If you want the *why* behind that split, read
[`../architecture/concept.md`](../architecture/concept.md) first; this
document is the *what and how*.

Where this doc summarises, the deep dives are authoritative:

| Topic | Deep dive |
|---|---|
| Rooms, states, phases, effects, guards, world | [`state-machine.md`](state-machine.md) |
| System design, the turn loop, the LLM boundary | [`../architecture/overview.md`](../architecture/overview.md) |
| The four-tier intent router | [`../architecture/semantic-routing.md`](../architecture/semantic-routing.md) |
| Agent verbs and `host.*` | [`../architecture/hosts.md`](../architecture/hosts.md), [`../architecture/agent-plugin.md`](../architecture/agent-plugin.md) |
| Agent rooms (`/meta`, named agents) | [`meta-mode.md`](meta-mode.md) |
| The authoritative YAML schema | `kitsoki docs app-schema` ([`../embedded/app-schema.md`](../embedded/app-schema.md)) |

---

## 1. The one idea

```mermaid
flowchart LR
    subgraph Room["Room"]
        User["User<br/>(or external thread)"]
        Router["Router<br/>(synonyms → templates → cache → LLM)"]
        App["State Machine<br/>(options defined by room)"]
    end
    World["World<br/>(typed, persisted)"]
    Hosts["Hosts + Agent<br/>(host.* / LLM calls)"]
    Ext["Environment<br/>(shell · files · LLM · Jira · …)"]

    User -- "free text" --> Router
    Router -- "named intent + typed slots" --> App
    App -- "transition + effects\n(binds results)" --> World
    World -- "read world state" --> App
    App -- "rendered view" --> User

    App -- "invoke: (declared effects)" --> Hosts
    Hosts -- "actions" --> Ext
    Ext -- "typed results" --> Hosts
    Hosts -- "result → bind" --> App
```

The arrow that matters is the second one. Free text comes in, but a
**declared, finite alphabet of intents** decides what can happen next.
The LLM (and the cheaper routing tiers in front of it) only *translate*
free text into one of the intents the current room exposes — it never
decides what to do, never writes the world, never invents an action.
Everything downstream of that translation is pure and replayable.

The one controlled crack in that purity is the side-channel on the
right: the story's declared `invoke:` effects are the *only* path to the
**environment** — the shell, files, an LLM, an external tracker. They
run through **hosts and the agent**, return a *typed* result that
`bind:`s back into the world, and record their inputs and outputs so the
turn replays exactly. The author chooses where those calls happen; the
machine never reaches outside on its own.

Three consequences fall out, and the whole rest of the architecture is
just these enforced mechanically:

- **The LLM can't invent actions.** If the room doesn't declare a verb,
  the user gets told "no".
- **The machine is pure.** Same world + same intent → same transition,
  always. No clocks, no randomness, no I/O inside the machine.
- **The author is in charge.** What can happen, in what room, with what
  guards and effects — all of it is reviewable YAML.

---

## 2. Rooms and states (the nodes)

### A room is *where you are*

Start with the word a reader already has an intuition for. A **room** is
a location in the story — the place the user currently stands. It is what
the TUI's location indicator shows as "where you are", and it owns the
per-location chrome: its `Footer`, `Theme`, and `Transcript`. A room has
a `view:` (what the user sees here), an `on:` map (the intents bound at
this location), and an `on_enter:` chain (what runs when you arrive). Move
between rooms by taking an intent; a story is a graph of rooms wired by
transitions. For most of this document, "room" is all you need.

### A state is the primitive a room is built from

Under the hood there is no separate "room" type — a room is a **state**,
and `State` is the real primitive. `type State` is *"a node in the
directed graph"* (`internal/app/types.go`), and it **nests**: a compound
or parallel state holds child states in its own `states:` map. So "state"
names a node at *any* level of the tree, while "room" names one
particular *use* of a state — a top-level one the operator can land on.
The struct draws the line directly: `Footer`, `Theme`, and `Transcript`
are each *"only meaningful on top-level (room) states; nested states must
leave it empty"*.

> **Every room is a state; not every state is a room.** A room is a
> top-level state. The atomic children inside a compound/parallel room
> are states the operator never lands on as a distinct location — they
> share the room's chrome and (unless overridden) its `on:` bindings.

In most stories every state is atomic *and* top-level, so room ≡ state
and the words get used interchangeably — the five `prd` rooms are five
atomic states. The distinction only bites when you reach for nesting:
then the compound parent is the room and its children are sub-states.
**Phase** is a third, related word: a *repeated* room (§10) — one
template instantiated several times in a pipeline.

A state — whether it surfaces as a whole room or as a child inside one —
comes in three flavours (`State.Type`, `internal/app/types.go`):

- **Atomic** — a leaf. Has a `view:`, an `on:` intent map, and an
  `on_enter:` effect list.
- **Compound** (`type: compound`) — groups children under a `states:`
  map with a required `initial:` child. Children inherit the parent's
  `on:` bindings unless they override an intent. `target: .` means "stay
  in this atomic state"; `target: bar.lit` is a dot-path into a child.
- **Parallel** (`type: parallel`) — all children run concurrently; an
  `emit:`ted event from one region is observed in every sibling. Use it
  for orthogonal axes (e.g. lighting and narration cadence).

```yaml
states:
  # Atomic — a leaf: view + intent map + entry effects.
  street:
    view: "A dusty street. {{ world.gold }} gold in your poke."
    on: { enter_saloon: [{ target: saloon }] }

  # Compound — child states under `states:`, with a required `initial:`.
  vault:
    type: compound
    initial: locked
    on: { inspect: [{ target: . }] }              # children inherit this arc
    states:
      locked:
        view: "The vault is shut."
        on: { crack: [{ target: vault.open }] }   # dot-path into a child
      open:
        view: "The vault swings wide."

  # Parallel — regions run at once; an `emit:` crosses to every sibling.
  world_clock:
    type: parallel
    states:
      weather:
        type: compound
        initial: dry
        states:
          dry:  { on: { advance: [{ target: weather.rain, effects: [{ emit: precip }] }] } }
          rain: { view: "Rain." }
      calendar:                                   # observes the sibling's emit
        type: compound
        initial: day
        on: { precip: [{ target: calendar.day, effects: [{ set: { wet: true } }] }] }
        states:
          day: { view: "Day {{ world.day }}." }
```

States are nodes; transitions are edges. **Cycles are the typical
shape** — a main-menu room that loops to itself, a proposal lifecycle
that bounces draft↔review, a phase pipeline that retries on failure.
The loader enforces a handful of invariants (every `target:` resolves,
every intent is declared, every host is allow-listed, every
`relevant_world:` key exists) but never inspects "shape" beyond what it
needs for the next transition.

Full vocabulary and worked YAML: [`state-machine.md` §2–3](state-machine.md#2-the-directed-cyclic-graph).

---

## 3. Room hooks (the lifecycle)

A room reacts at three moments. There is deliberately **no `on_exit`**
hook — exit side effects belong on the *transition* leaving the room, so
they're visible on the edge that causes them.

| Hook | YAML | Fires when | Struct |
|---|---|---|---|
| **Entry** | `on_enter:` | the room is (re-)entered | `State.OnEnter []Effect` |
| **Intent** | `on: { <intent>: [transitions] }` | the user takes an action bound here | `State.On map[string][]Transition` |
| **Transition** | `effects:` on a transition arm | that specific edge is taken | `Transition.Effects []Effect` |

Two more hooks live on an *effect* (an `invoke:` specifically), not on
the room:

| Hook | YAML | Fires when | Struct |
|---|---|---|---|
| **Error redirect** | `on_error: <room>` | the `invoke:` returns an error | `Effect.OnError string` |
| **Job completion** | `on_complete:` | a `background: true` invoke terminates | `Effect.OnComplete []Effect` |

Those hooks compose into one room-local turn lifecycle, and that
composition **is** the state machine: everything inside the box below is
declared in the room's YAML, and the runtime only ever walks what the
room declares — calling out to a host handler or an LLM agent at each
`invoke:` and folding the typed result back into the world via `bind:`
before the next effect runs. (Purple = an LLM call, grey = a host
handler, blue = user-facing.)

```mermaid
flowchart TD
    classDef ext fill:#F3F4F6,stroke:#9CA3AF,color:#000
    classDef llm fill:#EDE9FE,stroke:#8B5CF6,color:#000
    classDef io  fill:#DBEAFE,stroke:#2563EB,color:#000

    enter(["a transition's target: lands here"]):::io

    subgraph ROOM["one room's YAML = one state-machine node + its outgoing edges"]
      direction TB
      subgraph OE["on_enter: — runs on every (re-)entry"]
        direction TB
        oe_eff["effects, in declared order:<br/>set · invoke · emit_intent<br/>(each may carry a when: guard)"]
        oe_bind["bind: result keys → world"]
        oe_eff --> oe_bind
      end
      view["render view<br/>(pongo over world)"]:::io
      wait(["user types → routed to one intent"]):::io
      subgraph ON["on: &lt;intent&gt;: — the room's outgoing edges"]
        direction TB
        pick{"first arm whose<br/>when: guard passes"}
        on_eff["effects: set · invoke · say"]
        on_bind["bind: result keys → world"]
        tgt{"target:"}
        pick --> on_eff --> on_bind --> tgt
      end
      OE --> view --> wait --> ON
    end

    host[["host.* handler<br/>shell · files · jira · …"]]:::ext
    agent[["agent.* — an LLM agent<br/>decide · ask · task · converse"]]:::llm

    oe_eff -- "invoke: call out" --> host
    oe_eff -- "invoke:" --> agent
    host   -- "typed result" --> oe_bind
    agent -- "typed result" --> oe_bind
    on_eff -- "invoke:" --> host
    on_eff -- "invoke:" --> agent
    host   -- "typed result" --> on_bind
    agent -- "typed result" --> on_bind

    enter --> OE
    tgt -->|". — stay, skip on_enter"| view
    tgt -->|"this room — re-enter"| OE
    tgt -->|"another room"| next(["that room's on_enter"]):::io
```

The box is the whole machine for this node: `on_enter` settles the world
on arrival (re-running idempotently on every re-entry — see below), the
view renders from that world, the user's input is routed to exactly one
`on:` intent, the first arm whose `when:` passes fires its effects, and
`target:` chooses the next node — `.` to re-render in place (skipping
`on_enter`), this room to re-enter (re-running `on_enter`), or another
room to hand off. The only steps that leave the box are the `invoke:`
round-trips to a host or the agent; their results re-enter only through
`bind:`.

All four foreground hooks on one room:

```yaml
states:
  apply_patch:
    on_enter:                       # Entry — runs on every (re-)entry
      - say: "Applying the patch…"
      - invoke: host.run
        id: apply                   # call-site address (flow fixtures stub by id)
        with: { cmd: "git apply {{ world.patch }}" }
        bind: { apply_exit: exit_code }
        on_error: apply_failed      # Error redirect — enter this room on error
    on:
      retry:                        # Intent — a bound action
        - target: .
          effects:                  # Transition — fires only on this edge
            - increment: { attempts: 1 }
```

### `on_enter` must be idempotent

`on_enter` is **not** a once-per-lifetime hook. The same chain re-fires
on `/reload` / hot-reload (`RerunOnEnter`), on explicit self-re-entry
(`target: <thisRoom>`, distinct from `target: .`), and on `on_error:`
sibling redirects. So any side-effecting `invoke:` in `on_enter` runs
two-or-more times per session. Make it idempotent — prefer get-or-create
host verbs (`host.chat.resolve` over `host.chat.create`), guard the
invoke on its own absence (`when: "world.idea_chat_id == ''"`), or keep
content-producing LLM calls out of `on_enter` entirely. The canonical
failure is a chat room whose `on_enter` unconditionally creates a chat:
a mid-conversation `/reload` orphans the thread. Full rules and the
fix patterns: [`state-machine.md` §8](state-machine.md#on_enter-must-be-idempotent).

### `on_error` redirects and the recursion cap

When an `invoke:` errors and `on_error:` names a room, the orchestrator
*enters* that room — re-running its `on_enter` chain. A self-redirect is
short-circuited; a sibling redirect that re-invokes the same failing
call would loop, so redirect entry is depth-capped
(`EnterRedirectMaxDepth = 4`). When the cap trips, the engine appends a
`HarnessError` and ends the turn cleanly. Prefer hosts that return
success idempotently over erroring so the redirect never forms.
([`state-machine.md` §5](state-machine.md#on_error-redirects-and-the-recursion-cap).)

### Background completion

An `invoke:` marked `background: true` spawns a job; the conversation
continues immediately. When the job finishes, the orchestrator fires the
matching `on_complete:` effects as a synthetic turn, updates the world,
and posts an inbox notification. Mid-flight, a handler can call
`host.RequestClarification` to pause and ask the user a question. Full
lifecycle: [`background-jobs/`](background-jobs/README.md).

```yaml
on_enter:
  - invoke: host.run
    background: true                # spawn a job; the turn continues now
    with: { cmd: "make test" }
    bind: { test_exit: exit_code }
    on_complete:                    # later, as a synthetic turn, when it ends
      - emit_intent: tests_finished
```

---

## 4. Effects (the only mutators)

Effects are the enumerated alphabet of mutations a transition or hook
can perform — the LLM never writes the world directly, only effects the
*author* declared do. They run in declaration order over an immutable
per-turn world snapshot (each effect sees the result of prior effects in
the same list).

| Verb | YAML | Meaning |
|---|---|---|
| `set` | `set: { k: "{{ tpl }}" }` | assign world variables |
| `increment` | `increment: { k: 1 }` | integer delta on a numeric var |
| `say` | `say: "line"` | append a narration line to the view |
| `invoke` | `invoke: host.X` | call a registered `host.*` handler |
| `emit` | `emit: evt` | broadcast an event to parallel siblings |
| `emit_intent` | `emit_intent: name` | dispatch a synthetic intent this turn (auto-advance); depth-capped at 8 |

`invoke:` carries sub-fields: `with:` (templated args), `bind:`
(`{world_key: result_key}` to copy fields out of the result),
`on_error:`, `background:`, `on_complete:`, and `id:` (a call-site
address that flow fixtures stub by). Args and results are **typed** per
host (`stdout`/`exit_code` for `host.run`; `answer`/`chat_id` for
`host.agent.converse`). Full table:
[`state-machine.md` §5](state-machine.md#5-effects); the host catalogue
is [`../architecture/hosts.md`](../architecture/hosts.md).

A chain reads top-to-bottom, each verb seeing the prior verbs' writes:

```yaml
effects:
  - set: { greeting: "Hello, {{ slots.name }}" }   # assign a world var
  - say: "{{ world.greeting }}"                     # narrate a line
  - invoke: host.agent.decide                      # call a host…
    with:
      question: "Does this patch fix the bug?"
      options: [accept, refine, reject]
    bind: { verdict: choice }                       # …copy result.choice → world.verdict
  - emit_intent: "{{ world.verdict }}"              # route on the bound result
```

**How a host result lands in the world.** A handler returns a *typed
result* — a small map of named fields (`host.run` → `stdout` /
`exit_code`; `host.agent.decide` → `choice` (plus `confidence`,
`reason`); `host.agent.converse` → `answer` / `chat_id`). A bare
`invoke:` runs the call for its side effects and throws the result away;
the **only** way a field reaches the world is to name it in `bind:`.
Each entry is `world_var: source`, where `source` is either:

- a **dot-path** into the result — `choice`, `submitted.summary_markdown`,
  `names[0]` (trailing `[N]` indexes array fields) — copied verbatim
  into the world var
  (`internal/orchestrator/host_dispatch_bind.go::lookupBindPath`); or
- an **expr template** (any `source` containing `{{`), rendered against a
  scope where `result` is the result map and `world.*` is the
  post-prior-binds world — so a value can be *derived* as it lands, e.g.
  `bind: { party_names: "{{ join(result.submitted.names, ',') }}" }`.

The orchestrator applies each bind *after* the call returns, records it
as a `set` on the world, and re-renders the view against the updated
world (§5). So `bind: { verdict: choice }` above lifts the decide
result's `choice` field into `world.verdict`, which the following
`emit_intent:` then routes on.

> **Current limitation — no post-bind hook for synchronous invokes.**
> `bind:` lands a result *value* (per above), but there is no post-bind
> *effect chain* for a synchronous `invoke:`. Because the machine only
> **queues** host calls (the orchestrator
> dispatches them *after* `machine.Turn` returns), effects that run at
> machine-time see the **pre-bind** world: a `set:` or `increment:`
> placed *after* an `invoke:` in the same chain does **not** see the
> just-bound value, and a `when:` that references the unbound key is
> surfaced as an authoring error (`machine.go::applyEffectsTraced`).
> Only three things re-evaluate against the post-bind world today:
> `emit_intent:` (deferred and re-run by `settlePostBindEmits`), a
> *subsequent* `invoke:`'s `with:` (re-rendered by `rerenderHostArgs`),
> and the *next* room's `on_enter:` after an `emit_intent` lands there.
> `on_complete:` runs post-bind too — but **only for `background: true`**
> invocations. So deriving a *single* world value from an `agent.decide`
> result is fine (a template `bind:`, above), but a *chain* of dependent
> `set:` / `increment:` / `when:` effects keyed off that result has no
> natural home for a synchronous call: today you route-only via
> `emit_intent`, push the effects into the next room's `on_enter`, or
> shape the result in the decide schema/prompt. Closing this — a
> per-invoke `then:` list (or unifying `on_complete:` to fire for
> synchronous invokes), folded with the `on_first_enter` /
> on_enter-idempotency question (§3) — is tracked in
> [`../proposals/post-host-bind-hook.md`](../proposals/post-host-bind-hook.md).

---

## 5. Views (what the user sees)

`view:` is a template (pongo2 / `{{ … }}` over the `internal/expr`
scope). It reads `world.*`, `slots.*`, and a few orchestrator-injected
keys (`run.session_id`, `run.turn`). It supports `{{ if }}…{{ else }}…
{{ end }}` and `{{ range … }}…{{ end }}` blocks.

A view can render the **live action menu** inline — the same computed
set of valid intents the TUI shows in its right-side pane — via
`menu.primary` / `menu.blocked` and the helper functions
`available(name)`, `blocked(name)`, `blocked_reason(name)`,
`intent_status(name)`. That lets the prose itself say "what can I do
right now".

```yaml
view: |
  {{ world.location_desc }}

  {{ if world.gold > 0 }}You have {{ world.gold }} gold.{{ else }}Your poke is empty.{{ end }}

  You can:
  {{ range menu.primary }}- {{ .display }}
  {{ end }}
```

One timing rule worth internalising: **the view renders *after*
`on_enter` `bind:` settles.** If a room's `on_enter` invokes a host with
`bind: { artifact: … }`, the view is re-rendered against the post-bind
world, so `{{ world.artifact.field }}` is already populated — no
`?? "(pending)"` fallback needed unless the bind is *conditional* (gated
by a `when:` on the invoke). Details and a worked example:
[`state-machine.md` §3 "View renders run AFTER on_enter bind settles"](state-machine.md#view-renders-run-after-on_enter-bind-settles).
How elements actually render on screen is the TUI's job
([`../tui/README.md`](../tui/README.md)); how a story *should* look is
[`story-style.md`](story-style.md). And because most surfaces are
plain-text comment threads (Jira, Bitbucket, Slack/Teams), every view
must also read as text alone — never leaning on color or an interactive
widget for anything essential
([`../architecture/transports.md` §7](../architecture/transports.md#7-every-story-must-work-text-only)).

---

## 6. Intents and slots (the alphabet)

An **intent** is a named action; it is the atom of free-text
translation. It may carry typed **slots** (`string`, `int`, `bool`,
`enum`, `text`, `list[T]`, …). Slot validation runs *before* any guard,
so a malicious or malformed payload can't slip past the declared
constraints.

```yaml
intents:
  go:
    title: "Go"
    examples: ["go south", "head north", "n"]
    synonyms: ["walk", "head"]
    slots:
      direction:
        type: enum
        values: [north, south, east, west]
        required: true
```

The harness never sees per-intent tools. The MCP server registers
exactly **one** generic `transition` tool; the machine's validator
polices the `intent` field and returns a structured error envelope
(`UNKNOWN_INTENT`, `INTENT_NOT_ALLOWED_IN_STATE`,
`SLOT_MISSING_REQUIRED`, `SLOT_TYPE_MISMATCH`, `SLOT_NOT_IN_ENUM`,
`GUARD_FAILED`, …) the harness can self-correct against. Why one generic
tool: a per-state tool catalog would change every turn and defeat prompt
caching, and would leak author-internal intent names into the LLM
prompt. Full reasoning and the error-code table:
[`state-machine.md` §4](state-machine.md#4-intents-and-slots).

Controlled navigation — "from phase 9 let the user go back to phase 3,
but only with a reason and only N times" — is not a jump primitive; it
falls out of four declarations cooperating (`next:` enumerates legal
destinations, `checkpoint_intents:` is the menu, slot schemas force the
context, guards + `cycle_budgets:` cap who and how often). See
[`state-machine.md` §10](state-machine.md#10-controlled-navigation-back-jumps-restart-and-feedback-arcs).

### The gate — the advancing intents are the decision point

The set of *advancing* intents a room exposes at its end — the ones
whose transitions move you onward — is its **gate**: the "what happens
next" decision. A gate is not a new primitive; it is just those intents
viewed as a choice. How it resolves depends on how many there are and
who is asked:

```yaml
review:
  # ... on_enter produces world.verdict ...
  decider: llm          # pin: let the engine resolve this gate (overrides run mode)
  on:
    approve:         { target: ship }     # ─┐
    request_changes: { target: revise }   #  ├─ these three advancing
    reject:          { target: archive }  # ─┘  intents are the gate
```

- **One advancing intent → auto-advance.** The engine fires it with no
  decider; the single-intent convention is just the degenerate gate.
- **Many advancing intents → a *decider* picks one.** `decider:` pins it
  per room — `"llm"` runs an `agent.decide` judge that chooses among the
  *enumerated* gate intents and records a `GateDecided` event; `"human"`
  rests for an operator; `""` (the default) follows the run's execution
  mode (`one-shot` resolves every gate by LLM, `staged` stops at a
  multi-way gate for a person). The decider only ever picks from the
  declared intents — it can't invent a destination.

This is the same mechanism that drives input-free progression and human
approval steps alike; the authoritative treatment, including how
`emit_intent:` and guards feed a gate, is
[§11](#11-driving-the-graph-without-input).

---

## 7. The turn (one round-trip, end to end)

A **turn** is: the user said something, the story responds. Underneath,
the orchestrator runs its own small state machine, serialized through a
per-session writer lock, recording every step to an append-only event
log (`internal/orchestrator/orchestrator.go::Turn`).

```mermaid
flowchart TD
    A["free text in"]
    R["Intent routing<br/>(4 tiers, §8)"]
    L["LLM translate<br/>harness.RunTurn — only if routing missed"]
    M["machine.Turn<br/>validate · pick transition · collect effects (pure)"]
    H["dispatch host calls<br/>invoke, bind results, re-render view"]
    E["settle emit_intent / on_complete"]
    P["persist events + journal"]
    V["render view → every subscribed surface"]

    A --> R
    R -- "resolved" --> M
    R -- "missed" --> L
    L --> M
    M --> H --> E --> P --> V
```

The ordered phases, grounded in the code:

1. **Routing tiers** — `TrySemantic` then `tryTurnCache` try to resolve
   the input cheaply before any LLM call (§8).
2. **Acquire the session lock** and **load the journey** (reconstruct
   state + world from the event log).
3. **LLM translate** — `harness.RunTurn`, only if the routing tiers
   missed. Returns the `(intent, slots)` call.
4. **`machine.Turn`** — pure: validate the call, pick the first guarded
   transition, collect effects and host calls. Same inputs → same
   output.
5. **Dispatch host calls** — `dispatchHostCalls` runs each `invoke:`,
   binds results into world, and re-renders the view against the
   post-bind world.
6. **Settle** post-bind `emit_intent` chains and background
   `on_complete` arcs.
7. **Persist** the events and journal entries (`appendEventsAndJournal`).
8. **Render** the view and post it to every subscribed transport.

Asynchronous off-ramps run through the *same* lock: background-job
completion, mid-flight clarification, off-path entry/exit, teleport
(inbox / agent banner jumps), and hot-reload (`RerunOnEnter`). Full
diagram and the off-ramp list: [`state-machine.md` §8](state-machine.md#8-the-turn-loop-state-machine-of-the-orchestrator)
and [`../architecture/overview.md` §3](../architecture/overview.md#3-the-journey-of-one-turn).

Because the LLM call and host invocations are the only non-deterministic
steps and both record their inputs/outputs, every turn downstream of
them is replayable — which is what makes deterministic flow tests
possible ([`../tracing/testing.md`](../tracing/testing.md)).

---

## 8. Intent routing and the agent

"How does the agent work with intent routing" is really one question:
**routing *is* the agent's `extract` verb running in front of a fallback
LLM call.** Every foreground turn descends a four-tier stack and stops at
the first tier that resolves (`internal/semroute/`, dispatched via
`host.RunExtractForRouting`):

| Tier | What it matches | Cost | Confidence | Badge |
|---|---|---|---|---|
| **Deterministic** | input exactly equals a menu display string or a unique intent example | map lookup | 1.00 | `▣` |
| **Synonym (bare)** | input's stem-bag ⊇ a declared `synonyms:` phrase | ~3 µs (Aho-Corasick) | 0.90 | `⌁` |
| **Synonym template** | input matches a `{slot}`-capturing template; captures go to typed parsers | <100 µs | 0.80 / 0.65 | `◐` |
| **Turn cache** | `(app, app_hash, state_path, lex.Signature(input))` seen before; re-validates against live world | ~80 µs SQLite | originating verdict | `⟲` |
| **LLM** | the only tier that costs seconds; writes its result back to the cache | 2–5 s | varies | `✦` |

The user sees a **route badge** next to their echoed input naming the
tier that resolved it. The point of the stack is latency: on the Oregon
Trail recording ~3 of 4 turns resolve without an LLM call. Authors grow
the synonym library over time with `kitsoki replay-routing` and
`kitsoki inspect --synonym-suggestions`. Full reference:
[`../architecture/semantic-routing.md`](../architecture/semantic-routing.md).

### The agent verb surface

The "agent" is kitsoki's name for an LLM call, full stop. Most of the
verbs are side-effect free — they read and return a verdict without
touching the world or the outside world; only `task` and `converse`
mutate. There are **five verbs**, ordered by blast radius, each a
`host.agent.*` handler (`internal/host/agent_*.go`):

| Verb | File | Shape | Mutates? |
|---|---|---|---|
| `extract` | `agent_extract.go` | free text → structured `(intent, slots)`; **this is the routing LLM tier** | no |
| `decide` | `agent_decide.go` | bounded choice → one option (the generic gate/judge decider) | no |
| `ask` | `agent_ask.go` | one-shot Q&A, read-only tool surface | no |
| `task` | `agent_task.go` | multi-turn agentic session with a declared tool surface; records replay artifacts | yes (sandboxed / file-diff) |
| `converse` | `agent_converse.go` | persistent multi-turn chat thread | yes |

All five route through `agent_dispatch.go`, stream by default into a
`StreamSink` when one is installed (live TUI progress), and can be backed
by any declared `agent_plugins:` entry — including the offline
`builtin.local_llm` backend, which is the natural choice for the routing
tier so routing keeps working air-gapped. Verb selection guide and the
plugin contract: [`../architecture/hosts.md`](../architecture/hosts.md#agent-verb-summary)
and [`../architecture/agent-plugin.md`](../architecture/agent-plugin.md).

A story author reaches the single-shot verbs from an effect:

```yaml
on_enter:
  - invoke: host.agent.decide
    with:
      question: "Does this patch fix the bug?"
      options: [accept, refine, reject]
    bind: { verdict: choice }
```

The `decide` verb is the generalised form of the bug-fix story's
hand-rolled judge — every room/phase that ends in a gate can resolve it
with a default / LLM / human decider rather than bespoke YAML.

---

## 9. Agent rooms (`/meta` and off-path)

Most of a story is on-path: the deterministic graph. Two mechanisms let
the user step *off* the graph into a free-form LLM conversation — these
are the "agent rooms".

### Off-path — the simple escape hatch

```yaml
off_path:
  trigger: help
  banner:  "(help mode)"
  return:  main
```

Triggering it saves the current state, runs a banner-marked free-form
sub-conversation, and rehydrates the saved state on exit. Traffic still
flows through the harness and store — every event is replayable — but the
inner graph is intentionally undeclared. It is the *only* place free-form
chat is allowed on-path. ([`state-machine.md` §11](state-machine.md#11-off-path-the-global-escape-hatch).)

### The agent off-ramp — a no-match door into the same chat

Off-path is reached through a **typed-trigger door**: the user must type
the declared trigger string. The **agent off-ramp** adds a second,
*automatic* door scoped to a single room. A room that declares
`agent_off_ramp:` says, in effect, "if the user says something I can't
map to any of my intents, don't bounce them — answer." When free-text
routing and the LLM resolve to **no declared intent** in such a room, the
orchestrator hands the original free text to an agent `converse` turn
instead of returning the usual "I didn't catch that" rejection (§6), and
the room stays put — no transition, no world write. It is automatic,
room-scoped off-path entry, triggered by a no-match rather than a typed
trigger, and it shares off-path's `converse` mechanism and agent/persona
precedence (`agent_off_ramp.agent:` > `off_path.agent:` > app default).

```yaml
main:                       # a normal menu/discovery room
  agent_off_ramp:
    agent: agent_qa        # the off-ramp voice (struct form)
  on:
    go_tickets: [{ target: ticket_search }]
    # …
```

The dominant entry is the **free-text no-match**: the router can't map the
utterance and the LLM declines to classify it, which surfaces as the
`LLM_CLARIFICATION` clarify code. `maybeOffRamp`
(`internal/orchestrator/offpath.go`) intercepts that code (plus the
`UNKNOWN_INTENT` / `INTENT_UNKNOWN` codes from the MCP / router paths) just
before the shared `ModeRejected` returns, and only when the resting state
declared the flag. The off-ramp fires **only** on those genuine no-match
codes — a recognised-but-blocked intent (`GUARD_FAILED`,
`INTENT_NOT_ALLOWED_IN_STATE`, a missing slot) still rejects or clarifies
as today, because those are signals the author wants surfaced, not chat
fodder. The decision to off-ramp is deterministic (a room flag × which
error code came back); only the answer is interpretive, and it is recorded
like any off-path turn — an `OffPathEntered` labeled `reason: "off_ramp"`
(with the triggering `error_code`), then `OffPathQuestion` / `OffPathAnswer`;
no `TurnEnded(rejected)`, no transition. The outcome is `ModeOffPath` (over
the web RPC: `{ mode:"offpath", view:<answer>, state:<unchanged>,
allowed_intents:<menu> }`), so a renderer shows the answer and the same
menu persists because state is unchanged.

A load-time invariant rejects the flag on a `terminal: true` or
`mode: conversational` state (a terminal state never routes free text; a
conversational room is already free-form, so the flag is meaningless). It
therefore belongs on a normal menu / discovery room — the dev-story hub
(`stories/dev-story/rooms/main.yaml`) is the first adopter. Two entrances,
one voice: see [`state-machine.md` §11](state-machine.md#11-off-path-the-global-escape-hatch).

### Meta mode — persistent named-agent sidebars

Meta mode is the richer surface that supersedes the old edit mode. The
user fires `/meta` (or `/meta story edit`, `/meta kitsoki ask`, …) to
pause the FSM and open a **persistent, multi-turn conversation with a
named agent**; `/onpath` resumes the saved state and reloads any files
the agent touched (`internal/metamode/controller.go` — `Enter` / `Send`
/ `Exit`). Two top-level YAML blocks configure it:

- **`agents:`** — declarative agent definitions: `system_prompt` (or
  `_path`), `model`, `tools` allow-list, `cwd`.
- **`meta_modes:`** — named overlays that pick an agent and add
  `trigger` / `banner` / `persist` / `return` policy.

```yaml
agents:
  story-author:
    system_prompt_path: prompts/author.md
    model: claude-opus-4-8
    tools: [Read, Glob, Grep, Edit, Write]

meta_modes:
  story:                            # invoked with /meta story
    trigger: meta
    banner:  "*** meta:story — editing this story ***"
    agent:   story-author
    persist: true
    return:  { intent: onpath, message: "back on path." }
```

Each meta session is backed by a chat row keyed
`(AppID, "meta:<mode>", scopeKey=state_path)` — same state resumes the
same chat, a different state opens a new one. The agent gets a
`[context]` preamble each turn (current state, app file, a live trace
file it can `Read`, the rendered view, and the world) so it can pin
edits to the right file. Edits land by **direct file edit** (the
controller diffs the story tree before/after and triggers an
orchestrator reload); there is no propose/review step anymore.

Kitsoki injects eight builtin meta modes every app gets for free
(`internal/app/builtin_meta_modes.go`), grouped `group.verb`:

| Mode | Agent | Surface |
|---|---|---|
| `story.edit` (default for bare `/meta story`) | `story-author` | full Claude toolset, edits the running story |
| `story.ask` | `story-explainer` | read-only (`Read`/`Glob`/`Grep`), backed by `host.agent.ask` |
| `story.improve` | `story-improver` | read-only introspection report for prompt/tool/script/flow-test improvements |
| `story.bug` | `story-bug-reporter` | files a story bug via `kitsoki bug create` |
| `kitsoki.edit` | `kitsoki-engineer` | edits the kitsoki repo (`${KITSOKI_REPO}`) |
| `kitsoki.ask` | `kitsoki-explainer` | read-only Q&A about kitsoki source |
| `kitsoki.improve` | `kitsoki-improver` | read-only introspection report for reusable engine improvements |
| `kitsoki.bug` | `kitsoki-bug-reporter` | files a kitsoki bug |

Bare verbs (`/meta ask`, `/meta improve`, `/meta bug`) resolve to the **story** group;
the whole `kitsoki.*` group is omitted when `${KITSOKI_REPO}` is unset.
The mapping that ties meta back to the agent verbs: read-only metas use
`host.agent.ask` (loader enforces the read-only tool surface);
free-form metas use `host.agent.converse` with a `permission_mode:`
gate. There is also per-call agent selection — any `host.agent.*` effect
can name an `agent:` for a single LLM call (precedence: per-call
`agent:` > `meta_modes[mode].agent` > `off_path.agent` > app default).
Full reference, slash-command table, persistence, and current
limitations: [`meta-mode.md`](meta-mode.md).

> **The convergence direction.** Off-path and meta-mode are being unified
> into one mechanism — off-path becomes `/meta default-agent` with a
> tool-restricted read-only agent. Today off-path still uses the legacy
> prompt-driven dispatch and does not yet honour `off_path.agent:`; reach
> for a `meta_modes:` entry for any "named, persistent sidebar" need.

---

## 10. Phases (compressing repeated rooms)

Many stories repeat the same shape — "execute a step, post the result,
await a reply, retry on failure". A **phase** is a repeated room: declare
the shape once as a `phase_templates:` entry, then instantiate it once
per phase in a `phases.graph:` (`internal/app/phases.go::expandPhases`).

```yaml
phase_templates:
  reviewed_phase:
    parameters:
      id:    { type: string, required: true }
      title: { type: string, required: true }
    states:
      "{id}_executing": { view: "Phase {{ tpl.title }} running", on: { … } }
      "{id}_awaiting_reply": { view: "Awaiting reply on {{ tpl.title }}" }
      "{id}_error": { view: "Phase {{ tpl.title }} failed" }

phases:
  template: reviewed_phase
  graph:
    phase_a: { title: "A", next: { continue: phase_b } }
    phase_b:
      title: "B"
      next: { continue: phase_c, on_failure: phase_a }
      cycle_budgets: { on_failure: 2 }
```

Substitution: `{name}` in a state key → the parameter value; `{{ tpl.X }}`
in a body → the parameter value; `{{ phase.next.<arc> }}` in a `target:`
→ `<next-phase-id>_executing`. `cycle_budgets:` is sugar — for each arc
the loader synthesises an `increment:` on a `cycle__<phase>__<arc>`
counter, a `when:` guard `< N` on the arc, and a trailing
`default: true → <phase>_error`. So a back-arc works the first N times
and then fails into the error state — no infinite loops.
`checkpoint_intents:` (top-level) is merged into every `*_awaiting_reply`
state — that's where you declare the user-facing resume verbs
(`continue`, `refine`, `restart_from`). Full mechanics:
[`state-machine.md` §9–10](state-machine.md#9-phase-templates-compressing-repeated-rooms).

---

## 11. Driving the graph without input

A room does **not** need user input — or even an LLM call — to move on.
The graph can drive itself: a run of *compute rooms* binds world state
and advances autonomously, which is exactly what turns a story into the
workflow-shaped pipeline of §10 — and the bridge to the neighbours
compared in §12. Progression with zero input is built from
`emit_intent:` plus the gate rules, layered:

1. **`emit_intent:` is the engine.** A room's `on_enter:` chain runs its
   host calls, binds results into the world, and then fires
   `emit_intent: <name>` — a *synthetic* intent dispatched against the
   current room **within the same turn**. It walks the same transition
   apparatus a user intent would, so it lands in the next room, runs
   *that* room's `on_enter`, which may emit again — a chain the
   orchestrator settles in `settlePostBindEmits`, depth-capped at
   `EmitIntentMaxDepth = 8`. The intent name can be literal
   (`emit_intent: done`) or computed from the world
   (`emit_intent: "{{ world.verdict.intent }}"`).

2. **Guards choose the direction; the emit supplies the trigger.** A
   `when:` guard never fires on its own — it is only evaluated once an
   intent is dispatched against the room. So world-condition branching
   with no input means an `emit_intent` *plus* guards, and you can place
   the guards in either of two spots:
   - on the **emit effects** — several `emit_intent:` effects each with
     its own effect-level `when:`, so only the matching one fires; or
   - on the **emitted intent's transition arms** — emit one intent
     unconditionally and let that intent's `on:` list pick the arm whose
     `when:` is satisfied (first match wins).

   Either way the choice is pure `world.*` evaluation by `internal/expr`
   — no human, no LLM. "If the build passed, deploy; otherwise triage"
   is one `emit_intent: route` whose two arms are guarded on
   `world.build_passed`. (A guarded transition *without* an emit is the
   *interactive* case: the same arms, but waiting for a user or external
   intent to arrive before the guards are tested.)

3. **The gate is where a room "owes" a next step.** The set of advancing
   intents at the end of a room/phase is its *gate*. How it resolves:
   - a guarded `emit_intent` already fired → that *is* the decision;
   - **exactly one advancing intent → the engine auto-advances it** —
     yes, the single-intent convention you remembered is real; it is the
     degenerate case of gate resolution, not the general mechanism;
   - a multi-way gate with no firing default → resolved by a
     **decider**: `default` (deterministic), `llm` (an `agent.decide`
     judge that picks among the *enumerated* gate intents and records a
     `GateDecided` event), or `human`. Which decider runs depends on the
     run's **execution mode**: `one-shot` advances autonomously through
     every gate within a turn; `staged` ends the turn at a multi-way
     gate so a human picks. Single-intent gates auto-advance in **both**
     modes (`internal/orchestrator/decider.go`, `ExecutionMode`).

So a fully deterministic, input-free stretch of a story is a run of
*compute rooms* whose `on_enter` binds world and `emit_intent`s onward,
with single-intent or guarded gates throughout. That is exactly the
bug-fix pipeline's "compute phases." (A pending proposal —
[`../proposals/auto-advance-states-proposal.md`](../proposals/auto-advance-states-proposal.md)
— would let such rooms skip even the `emit_intent: done` boilerplate by
auto-firing `done` after `on_enter` unless the room declares
`wait: true`; **not implemented today**.)

A compute room — "if the build passed, deploy; otherwise triage", with
no user and no LLM:

```yaml
build:
  on_enter:
    - invoke: host.run
      with: { cmd: "make build" }
      bind: { build_exit: exit_code }
    - emit_intent: route            # fire a synthetic intent this same turn
  on:
    route:                          # guards pick the arm — pure world.* eval
      - when: "world.build_exit == 0"
        target: deploy
      - default: true
        target: triage
```

---

## 12. How a story relates to workflows and agents

A story is usually pitched as a *conversation* — free text in, a
transition out. But the same machine spans two neighbours that look
nothing like a chat: a **workflow engine** (a pipeline that runs
head-to-tail with no input — the self-driving graph of §11) and an
**agentic orchestrator** (a lead LLM that spawns sub-tasks). Here is
where a story sits against each.

### 12.1 vs. a workflow engine

A workflow engine (Temporal, Airflow, LangGraph) is author-declared
steps wired by data dependencies; each step is opaque code that runs to
completion and hands output to the next. A story can *be* exactly this:
the §11 compute-room chain runs head-to-tail with no input, and
**phases** (§10) are the workflow-shaped construct — "execute, post,
await, retry."

What a story adds over a plain pipeline:

- **Every step is a room with an intent vocabulary**, so it is
  interruptible and inspectable — a human, or an external event
  (a PR comment, a Jira reply), can enter at any gate. A workflow step
  is a black box you can only watch.
- **Branching is `when:` over a typed world**, not edges baked into
  code.
- **Every transition, effect, and decision is a recorded event**, so the
  whole run replays exactly.
- **The gate decider generalises `next()`.** The *same* graph runs
  autonomously (`one-shot`), with an LLM judge at the forks, or paused
  for a human (`staged`) — by flipping execution mode, not rewriting the
  graph.

The clean framing: a workflow is a story in which every gate is
single-intent or deterministically guarded and nothing waits. Add one
multi-way or `wait` gate and it becomes interactive again. **A story is
a superset of a workflow.**

If you come from a workflow engine (Airflow, Temporal, Step Functions,
LangGraph, n8n, Prefect), here is the rough Rosetta Stone:

| Workflow term | Story term | Notes |
|---|---|---|
| Workflow / DAG / pipeline | **Story** (the state graph) | author-declared YAML; cyclic, not just acyclic |
| Step / task / node / activity | **Room** (a state) | a "compute room" is a step that just runs host calls and routes |
| Edge / dependency arrow | **Transition** (`on:` arc) | guarded by `when:`; first matching arm wins |
| Conditional / branch node | **Guarded transition arms** | `when:` over `world.*`, evaluated by `internal/expr` |
| Trigger / start node | **Initial state + inbound event** | a turn, an external thread message, or a teleport |
| Workflow input / parameters | **Slots + initial `world`** | slots are per-turn; world is the persisted bag |
| Context / variables passed between steps | **World** | typed, persisted, immutable-per-turn |
| Task output / return value | **`bind:`** (result → world) | flat copy; see the §4 post-bind limitation |
| Worker / executor | **Host handler** (`host.*`) | the allow-listed side-effect registry |
| Side effect / action | **Effect** (`invoke: host.*`, `set`, `say`, …) | enumerated alphabet; the LLM can't add new ones |
| Fan-out / parallel branch | **Parallel state** (`type: parallel`) + `emit:` | regions run concurrently; events cross siblings |
| Sub-workflow / nested DAG | **Compound state** or an **imported app** | nesting via `states:`; cross-file via `imports:` |
| Loop / retry policy | **Cyclic transition** + **`cycle_budgets:`** | budget synthesises increment + guard + fail-out |
| Async / long-running task | **`background: true` invoke** + **`on_complete:`** | completion fires a synthetic turn |
| Human approval / manual gate | **`wait` gate / `checkpoint_intents:` / `human` decider** | a multi-way gate in `staged` mode rests for a person |
| Decision node / router | **Gate resolved by a decider** | `default` (deterministic) / `llm` (`agent.decide`) / `human` |
| Signal / event / webhook | **`emit:` event, inbox notification, or external intent** | external surfaces feed intents through the same path |
| Scheduler / engine | **Orchestrator** (`internal/orchestrator`) | the only writer; runs the turn loop under a per-session lock |
| Run / execution instance | **Session** | one row + an append-only event log |
| Run history / audit trail | **Event log / journal** | replayable byte-for-byte; non-determinism confined to LLM + host calls |
| Idempotency key | **Idempotent `on_enter` / get-or-create host verbs** | `on_enter` re-fires; see §3 |
| Step that calls an LLM | **`host.agent.*` effect** | `extract`/`decide`/`ask` read-only; `task`/`converse` mutate |

### 12.2 vs. Claude Code with a lead agent and subagents

Launch Claude Code with an agent that spawns subagents for different
tasks, and the **orchestration lives inside the model's head**: the lead
agent decides, at its own discretion and turn by turn, when to spawn a
subagent, what to delegate, and when the job is done. The control flow
is emergent, not enumerated, and not replayable as a graph; rerunning
the same prompt produces a different shape. Trust is "whatever the lead
model judges, within the tools it was granted."

A story inverts every one of those:

| | Claude Code (lead agent + subagents) | Story |
|---|---|---|
| Who decides the next step | the lead model, at runtime | the **graph** — transition + guard + decider |
| The plan | emergent, in the model's head | **author-declared YAML**, fixed and reviewable |
| A subagent | the lead model spawns one when it decides to | a `host.agent.task` call the **graph** invokes at a declared point |
| Choosing among options | the model picks any action its tools allow | a decider picks among **enumerated intents**, recorded as `GateDecided` |
| Blast radius | the granted tool set, applied wholesale | per-call declared `tools:` / `bash_profile:` / sandbox, enforced |
| Replay | one long non-deterministic transcript | graph walk; non-determinism confined to recorded agent calls |

The mapping is tight: a **subagent ≈ a `host.agent.task` invocation**
(a bounded, tool-scoped LLM sub-session with recorded replay artifacts,
§13 of the overview), and **"the lead agent picks which subagent" ≈ a
multi-way gate resolved by `agent.decide`**. The difference is *who
holds the orchestration*: in Claude Code the model holds it; in a story
the graph holds it and calls the model only at declared points, for
decisions scoped to a declared alphabet.

When each wins: reach for the Claude-Code-style agent when the work is
open-ended and you genuinely cannot enumerate the steps ahead of time.
Reach for a story when the *same* workflow must run reliably, be
audited, resume across surfaces, and never exceed its declared
authority — paying the cost of declaring the graph up front. The
agent rooms (§9) are the seam between the two worlds: inside `/meta`
or `host.agent.task` you get the open-ended agent; the story around it
keeps that agent on a declared leash.

### 12.3 A worked example: the design pipeline

The dev-story hub's design pipeline
(`stories/dev-story/rooms/design*.yaml`) is §12.2 made concrete: six
rooms, **eight distinct LLM agents**, and a deterministic publish step,
each stage dropping a numbered artifact on disk before the next agent
reads it. In the diagram: purple = an LLM agent, blue = human input, grey
= a deterministic script, green = an artifact on disk.

```mermaid
flowchart TD
    classDef llm   fill:#EDE9FE,stroke:#8B5CF6,color:#000
    classDef art   fill:#ECFDF5,stroke:#10B981,color:#000
    classDef det   fill:#F3F4F6,stroke:#9CA3AF,color:#000
    classDef human fill:#DBEAFE,stroke:#2563EB,color:#000

    subgraph P1["room: proposal — discovery + brief (loops every turn)"]
      direction TB
      idea(["operator: free-text idea"]):::human
      namer["design_namer<br/>agent.decide"]:::llm
      uniq["design_workspace.star<br/>slug collision-proof"]:::det
      interv["proposal_interviewer<br/>agent.converse · ONE persistent thread"]:::llm
      writer["proposal_brief_writer<br/>agent.task · fresh session/turn"]:::llm
      brief[/"001-brief.md"/]:::art
      ready(["operator: say 'ready'"]):::human
      judge{"design_brief_judge<br/>agent.decide"}:::llm
      idea --> namer --> uniq --> interv --> writer --> brief
      interv -. loops .-> writer
      brief --> ready --> judge
      judge -. clarify .-> interv
    end

    subgraph P2["room: proposal_existing_state"]
      direction TB
      scout["design_scout<br/>agent.decide"]:::llm
      es[/"002-existing-state.md"/]:::art
      g1{"overlap?"}:::human
      scout --> es --> g1
    end
    judge -->|continue| scout

    subgraph P3["room: proposal_idea_completeness"]
      direction TB
      comp["proposal_completeness_judge<br/>agent.decide"]:::llm
      ic[/"003-idea-completeness.md"/]:::art
      g2{"complete?"}:::human
      comp --> ic --> g2
    end
    g1 -->|"proceed_new / change_existing"| comp

    subgraph P4["room: proposal_references"]
      direction TB
      res["design_researcher<br/>agent.decide"]:::llm
      refs[/"004-references.json"/]:::art
      g3{"confirm?"}:::human
      res --> refs --> g3
    end
    g2 -->|confirm| res

    subgraph P5["room: design_draft"]
      direction TB
      author["design_author<br/>agent.task · agentic write"]:::llm
      draft[/"005-proposal.md"/]:::art
      g4{"accept?"}:::human
      author --> draft --> g4
    end
    g3 -->|confirm| author

    subgraph P6["room: proposal_publish (no LLM)"]
      direction TB
      pub["publish_design.star<br/>host.proposal.publish"]:::det
      out[/"docs/proposals/&lt;slug&gt;.md<br/>+ archived 001–004"/]:::art
      pub --> out
    end
    g4 -->|accept| pub

    g2 -. clarify .-> interv
    g3 -. clarify .-> interv
    g4 -. "clarify / refine" .-> author
```

Read it as the §12.2 inversion in practice:

- **Every purple node is a separate agent.** Each has its own
  `system_prompt`, `model`, and `tools:` in `app.yaml agents:` — a namer
  that emits only a kebab slug; a read-only scout (`[Read, Grep, Glob]`);
  an author with `Write/Edit` constrained to the one proposal file. The
  **graph**, not a lead model, decides which runs when.
- **Each is its own conversation.** All but one are *one-shot* —
  `decide`/`task` open a fresh session per call and return (the verb
  surface, §8). The exception is `proposal_interviewer` (`agent.converse`),
  which holds a single persistent chat thread across the whole discovery
  loop via a `chat_id` resolved in `on_enter`. The two agents in the
  *same* discovery room (interviewer + brief-writer) are deliberately
  separate conversations with separate jobs.
- **The glue between agents is deterministic and recorded.** Slug
  uniqueness (`design_workspace.star`) and publish (`publish_design.star`)
  are plain `host.run` scripts, not LLM calls; the human gates resolve
  *enumerated* intents recorded as `GateDecided`. Nothing an agent emits
  advances the pipeline until a deterministic step or a declared gate
  consumes it.
- **The interim artifacts are the hand-offs.** `001-brief.md` …
  `005-proposal.md` land in a per-session workspace as each stage
  completes, and the next agent reads the upstream *file* (the author reads
  the brief, the references list, and the existing-state report) rather
  than a shared in-model scratchpad. Publish moves `005` into
  `docs/proposals/` and archives `001–004` beside it, so the reasoning that
  produced a proposal survives with it.

Eight subagents an emergent lead model might have spawned in its head,
pinned instead to declared points in a replayable graph — each scoped,
each recorded, each resumable across surfaces.

---

## 13. Where it all lives

| Concern | Package |
|---|---|
| YAML loader, types, schema validation | `internal/app/` (`types.go`, `loader.go`, `phases.go`, `builtin_meta_modes.go`) |
| Pure machine — `Turn`, `Validate` | `internal/machine/` |
| Intent call + validation errors | `internal/intent/` |
| Typed world snapshot | `internal/world/` |
| Guard / template evaluator (AST whitelist) | `internal/expr/` |
| Orchestrator turn loop (only writer to the store) | `internal/orchestrator/` |
| Intent router (4 tiers) | `internal/semroute/`, `internal/slotparse/` |
| Agent verbs + `host.*` registry | `internal/host/` (`agent_*.go`) |
| Meta-mode controller + agents | `internal/metamode/`, `internal/agents/` |
| Background jobs, inbox, chats | `internal/jobs/`, `internal/inbox/`, `internal/chats/` |
| Persistence (append-only events) | `internal/store/` |
| Surfaces | `internal/tui/`, `internal/mcp/`, `internal/transport/` |

Read them roughly in that order — `app` → `machine` → `intent`/`world`/
`expr` → `orchestrator` → `semroute`/`host` → `metamode` — to see a
story end-to-end, top-down.

---

## See also

- **Write one** → [`authoring.md`](authoring.md), [`../recipes/`](../recipes/README.md)
- **Make it look right** → [`story-style.md`](story-style.md), [`choice-widget.md`](choice-widget.md)
- **Compose across files/repos** → [`imports.md`](imports.md), [`prompts.md`](prompts.md)
- **Test and debug it** → [`../tracing/testing.md`](../tracing/testing.md), the `kitsoki-debugging` skill
- **The engine internals** → [`../architecture/README.md`](../architecture/README.md)
