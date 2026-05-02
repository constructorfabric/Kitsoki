# `app.yaml` Schema Reference

The authoritative reference for a hally application definition. Generated
from the Go types in `internal/app/types.go` — if something here disagrees
with that file, the Go types win.

YAML parsing is **strict**: unknown fields cause a load error. All field
names below are the literal `yaml:"..."` tag names.

## Top level: `AppDef`

```yaml
app:        <AppMeta>                  # required
world:      { <name>: <VarDef>, ... }  # optional
intents:    { <name>: <Intent>, ... }  # optional (global intent library)
root:       <string> | <State>         # required — initial state name or inline state
states:     { <name>: <State>, ... }   # optional
off_path:   <OffPathDef>               # optional
hosts:      [ <string>, ... ]          # optional allow-list of host handler names
proposals:  { <name>: <ProposalKind> } # optional
include:    [ <glob>, ... ]            # optional — merge other YAMLs relative to this file
```

- `root` may be a string (name of a state in `states:`) or an inline `State`
  for a compound/parallel root.
- `include` globs are expanded relative to the directory of the main YAML.
  Duplicate state/intent names across includes error out; the `hosts` list
  is unioned.

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

| Field       | Purpose                                                           |
|-------------|-------------------------------------------------------------------|
| `set`         | Assign world variables (value may be expr, literal, or templated) |
| `increment`   | Integer delta on numeric world variables                          |
| `say`         | Append narrative line (Go template over world/slots)              |
| `invoke`      | Call a host handler; must appear in top-level `hosts:` list       |
| `with`        | Arguments for `invoke`                                            |
| `bind`        | `{ world_key: result_key }` — copy host result into world         |
| `on_error`    | Transition target if host invoke errors; sets `$host_error`       |
| `emit`        | Broadcast named event to parallel regions                         |
| `background`  | When `true`, dispatches `invoke` as a background job instead of running synchronously. Requires `invoke:` to be set (validated at load time). Binds the job ID into world — use `bind: {last_job_id: job_id}` to choose the world key, or the default `last_job_id` is used. |
| `on_complete` | Ordered `Effect` list fired in the originating state's context when the background job terminates (done, failed, or cancelled). Has access to `world.last_job_id`, `world.last_job_status`, and `world.last_job_result` (the host result data). Cannot itself contain `background: true` (validated at load time). |

**Background dispatch example:**

```yaml
on_enter:
  - invoke:     host.run_tests
    with:        { suite: "{{ world.selected_suite }}" }
    background:  true
    bind:        { last_job_id: job_id }
    on_complete:
      - set:     { test_result: "{{ world.last_job_result.exit_code }}" }
      - say:     "Tests finished with exit code {{ world.test_result }}."
```

**Same-turn race note:** a `background: true` effect followed by another effect
in the same `on_enter:` block executes in the same turn and sees `world.last_job_id`
(which is set immediately when the job is submitted), but does **NOT** see
`world.last_job_result` — the result is only available in the `on_complete:`
chain, which runs in a later synthetic turn once the job terminates.

Conventional order within a single effect: `set` → `increment` → `say` →
`invoke` → `emit`.

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
