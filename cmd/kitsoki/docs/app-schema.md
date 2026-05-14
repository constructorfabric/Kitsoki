# `app.yaml` Schema Reference

The authoritative reference for a kitsoki application definition. Generated
from the Go types in `internal/app/types.go` — if something here disagrees
with that file, the Go types win.

YAML parsing is **strict**: unknown fields cause a load error. All field
names below are the literal `yaml:"..."` tag names.

## Top level: `AppDef`

```yaml
app:             <AppMeta>                       # required
world:           { <name>: <VarDef>, ... }       # optional
intents:         { <name>: <Intent>, ... }       # optional (global intent library)
root:            <string> | <State>              # required — initial state name or inline state
states:          { <name>: <State>, ... }        # optional
off_path:        <OffPathDef>                    # optional
hosts:           [ <string>, ... ]               # optional allow-list of host handler names
proposals:       { <name>: <ProposalKind> }      # optional
include:         [ <glob>, ... ]                 # optional — merge other YAMLs relative to this file
imports:         { <alias>: <ImportDef>, ... }   # optional — aliased sub-story composition
exits:           { <name>: <ExitDef>, ... }      # optional — child-side exit contract (only meaningful when this app is imported)
exports:         <ExportsBlock>                  # optional — what an imported child surfaces (intents:)
host_interfaces: { <name>: <HostInterfaceDef> }  # optional — named capability surfaces
```

- `root` may be a string (name of a state in `states:`) or an inline `State`
  for a compound/parallel root.
- `include` globs are expanded relative to the directory of the main YAML.
  Duplicate state/intent names across includes error out; the `hosts` list
  is unioned.
- `imports:`, `exits:`, `exports:`, `host_interfaces:` together implement
  the story-imports surface — aliased composition with private worlds,
  projected world_in/world_out, named exits, intent re-export, and
  rebindable host_interfaces. Full reference and worked examples in
  [`docs/imports.md`](../../docs/imports.md).

## `AppMeta`

```yaml
app:
  id:       <string>           # required — must be non-empty
  version:  <string>           # optional, recommended (semver)
  title:    <string>
  author:   <string>
  license:  <string>
```

## `VarDef` (world variable)

```yaml
world:
  counter:       { type: int,    default: 0 }
  name:          { type: string, default: "" }
  status:        { type: enum,   values: [idle, running, done], default: idle }
  wearing_cloak: { type: bool,   default: true }
```

Fields:

| Field     | Required | Notes                                               |
|-----------|----------|-----------------------------------------------------|
| `type`    | yes      | `string` / `int` / `bool` / `enum` / other          |
| `default` | no       | Zero value for the type when omitted                |
| `values`  | no       | Enum values; only meaningful when `type: enum`      |

## `State`

```yaml
states:
  foyer:
    type:            atomic | compound | parallel    # default: atomic
    mode:            <string>                        # e.g. "conversational" → Oracle Room
    description:     <string>                        # shown in location indicator
    view:            |-                              # Go template over {{ world.* }}
      You are in the foyer. Counter = {{ world.counter }}.
    terminal:        false
    initial:         <child-name>                    # required for compound; supports {{...}}
    states:          { ... nested States ... }       # compound/parallel only
    on:                                              # intent → transition list
      go:
        - <Transition>
        - <Transition>
    on_enter:        [ <Effect>, ... ]               # fires on entry
    intents:         { ... local Intent overrides ... }
    menu:            [ <intent>, ... ]               # override default menu ordering
    relevant_world:  [ <world-key>, ... ]            # pinned in TUI location indicator
    relevant_slots:  [ <slot-name>, ... ]
    timeout:         <TimeoutDef>                    # optional auto-transition
```

Rules:

- State names are case-sensitive. Nested paths use dot notation internally
  (`bar.dark`); in YAML you may write `../../foyer` (slash form) for relative
  references — the loader resolves them.
- `on: { <intent>: [...] }` tries transitions in order; the first whose
  `when:` evaluates true (or which has `default: true`) wins.
- `relevant_world` entries must exist in the top-level `world:` schema.
- Intent names in `on:` must be declared globally (`intents:`), locally
  (`states.X.intents:`), or be the wildcard `"*"`.

## `Transition`

```yaml
on:
  go:
    - target:      bar                 # required — dest state path, "." = self
      when:        "slots.direction == 'south'"
      effects:     [ <Effect>, ... ]
      guard_hint:  "Head outside first."   # shown when guard fails
      view:        "You slip past the usher..." # overrides target's view for this turn
      emit:        [ lights_dimmed ]     # events broadcast to parallel regions
      push_history: true                 # default true; false for stackless transitions
    - default: true                      # catch-all
      target: foyer
      effects:
        - say: "You can't go that way."
```

- `target` must resolve to an existing state unless it contains a template
  expression (`{{ world.dynamic_room }}`), in which case validation is
  deferred to runtime.
- `when:` is an expr-lang expression; variables: `world.*`, `slots.*`,
  `$host_error` (when inside an `on_error` target).
- `default: true` is the catch-all. Put it **last** in the list.

## `Effect`

```yaml
effects:
  - set:        { counter: "{{ world.counter + 1 }}", last_dir: "{{ slots.direction }}" }
  - increment:  { counter: 1 }
  - say:        "You head {{ slots.direction }}."
  - invoke:     host.run
    with:       { cmd: "git status", cwd: "{{ world.workspace_root }}" }
    bind:       { last_output: stdout, last_code: exit_code }
    on_error:   error_room
  - emit:       lights_dimmed
```

Fields (any subset):

| Field         | Purpose                                                              |
|---------------|----------------------------------------------------------------------|
| `set`         | Assign world variables (value may be expr, literal, or templated)    |
| `increment`   | Integer delta on numeric world variables                             |
| `say`         | Append narrative line (Go template over world/slots)                 |
| `invoke`      | Call a host handler; must appear in top-level `hosts:` list          |
| `with`        | Arguments for `invoke`                                               |
| `bind`        | `{ world_key: result_key }` — copy host result into world            |
| `on_error`    | Transition target if host invoke errors; sets `$host_error`          |
| `emit`        | Broadcast named event to parallel regions                            |
| `background`  | `true` → dispatch `invoke` as a background job (see §Background jobs) |
| `on_complete` | Effect list fired when the background job terminates (see §Background jobs) |

Conventional order within a single effect: `set` → `increment` → `say` →
`invoke` → `emit`.

## Background jobs

> Detailed reference: [docs/background-jobs/](../../../docs/background-jobs/README.md)

Background jobs let a state machine fire a long-running shell command or LLM
call without blocking the current turn. The job runs in a goroutine; when it
finishes a synthetic turn fires `on_complete:` effects in the originating
state's context and posts an inbox notification.

### Lifecycle

```
submit turn              on_complete turn (synthetic, later)
──────────────────────   ─────────────────────────────────────
on_enter: fires          job goroutine exits (done/failed/cancelled)
  background: true   →   orchestrator listener wakes
  bind: last_job_id      world.last_job_id   = <id>      (re-set)
  job starts async       world.last_job_status = "done"  (new)
                         world.last_job_result = <data>  (new)
                         on_complete: effects fire
                         inbox notification posted
```

### Effect fields for background dispatch

| Field         | Purpose                                                                     |
|---------------|-----------------------------------------------------------------------------|
| `background`  | `true` → dispatch the `invoke:` handler as a background job. Requires `invoke:` (validated at load time). |
| `bind`        | `{ world_key: job_id }` — captures the job ID synchronously into world. Omit to use the default key `last_job_id`. |
| `on_complete` | Ordered `Effect` list fired once the job terminates. May not itself contain `background: true` (validated at load time). |

### World variables injected on_complete

These are set by the orchestrator's synthetic turn and available inside
`on_complete:` effects (and the state's view template on the next render):

| Variable            | Type              | Value                                        |
|---------------------|-------------------|----------------------------------------------|
| `last_job_id`       | string            | Job ULID (same value bound at dispatch)      |
| `last_job_status`   | string            | `"done"` / `"failed"` / `"cancelled"`        |
| `last_job_result`   | map (any)         | `Result.Data` from the handler — e.g. `{stdout, exit_code, ok}` for `host.run` |

`last_job_result` is not declared in the app's `world:` schema; it is injected
dynamically and accessible only inside `on_complete:` templates. Do not
reference it in a regular state `view:` — it will be empty outside of the
synthetic turn.

### Same-turn race

A `background: true` effect followed by another effect in the same
`on_enter:` or `effects:` block executes in the **same turn** and sees
`world.last_job_id` (bound synchronously when the job is submitted), but does
**not** see `world.last_job_result` — the result is only available in the
`on_complete:` chain, which runs in a later synthetic turn once the job
terminates.

### Minimal runnable example

```yaml
# See also: testdata/apps/background_jobs/app.yaml
on_enter:
  - invoke:     host.run
    with:       { cmd: "sleep 1 && echo done" }
    background: true
    bind:       { last_job_id: job_id }
    on_complete:
      - set:    { result: "{{ world.last_job_result.stdout }}" }
      - say:    "Job complete. Output: {{ world.result }}"
```

A full three-state example (lobby → running → done) lives at
`testdata/apps/background_jobs/app.yaml`.

### Mid-flight clarifications

A background handler can pause mid-execution to ask the user a question
before resuming. The machinery is transparent to the room YAML author once
the clarifying sub-state is wired.

#### Handler side (Go)

```go
// Inside a host.Handler running as a background job:
rawJSON, err := host.RequestClarification(ctx, jobs.ClarificationSchema{
    Prompt: "Which branch should I use?",
    Fields: map[string]string{"branch": "string"},
})
// rawJSON is the JSON-encoded answer submitted by the user, e.g. `"main"`.
```

`host.RequestClarification` is a blocking call that:
1. Writes the schema to the DB and flips the job row to `awaiting_input`.
2. Signals the scheduler to fan out a `JobAwaitingInput` event.
3. Polls every 200 ms until the answer is stored, then returns the raw JSON.

#### Orchestrator side (automatic)

When the orchestrator's session listener receives `JobAwaitingInput` it calls
`handleJobAwaitingInput`, which fetches the schema and posts an
`action_required` notification. The notification's `TeleportJobID` and
`TeleportState` carry the job ID and origin state so the TUI can surface a
banner and teleport the user back.

#### YAML side (app author)

Add a `*_clarifying` sub-state to the originating room with an
`answer_clarification` intent:

```yaml
hosts:
  - host.jobs.answer_clarification   # built-in; no extra registration needed

intents:
  answer_clarification:
    title: "Answer clarification"
    slots:
      job_id: { type: string, required: true }
      answer: { type: string, required: true }

states:
  lobby_running:
    view: "Running…"
    on_enter:
      - invoke: host.my_long_task
        background: true
        bind: { last_job_id: job_id }
        on_complete:
          - say: "Done!"

  lobby_clarifying:
    view: |
      Job {{ world.last_job_id }} needs your input.
      {{ world.clarification_prompt }}
    on:
      answer_clarification:
        - target: lobby_running
          effects:
            - invoke: host.jobs.answer_clarification
              with:
                job_id: "{{ slots.job_id }}"
                answer: "{{ slots.answer }}"
```

#### Round-trip summary

1. Handler calls `host.RequestClarification(ctx, schema)` and blocks.
2. User sees an `action_required` banner in the inbox.
3. Selecting it teleports to `lobby_clarifying` (the `TeleportState`).
4. User submits `answer_clarification` with `{job_id, answer}`.
5. `host.jobs.answer_clarification` calls `AnswerClarification(jobID, value)`.
6. The handler's poll loop returns the answer; the job resumes.
7. On completion, `on_complete` effects fire normally.

**Important:** the `*_clarifying` state must be reachable from the origin
state or be a distinct state with the `answer_clarification` intent. The
TeleportState in the notification is set to the job's `OriginState` — the
state where the job was submitted — so make sure that state (or a teleport
alias) handles `answer_clarification`.

### Flow tests with background jobs

Flow fixtures can test the full background-job lifecycle deterministically
using `host_handlers:`, `advance_clock:`, and `expect_inbox:`.

#### How it works

When a fixture declares `host_handlers:` (or any turn uses `advance_clock:` or
`expect_inbox:`), the flow runner automatically switches to the
**orchestrator-backed path**:

- An in-memory SQLite store and fake clock are created.
- The scheduler and session listener are wired together.
- Each `host_handlers:` entry becomes a stub closure (no real I/O).
- `advance_clock: "2s"` moves the fake clock forward and then drains the
  scheduler + listener, so `on_complete` effects are applied before assertions
  run.

The legacy path (no `host_handlers`, no `advance_clock`) is unchanged.

#### Fixture fields

```yaml
# Declare stub handlers. Keys are the handler name declared in `hosts:`.
host_handlers:
  host.run:
    data: { stdout: "hello", exit: 0 }   # host.Result.Data returned on success
    delay: "1s"                           # optional: block for this virtual duration
    error: "something_went_wrong"         # optional: domain-level error (Result.Error)
    infra_error: "connection refused"     # optional: infrastructure error (Go error)
```

On a turn:

```yaml
turns:
  - intent: { name: start }
    advance_clock: "2s"        # move fake time forward; waits for scheduler+listener
    expect_state: running
    expect_world:              # assertions run AFTER advance_clock drains
      result: "hello"
    expect_inbox:
      unread: 2                # total unread count
      needs_attention: 0       # action_required severity count
      severities: ["info", "success"]  # sorted severity list for all unread items
```

#### `host_handlers` fields

| Field         | Purpose                                                                       |
|---------------|-------------------------------------------------------------------------------|
| `data`        | Map returned in `host.Result.Data` on a successful invocation.               |
| `error`       | Non-empty → `host.Result.Error` set (domain error; job terminates as failed). |
| `infra_error` | Non-empty → `(Result{}, error)` returned (infrastructure failure).           |
| `delay`       | Duration string (e.g. `"1s"`) — the stub blocks for this virtual time.        |

`delay` and `infra_error`/`error` are independent: you can combine delay with
an error to simulate a slow then failing handler.

#### `advance_clock` + notification counts

The stub's `delay:` field simulates a long-running handler. To make it
complete in the test, set `advance_clock:` to a duration ≥ the handler delay
on the same turn (or a subsequent turn). After advancing the clock:

- The scheduler drains (all job goroutines reach terminal state).
- The session listener drains (`on_complete` effects are applied).
- The inbox receives a notification with severity `info` (job submitted) and
  `success`/`error`/`warn` (job terminal state). Both are counted in
  `expect_inbox.unread`.

#### Minimal example

```yaml
test_kind: flow
app: ../app.yaml
initial_state: lobby
initial_world:
  result: ""
  last_job_id: ""

host_handlers:
  host.run:
    data: { stdout: "hello" }
    delay: "1s"

turns:
  - intent: { name: start }
    advance_clock: "2s"
    expect_world:
      result: "hello"
    expect_inbox:
      unread: 2
      severities: ["info", "success"]

expect_no_errors: true
```

A full working example lives at
`testdata/apps/background_jobs/flows/happy_path.yaml`.

## `Intent`

```yaml
intents:
  go:
    title:        "Go"
    description:  "Move in a compass direction."
    examples:     ["go south", "head north", "n"]
    priority:     100                     # higher → more prominent in menu
    hidden:       false                   # if true, usable but not listed
    slots:
      direction: <Slot>
```

## `Slot`

```yaml
slots:
  direction:
    type:         enum                    # required
    required:     true
    default:      ""
    values:       [north, south, east, west]
    description:  "Which direction to move."
    examples:     ["south", "n"]
    format_hint:  "One of n/s/e/w."
    prompt:       "Which direction?"
    validator:    "value != 'down'"       # expr — value is the slot value
```

## `OffPathDef`

Global escape hatch from any state back to a named one.

```yaml
off_path:
  trigger:  "help"            # intent name / pattern that activates off-path
  banner:   "(help mode)"
  return:   main              # state to re-enter after exiting off-path
```

## `TimeoutDef`

```yaml
states:
  waiting:
    timeout:
      after:  "30s"           # Go duration string: 500ms, 5m, 2h
      target: timeout_room
```

## `ProposalKind` (advanced — proposal pattern)

A three-step draft → review → execute pattern.

```yaml
proposals:
  git_commit:
    schema: { message: string, files: string }   # typed draft fields
    draft:   { prompt: "prompts/commit_draft.tmpl" }
    refine:  { prompt: "prompts/commit_refine.tmpl" }
    execute:
      invoke:      host.run
      with:        { cmd: "git commit -m {{ draft.message }}" }
      repeatable:  false
      on_success:  stay                 # "stay" | "back" | <state-name>
      background:  false
      on_complete: [ <Effect>, ... ]
    views:
      drafting:   "Proposed commit:\n{{ draft.message }}"
      reviewing:  "Accept?"
    policy:
      auto_accept_if:  "draft.files.length < 3"
      require_confirm: false
```

`ProposalStep`:

| Field    | Purpose                          |
|----------|----------------------------------|
| `prompt` | Path to prompt template file     |

`ProposalExecute`:

| Field         | Purpose                                                |
|---------------|--------------------------------------------------------|
| `invoke`      | Host handler name                                      |
| `with`        | Templated args (see Effect.with)                       |
| `repeatable`  | Allow rerun/modify_and_rerun after success             |
| `on_success`  | `stay` / `back` / named state                          |
| `background`  | Run as background job (`internal/jobs`)                |
| `on_complete` | Effects fired when background job finishes             |

`ProposalPolicy`:

| Field             | Purpose                                                          |
|-------------------|------------------------------------------------------------------|
| `auto_accept_if`  | Expr over `{$proposal, $world, $slots}`; skips review when true  |
| `require_confirm` | Always require explicit user confirmation before execute         |

## `ImportDef` (story-imports composition)

```yaml
imports:
  <alias>:
    source:   <string>                    # required: ./path | @kitsoki/<name> | /abs
    version:  <string>                    # optional metadata (v1: not enforced)
    entry:    <child-state>               # required: where the child starts when invoked
    world_in: { <child-key>: <expr> }     # parent → child projection (eval'd in parent scope)
    exits:                                # child-exit → parent-state mapping
      <child-exit>:
        to: <parent-state>
        set: { <parent-key>: <child-expr> }  # per-exit world_out projection
    hosts: inherit | declared             # default: inherit
    host_bindings: { <iface>: <handler> } # rebind a child or grandchild iface
    intents:
      export: [<parent-intent>, ...]      # parent → child intent re-export
      import: [<child-intent>, ...]       # child → parent intent re-export
    overrides:
      states:  { <child-state>: <State> }   # whole-state replacement
      intents: { <child-intent>: <Intent> } # whole-intent replacement
      prompts: { <child-rel-path>: <parent-rel-path> }  # prompt-file substitution
```

## `ExitDef`

```yaml
exits:
  <name>:
    description: <string>          # optional
    requires:    [<world-key>, ...] # optional, statically checked at load
```

Only meaningful when this app is imported by another. Standalone load
synthesises `__exit__<name>` terminal states for any `@exit:<name>`
target the app uses.

## `HostInterfaceDef`

```yaml
host_interfaces:
  <name>:
    description: <string>
    operations:
      <op>:
        input:  <shape>      # metadata; not validated against handler v1
        output: <shape>
    default: <handler>       # default binding when no importer overrides
```

Invoked from state effects via `invoke: iface.<name>.<op>`. At
top-level Load, every `iface.<name>.<op>` is rewritten to
`<binding>.<op>`; the runtime host registry's prefix-fallback maps
`<binding>.<op>` → `<binding>` when no per-op handler is registered.

Full reference, including the multi-layer composition surface, is in
[`docs/imports.md`](../../docs/imports.md).

## Validation (what the loader enforces)

The loader (`internal/app/loader.go`) performs these checks and collects all
errors with `errors.Join` so you see the complete problem set on first load:

- `app.id` must be non-empty.
- Strict YAML: unknown fields are errors.
- Every `target:` that is not a template and not `.` must resolve to an
  existing state (relative-path resolution happens first).
- Every `invoke:` value must appear in the top-level `hosts:` list.
- Every `relevant_world:` key must exist in `world:`.
- Every intent referenced in `on:` must be declared globally, locally, or be
  `"*"`.
- For `type: compound` states with a literal `initial:`, the named child
  must exist. (Template `initial:` values skip the check.)
- `include` globs cannot produce duplicate state/intent names (the `hosts`
  list is the only mergeable list).
- For each `imports.<alias>`:
  - the alias must not collide with an existing parent state name;
  - every `@exit:<name>` in the child must be mapped in
    `exits:`, unless the app is loaded standalone (where they
    materialise as `__exit__<name>` terminals);
  - `overrides.states.<X>` / `.intents.<X>` / `.prompts.<X>` keys must
    name existing child elements;
  - `intents.export` references must exist in the parent's `intents:`;
  - `intents.import` references must be listed in the child's
    `exports.intents`;
  - `host_bindings.<name>` must match either an iface the immediate
    child declares or one accessible by alias-prefix from a grandchild;
  - `hosts: declared` mode requires every child host to be in the
    parent's own `hosts:` list;
  - every transition into `@exit:<name>` must set every key in the
    child's `exits.<name>.requires`;
  - `..` relative targets inside the child cannot walk above the
    alias wrapper (cross-boundary parent targets are forbidden);
  - import cycles (any number of layers) are detected and rejected.

## What the loader does **not** check (runtime only)

- `when:` and `validator:` expression correctness — bad expr fails at
  runtime.
- World-variable type coercion — a `default: "0"` on `type: int` passes
  the loader.
- `target:` values containing `{{ ... }}` — resolved at runtime.
- Host handler availability — only the allow-list is checked; missing
  handlers error at invoke time.

## Minimal runnable app

```yaml
app:
  id: tiny
  version: 0.1.0
  title: "Tiny App"

world:
  counter: { type: int, default: 0 }

intents:
  increment:
    description: "Add one to the counter."
    examples: ["add one", "++", "bump"]
  show:
    description: "Show the counter."
    examples: ["show", "what's the count?"]

root: main

states:
  main:
    view: |
      counter = {{ world.counter }}
    on:
      increment:
        - target: main
          effects:
            - increment: { counter: 1 }
      show:
        - target: main
```

## Bigger examples in-tree

- `testdata/apps/cloak/app.yaml`     — classic IF game; guards, enums, default branches
- `testdata/apps/dev-story/app.yaml` — multi-room dev workflow; hosts, proposals, Oracle Room, background jobs
- `testdata/apps/proposal_smoke/app.yaml` — minimal proposal-pattern example
