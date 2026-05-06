# Proposal — Host bug-fix and PR-refine as hally rooms driven by Jira and Bitbucket

**Status:** Draft v2.  Authored from the cyber-repo `devstory` story
consumer side.  Supersedes v1, which was written against an older
snapshot of `tools/loopy/bug-fix.py` (14 raw phases, no stage groups)
and assumed hally would absorb the orchestrator role wholesale.

**v2 reframing.**  Per `design.md` §1.4, hally is a multi-transport
conversation engine: the TUI is one surface, Jira ticket comments and
Bitbucket PR comments are equivalent peers.  The current bug-fix
pipeline (`tools/loopy/bug-fix.py`, 10,573 LoC; `loop.py`, 5,042 LoC)
already implements the conversation against Jira at coarse stage
granularity.  The goal of this proposal is to push the *conversation
state machine* down into hally at fine-grained phase granularity, while
**leaving `loop.py` as the orchestrator** — it keeps ticket selection,
polling, knowledge extraction, capability boundaries, metrics,
cross-run analysis, and dashboards.

**TL;DR.**  Hally needs five things, in priority order:

1. **Persistent singleton sessions keyed by external ID** — one session
   per `(transport, thread)` such as `jira:PLTFRM-12345`, with a
   writer-lock so concurrent invocations serialize.
2. **Output-only transport interface (`Transport.Post`)** — TUI panel,
   Jira comment, Bitbucket comment.  No inbound `Open(handler)` loop in
   v1; inbound is `loop.py`'s job.
3. **Phase template + checkpoint marker** — declare a room as N phases
   of identical shape with a transition graph; mark which phases
   pause for an external reply (checkpoints) vs. auto-advance.
   Cycle budgets per arc.
4. **Room-extensible reply vocabulary** — each checkpoint declares its
   own intent menu (`continue`, `quit`, `refine`, `restart_from`,
   `jump_to`, `block_on`, anything the room needs); hally's existing
   intent-router parses raw reply bodies against that menu.  No fixed
   framework triple.
5. **Sub-room composition** — import bug-fix into devstory cleanly
   (namespacing, intent shims, world scoping, exit contract).  Last
   priority; doesn't block bug-fix shipping standalone.

Daemon mode, webhook receivers, and `hally serve` are **out of scope
for v1** while `loop.py` is the orchestrator.

---

## 1.  Context — what bug-fix.py is today (post-merge)

`tools/loopy/bug-fix.py` is a 14-phase LLM-driven pipeline.  Phases
(omitting the optional `2`/`10`/`11` video phases):

```
-1   context extraction        7    test review
 0   preflight                 7.5  test implementation
 0.5 worktree setup            8    code review
 1   bug reproduction          9    security review
 1.5 service trace             9.5  build & deploy
 1.7 coverage review           9.6  version check
 3   fix proposal              9.7  validation
 4   missing-test report      12    PR creation
 5   missing-spec report      13    process review
 6   implementation plan
 6.5 fix implementation
```

Each phase produces a typed JSON artifact (validated by the `wiggum`
MCP server) and writes evidence into the run directory.  Several phases
carry **feedback arcs** to earlier phases — the L2 self-improvement
loops:

| From | Trigger | To | Why |
|---|---|---|---|
| 7.5 | tests fail after fix | 6.5 | re-implement |
| 8   | `[BLOCKER]` in review | 6.5 | re-implement |
| 9.7 | validation re-run fails | 3 | re-propose |
| 9.7 | validation re-run fails | 6.5 | re-implement (cheaper) |

Each feedback arc has a budget of 3 cycles before it hard-fails.

`tools/loopy/loop.py` wraps the pipeline.  `DEFAULT_STAGE_GROUPS`
batches the 14 phases into **4 stages**:

| Stage | Phases |
|---|---|
| reproduction     | -1 → 1.7 |
| propose_fix      | 3 → 6 |
| implement_review | 6.5 → 9 |
| validate         | 9.5 → 13 |

The Jira comment pause happens **between stages**, not between phases.
This is a UX choice — reviewers don't want 14 pings — not a model
truth.  Hally must support both: model phases at fine grain, surface
checkpoints at whatever cadence the transport configuration chooses.

`loop.py` further owns:

- Jira polling (60s default), comment-cursor tracking
- bot-prefix filtering (`[Bot]`-prefixed comments are ignored)
- authorized-reviewer ACL (`is_authorized_reviewer`)
- a rich reply parser: `continue`/`approve`/`lgtm`/`proceed`/`ok`/
  `approved`/`yes`/`override` → approve; `quit`/`skip`/`abandon`/
  `cancel`/`stop` → skip; `restart from <stage>` / `redo <stage>` /
  `rerun <stage>` → restart_from; anything else → feedback
- knowledge subsystem (`knowledge/`, INDEX.json, promotion)
- capability boundaries, metrics, cross-run analysis
- dashboards (`dashboard.py`)
- L4 self-improvement (rewriting `bug-fix.py` source on recurring
  failures; out of scope for hally)

`loop.py` stays as the orchestrator.  Hally hosts the per-ticket
conversation only.

## 2.  What's already in hally

Capabilities relevant to this proposal that are **already wired**:

- States, intents, transitions, guards, effects, host invocations.
- Event-sourced session persistence with periodic snapshots
  (`internal/store/`).  Resume from snapshot + replay events works.
- `host.oracle.ask` (one-shot prompt-file invocation, resolved per
  HALLY-GAPS §7.4).
- `timeout: { after: "10s", target: ... }` per-state timer.
- `Effect.Increment` (`internal/app/types.go:113`) — already covers
  the v1 "increment a cycle counter" need; supersedes the original
  proposal's separate Increment-effect ask.
- Background-jobs subsystem (`internal/jobs/`) — schema, store,
  scheduler, supervisor.  See `background-jobs-proposal.md` (data
  plane done, integration plane missing).
- Intent router that maps free text to per-state intent menus using
  each intent's `examples:` and slot schema.  Used by the TUI today;
  reusable for parsing inbound comment bodies.

Capabilities that exist but are **stubbed or unwired**:

- `host.oracle.talk` (multi-turn Claude session) — stub.
- `background: true` on effects — parsed, ignored.
- `world_override` on flow-test turns — silently ignored.

Capabilities that **do not exist** today and are required by this
proposal:

- External-key indexing of sessions (one session per `(transport, thread)`).
- Writer-lock semantics for concurrent session-continue invocations.
- A `Transport.Post` interface and Jira/Bitbucket implementations.
- Phase templates (compose N phases of identical shape from one
  template + a graph).
- A `checkpoint: true/false` flag per phase.
- Sub-room composition (the existing `include:` is a glob-merge, not
  real composition).
- `host.oracle.ask_with_mcp` (MCP-aware oracle invocation).

---

## 3.  Primitive 1 — Persistent singleton sessions + external keys

### 3.1  Why

`loop.py` dispatches per ticket and per inbound comment.  It needs:

- A way to address a session by `(transport, thread)` so `hally session
  continue --key jira:PLTFRM-12345` finds the right session.
- A guarantee that two concurrent invocations against the same key
  serialize cleanly.

### 3.2  Schema

```sql
CREATE TABLE external_keys (
    transport   TEXT    NOT NULL,
    thread      TEXT    NOT NULL,
    session_id  TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (transport, thread)
) STRICT;
CREATE INDEX external_keys_session_idx ON external_keys(session_id);
```

A session may carry multiple keys (bug-fix attaches both
`jira:PLTFRM-12345` and `bitbucket:DBI/repo/pulls/42` once the PR is
opened).  First key is set on `session create`; later keys via:

```yaml
effects:
  - bind_external_key:
      transport: bitbucket
      thread:    "{{ world.bb_pr_thread }}"
```

### 3.3  Writer lock

At session load:

```
BEGIN IMMEDIATE on the session's events table
  → if held by another process, wait up to --busy-timeout (default 5s)
  → if still held, exit with code 75 (EX_TEMPFAIL) and message
    "session busy: held by pid <X> since <ts>"
```

Lock held for one event only — load → run to next checkpoint → post →
commit → release.  Long-paused sessions hold no lock.

### 3.4  CLI surface

```bash
hally session create   --app stories/bugfix/app.yaml --key jira:PLTFRM-12345
hally session continue --key jira:PLTFRM-12345 --intent <name> --slots <json>
hally session continue --key jira:PLTFRM-12345 --raw "<reply body>"
hally session show     --key jira:PLTFRM-12345
hally session list     --transport jira
hally tui              --key jira:PLTFRM-12345    # attended attach
```

`session list` returns one row per session with last-active time,
current state path, and all bound external keys.

### 3.5  Public Go API

```go
sessions.CreateSession(def, opts)                     // existing; takes ExternalKey opt
sessions.LookupByKey(transport, thread)               // new
sessions.BindExternalKey(id, transport, thread)       // new
sessions.ListSessionsByTransport(transport)           // new
sessions.WithWriterLock(id, fn) error                 // new; returns ErrSessionBusy on conflict
```

### 3.6  Resolves

- The "session keying" gap from the bug-fix analysis.
- Provides the `loop.py` ↔ hally contract per `design.md` §1.4.

---

## 4.  Primitive 2 — Output-only `Transport.Post` interface

### 4.1  Why

Bug-fix posts to Jira at each checkpoint.  PR-refine posts to Bitbucket.
The TUI renders to a panel.  All three are the same operation: format
phase output for a surface.  Hard-coding any one of them is wrong.

### 4.2  Interface

`internal/transport/transport.go`:

```go
package transport

type Transport interface {
    // ID returns the transport key, e.g. "jira", "bitbucket", "tui".
    ID() string

    // Post sends a message to the external thread.  body is rendered
    // by hally from a per-transport template (Jira wiki, markdown,
    // ANSI for TUI).  Returns the posted message ID for traceability.
    Post(ctx context.Context, key SessionKey, msg Message) (string, error)
}

type SessionKey struct {
    Transport string  // "jira", "bitbucket", "tui"
    Thread    string  // "PLTFRM-12345", "DBI/repo/pulls/42", "<session-uuid>"
}

func (k SessionKey) String() string { return k.Transport + ":" + k.Thread }

type Message struct {
    PhaseID     string         // "phase_3", "phase_9_7"
    Title       string
    Body        string
    Attachments []Attachment
    BotMarker   string         // e.g. "[hally]" — prepended for inbound filtering
}
```

Drivers register at startup:

```go
transport.Register("jira",      jira.New)
transport.Register("bitbucket", bitbucket.New)
transport.Register("tui",       tui.New)
```

The existing TUI render path is refactored to implement this interface
as the first concrete driver.  No external behavior change.

**No `Open(handler)` method in v1.**  Inbound is `loop.py`'s job.

### 4.3  Bot-output marker

Every `Post` prepends `BotMarker` (default `[hally]`) to the message
body.  `loop.py`'s existing `[Bot]` filter generalizes — orchestrators
filter their own outputs by prefix on inbound polling.  Configured
per-transport in `app.yaml`:

```yaml
transports:
  jira:
    base_url:    "${JIRA_URL}"
    auth:        basic
    user_env:    JIRA_USERNAME
    token_env:   JIRA_API_TOKEN
    bot_marker:  "[hally]"      # default
  bitbucket:
    base_url:    "https://127.0.0.1:3128/bitbucket"
    auth:        bearer
    token_file:  "~/.config/acronis/bitbucket-token"
    bot_marker:  "[hally]"
```

### 4.4  Resolves

- The "multi-transport" gap from question 3 of the devstory hally
  survey.
- HALLY-GAPS §7.11 (plugin / external-host extension): partial — only
  the output side.

---

## 5.  Primitive 3 — Phase template + checkpoint marker + cycle budgets

### 5.1  Why

A naive port of bug-fix to flat hally states is roughly 14 phases ×
{executing, awaiting_reply, error} ≈ 42 states, each with the same
shape, plus the L2 feedback arcs.  Authoring this by hand is
unmaintainable.  What we have is one *shape* (a phase) repeated N
times with a transition graph between phase instances.  Make both
first-class.

The checkpoint marker is what reconciles fine-grained phases with the
"don't ping reviewers 14 times" UX truth: phases are model nodes;
checkpoint phases are the subset that pause for an external reply.

### 5.2  Phase template

A `phase_templates:` block in `app.yaml`:

```yaml
phase_templates:
  reviewed_phase:
    parameters:
      id:            { type: string,  required: true }
      script:        { type: string,  required: false }
      prompt:        { type: string,  required: false }
      mcp_schema:    { type: string,  required: false }
      title:         { type: string,  required: true }
      checkpoint:    { type: boolean, required: false, default: false }

    states:
      "{id}_executing":
        on_enter:
          - invoke: host.oracle.ask_with_mcp
            with:
              prompt: "{{ tpl.prompt }}"
              schema: "{{ tpl.mcp_schema }}"
            bind:
              "{{ tpl.id }}_artifact": "stdout"
            on_error: "{{ tpl.id }}_error"
        on:
          done:
            # Branch on checkpoint flag — auto-advance or pause.
            - target: "{{ tpl.id }}_awaiting_reply"
              when:   "tpl.checkpoint == true"
              effects:
                - invoke: transport.post
                  with:
                    transport: "{{ world.transport }}"
                    key:       "{{ world.session_key }}"
                    phase_id:  "{{ tpl.id }}"
                    title:     "{{ tpl.title }}"
                    body:      "{{ {{ tpl.id }}_artifact }}"
            - target: "{{ phase.next.continue }}"
              default: true

      "{id}_awaiting_reply":
        # Intents declared at the room level — see §6.
        on:
          continue: [{ target: "{{ phase.next.continue }}" }]
          quit:     [{ target: terminated }]
          # refine, restart_from, jump_to, block_on, etc. are merged in
          # from the room-level `checkpoint_intents:` block.

      "{id}_error":
        view: |
          ── Phase {{ tpl.title }} — failed ─────────────────────────
          {{ world.last_error }}
        on:
          retry: [{ target: "{{ tpl.id }}_executing" }]
          quit:  [{ target: terminated }]
```

### 5.3  Phase graph

```yaml
phases:
  template: reviewed_phase

  graph:
    # checkpoint defaults to false — phases auto-advance unless declared.
    # Mark only phases that close a stage group (loop.py's UX cadence) or
    # are major decision points (e.g. validation with feedback arcs).

    phase_minus_1:
      script:     scripts/bugfix/phase_minus_1.py
      prompt:     prompts/00-context-extraction.txt
      mcp_schema: schemas/00-context.json
      title:      "Context Extraction"
      next:
        continue: phase_0

    phase_0:
      script:     scripts/bugfix/phase_0.py
      title:      "Preflight"
      next:
        continue: phase_0_5

    # phase_0_5 through phase_1_5 — auto-advance, matches loop.py's
    # "reproduction" stage group: silent until reaching the last phase.

    phase_1_7:
      title:      "Coverage Review"
      checkpoint: true                       # end of "reproduction" stage
      next:
        continue: phase_3

    phase_6_5:
      script:     scripts/bugfix/phase_6_5_implement.py
      prompt:     prompts/06.5-fix-implementation.txt
      mcp_schema: schemas/06.5-implementation.json
      title:      "Fix Implementation"
      next:
        continue:                phase_7
        on_test_fail_from_7_5:   phase_6_5    # self-loop
        on_blocker_from_8:       phase_6_5    # self-loop
        on_validation_fail_short: phase_6_5    # cheap loop from 9.7

    phase_7_5:
      title:      "Test Implementation"
      next:
        continue:    phase_8
        on_failure:  phase_6_5

    phase_9_7:
      title:      "Validation"
      checkpoint: true                       # end of "validate" stage
      next:
        continue:                 phase_12
        on_validation_fail:       phase_3
        on_validation_fail_short: phase_6_5
      cycle_budgets:
        on_validation_fail:       3
        on_validation_fail_short: 3

    # … remaining phases …

    phase_13:
      title:      "Process Review"
      checkpoint: true
      next:
        continue: terminated
```

### 5.4  Cycle budgets

`cycle_budgets:` is the declarative form.  At template-expansion time
the runtime synthesizes, for each declared arc:

1. An `Effect.Increment` on the arc's `effects:` block, against a
   counter keyed `cycle__<phase_id>__<arc>` in `world`.  This is the
   existing increment effect (`internal/app/types.go:113`); no new
   effect type.
2. A guard on the arc's transition: `cycle__<phase_id>__<arc> < N`,
   with a fall-through transition to `<phase_id>_error` carrying a
   `guard_hint` of "cycle budget exceeded for {arc}".

The counter is written through the normal event log
(`EffectApplied{Set: cycle__phase_9_7__on_validation_fail = 2}`), so
replay is deterministic and the budget can be inspected via `hally
session show`.

Authors never write the increment or the guard themselves — the
template handles it.  An author who wants to override the synthesis
(e.g. a different error target) writes the arc's transitions
explicitly and omits `cycle_budgets:`.

### 5.5  Resolves

- The "phase-template / sub-room composition" gap from question 7 of
  the devstory hally survey.  This proposal picks templates over full
  sub-room composition for §5.  Sub-rooms are §8 (last priority).
- The "retry-budget primitive" listed as gap #2 in the bug-fix
  analysis — folded into `cycle_budgets:` here.
- Reconciles "fine-grained phases" (`design.md` §1.4) with the "don't
  ping reviewers 14 times" UX requirement.

---

## 6.  Primitive 4 — Room-extensible reply vocabulary

### 6.1  Why

`loop.py` accepts a rich reply vocabulary:

- continue: `continue`, `approve`, `lgtm`, `proceed`, `ok`, `approved`,
  `yes`, `override`
- skip: `quit`, `skip`, `abandon`, `cancel`, `stop`
- restart from: `restart from <stage>`, `redo <stage>`, `rerun <stage>`
- everything else: feedback (free-form refine)

A fixed framework triple of `continue` / `refine` / `quit` would regress
this in production.  Worse, it would prevent rooms from declaring
room-specific intents (e.g. PR-refine's `merge_now`, `request_changes`,
`needs_more_info`, `approve_with_nits`).

### 6.2  Design

Each checkpoint state declares its own intent menu.  The orchestrator
either:

1. Pre-parses well-known keywords and passes
   `--intent <name> --slots <json>`, OR
2. Passes the raw reply body via `--raw "<body>"` and lets hally's
   existing intent-router map the body to one of the menu's intents
   using each intent's `examples:` and slot schema.

Synonyms are declared as intent `examples:`, not hardcoded:

```yaml
checkpoint_intents:                          # merged into every checkpoint state
  continue:
    description: Approve this phase and advance.
    examples: [continue, approve, lgtm, proceed, ok, yes, override]
  quit:
    description: Abandon this session.
    examples: [quit, skip, abandon, cancel, stop]
  refine:
    description: Re-run this phase with feedback applied.
    slots:
      feedback: { type: text, required: true }
    examples: ["please retry with X", "feedback: ..."]
  restart_from:
    description: Restart from an earlier stage.
    slots:
      stage:
        type: enum
        values: [reproduction, propose_fix, implement_review, validate]
    examples:
      - "restart from propose_fix"
      - "redo reproduction"
      - "rerun validate"
  jump_to:
    description: Jump forward to a specific phase.
    slots:
      phase: { type: string }                # e.g. "phase_9_7"
    examples: ["jump to validate", "skip to phase_9_7"]
  block_on:
    description: Mark this session as blocked pending external action.
    slots:
      reason: { type: text, required: true }
    examples: ["blocked: waiting on data", "block on stand provisioning"]
```

### 6.3  Reply attribution as a standard slot

Every transport-sourced intent populates `world.last_reply_author`
with the surface-native author identifier (Jira display name, Bitbucket
username, etc.).  Rooms gate on it without re-implementing ACL:

```yaml
phase_8_awaiting_reply:
  on:
    continue:
      - target: phase_9
        when:   "world.last_reply_author in world.allowed_authors"
        guard_hint: "Only authorized reviewers may approve this phase."
```

### 6.4  Resolves

- Open question §11.2 from v1 of this proposal.
- Reverses v1's fixed-vocabulary design.  Implements the
  `design.md` §1.4 stance.

---

## 7.  Smaller primitives

### 7.1  MCP-aware oracle invocation

`host.oracle.ask_with_mcp` — same signature as `host.oracle.ask` plus a
`mcp_servers:` map injected into the `claude -p --mcp-config` argument.
Supports wiggum-style typed JSON responses; the response is bound into
world slots after schema validation by the named MCP server:

```yaml
- invoke: host.oracle.ask_with_mcp
  with:
    prompt: prompts/03-fix-proposal.txt
    mcp_servers:
      wiggum:
        command: python3
        args:    [tools/loopy/wiggum-mcp.py, --schema, schemas/03-fix-proposal.json]
    schema: schemas/03-fix-proposal.json
  bind:
    proposal: "stdout_json"
```

Resolves a portion of HALLY-GAPS §7.10 (typed `host.run_json`).
Required by every LLM-driven phase in bug-fix.

### 7.2  `world_override` in flow tests

Implement the existing-but-ignored key in `internal/testrunner/`.
Per-turn world mutations applied before guard evaluation.  Resolves
HALLY-GAPS §7.19.  Required to test the L2 feedback arcs without
hand-stitching a full preceding flow.

### 7.3  `Effect.Increment` — already done

The original v1 proposal asked for a new `Increment` effect.
`internal/app/types.go:113` already has it.  **Dropped from scope.**

### 7.4  Streaming `host.run_stream`

Per HALLY-GAPS §7.1.  Lower priority — bug-fix doesn't need it (Jira
comments are batch-posted, not streamed).  Useful later for `hally tui
--key X` attach-mode showing live progress.  Defer.

---

## 8.  Sub-room composition (last priority)

### 8.1  Why

`stories/devstory/rooms/bugfix.yaml` (618 LoC) wraps `bug-fix.py` as
three thin proposals (repro/apply/verify) inside the wider devstory
app.  After §3-§7 land, the bug-fix room has its own complete YAML
that exists as a standalone hally app.  The devstory app should be
able to import it as a sub-room rather than duplicating its content.

The existing `include:` (`internal/app/types.go:31`) is a glob-pattern
merge, not real composition.  It dumps all states into one flat
namespace and offers no scoping.

### 8.2  Design (sketch — full spec in phase H)

```yaml
# stories/devstory/app.yaml
imports:
  bugfix:
    app:    stories/bugfix/app.yaml
    prefix: bugfix/                          # all child states namespaced
    expose:
      entry:    bugfix/idle                  # parent-callable entry state
      exits:                                 # named return points
        completed: bugfix_completed
        abandoned: bugfix_abandoned
        failed:    bugfix_failed
    world:
      scope: subtree                         # child world vars don't pollute parent
```

Parent declares an outgoing transition:

```yaml
states:
  main:
    on:
      start_bugfix:
        - target: bugfix/idle
          effects:
            - set: { bugfix.ticket: "{{ slots.ticket }}" }
```

Child returns via its declared exits; parent maps each to a state it
owns:

```yaml
states:
  bugfix_completed:
    view: "Bug fix complete; PR opened."
    on:
      back_to_main: [{ target: main }]
```

### 8.3  Why last priority

- Bug-fix can ship as a standalone app first (`stories/bugfix/app.yaml`).
- Devstory's existing `bugfix.yaml` keeps working until composition lands.
- Composition is a non-trivial design (namespace, world scoping, intent
  re-export, exit contract) — better to ship it after the bug-fix
  room's shape has settled in production.

### 8.4  Resolves

- The "phase-template / sub-room composition" gap from question 7 of
  the devstory hally survey — the *sub-room* half.

---

## 9.  Out of scope (explicit non-goals for v1)

- **Daemon mode (`hally serve`).**  `loop.py` is the orchestrator; no
  long-running hally process.  Re-evaluate if the orchestrator boundary
  moves.
- **Webhook receivers / HTTP listener.**  Polling-only via `loop.py`.
- **Inbound `Transport.Open(handler)` loop.**  Inbound is the
  orchestrator's job.
- **L4 self-improvement.**  bug-fix.py editing its own source code on
  recurring failure stays in `loop.py`'s wrapper.  Hally hosts the
  phase pipeline; it does not need to know about pipeline self-edits.
- **Knowledge subsystem, capability boundaries, metrics, dashboards.**
  All stay in `loop.py`.  Hally has no opinion on them.
- **Multi-tenant cloud sync.**  Sessions live in local SQLite.
- **L1 within-phase LLM retry.**  Handled by the LLM oracle host
  (`claude -p --resume`), not by hally state machinery.

---

## 10.  Phased delivery plan

| Phase | Scope | Effort |
|---|---|---|
| **A. Charter update** *(done)* | `design.md` §1.2 (relaxed "not a workflow engine" framing), new §1.4 (conversation surfaces, orchestrator boundary, singleton sessions, phase/checkpoint distinction, room-extensible reply parsing), §4.1 (per-event invocation note), §8 (external_keys schema + writer-lock semantics). | ½ day |
| **B. Quick wins** | `world_override` in flow tests (§7.2); `host.oracle.ask_with_mcp` (§7.1); validate `Effect.Increment` covers cycle-counter need. | ~1 week |
| **C. Persistent singleton sessions + external keys** | external_keys table, `LookupByKey`, `BindExternalKey`, writer lock, `hally session create/continue/show/list/attach` CLI (§3). | ~1 week |
| **D. Output `Transport.Post` + TUI as first impl** | Refactor TUI render path to implement `Transport.Post`; bot-marker convention; transport registry (§4).  No external behavior change. | ~3-4 days |
| **E. Phase template + checkpoint marker + cycle budgets** | Template expansion, checkpoint flag, event-sourced cycle counters via `Effect.Increment`, room-extensible intent menus per checkpoint (§5, §6). | ~2 weeks |
| **F. Jira `Transport.Post` driver** | Comment-create against Jira REST.  Bot-marker output prefix.  Reply-attribution slot. | ~3-4 days |
| **G. Bitbucket `Transport.Post` driver + PR-refine room** | Validates abstraction across two transports.  PR-refine room is the second concrete user. | ~1-2 weeks |
| **H. Sub-room composition** | Namespacing, intent shims, world scoping, exit contract (§8). | ~2 weeks |

Total hally-side: **~6-8 weeks** for A-H.

In parallel (cyber-repo):

- **bug-fix.py decomposition.**  Per-phase scripts replace the
  10,573-line mega-script.  Lives in cyber-repo.  Multi-month, not
  blocking hally work — phase G can land against the existing
  monolithic `bug-fix.py --from-phase N --to-phase M` interface.

---

## 11.  Migration — how bug-fix lands on hally

After phases A-G:

1.  cyber-repo extracts `prompts/phase_*.txt` (one per LLM-driven
    phase) into `stories/bugfix/prompts/`.
2.  cyber-repo extracts `wiggum-schemas/*.json` into
    `stories/bugfix/schemas/`.
3.  cyber-repo writes `stories/bugfix/app.yaml` — one phase template,
    one phases.graph, one transports block, one `checkpoint_intents`
    block.  ~250 lines of YAML.
4.  `loop.py` shrinks: ticket selection, polling, comment dispatch,
    knowledge subsystem stay.  Stage-group dispatch (`dispatch_stage`)
    is replaced by `hally session continue --key jira:<TICKET> --raw
    "<comment body>"`.  Per-stage `bug-fix.py --from-phase N --to-phase
    M` invocations move into hally's phase-runner.
5.  `bug-fix.py` is decomposed into per-phase scripts invoked by the
    room's `host.oracle.ask_with_mcp` effect (parallel cyber-repo
    work).
6.  Phase H wraps `stories/bugfix/app.yaml` into `stories/devstory/`
    via sub-room composition.  Existing `devstory/rooms/bugfix.yaml`
    is replaced by a thin `imports:` declaration.

End state: bug-fix runs as `loop.py` orchestrating per-event invocations
of `hally session continue`, addressable by ticket key, posting per-
phase (or per-stage, depending on `checkpoint:` flags) comments to
Jira, accepting room-extensible reply vocabularies, retrying with
event-sourced cycle budgets, opening a Bitbucket PR, attaching the PR
thread as a second external key, and continuing the conversation
through PR-refine on Bitbucket.

---

## 12.  Cross-references

- `background-jobs-proposal.md` — orthogonal to this proposal.  Useful
  if a per-phase script exceeds a soft duration cap and needs to run
  asynchronously, but not on the bug-fix critical path.
- `ai-collaboration-proposal.md` — relevant if hally itself uses LLMs
  to author or maintain phase graphs.
- `design.md` §1.4 — defines the conversation-surfaces /
  orchestrator-boundary framing this proposal stacks on.
- `design.md` §3 (proposals), §4 (jobs / inbox), §5 (history) — the
  existing primitives we extend, not replace.
- `dev-story-design.md` — the original devstory design that this
  proposal grew out of.
- cyber-repo `stories/devstory/HALLY-GAPS.md` — the per-room gap log.
  Items resolved or partially-resolved by this proposal:
  §7.1 (streaming, deferred), §7.10 (host.run_json; via MCP-aware
  oracle), §7.11 (plugin / external-host extension; **partially
  resolved** by §4 — output side only), §7.19 (world_override).

---

## 13.  Open questions

1.  **Reply parsing fallback.**  When `--raw` is used and the intent
    router can't match the body to any menu intent above its
    confidence threshold, what's the fallback?  Recommendation: emit
    a `clarify` event back through the transport (post a comment
    asking the reviewer to specify) and stay in `awaiting_reply`.
2.  **Writer-lock timeout default.**  5s is a starting guess.  Real
    answer comes from observing `loop.py`'s dispatch cadence (currently
    60s polling, ~30s dispatch delay) — collisions should be rare.
3.  **`hally tui --key X` UX when an orchestrator is actively driving
    the same key.**  Recommendation: TUI shows a banner ("Jira-driven
    session — your input may collide with reviewer comments") and
    competes for the writer-lock like any other invocation.
4.  **Per-phase vs. per-stage checkpoints.**  Initial `app.yaml` should
    match `loop.py`'s 4-stage cadence (checkpoint at the last phase of
    each stage, auto-advance the rest) for behavioral parity.  Tighter
    cadences for attended TUI sessions become a phase E follow-up.
