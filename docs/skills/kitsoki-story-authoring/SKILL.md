---
name: kitsoki-story-authoring
description: Author and edit kitsoki stories — the YAML state machines under `stories/<name>/`. Use when the user wants to add or modify a room, intent, transition, effect, host call, world var, view, prompt, flow fixture, import, exit, or phase template. Covers YAML shape, effect/host vocabulary, typed view elements, style guide, load-time invariants, and the authoring loop. For "this room misbehaves / bounced to idle" reach for `kitsoki-debugging` instead.
---

# Kitsoki Story Authoring

A "story" is a directory the engine loads as one app: `app.yaml` (the
manifest), `rooms/*.yaml` (state definitions, glued in via `include:`),
`prompts/*.md` (LLM templates), `views/*.pongo` (typed-element base
templates), `flows/*.yaml` (Mode-2 deterministic tests), optional
`schemas/*.json` (typed-JSON contracts for `ask_with_mcp`), and an
optional `README.md`.

The gold-standard reference stories live in this repo. Read them in
this order — most authoring questions resolve by mimicking the closest
existing room:

| Story | Why look here |
|---|---|
| `stories/oregon-trail/` | The canonical typed-elements view layout, status bar, multi-import composition (imports `frontier_event` which imports `robbery`). |
| `stories/bugfix/` | Operator-facing pipeline shape: phase rooms with `on_enter` oracle calls, `iface.*` host_interfaces, `accept`/`refine`/`restart_from` checkpoint intents, `@exit:done` / `@exit:abandoned`. |
| `stories/robbery/` | Smallest complete importable sub-story — `host_interfaces`, `exits` with `requires:`, `world_in:`, an importer README contract. |
| `stories/dev-story/` | Live-result lists (`iface.ticket.search` bound into a `code:` `{% for %}`), readiness banners with `available()` / `blocked_reason()` helpers. |

The authoritative schema is `kitsoki docs app-schema`. The authoring
prose lives at `docs/authoring.md`, the style guide at
`docs/story-style.md`, the state-machine semantics at
`docs/state-machine.md`, the imports / composition reference at
`docs/imports.md`, and the host registry at `docs/hosts.md`. When in
doubt about a field, **grep the gold-standard stories first; consult
the schema doc second; ask the user last**.

## 1. Anatomy of a story

```
stories/<name>/
├── app.yaml                  manifest — app/world/intents/root/include
├── rooms/                    one file per logical room; merged via include:
│   └── <room>.yaml
├── prompts/*.md              oracle prompt templates (Go-template + pongo)
├── views/                    optional; typed-element base templates
│   ├── base.pongo            block contract: status / heading / body / choices / footer
│   └── partials/             reusable {% include %}-able fragments
├── schemas/*.json            JSON-schema contracts for ask_with_mcp
├── flows/*.yaml              Mode-2 deterministic test fixtures
├── scenarios/*.yaml          optional; warp bases for operator smoke tests
└── README.md                 mandatory if the story is importable
```

## 2. Top-level shape of `app.yaml`

```yaml
app:
  id: my-app
  version: 0.1.0
  title: "Short human title"
  author: "Owner"
  license: "CC0"

# Every host.* the story invokes must be listed here. iface defaults
# (declared under host_interfaces:) are added implicitly by the loader.
hosts:
  - host.oracle.ask_with_mcp
  - host.inbox.add

# Provider-neutral capability surfaces. Importers rebind via host_bindings.
host_interfaces:
  ticket:
    operations:
      get: { input: { id: string }, output: { id: string, title: string } }
    default: host.local_files.ticket

# Typed key/value bag. EVERY world.* read in any view, effect, or guard
# MUST be declared here with a type and default; loader rejects unknowns.
world:
  ticket_id:    { type: string, default: "" }
  cycle:        { type: int,    default: 0 }
  judge_mode:   { type: string, default: "human" }
  artifact:     { type: object, default: {} }
  party_alive:  { type: int,    default: 5 }
  ready:        { type: bool,   default: false }

intents:                       # global intent library
  accept:
    description: "Advance to the next phase."
    examples: ["continue", "accept", "lgtm"]
    priority: 85
    slots:
      feedback: { type: string, required: false }

exports:                       # intents lifted into a parent's table
  intents: [accept, refine, quit, look]

exits:                         # named return points for an importer
  done:
    requires: [done_artifact]  # static check on every @exit:done transition
  abandoned: {}

root: idle                     # initial state

include:
  - rooms/*.yaml               # merge each file's `states:` block in
```

`include:` merges files into one flat AppDef (same namespace, same
world). `imports:` is a separate mechanism for embedding an *aliased*
sub-story with private world and explicit boundary projections — see
§7 below.

## 3. The shape of a room (state)

```yaml
states:
  proposing:
    description: "Draft the fix proposal; review and advance to implementing."
    relevant_world: [ticket_id, artifact, cycle]   # location indicator keys
    view:
      extends: "base"
      blocks:
        body:
          - kv:
              pairs:
                Ticket:     "{{ world.ticket_id }} — {{ world.ticket_title }}"
                Confidence: '{{ world.artifact.confidence|default:"(pending)" }}'
          - heading: "Artifact"
          - code: '{{ world.artifact.summary_markdown|default:"(pending — oracle has not returned)" }}'
        choices:
          - heading: "Actions"
          - list:
              items:
                - label: "continue"
                  hint:  "post the proposal and advance"
                - label: "refine feedback=…"
                  hint:  "re-draft with feedback"
                - label: "quit"
                - label: "look"
    on_enter:
      - invoke: host.oracle.ask_with_mcp
        with:
          prompt: prompts/proposing_executing.md
          schema: schemas/proposing_artifact.json
          working_dir: "{{ world.workdir }}"
          args:
            ticket_id:    "{{ world.ticket_id }}"
            ticket_title: "{{ world.ticket_title }}"
        bind:
          artifact: submitted
        on_error: idle
    on:
      accept:
        - target: implementing
          effects:
            - set: { cycle: 0 }
      refine:
        - when: "world.cycle >= 3"
          target: "@exit:abandoned"
          effects:
            - set: { abandon_reason: "proposing_budget_exhausted" }
        - target: proposing
          effects:
            - set:
                refine_feedback: "{{ slots.feedback }}"
                cycle:           "{{ world.cycle + 1 }}"
      quit:
        - target: "@exit:abandoned"
      look:
        - target: .
```

The shape rules:

- **Order matters inside each intent's transition list.** First matching
  `when:` wins. Always end with `default: true` (the catch-all) — a missing
  default lets `GUARD_FAILED` reach the user.
- `target: .` means "stay in the same atomic state" — use for `look` and
  for any read-only intent.
- `target: "@exit:<name>"` is the importable exit form; the loader
  rewrites it based on the parent's `imports.<alias>.exits.<name>.to` (or
  synthesises a terminal in standalone mode).
- `relevant_world:` keys MUST exist in the top-level `world:` schema.

### Compound and parallel states

```yaml
bar:
  type: compound
  initial: dark               # required; supports {{ world.x }} templating
  states:
    dark: { ... }
    lit:  { ... }
```

```yaml
game:
  type: parallel
  states:
    lighting: { type: compound, initial: bright, states: { ... } }
    narrator: { type: compound, initial: idle,   states: { ... } }
```

Children inherit the parent's `on:` bindings unless overridden. `emit:
foo` from one parallel region is observed as an event by siblings.

## 4. Effect vocabulary

Effects run in declaration order inside one transition. The world is
immutable per turn; later effects see the snapshot built by earlier
ones.

| Effect | Shape | Notes |
|---|---|---|
| `set` | `set: { k: "{{ ... }}", k2: 7 }` | Templated. Strings are pongo; numerics/bools render as their Go literal. |
| `increment` | `increment: { counter: 1 }` | Integer delta, +/-. |
| `say` | `say: "..."` | Appends a narrative line to the rendered view. Templated. |
| `emit` | `emit: event_name` | Broadcasts to parallel siblings. |
| `emit_intent` | `emit_intent: accept` + optional `slots: { ... }` | Dispatches a synthetic intent against the current state — used to auto-advance from `on_enter` after a confident LLM judge. Mutually exclusive with `target:`. Depth-capped at 8. |
| `invoke` | `invoke: host.X` + `with:` `bind:` `on_error:` | See §5. |

`when:` on an effect (not just on a transition) gates that single
effect — useful inside `on_enter:` for conditional host calls.

## 5. Host calls (`invoke:`)

```yaml
- invoke: host.oracle.ask_with_mcp
  with:
    prompt: prompts/proposing_executing.md
    schema: schemas/proposing_artifact.json
    working_dir: "{{ world.workdir }}"
    args:
      ticket_id:    "{{ world.ticket_id }}"
      ticket_title: "{{ world.ticket_title }}"
  bind:
    artifact: submitted        # copies Result.Data.submitted into world.artifact
  on_error: idle               # transition target if the handler errors
```

Rules:

- Every `invoke: host.X` requires `host.X` (or one of its prefix
  ancestors) in the top-level `hosts:` allow-list. The loader rejects
  the manifest otherwise. `iface.<name>.<op>` invocations are wired up
  implicitly via `host_interfaces.<name>.default`.
- `with:` arguments are templated. Map/slice values render as compact
  JSON (sorted keys for maps) when spliced into a string.
- `bind:` copies fields out of `Result.Data` into world keys. The right-hand
  side may be a bare key name (`bind: artifact: submitted`), a dotted
  path into the result (`party_member_1: "submitted.names[0]"`), or a
  templated expression.
- `on_error:` redirects to the named state when the handler returns an
  error. The orchestrator sets `$host_error.{code,hint}` for the target
  state's first guard. **Beware the silent-bounce anti-pattern** — see §10.
- `background: true` runs the call asynchronously; pair with
  `on_complete:` (an effect list) for the completion turn. Result lands
  in `world.last_job_result` only inside `on_complete:`.

The built-in handlers (full reference in `docs/hosts.md`):

| Handler | Use for |
|---|---|
| `host.run` | Shell out (argv mode preferred when args come from world). Returns `{stdout, exit_code, ok, stdout_json}`. |
| `host.oracle.ask` | One-shot Claude call with a Markdown prompt template, `{{ args.X }}` placeholders. Returns `{stdout, exit_code, ok}`. |
| `host.oracle.ask_with_mcp` | Same plus MCP servers + typed-JSON validator. The schema-checked payload comes back as `Result.Data.submitted`. The canonical pattern for "Claude produces a structured artifact." |
| `host.oracle.talk` | Conversational, optionally chat-aware via `chat_id`. |
| `host.transport.post` | Post a message to a registered transport (tui / jira / bitbucket). |
| `host.inbox.add` | Mirror an artifact into the operator's local inbox. |
| `host.chat.*` | Persistent multi-turn chat threads scoped by `(app, room, scope_key)`. |

## 6. Views (the typed-element form)

**Always `view: extends: "base"`. Never a `view: |` string** unless
you're touching legacy code you can't migrate yet — typed elements
isolate render failures per-element and keep the chrome alive.

Base templates live at `views/base.pongo`; they define five blocks
(`status`, `heading`, `body`, `choices`, `footer`) that rooms override.

Element kinds — pick the right one, never reach for ANSI or backticks:

| Kind | Use for |
|---|---|
| `prose:` | One paragraph of narration. Reflows. |
| `heading:` | Section break. No trailing colon. Never bulleted. |
| `list:` | Bulleted actions or enumerations. Optional `hint:` column. |
| `kv:` | Short key/value status. Key column auto-aligns. |
| `code:` | Layout-preserved content (ASCII tables, `{% include %}`, `{% for %}` loops over a world array). |
| `template:` | Raw pongo — escape hatch for legacy / unported shapes. |

Every element can carry a `when:` guard; the renderer drops elements
whose guard is false. Use this to fan out per-element conditional
rendering instead of `{% if %}` inside a `view: |` string.

### Author checklist before shipping a new room

- [ ] `view: extends: "base"`, not `view: |`.
- [ ] Each paragraph is its own `prose:`; section breaks are `heading:`.
- [ ] Status pairs are in a `kv:`, never hand-aligned.
- [ ] Actions are a `list:` with `hint:` for cost/consequence.
- [ ] Empty / pending values use the lowercase parenthetical placeholders
      (`(pending)`, `(none)`, `(not yet chosen)`, `(empty — type X to search)`).
- [ ] `look` is the last action and `target: .`.
- [ ] The view renders to ≥ 1 visible line against an empty world `{}`.
      Action menu / reply prompt is unconditional.
- [ ] No `{% for %}` over a world key that might be absent / nil; use a
      typed `list:` or guard with `{% if %}`.
- [ ] The intent name IS the label — no backticks, no paraphrase.
- [ ] `look` needs no hint. `quit` always reads `"abandon the pipeline"`
      or similar.

### Action-menu availability — two standard shapes

**Affordability** (action always offered, hint shows cost):

```yaml
- label: "pay"
  hint:  "buy them off (${{ world.threat_level * 50 }})"
  when:  "world.party_money >= world.threat_level * 50"
- label: "pay"
  hint:  "not enough money (${{ world.threat_level * 50 }} needed)"
  when:  "world.party_money < world.threat_level * 50"
```

**Prerequisite** (action greyed-out until reachable):

```yaml
- label: "start the journey"
  when:  "available('start_journey')"
- label: "✗ start_journey — {{ blocked_reason('start_journey') }}"
  when:  "!available('start_journey')"
```

The `available(name)` / `blocked(name)` / `blocked_reason(name)` /
`intent_status(name)` helpers read the computed menu derived from the
state's `on:` bindings + each transition's first arm's guard +
`guard_hint:`.

### Two voices, never mixed inside a room

- **In-character** — Oregon Trail, Robbery. Full sentences, quoted
  dialog gets its own `prose:`. `say:` follows suit.
- **Operator-facing** — dev-story, bugfix, implementation, kitsoki-dev.
  Terse, declarative. "Bug-fix pipeline parked. Waiting for `start`."
  not "The bug glares at you menacingly."

### Placeholder vocabulary

Always lowercase, always in parentheses. Splice via `|default:` (pongo
filter) on the world reference:

| Meaning | Render as |
|---|---|
| Unset configurable value | `(not yet chosen)` |
| Empty list | `` (empty — type `tickets` to search) `` |
| Awaiting host result | `(pending)` or `(pending — <what's running>)` |
| Not applicable | `(n/a)` |
| Nothing / null / absent | `(none)` |

## 7. Imports, exits, and composition

Use `imports:` (not `include:`) when embedding another *app* as an
aliased sub-story. State paths, intent names, and world keys get the
alias prefix at load time; nothing crosses the boundary unless declared.

```yaml
imports:
  bf:
    source: ./bugfix             # path | @kitsoki/<name> | absolute
    entry: idle
    hosts: declared              # strict allow-list mode; default "inherit"
    world_in:
      ticket_id:    "{{ world.picked_ticket }}"
      base_branch:  "main"
    exits:
      done:
        to: pr_open
        set:                     # evaluated in CHILD scope
          pr_url: "{{ world.pr_url }}"
      abandoned:
        to: ticket_search
    host_bindings:
      ticket:   host.jira
      vcs:      host.git
    intents:
      export: [look]             # parent → child
      import: [start, accept, refine, restart_from, quit]
    overrides:
      states:
        idle: { ... }            # full state replacement (not deep-merge)
      intents:
        accept: { ... }
      prompts:
        "prompts/judge_proposing.md": "prompts/judge_proposing_jira.md"
```

Child stories declare named return points with `exits:` and target them
with `target: "@exit:<name>"`. `requires:` keys are statically checked:
every transition into `@exit:<name>` must set every required key in its
effects, or the loader rejects the story.

`host_interfaces:` declares named capability surfaces the child invokes
as `iface.<name>.<op>`; the parent's `host_bindings.<name>: <handler>`
swaps in the concrete dispatcher. The host registry's prefix-fallback
means one handler at `host.git` satisfies `iface.vcs.commit`,
`iface.vcs.push`, etc., unless you register per-op handlers.

Every importable story needs a `README.md` documenting entry state,
exits + `requires:`, `world_in:` contract, intent export/import surface,
`host_interfaces:` contract, and host requirements. `stories/robbery/README.md`
and `stories/bugfix/README.md` are the templates.

## 8. Phase templates (compressing repeated rooms)

When a story repeats the same shape (execute → post → await reply →
retry on failure) over many phases, declare it once:

```yaml
phase_templates:
  reviewed_phase:
    parameters:
      id:         { type: string,  required: true }
      title:      { type: string,  required: true }
      checkpoint: { type: boolean, default: false }
    states:
      "{id}_executing": { ... }
      "{id}_awaiting_reply": { ... }
      "{id}_error": { ... }

phases:
  template: reviewed_phase
  graph:
    phase_a:
      title: "Phase A"
      next: { continue: phase_b }
    phase_b:
      title: "Phase B"
      checkpoint: true
      next: { continue: phase_c, on_failure: phase_a }
      cycle_budgets:
        on_failure: 2     # synthesises increment+guard+default → phase_b_error
```

`{name}` substitutes inside state keys; `{{ tpl.X }}` inside bodies;
`{{ phase.next.<arc> }}` inside `target:` resolves to
`<next-phase>_executing`. `cycle_budgets:` synthesises a counter
+ guard + fall-through-to-error trio per declared arc — use it instead
of hand-rolling retry caps.

`checkpoint_intents:` (a top-level map) is merged into every
`*_awaiting_reply` state — that's where you declare `continue`,
`refine`, `restart_from`, `quit`, etc. Slot schemas force context:
`refine` requires `feedback`, `restart_from` requires an enum `stage`.

## 9. Flow fixtures (Mode-2 deterministic tests)

Every non-trivial room deserves at least one flow fixture under
`flows/`. They run intent-only (no LLM, no harness) — fast, hermetic,
checkable in CI.

```yaml
test_kind: flow
app: ../app.yaml
initial_state: proposing            # dotted-path also works (bf.proposing)
initial_world:
  ticket_id:       "TKT-1"
  ticket_title:    "Fix the thing"
  artifact:        { summary_title: "Patch X", confidence: 0.85 }
  judge_mode:      "human"
turns:
  - intent:
      name: accept
    expect_state: implementing
    expect_world:
      cycle: 0
  - intent:
      name: refine
      slots: { feedback: "miss IPv6" }
    expect_state: proposing
    expect_world:
      refine_feedback: "miss IPv6"
      cycle: 1
```

Run:

```sh
kitsoki test flows stories/<name>/app.yaml
kitsoki test flows stories/<name>/app.yaml --flows flows/single_case.yaml --v
```

Flow fixtures double as **warp bases** — `kitsoki run ... --warp
flows/scenario.yaml` boots straight into `initial_state` with
`initial_world`. Check live scenarios in next to `app.yaml` under
`scenarios/`.

## 10. Pitfalls (the load-time and run-time checklist)

The loader rejects these at parse time — fix them before you bother
running:

- **`invoke: host.X` for an undeclared host.** Add to top-level `hosts:`.
- **`relevant_world: [foo]` for an undeclared world key.** Declare it
  with a type and default.
- **Transition `target:` to a non-existent state.** Either define the
  state or use `{{ world.dynamic }}` if you really mean dynamic.
- **`@exit:X` referenced by the child but not mapped by the parent's
  `imports.<alias>.exits.X.to:`.**
- **`@exit:X` with `requires:` keys the transition's effects don't set.**
- **Alias collision** between an `imports.<alias>` name and an existing
  state in the same scope.
- **State name collision across includes.** Rename one.
- **`overrides.states.<X>` / `.intents.<X>` / `.prompts.<X>` where `<X>`
  isn't declared in the child.**

The renderer / runtime traps — invisible until a user hits them:

- **No `default: true` on the last transition.** Benign cases hit
  `GUARD_FAILED` and the user sees a hint instead of a clean fallthrough.
  Always provide one (even if it's `target: . effects: [say: "Can't do
  that here."]`).
- **`view: |` string with a single bad `{{ … }}` →** the orchestrator's
  render-after-bind silently swallows the error and ships zero bytes —
  the user is dropped into a blank screen. **`view_bytes: 0` in the
  trace is a P0 bug.** Use `view: extends: "base"` so the chrome
  carries the floor.
- **Action menu conditional on world state.** Even if the body can't
  render, the user MUST still see the menu. Put it in `choices:` or as
  a non-guarded `prose:` line at the bottom of `body:`.
- **`{% for %}` over a possibly-absent world key.** Guard with `{% if %}`
  or use a typed `list:` with `from:`.
- **`on_error: idle` everywhere.** This is "silent fail" — the user gets
  bounced with no diagnostic. Prefer making the handler idempotent
  (idle's auto-create with the `bf_autostart_attempted` flag is the
  template). When you do use `on_error:`, ensure the destination view
  surfaces `world.last_error` somewhere.
- **Happy-path test that only checks `next_state`.** Rooms can advance
  while running a no-op `on_enter:`. Assert the side effects: `git show
  --name-only HEAD` after a commit, `stat` after a workspace.create,
  the actual world key after a bind.
- **Background job referencing `world.last_job_result` outside
  `on_complete:`.** That key only exists inside the completion turn.
- **`emit_intent:` and `target:` on the same effect.** Mutually
  exclusive. The runtime depth-caps `emit_intent:` at 8.
- **World values typed as `object` reading dotted paths in views without
  a fallback.** A bound key from `bind: artifact: submitted` is present
  by the time the view renders (the orchestrator re-renders after bind);
  a *conditionally* invoked bind still needs `?? "(pending)"` because
  the field is absent on the not-taken branch.

## 11. The authoring loop

The order most authors settle into:

1. **Sketch the graph** in `app.yaml` + `rooms/`. Placeholder views are
   fine; typed `extends: "base"` from the start saves migration work.
2. **`kitsoki turn`** to probe one state-shape at a time. Stateless,
   JSON output, no DB:
   ```sh
   kitsoki turn stories/<name>/app.yaml \
     --state <state.path> \
     --intent <intent_name> \
     --world '@/tmp/world.json'
   ```
3. **Write a flow fixture** for the path you just probed; lock it with
   `kitsoki test flows`.
4. **`kitsoki viz stories/<name>/app.yaml`** to sanity-check the graph
   shape (or `kitsoki viz --mermaid`).
5. **`kitsoki render -o APP.md`** for review-friendly docs.
6. **`kitsoki run stories/<name>/app.yaml`** to play it for real.
   Hot-reload picks up edits as you go.

If a user reports a runtime misbehaviour (silent bounce, wrong target,
view blank, "going back to idle") — **stop authoring and switch to the
`kitsoki-debugging` skill.** It drives `kitsoki turn` against the
real on-disk state and surfaces the host-call errors the TUI's
`on_error:` arcs swallow.

## 12. Constraints when editing in this repo

- Never call `git`. The user owns commit/push.
- Stay inside the story directory the user is editing; don't drift to
  `testdata/` (engine fixture data — its own tests depend on it).
- Don't refactor across rooms unless asked. A one-file request is a
  one-file change.
- When the user references a label or phrase, edit the room that
  *produces* that view — not the first grep hit elsewhere in the tree.
- Hot reload watches `mtime` on the watched `app.yaml`; editor temp-file
  writes (vim default) may need `:set nobackup nowritebackup` for the
  reload to fire. Save in place.
