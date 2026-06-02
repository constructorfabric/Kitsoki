# Story imports — composing apps across files and repos

The `imports:` block lets one app embed another as an aliased sub-story,
sharing nothing by default. The child runs against its own private
world; the parent projects values across the boundary explicitly via
`world_in:` / per-exit `set:` projections. State paths, intent names,
agents, and host_interfaces are all prefixed under the import alias at
load time.

This page is the authoring + design reference. The proposal that
seeded this work was deleted on merge (per the proposals-folder
lifecycle); the rationale-rich sections worth preserving have been
folded into this document.

## When to use `imports:` vs `include:`

| Use case | Mechanism |
|---|---|
| Split one app across multiple YAML files in the same repo | `include: [<glob>]` |
| Embed a reusable sub-story (your own or someone else's) | `imports:` |
| Cross-repo composition with a clean contract | `imports:` |
| Extend a shipped sub-story without forking it | `imports:` + `overrides:` |

`include:` merges files into a single flat AppDef — same namespace,
same world. `imports:` is encapsulated: every name from the child
lives under an alias prefix; the parent decides which world keys cross
the boundary in either direction.

## The shape

```yaml
imports:
  <alias>:                          # becomes the prefix for everything in the child
    source: ./relative/path         # path | @kitsoki/<name> | absolute path
    version: ">=0.3 <0.4"           # optional metadata (v1: not enforced)
    entry: <child-state>            # required: where the child starts when invoked

    # parent → child: evaluated in PARENT scope, written into <alias>__<key>
    world_in:
      <child-key>: "{{ <parent-expr> }}"

    # child → parent: per-exit projection, evaluated in CHILD scope, written
    # into PARENT world keys when the named exit fires.
    exits:
      <child-exit-name>:
        to: <parent-state>
        set:
          <parent-key>: "{{ <child-expr> }}"

    # host_interface (re)binding
    host_bindings:
      <child-iface>: <handler>      # rebinds an iface declared by the immediate child
      <other-alias>__<iface>: <h>   # rebinds an iface in a grandchild

    # intent re-export, both directions (proposal §6)
    intents:
      export: [<parent-intent>, ...]  # parent intents made available to the child
      import: [<child-intent>, ...]   # child intents lifted into the parent's table

    # patch-style replacement (proposal §10)
    overrides:
      states:
        <child-state>: {...}        # full state replacement (not deep-merge)
      intents:
        <child-intent>: {...}       # full intent definition replacement
      prompts:
        <child-rel-path>: <parent-rel-path>  # path substitution in Effect.With["prompt"]

    # strict allow-list (proposal §9)
    hosts: declared                 # "inherit" (default) | "declared"
```

## Source resolution

The `source:` field accepts three forms, resolved in this order:

1. **Relative path** (`./foo`, `../bar`) — resolves against the
   importer's `app.yaml` directory.
2. **Absolute path** (`/abs/path`) — used as-is; for tests / scratch.
3. **`@kitsoki/<name>`** — walks up from the importer's directory
   looking for either a `.kitsoki-root` marker file or a `go.mod`
   declaring module `kitsoki` (or `*/kitsoki`); resolves to
   `<repo-root>/stories/<name>/app.yaml`. Strict module name match —
   `kitsoki-tools` won't pose as the kitsoki repo.

## State paths after fold

Imports flatten into a compound state under `parent.States[<alias>]`:

```yaml
# In the parent
imports:
  bf:
    source: ./bugfix
    entry: idle

# After load, the parent's state tree contains:
#   bf (compound, Initial=idle, OnEnter=[world_in setters])
#     idle, applying, verifying, ...  (the child's states, prefixed iface refs,
#                                       intent names → <alias>__<name>, world keys
#                                       → <alias>__<key>)
```

State addressing:

| Target | What it means |
|---|---|
| `bf` | Enter the import (fires the wrapper's OnEnter, then drills into `entry`). The canonical way to invoke a child. |
| `bf.idle` | Reach into the child at a specific state. Discouraged — couples parent to child internals. The loader accepts it; the validator allows it. |
| `bf/idle` | Same as `bf.idle` — slashes normalise to dots at validation. Idiomatic in YAML when copying the proposal §8 spec. |
| `main` (from parent) | Parent's own state. |
| `../foo` (from inside the child) | Sibling at the same nesting depth — the rewriter produces these for bare-name sibling refs in the child. |
| `../../...` (from inside the child) | Allowed up to but not beyond the alias wrapper. Walking above the wrapper is a load error per proposal §8. |

### `emit_intent:` across the fold boundary

`emit_intent:` (and the runtime's synthetic-intent dispatcher) handles
import compounds transparently:

- The child's `emit_intent: "{{ world.foo.intent }}"` is left as-is
  by the rewriter (only `world.<key>` refs inside the template
  rewrite). The verdict-bearing world key has the alias prefix.
- At runtime the template renders to the BARE intent name (e.g.
  `"accept"`) — exactly what a live LLM-judge produces, because the
  judge prompt doesn't know about the alias chain.
- `internal/machine/machine.go::resolveEmittedIntentName` walks the
  active state's leaf → root path consulting each compiled state's
  `IntentAliases` map (populated by the rewriter — one entry per
  rename). The first hit returns the renamed name (e.g.
  `bf__accept`, or `core__bf__accept` after two folds). Misses fall
  through to the bare name so standalone stories with no imports
  behave unchanged.

Authoring implication: an LLM-judge inside an imported child can
emit verdicts using the child's own intent vocabulary (bare names);
no per-import prompt wrapping is needed.

## Worked example: the Oregon Trail three-layer chain

`stories/oregon-trail/` → imports `stories/frontier_event/` → imports
`stories/robbery/`. The chain demonstrates every section of the proposal:

```yaml
# stories/oregon-trail/app.yaml — top layer
imports:
  frontier:
    source: ../../stories/frontier_event
    entry: scouting
    hosts: declared                              # § 9 strict mode
    world_in:                                    # parent → child projection
      party_money:    "{{ world.money }}"
      threat_level:   "{{ world.miles_traveled < 500 ? 1 : ... }}"
    exits:                                       # § 7 child → parent
      resolved:
        to: robbery_aftermath
        set:
          money:      "{{ world.money - world.frontier__paid_amount }}"
          last_event: "bandit_resolved"
      member_lost:
        to: robbery_aftermath
        set:
          party_alive: "{{ world.party_alive - 1 }}"
          last_event:  "bandit_killed_member"
    host_bindings:                               # §11.2 multi-layer composition
      narrator:           host.run               # rebinds the immediate iface
      bandits__narrator:  host.run               # rebinds a GRANDCHILD iface via prefix
    intents:
      export: [look]                             # § 6 down (parent → child)
      import: [scout]                            # § 6 up (child → parent)
    overrides:
      states:
        scouting: {...}                          # §10 whole-state replacement
      intents:
        scout: {...}                             # §10 whole-intent replacement
      prompts:
        "prompts/scout_brief.md": "prompts/scout_brief_trail.md"  # §10 prompt swap
```

Run all 28 oregon-trail flows to see the chain in action:

```bash
kitsoki test flows stories/oregon-trail/app.yaml
```

Or run a single layered demo:

```bash
kitsoki test flows stories/oregon-trail/app.yaml --flows stories/oregon-trail/flows/robbery_paid.yaml --v
```

## `host_interfaces:` — capability binding (proposal §11)

Declare a named capability surface in the child:

```yaml
host_interfaces:
  narrator:
    operations:
      announce: { input: { message: string }, output: { ok: bool } }
      close:    { input: { summary: string }, output: { ok: bool } }
    default: host.run
```

Invoke via `iface.<name>.<op>` inside the child's states:

```yaml
on_enter:
  - invoke: iface.narrator.announce
    with: { cmd: "scene-set" }
```

At top-level Load the rewriter produces `<binding>.<op>` — e.g.
`host.run.announce`. The runtime host registry's prefix-fallback
(`Get("host.run.announce")` falls back to `Get("host.run")` if no
per-op handler is registered) lets a single-handler binding satisfy a
multi-op interface, or you can register one handler per op for true
multi-op dispatch.

Bindings compose across layers — see the `bandits__narrator` example
above. The grandparent's host_bindings is what the leaf's invoke
ultimately resolves to.

## `exits:` — the child-side contract

Children declare their named return points:

```yaml
exits:
  completed:
    requires: [pr_url]              # static check: every @exit:completed
                                    # transition must set pr_url in its effects
  abandoned:
    description: "Bailed without shipping."
```

Transitions in the child target `@exit:<name>`:

```yaml
on:
  open_pr:
    - target: "@exit:completed"
      effects:
        - set: { pr_url: "{{ slots.url }}" }
```

The loader rewrites every `@exit:X` reference based on:
- **Standalone load** (no parent import): synthesised `__exit__<name>`
  terminal state. The child is runnable on its own for tests and
  smoke runs.
- **Imported load**: parent's `imports.<alias>.exits.<name>.to` is
  substituted; the parent's per-exit `set:` is appended to the
  transition's effects.

`requires:` is enforced at load time — a transition into `@exit:X`
must set every key in `X.requires` somewhere in its effects (or the
key must have a non-zero schema default).

## Operator tooling: `/warp` and `--warp`

For smoke testing or debugging an imported flow without playing the
whole game, the TUI's generic `/warp` slash command teleports the
session to any state with optional world overrides:

Inline form:

```
/warp leg_c_awaiting_reply world.money=400 world.current_landmark="Chimney Rock"
```

File form (load a checked-in "warp basis"):

```
/warp file:scenarios/chimney_robbery.yaml
```

Boot-time form (skip the intro entirely):

```bash
kitsoki run stories/oregon-trail/app.yaml --warp scenarios/chimney_robbery.yaml
```

A warp basis is a small YAML:

```yaml
name:        "Optional human label"
description: "Optional one-liner"
state:       leg_c_awaiting_reply
world:
  money: 400
  party_alive: 5
  current_landmark: "Chimney Rock"
```

The flow-fixture-style `initial_state` + `initial_world` keys are also
accepted, so any flow fixture doubles as a warp basis verbatim. The
path resolves relative to the loaded app's directory so authors can
check scenarios in next to `app.yaml`.

See `stories/oregon-trail/scenarios/` for live examples.

## Validation surface

The loader rejects every common authoring error with a clear message
(see `internal/app/imports_negative_test.go` for the full catalogue):

- Alias collision with an existing parent state.
- `@exit:<name>` referenced by the child but not mapped by the parent.
- `overrides.states.<X>` / `.intents.<X>` / `.prompts.<X>` where `<X>`
  isn't declared in the child.
- `intents.export` of a parent intent that doesn't exist.
- `intents.import` of a child intent that isn't listed in the child's
  `exports.intents`.
- `host_bindings.<name>` where `<name>` doesn't match any iface
  declared by the immediate child or accessible by alias-prefix.
- `hosts: declared` mode with a child host the parent doesn't list.
- `@exit:<name>` with `requires:` keys that the transition's effects
  don't set.
- Cross-boundary parent target (a `../...` walk that escapes the
  child's wrapper).
- Parent transition reaches into a child past its declared entry
  (`target: <alias>.<not-entry>` is rejected; use `target: <alias>`
  or expose a new exit from the child).
- Import cycles (any number of layers; depth-first canonical-path
  stack).

## Limitations and deferred items

What's NOT in v1, with rationale:

- **Structural validation of `host_interface` operation input/output
  shapes against the bound handler's schema** — `operations:` shapes
  parse and propagate but aren't cross-checked against the registered
  handler. Needs a handler-schema registration mechanism that doesn't
  exist today.
- **Trace events carrying an explicit `import_chain` field** — the
  state path itself (`frontier.bandits.encounter`) carries the alias
  chain via dot-segmentation, and `AppDef.ImportWrappers` records
  alias → entry-state for every import; both are enough for current
  debugging. A typed `import_chain: [oregon, frontier, bandits]`
  field on trace events is cleaner for log filtering but invasive to
  thread through every event call site.
- **Cross-layer host sandboxing** — hosts remain globally callable;
  the `hosts: declared` mode is the v1 surface for the audit-conscious
  case. Real per-import host sandboxing (separate registries per
  import alias) needs a runtime registry refactor.
- **`version:` enforcement** — `ImportDef.Version` is parsed and
  stored for traceability; v1 has no registry / lockfile to enforce
  against.

## File layout

```
stories/
  <story-name>/
    app.yaml                  — manifest (the loadable surface)
    prompts/                  — narrator / agent prompt files
    flows/                    — Mode 2 deterministic test fixtures
    scenarios/                — warp bases (operator smoke tests)
    README.md                 — contract documentation (mandatory for shipped stories)
```

The mandatory README documents: entry state, exits with semantics,
`world_in` / `world_out` contracts, intent export/import surface,
`host_interfaces` contract, host requirements. Examples in
`stories/robbery/README.md` and `stories/frontier_event/README.md`.

## See also

- [`hosts.md`](hosts.md) — host registry behavior, including the
  prefix-fallback used by iface dispatch.
- [`authoring.md`](authoring.md) — general `app.yaml` authoring.
- `stories/robbery/`, `stories/frontier_event/`, `stories/oregon-trail/`
  — live three-layer demo.
