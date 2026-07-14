# Architecture

This document describes kitsoki as a **system** — what it is, what it
believes, and how the pieces of the model fit together. It is meant
for someone trying to understand kitsoki, not for someone working in
the code; the implementation map at the end points to where each idea
lives.

For comparative grounding — what kitsoki borrows from interactive
fiction, statechart frameworks, dialogue managers, and LLM
orchestration — see [`prior-art.md`](prior-art.md). For runnable
apps, see [`testdata/apps/`](../../testdata/apps). For the state-machine
vocabulary in detail, see [`state-machine.md`](../stories/state-machine.md).

---

## 1. The shape of the system

Kitsoki sits between two surfaces of common pain.

**Traditional CLIs** demand exact syntax. `kubectl patch deployment foo
-p '{"spec":{"replicas":3}}' --type=merge` is unforgiving — one missing
flag, one mistyped JSON path, and you get nothing back except a
recovery hint. The compensating virtue is that the CLI does exactly
what you asked, every time.

**Chat agents** accept anything. You can say "scale the frontend to
three replicas, please" and a sufficiently smart agent figures it out.
The compensating cost is that it might also figure out something
*adjacent* — restart pods, edit your manifest, page the on-call —
because no surface boundary tells it not to.

Kitsoki is neither. Kitsoki is a conversation **engine** that splits the
difference: free text in, but a declared, finite alphabet of
**intents** decides what can happen next.

```mermaid
flowchart LR
    User["User<br/>(or external thread)"]
    LLM["LLM<br/>(translator)"]
    App["Application<br/>(state graph)"]
    World["World<br/>(persistent state)"]

    User -- "free text" --> LLM
    LLM -- "named intent + typed slots" --> App
    App -- "transition" --> World
    World -- "rendered view" --> User
```

The arrow that matters most is the second one. The LLM never edits the
world directly; it only proposes an intent. If the intent isn't valid
in the current room, or its arguments don't fit the slot schema, the
machine refuses and the LLM gets a structured error to retry against.
The user's free text was a question, not a command — the application
decides what counts as a meaningful answer.

Three things follow:

- **The LLM cannot invent actions.** Every action is declared by the
  application's author. If a user says "delete everything", the LLM
  can only translate that into one of the verbs the current room
  exposes. If no such verb exists, the user gets told "no".
- **The state machine is pure.** Given the same world and the same
  intent, kitsoki always picks the same transition. Replay is mechanical.
- **The author is in charge.** What can happen, in what room, with
  what guards, with what effects — all of it is in YAML, all of it is
  reviewable, none of it is a surprise.

---

## 2. The domain model

Six concepts cover almost everything kitsoki does.

| Concept | What it is |
|---|---|
| **Conversation** | One ongoing exchange between a user and an application, threaded over a *surface*. |
| **Application** | A directed cyclic graph of *rooms* with a vocabulary of intents and a typed *world*. Author-declared in YAML. |
| **Room** | A node in the graph. Has its own intent vocabulary (which actions are valid here), an optional view template (what the user sees), and an optional set of side effects fired on entry. |
| **Intent** | A named action a user can take. May carry typed arguments (slots). The atom of free-text translation. |
| **World** | The application's persistent memory — a typed key/value bag. Rooms read it through guards and view templates; transitions write it through effects. |
| **Transition** | An edge between rooms. Takes an intent, may evaluate a guard, applies an ordered list of effects, lands in a target room. |

A few more concepts come up at the periphery:

| Concept | What it is |
|---|---|
| **Slot** | A typed parameter on an intent — `direction: north`, `branch: main`. Validated before any guard runs. |
| **Effect** | A small declarative mutation: `set` a world value, `increment` a counter, `say` a line of narration, `invoke` a host, `emit` an event to parallel regions. |
| **Host** | A named handler the application can invoke as an effect — `host.run` for a shell command, `host.agent.ask` for a one-shot Claude call, `host.transport.post` to deliver a message to an external thread. The application's allow-list of hosts is part of the YAML. |
| **Phase** | A repeated room. Phase templates compress pipelines like "execute, post, await reply, retry on failure" into one declaration plus per-phase parameters. |
| **Off-path** | A global escape hatch: the user can ask a free-form question (often "help") that suspends the current room, runs a sub-conversation, and rehydrates the original room on exit. |

These compose into the application's graph:

```mermaid
flowchart LR
    subgraph App["Application — author-declared YAML"]
        direction LR
        R1((Room A))
        R2((Room B))
        R3((Room C))
        R4((Room D<br/>terminal))
        R1 -- intent X --> R2
        R1 -- intent Y --> R3
        R2 -- intent Z<br/>guard: world.k > 0 --> R3
        R2 -- default --> R1
        R3 -- intent W --> R4
        R3 -- intent W<br/>retry --> R1
    end
```

Cycles are not just allowed — they are the *typical shape*. A "main
menu" room loops back to itself between sub-conversations; a proposal
lifecycle bounces between draft and review until accepted; a phase
pipeline retries on failure until a budget runs out.

---

## 3. The journey of one turn

A **turn** is one round-trip: the user said something (or an external
thread did), and the application responds. From the user's
perspective, this looks like typing and seeing a reply. The model
underneath is:

```mermaid
flowchart TD
    A["User free text"]
    B{"Already a<br/>structured intent?"}
    C["Translation<br/>LLM picks an intent + slots<br/>from this room's vocabulary"]
    D["Validation<br/>Does this intent exist here?<br/>Are the slots well-typed?"]
    E["Decision<br/>Pick the first transition whose<br/>guard is satisfied"]
    F["Effects<br/>set, increment, say, invoke host,<br/>emit event"]
    G["Persistence<br/>Append events to the journey log"]
    H["Render<br/>New view appears on every<br/>subscribed surface"]
    I["Retry<br/>Tell the LLM what was wrong,<br/>let it try again"]
    J["Human escape<br/>If retries exhaust, ask the user<br/>directly with a clarify dialog"]

    A --> B
    B -- yes --> D
    B -- no --> C
    C --> D
    D -- ok --> E
    D -- malformed<br/>or unknown --> I
    I -- next attempt --> C
    I -- budget<br/>exhausted --> J
    E --> F --> G --> H
```

What the user sees is the path `A → … → H`. What the system *does*
includes the retry off-ramp and the human escape; both happen quietly
unless the LLM keeps producing invalid output. The human escape is
the deliberate fall-through: "I asked the LLM, it tried, it failed,
now it's your turn — pick from this menu."

The system goes out of its way to keep the user out of that branch.
It also goes out of its way to never let the LLM **cause** something
the author didn't declare.

A related **never-silent invariant** covers `on_error:` redirects
(a story-declared fallback room, distinct from the retry/human-escape
path above): whichever room an error redirect lands in, the rendered
view always carries a visible trace of the failure before the turn
ends, enforced once in a shared seam every dispatch path routes
through. See [state-machine.md](../stories/state-machine.md#on_error-redirects-and-the-recursion-cap).

An optional **semantic routing** stack — author-declared synonyms,
synonym templates that capture typed slots, and a per-session
turncache — can sit between the deterministic menu match and the LLM
call. It is **off by default** (free-text routing is an isolated
main-model decision); enable it with `--semantic-routing` /
`KITSOKI_SEMANTIC_ROUTING`. See
[semantic-routing.md](semantic-routing.md) for the toggle and the full
tier list. When enabled, every foreground turn runs the tiers in order
and stops at the first that resolves:

1. **Deterministic** (`TryDeterministic`) — input exactly matches a
   menu entry's display string or a unique intent example. Cost: a
   map lookup. Confidence: 1.00.
2. **Synonym** (`TrySemantic`, bare-string path) — input contains the
   stem-bag of an author-declared synonym for an allowed intent.
   Cost: ~3 µs via the Aho-Corasick pre-filter. Confidence: 0.90.
3. **Synonym template** (`TrySemantic`, template path) — input
   matches a `{slot}`-capturing template like `"buy {items} for
   {total_cost}"`; captured ranges are fed to typed parsers in
   `internal/slotparse` (int / money / enum / bool / list[T] / date).
   Cost: per-template NFA walk plus per-slot parse, all <100 µs in
   practice. Confidence: 0.80 (all slots filled) or 0.65 (some named
   but unparseable).
   A verdict strictly between 0 and the mid-bar is a **near-miss**: it
   never auto-resolves to the closest intent (the source of
   adjacent-command misroutes) — it is recorded on the trace
   (`turn.near_miss`) and falls through to the next tier, escalating
   to the S1 workbench once that exists. See
   [semantic-routing.md](semantic-routing.md#13-synonym-templates).
4. **Turn-result cache** (`tryTurnCache`) — keyed by `(app, app_hash,
   state_path, lex.Signature(input))`. A hit re-runs `Machine.Validate`
   against the live world; on success it short-circuits via
   `SubmitDirect`. Cost: ~80 µs SQLite roundtrip. Confidence:
   originating LLM verdict's self-reported value.
5. **LLM** (`harness.RunTurn`) — the only tier that costs seconds.
   Successful resolutions write back to the cache so subsequent turns
   with the same lexical signature short-circuit at tier 4.

Each tier emits a structured trace event
(`turn.deterministic_*` / `turn.semantic_*` / `turn.turncache_hit` /
`turn.llm_routed`) that the TUI subscribes to. The user-visible
result is a small **route badge** next to the echoed input — `▣` for
the deterministic match, `⌁`/`◐` for synonym/template hits, `⟲` for
a cache hit, `✦` for the LLM, `◇` for off-path — and a progressive
`[⋯ resolving…]` chip that pulses as the resolver descends the stack.
Faded prior icons remain so the user can see how far down the
pipeline the turn fell (`[▣· ⌁· ⟲· ✦ ask_question{…}]`). The chip
itself lives in `internal/tui/routing_chip.go` and is driven by
events, not orchestrator state, so it stays trivially unit-testable.

The user-visible point of the routing stack is **latency**: a synonym
or cache hit returns in microseconds; the LLM takes seconds. On the
Oregon Trail recording the calibration loop reaches a ~22% LLM
fallthrough rate — three out of four turns resolve without an LLM
call. Authors grow the synonym library over time using
`kitsoki replay-routing` and `kitsoki inspect --synonym-suggestions`
(see [`semantic-routing.md`](semantic-routing.md)).

---

## 4. The LLM's role (and its boundaries)

The LLM does exactly one thing: **translate free text into a named
intent with typed slots, picked from the current room's vocabulary.**
It never:

- decides what to do — the state machine does that;
- writes the world — only effects do that;
- invents new actions — the room declares them all;
- holds context across turns of its own accord — the application's
  world is the context.

There are four ways the translation can run, with different
trade-offs:

| Mode | Determinism | Cost | When |
|---|---|---|---|
| `claude` | No (real LLM) | Free with Claude Code login | Default for local play. |
| `live` | No (real LLM) | Paid (Anthropic API) | CI without Claude Code, or to pin a specific model. |
| `replay` | Yes | Zero | Flow tests, demos, byte-reproducible reruns. Reads a hand-written or captured **recording** that maps `(state, input)` to `(intent, slots)`. |
| `recording` | Wraps another | Wraps another | Capture an LLM session to a recording file for later replay. |

The fact that one of these modes is fully deterministic is what makes
kitsoki testable. The same flow YAML that drives the `replay` harness
in CI also drives the `record` command that produces a reproducible
demo GIF. There is no "recording drift" because there is no second
implementation to drift from.

---

## 5. Conversations across surfaces

A conversation has to live somewhere — a TUI window, a Jira ticket
comment thread, a Bitbucket PR, or (on the roadmap) a Slack/Teams
thread. Kitsoki calls each of these a **surface** (or, viewed from
inside, a **transport**), and the same application works across all of
them. Because most surfaces are plain-text comment threads, this puts a
hard requirement on every story: it must be fully drivable and legible
as text, never depending on color or interactive widgets for anything
essential. See [`transports.md` §7](transports.md#7-every-story-must-work-text-only).

```mermaid
flowchart LR
    subgraph Surfaces
        TUI["TUI window<br/>(local)"]
        Jira["Jira ticket comments"]
        BB["Bitbucket PR comments<br/>(planned)"]
        MCP["MCP client<br/>(Claude Desktop, etc.)"]
    end

    subgraph Kitsoki
        S["one session<br/>per (transport, thread)"]
        APP["application<br/>state graph"]
    end

    TUI <-- "transcript" --> S
    Jira <-- "comments" --> S
    BB <-. "comments" .-> S
    MCP <-- "transition tool" --> S
    S --> APP
```

Today the TUI, Jira, and Bitbucket transports ship against the same
`Transport` interface. See [`transports.md`](transports.md) for the
per-transport status.

The user-visible consequence: **the same conversation can move
between surfaces without losing state.** A bug-fix room driven from a
Jira ticket can be inspected from the local TUI on the developer's
laptop; the same session ID resolves both ways. An external
orchestrator (today `loop.py`) drives the session by feeding inbound
comments to kitsoki one at a time; output flows to whichever transports
the application has wired up.

This is what makes kitsoki usable as the **conversational workflow engine behind a
ticket-thread bot**. The state machine is identical; only the surface
moves.

---

## 6. Long-running work and notifications

Many useful things take longer than a turn — a build, a deploy, a
deep LLM analysis. If the conversation blocked while they ran, the
user would either wait or give up.

Kitsoki handles this with **background jobs**. An effect marked
`background: true` spawns a goroutine; the user gets back to the
conversation immediately. When the job finishes, three things happen:

1. The originating room's `on_complete:` effects fire as a synthetic
   turn (so all the usual `set`/`say`/`invoke` work the same way).
2. The world is updated with the job's result.
3. An **inbox notification** appears on the user's surface.

```mermaid
sequenceDiagram
    participant User
    participant Room
    participant Job
    participant Inbox

    User->>Room: take some action
    Room->>Job: spawn background work
    Room-->>User: "job started" + room continues
    Note over User,Room: user keeps doing other things
    Job-->>Room: result available
    Room->>Room: apply on_complete effects
    Room->>Inbox: notification ready
    Inbox-->>User: badge in inbox panel
```

Some background work needs to **pause and ask** mid-flight — "should
I commit to `main` or `develop`?" The handler calls a clarification
helper, which surfaces an `action_required` notification. The user
answers; the handler resumes. The user never sees the goroutine, the
poll loop, or the database row that mediated the pause.

The pattern is intentional: the human and the system trade attention.
The user can keep working while a long task runs; the system can ask
for help when it needs to without crashing the conversation.

---

## 7. Persistence, replay, and auditability

Every turn produces an ordered list of **events**: the harness was
called, the validation passed, this transition fired, that effect
applied, the new room rendered. The events are written to a single
per-session log. The world snapshot is a *cache* derived from the
log — replaying the log on a fresh database produces the same world.

The user-facing consequences:

- **Sessions survive.** Close the TUI, reopen it the next day; the
  conversation picks up where it left off. The session lives in
  `$XDG_DATA_HOME/kitsoki/sessions.db`.
- **The transcript is real.** What the user saw on screen is
  reconstructable from the event log, byte-for-byte.
- **Bugs are diagnosable.** When something goes wrong, an operator
  can read the log, see exactly which intent fired, which guard
  matched, which host returned what — without re-running the LLM.
- **Demos are testable.** The same flow YAML drives both the
  reproducible recording and the deterministic CI test.

This is what "deterministic" actually buys. The LLM is allowed to be
non-deterministic; everything *downstream* of the LLM is recorded
exactly enough that the next person who needs to understand what
happened doesn't have to be online.

---

## 8. Trust and authoring

A kitsoki application is one YAML file (or a tree of them via
`include:`). That YAML declares:

- the **rooms** and their connections,
- the **intent vocabulary**, both globally and per-room,
- the **world schema** with typed defaults,
- the **host allow-list** — which side effects this app may invoke,
- the **off-path trigger** — when free-form chat is acceptable,
- and any **phase templates** for repeated pipelines.

The YAML is the only source of truth. There are no hidden defaults
that the runtime might inject; loader-side validation is strict
(unknown fields are errors). When a host is invoked that wasn't in
the allow-list, the app fails to load.

The author can therefore reason about the application by reading a
single tree of YAML files. Reviewers can do the same. When a
collaborator (LLM or human) proposes a change, it's a diff against
that tree, reviewable like any code change.

Authors who want to evolve an app while operating it use declared meta
modes or Studio MCP instead of the removed TUI edit-mode overlay.
`/meta` opens a meta chat against the running app and reloads applied
edits in place when possible; `kitsoki mcp` exposes deterministic
`story.read`, `story.write`, `story.validate`, `story.graph`, and
`story.test` tools against an authoring workspace handle.

### 8.1 The LLM as an adversary

The trust model is asymmetric. The application author is trusted —
they wrote the YAML the operator chose to run. The **LLM is treated
as adversarial**: it can be prompt-injected by the user, by an
external surface (a Jira comment, a PR body), or by its own
hallucination. Kitsoki's job is to make that not matter.

Concretely, every layer below assumes a hostile LLM:

- **The expression language is whitelisted.** Guards and templates
  evaluate against a typed scope (`world.*`, `slots.*`, `event.*`,
  `run.*`) with a fixed operator and function set; no reflection, no
  I/O, no user-defined functions. See
  [`state-machine.md` §7](../stories/state-machine.md#7-guards-the-expr-language).
- **Effects are an enumerated alphabet.** `set`, `increment`, `say`,
  `emit`, `invoke: host.*`. The LLM never writes `world` directly —
  only the effects on a transition the *author* declared do that.
- **`host.*` is an opt-in registry.** Every host the app uses must
  appear in the top-level `hosts:` allow-list. `host.run` (shell) is
  not registered unless the author asked for it. An operator can
  refuse to compile-in a host module if they don't want apps to be
  able to invoke it.
- **Slot validation runs before any guard.** A malicious payload
  cannot escape the enum/regex/range constraints declared on the
  slot — see [`state-machine.md` §4](../stories/state-machine.md#4-intents-and-slots).
- **Off-path is marked and opt-in.** Operators can disable free-form
  chat entirely. When enabled, the TUI visibly frames the session so
  the user knows they are outside the deterministic graph.

A malicious `transition` call can therefore only cause an effect the
*author* declared on a transition the author bound — and only after
slot validation, guard evaluation, and (for `host.*` effects) the
host's own arg-validation pass. Secrets live in operator-owned
configuration (`~/.kitsoki/secrets.yaml`, OS keychain integrations)
and are referenced by name from host configs; app YAML never carries
secrets. App signing (detached signatures on the YAML tree, so a
distributed app can be verified) is future work — for now the
operator audits the YAML before running it.

---

## 9. Determinism boundary (where surprise is allowed)

Kitsoki is deliberate about where non-determinism can live.

| Layer | Deterministic? | Notes |
|---|---|---|
| Application state machine | Yes, always | Same world + same intent → same transition. No clocks, no random, no I/O inside the machine. |
| Expression evaluator (guards, templates) | Yes | Pure expressions over `world` and `slots`; no user functions. |
| YAML loader | Yes | Strict; unknown fields fail load. |
| Effects (`set`, `increment`, `say`, `emit`) | Yes | Pure mutations over the world snapshot. |
| Effects that invoke a host | No, in general | Hosts touch the network/filesystem; their results are recorded so replays are reproducible. |
| LLM translation | Configurable | `claude`/`live` are non-deterministic; `replay` is fully deterministic against a recording. |
| Time | Injected | Production uses real time; tests inject a virtual clock. |
| IDs | Injected | Production uses ULIDs derived from real time; tests inject a deterministic generator. |

The architecture confines non-determinism to two places: the LLM
call, and host invocations. Both record their inputs and outputs to
the event log. Everything downstream is replayable from those
recordings — which is what makes the test pyramid possible:

- **Mode 2 flow tests** drive the machine through a recording.
  Zero LLM cost, deterministic, run on every PR.
- **Mode 1 intent tests** measure how reliably real LLMs translate a
  given input. Run on demand.

---

## 10. Putting it together

A useful summary frame: kitsoki is what you get when you take three
things — a state-graph application engine, a structured-output LLM
adapter, and a multi-surface conversation runtime — and decide that
the *application author*, not the LLM, is in charge.

Concretely:

- **Authors** describe a finite, reviewable graph of rooms and intents.
- **Users** drive the application with free text, on whichever surface
  they're already in.
- **The LLM** translates that free text into an intent the author
  declared, retrying when its output doesn't fit.
- **The orchestrator** runs the resulting transitions deterministically,
  records every event, and posts the new view to every subscribed
  surface.
- **External orchestrators** (a Jira poller, a webhook receiver) feed
  inbound events through the same primitives, so the same application
  works whether driven by a TUI or a ticket comment.

The result is a system in which the LLM contributes its strengths
(natural-language understanding) and is denied its weaknesses
(deciding what to do, writing state without permission). The
application author retains agency; the user gets a forgiving surface;
the operator gets a replayable log.

---

## 11. Implementation map

The conceptual model above is realised by a small set of Go packages
under `internal/`. This section is for someone working on kitsoki
itself; if you're an application author or a user, you can stop
here and head to [`authoring.md`](../stories/authoring.md) or
[`developer-guide.md`](../guide/development/developer-guide.md).

### 11.1 Layered view

```mermaid
flowchart TD
    subgraph Surfaces["Surfaces (one or many, concurrent)"]
        TUI["TUI<br/>cmd/kitsoki run"]
        MCP["MCP server<br/>cmd/kitsoki serve"]
        EXT["External driver<br/>cmd/kitsoki session continue"]
    end

    subgraph Orch["Orchestrator (internal/orchestrator)"]
        ORCH["per-session writer lock<br/>turn loop: ingest → harness → machine → effects → store<br/>dispatches host invocations and transport posts<br/>bridges background jobs into synthetic completion turns"]
    end

    subgraph Core["Core seams"]
        HARNESS["Harness<br/>(LLM)"]
        MACHINE["Machine<br/>(pure)"]
        HOSTS["Hosts<br/>(effects)"]
        TRANSPORTS["Transports<br/>(output)"]
    end

    PERSIST[("Persistence<br/>internal/store, /chats, /jobs<br/>SQLite, append-only events")]

    Surfaces -- "inbound:<br/>free text or<br/>structured intent" --> Orch
    Orch --> HARNESS
    Orch --> MACHINE
    Orch --> HOSTS
    Orch --> TRANSPORTS
    MACHINE --> PERSIST
    HOSTS --> PERSIST
    TRANSPORTS --> PERSIST
```

### 11.2 Five interfaces

| Interface | Defined in | Implementations |
|---|---|---|
| `Machine` | `internal/machine` | `machine.machine` (only one — the pure core) |
| `Harness` | `internal/harness` | `claude_cli`, `live`, `replay`, `recording` |
| `Store` | `internal/store` | `sqlite` (production); in-memory test stub |
| `Transport` | `internal/transport` | `tui`, `jira`, `bitbucket` |
| `host.Handler` | `internal/host` | one per built-in (`host.run`, `host.agent.*`, `host.chat.*`, …) |

### 11.3 Package map

Pure core, no I/O:

| Package | Purpose |
|---|---|
| `internal/app` | YAML loader, types, schema validation. Source of truth for `app.yaml`. |
| `internal/intent` | `IntentCall`, `ValidationError`, error-code enum. |
| `internal/world` | Typed world snapshot — immutable map passed to guards, templates, effects. |
| `internal/expr` | `expr-lang/expr` evaluator with an AST whitelist. |
| `internal/machine` | The pure state machine. `Machine.Turn` is `(state, world, intent) → (next state, effects, events)`. |
| `internal/proposal` | Draft → review → execute lifecycle helpers. |
| `internal/history` | Bounded room-history stack used by `back` intents. |
| `internal/workspace` | Typed workspace context loaded by `host.workspace_manager.get`. |

Coordination:

| Package | Purpose |
|---|---|
| `internal/orchestrator` | The only writer to `Store`. Drives the turn loop, dispatches effects, manages background-job listeners, hot-reloads on `app.yaml` change. |
| `internal/host` | Registry of named side-effect handlers + built-ins. |
| `internal/harness` | LLM abstraction. |
| `internal/transport` | Output-only adapters. |
| `internal/clock` | Injectable time source (real / fake). |
| `internal/jobs` | Background-job scheduler + persistence + clarification flow. |
| `internal/inbox` | In-app notifications and teleport metadata. |
| `internal/chathost` | Adapter bridging `internal/chats.Store` into the host's `ChatStore` interface. |

Persistence:

| Package | Purpose |
|---|---|
| `internal/store` | SQLite-backed event log + session metadata + external-key index. |
| `internal/chats` | Persistent multi-turn chat threads, the `chat_pty_sessions` PTY-lifecycle rows, and the `chat_input_queue` FIFO of pending drives. |
| `internal/ulid` | 26-char monotonically-increasing IDs for sessions, jobs, chats, messages. |

Surfaces:

| Package | Purpose |
|---|---|
| `internal/tui` | Bubble Tea TUI. |
| `internal/mcp` | MCP server (`kitsoki serve`). |
| `internal/viz` | Graphviz DOT and Mermaid emitters. |
| `internal/trace` | Structured slog-based event tracing. |
| `internal/tmux` | tmux-CLI wrapper rooted at a kitsoki-owned socket (`$XDG_STATE_HOME/kitsoki/tmux.sock`). Used by the PTY-attach lifecycle to spawn / list / kill / attach / push status-bar updates. |
| `internal/chatattach` | The chat-attach lifecycle: acquire chat lock → ensure tmux session running `claude --resume <id>` → record `pty_attached` → run a caller-supplied `runTmux` callback (the CLI passes `tmux.AttachStreaming`; the TUI passes a bubbletea `tea.ExecCommand` wrapper) → flip to `pty_background` on detach. Ships `kitsoki-tmux.conf` (status bar config) via `go:embed`. Shared by `kitsoki chat attach` and TUI `/attach`. |

Authoring & testing:

| Package | Purpose |
|---|---|
| `internal/metamode` | Controller, chat storage adapter, and turn context for declared `/meta` modes. |
| `internal/mcp/studio` | Studio MCP facade: authoring workspace handles, deterministic `story.*` tools, and driving-session handles. |
| `internal/testrunner` | Mode 1 (intent pass-rate) and Mode 2 (deterministic flow) test runners. |

CLI:

| Package | Purpose |
|---|---|
| `cmd/kitsoki` | Cobra root + every subcommand. |

### 11.4 Persistence schema

Three concerns share one SQLite file (default
`$XDG_DATA_HOME/kitsoki/sessions.db`):

```
sessions             one row per session       (id, app_id, state_path, world_json, …)
events               append-only event log     (session_id, seq, ts, kind, payload_json)
jobs                 background-job lifecycle  (id, session_id, status, payload_json, …)
chats                persistent chat threads   (id, app_id, room, scope_key, status, claude_session_id, …)
chat_messages        chat transcript rows      (chat_id, seq, role, content, ts)
chat_locks           per-chat singleton lock   (chat_id, owner_pid, owner_host, heartbeat_at)
chat_pty_sessions    tmux-hosted claudes       (chat_id, tmux_session, tmux_host, mode, last_idle_at, …)
chat_input_queue     pending drives FIFO       (drive_id, chat_id, transport, payload, status, on_complete_json, …)
external_keys        (transport, thread) → session_id
```

The orchestrator is the only writer to `events` and `sessions`; chats
and jobs each have their own per-row lock. The full event-kind enum
is defined in [`internal/store/event.go`](../../internal/store/event.go).

### 11.5 Chained host-call rerender contract

When `on_enter:` declares multiple `invoke:` steps and step N+1
references a slot bound by step N inside a nested template (e.g.
`{{ world.X }}` inside `args:` of the second call), the orchestrator
re-renders step N+1's `with:` block against the post-bind world at
dispatch time. The machine snapshots the unresolved templates as
`HostInvocation.RawWith` at machine time; the orchestrator re-walks
them just before invoking each handler.

**Per-leaf fallback.** If a nested template raises during re-render
(e.g. a type mismatch on a structured artifact slot, or an
expression error against the current world), only *that leaf* falls
back to its corresponding pre-render value from the up-front-resolved
`hc.Args`. Sibling leaves still render against the fresh world.
Implemented in
[`internal/orchestrator/orchestrator.go::rerenderHostArgs`](../../internal/orchestrator/orchestrator.go)
and the recursive walk in `resolveTemplateValueLeafFallback`.

**`HostDispatched` event.** Emitted immediately before each handler
invocation. Its payload includes the post-rerender args and a
`rerender_fell_back` boolean that flags whether any leaf was
substituted. This makes the trace honest about what the handler
actually received, distinct from the pre-bind args snapshotted by
`HostInvoked` at machine time. Useful for tracing chained-bind issues
("did step 2 see step 1's output?") without re-running the LLM.

**Backward compatibility.** `HostInvoked` is still emitted with the
pre-bind args; existing replay cassettes and traces are unchanged.
`HostDispatched` is additive — store replay treats it as a no-op.
Enum entries live in
[`internal/store/event.go`](../../internal/store/event.go).

### 11.6 Observability

- **Trace** — `kitsoki run` auto-writes a JSONL trace to
  `.kitsoki/sessions/<utc-timestamp>-<app-id>.jsonl` on every
  invocation; there is no `--trace` flag on `run`. For one-shot
  headless turns, `kitsoki turn --trace <path>` writes to an explicit
  path (pair with `--trace-pretty -` to also pretty-print to stdout).
  Each line is one JSON object covering `turn.*`, `harness.*`,
  `machine.*`, `store.*`, `host.*`, and `jobs.*` events. See
  [`docs/tracing/trace-format.md`](../tracing/trace-format.md) for the schema.
- **Inspect** — `kitsoki inspect --session-id <id>` prints a read-only
  JSON snapshot of a stored session.
- **Visualise** — `kitsoki viz` emits Graphviz DOT or Mermaid.

The `turn.done` events carry `view_rendered`, so the JSONL trace is a
complete after-the-fact transcript.

---

## 12. Agent-split foundations (Phase 1)

The agent-split work separates the agent surface into five named verbs
ordered by blast radius (`extract`, `decide`, `ask`, `task`, `converse`).
Phase 1 lands the shared infrastructure all five verbs build on.
See [`docs/architecture/hosts.md` — Agent verb summary](hosts.md#agent-verb-summary)
for the verb table and selection guide.

### Streaming sink (`internal/host/agent_stream.go`)

Every agent handler calls `AgentStreamer.Run(ctx)` instead of forking claude
directly. When a `StreamSink` is installed on the context (the TUI wires one in
for live progress), the streamer appends `--output-format stream-json --verbose`
and tees each event to the sink via the existing `emitStreamEvent` path. When no
sink is present, it falls back to `--output-format text`. This ensures all five
verbs stream by default in any `StreamSink`-aware context and produce no
behavioural regression in test/CLI paths.

### Bash-profile wrapper (`internal/host/bash_profile.go` + `bash_mcp.go`)

Three profiles restrict the Bash tool surface for `ask` and `decide` agents:

- **read-only** — conservative built-in allowlist: grep, rg, find, cat, head,
  tail, ls, wc, file, stat, jq, sort, uniq, cut, tr, echo, printf, env, which,
  type, git (read-only subcommands only — see below), sed (no `-i`/`--in-place`),
  awk (no `system()` or `getline`). `python3` is **not** on the default allowlist
  because `python3 -c '…'` allows arbitrary code; authors who need it must use the
  `commands:` profile and explicitly own the risk.
- **commands** — explicit argv0 allowlist maintained by the author.
- **sandboxed-write** — any command allowed, but cwd is a per-call temp dir
  and `HTTP_PROXY` is set to an invalid value to block HTTP/HTTPS. Raw TCP
  connections are not blocked — full network isolation is a hardening TODO.

#### Read-only per-subcommand checks

The read-only profile applies per-subcommand checks for programs whose default
operation is safe but whose dangerous subsets would be missed by argv0 matching alone:

- **git** — only `log`, `diff`, `show`, `status`, `blame`, `rev-parse`, `ls-files`,
  `cat-file`, `branch` (listing flags only: `-a`, `-r`, `--list`, …), `tag -l`,
  `remote -v` are allowed. All mutating subcommands (`push`, `pull`, `fetch`,
  `reset`, `rm`, `commit`, `checkout`, `merge`, `rebase`, `clean`, `stash`, `gc`,
  `prune`) and any flag containing `--exec` or `--upload-pack` are rejected.
- **sed** — `-i`/`--in-place` (in-place file mutation) is rejected; stream editing
  from stdin to stdout is allowed.
- **awk** — scripts containing `system(` or `getline` are rejected; safe field-
  extraction scripts are allowed. Note: awk scripts using `{}` block syntax contain
  shell metacharacters (`{`, `}`) which are caught by the metacharacter check
  before the awk-specific check; authors who need awk with block syntax should use
  the `commands:` profile.

Shell metacharacters (`;`, `|`, `&`, backticks, `$(…)`, `{`, `}`) are rejected in
all profiles to prevent injection chains. The wrapper is best-effort: it does not
parse shell quoting; a command containing metacharacters inside a quoted string is
blocked even if it is safe. Full shell AST parsing is a hardening TODO.

#### Bash MCP enforcement (`internal/host/bash_mcp.go`)

Before this fix, `ApplyBashProfile` was defined but never called from production
code. Claude's built-in Bash implementation ran commands directly, bypassing all
profile restrictions.

The fix routes every Bash call through a kitsoki-owned MCP server
(`BashMCPServer`). When an `ask` or `decide` agent declares `Bash` in its tool
list, the handler:

1. Calls `BuildBashMCPEntry` to write a profile config temp file and build an MCP
   server entry that runs `kitsoki mcp-bash --profile-config <path>`.
2. Rewrites `"Bash"` → `"mcp__kitsoki-bash__Bash"` in `--allowedTools` so claude
   routes Bash calls through the kitsoki server.
3. Passes `--mcp-config` pointing at the combined MCP config (kitsoki-bash plus any
   submit validator) to claude.

The `kitsoki mcp-bash` subprocess reads the profile config, calls `ApplyBashProfile`
on every command, and either rejects (returning a tool error the LLM sees and can
self-correct from) or execs the command directly (no `sh -c`; command is split on
whitespace to avoid shell injection).

For `task` and `converse`, the agent gets unrestricted Bash from claude's built-in
implementation. Those verbs are the mutation verbs and their Bash surface is
intentionally unrestricted within the agent's declared permissions.

### Validator sandbox (`internal/host/validator_sandbox.go`)

Validator subprocesses for `decide` and `extract` calls run inside
`RunValidatorSandboxed`. Platform support:

- **Linux** — attempts `unshare -rn` (new user namespace + new network namespace)
  via a probe invocation (`unshare -rn /bin/true`) before running the real
  subprocess. The `-r` flag maps the current UID to root inside the user namespace,
  enabling unprivileged network-namespace creation on kernels where
  `CONFIG_USER_NS=y` and `/proc/sys/kernel/unprivileged_userns_clone` is enabled.
  If the probe fails (binary not found, kernel rejects the syscall, or unprivileged
  user namespaces are disabled), the handler emits `slog.WarnContext` with
  "validator sandbox unavailable: network not isolated; only HTTP_PROXY env-var
  protection applies" and runs the subprocess unsandboxed. Operators who see this
  warning should check `/proc/sys/kernel/unprivileged_userns_clone` or consider
  enabling user namespaces. Filesystem write isolation is NOT enforced on Linux in
  Phase 1; full mount-namespace isolation requires root or a suid helper and is a
  hardening TODO.
- **macOS** — uses `sandbox-exec -f <profile>` with a Seatbelt profile that denies
  writes outside the scratch dir and denies network. Falls back to the env-var deny
  path when `sandbox-exec` is not found.
- **Windows** — no sandbox support in Phase 1. Callers must set
  `UnsafeNoSandbox: true`; the loader emits a warn-line when an app running on
  Windows declares a validator without this opt-out.

All platforms set `HOME` and `TMPDIR` to the scratch dir and apply `MakeSandboxEnv()`
(HTTP_PROXY trick) to the subprocess environment.

---

## 13. Task spans and replay modes

`host.agent.task` is the agentic verb in the agent-split vocabulary. Unlike
the single-shot agent verbs (`extract`, `decide`, `ask`), a task span launches
a multi-turn Claude session with a declared tool surface, monitors every tool
call, and records enough state to re-run or analyse the task later without
invoking the LLM again.

### 13.1 What a task span records

When a `host.agent.task` invocation completes, three replay artifacts are
captured in the terminal `task.end` journal event:

| Artifact | Field | What it is |
|---|---|---|
| Initial state hash | `initial_state_hash` | `git:<HEAD SHA>` for git trees; `tree:<sha256>` for non-git directories. Fingerprints the working tree *before* the task ran. |
| Final diff | `final_diff` | Output of `git diff --no-color HEAD`, or empty string if nothing changed. |
| Files changed | `files_changed` | List of paths from `git diff --name-only HEAD`. |
| Replay mode | `replay_mode` | One of `file_diff`, `sandboxed_write`, `external_side_effect` — see §12.2. |

For Mode B tasks (sandboxed writes), an optional tarball of the scratch
directory is available for offline inspection.

During the task, every tool call the embedded Claude session makes is
journalled as a `task.tool` event. The streaming surface emits transient
`task.tool.start` / `task.tool.end` events so a TUI can show live tool
activity; only the rolled-up `task.tool` is persisted.

The three journal event kinds defined in `internal/journal/types.go`:

| Kind | Stream | Journal | Purpose |
|---|---|---|---|
| `task.tool.start` | Yes | No | Tool invocation began (live feed). |
| `task.tool.end` | Yes | No | Tool invocation finished (live feed). |
| `task.tool` | No | Yes | Rolled-up tool record (input preview, output preview, seq, trace IDs). |
| `task.acceptance.attempt` | Yes | Yes | Each attempt against the acceptance validator. |
| `task.end` | Yes | Yes | Terminal event with replay artifacts and outcome. |

### 13.2 Replay modes

Every task span is classified into one of three replay modes at recording time.
The classification is deterministic: it depends only on the agent declaration,
not on what actually ran.

**Mode A — `file_diff`**: The agent uses only filesystem tools (Read, Grep,
Glob, Edit, Write, Bash with a read-only or unrestricted profile) and has no
declared external side effects. The task can be re-applied to any working tree
that matches `initial_state_hash` by feeding `final_diff` through `git apply`.
This is the regression-replay mode for code-writing tasks and is the default.

**Mode B — `sandboxed_write`**: The agent's `BashProfile` is
`BashProfileSandboxWrite`. Writes are isolated to a scratch directory. The
task can be replayed by re-running the agent against a fresh scratch
environment; a tarball of the original scratch dir is captured for comparison.

**Mode C — `external_side_effect`**: The agent has `external_side_effect: true`
(author-declared) or the tool surface includes `WebFetch` / `WebSearch`.
Replay of external writes is not safe. Mode C spans are skipped in
`kitsoki replay --mode file_diff` and counted in the end-of-replay summary:
`"skipped N external-side-effect spans."` They can be re-run interactively
with `--mode llm_rerun` or `--mode hybrid`.

Classification logic lives in `internal/host/agent_task_replay.go`:
`inferReplayMode(agent Agent, tools []string) ReplayMode`.

### 13.3 Read-snapshot cap

To keep replay artifacts manageable, tool output stored in `task.tool` journal
events is capped at **256 KiB**. Output that fits is stored verbatim. Output
that exceeds the cap is replaced with:

```
sha256:<hex-of-full-output> (first 4096 bytes follow)
<first 4096 bytes of the output>
```

This preserves debuggability (the digest identifies the exact content; the
prefix gives enough context to see what was large) without unbounded journal
growth. The cap applies to the stored preview only; the Claude session itself
sees the full output.

### 13.4 Acceptance loop

`host.agent.task` runs an acceptance loop. On first run, the agent gets a fresh Claude session
(`--session-id` from a new ULID). On each retry (up to `max_retries`, default
5), `--resume` continues the same session so the agent retains its conversational
context. The kitsoki-internal `kitsoki.agent.validate` and
`kitsoki.agent.task_context` MCP tools are injected automatically into the
tool surface for the duration of the task.

### 13.5 KITSOKI_SESSION_ID propagation

The outer kitsoki session ID is injected into the subprocess environment as
`KITSOKI_SESSION_ID`. This lets tool scripts and shell commands invoked by the
embedded Claude session emit trace events that are attributed to the right
session in the journal, enabling cross-process span stitching when the agent
runs `kitsoki turn` or other kitsoki subcommands as tools.

---

## 14. Where to go next

- **Authoring an app** → [`authoring.md`](../stories/authoring.md) and
  `kitsoki docs app-schema`.
- **State machine vocabulary** → [`state-machine.md`](../stories/state-machine.md).
- **Building or contributing** → [`developer-guide.md`](../guide/development/developer-guide.md).
- **Testing** → [`testing.md`](../tracing/testing.md).
- **Background jobs** → [`background-jobs/`](../stories/background-jobs/README.md).
- **Hosts and transports** → [`hosts.md`](hosts.md), [`transports.md`](transports.md).
- **Prior art and comparative grounding** → [`prior-art.md`](prior-art.md).
