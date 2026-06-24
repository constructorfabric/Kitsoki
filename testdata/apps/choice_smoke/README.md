# choice_smoke — canonical reference fixture for the choice widget

This app is the canonical reference for the
[choice widget](../../../docs/stories/choice-widget.md).
Every feature in the per-mode YAML shape, validation rules,
coexistence, and limitations of the widget has a dedicated
demo state ("spoke") here. Each spoke is also a regression test: every
spoke gets at least one flow fixture under `flows/` so
`./kitsoki test flows testdata/apps/choice_smoke/app.yaml` exercises
the dispatched-intent shape end-to-end.

The two jobs:

1. **Interactive feature demo.** `./kitsoki run testdata/apps/choice_smoke/app.yaml`
   drops you on the `intro` state and walks you through every feature
   in a curated order — single mode → multi mode → form mode — with
   each spoke ending in a commit intent that automatically advances
   to the next feature. The final spoke routes to a `complete`
   summary screen with a restart option.
2. **Regression test fixture.** `./kitsoki test flows testdata/apps/choice_smoke/app.yaml`
   drives each spoke via `intent: { name, slots }` literals — the
   widget is pure presentation, so flow coverage proves the slot
   shape the widget emits at commit is the slot shape the
   orchestrator expects. The `full_walkthrough.yaml` fixture chains
   every commit intent in canonical order to prove the chain is
   contiguous.

## Walkthrough mode (default)

The root state is `intro`. From there the user can:

- Press Enter on **Begin** to start the linear walkthrough at
  `single_basic`, or
- Pick **Jump straight to the feature menu** to land on the `menu`
  state — a power-user feature picker that can drop you on any spoke
  for re-reference.

Every spoke also has a `goto_menu` arc — pressing **Open the feature
menu** (or dispatching `goto_menu` directly) exits the chain into
`menu` from anywhere. From `menu` every spoke is one jump away
(`goto_<spoke>` items), and `goto_intro` / `goto_complete` route back
to either walkthrough endpoint.

### Canonical chain order

| #  | State                     | Commit intent           | → Successor              |
|----|---------------------------|-------------------------|--------------------------|
| 1  | `intro`                   | `begin`                 | `single_basic`           |
| 2  | `single_basic`            | `chose_color`           | `single_no_slots`        |
| 3  | `single_no_slots`         | `chose_bare`            | `single_per_item_when`   |
| 4  | `single_per_item_when`    | `chose_gated`           | `single_templated`       |
| 5  | `single_templated`        | `chose_templated`       | `single_templated_slots` |
| 6  | `single_templated_slots`  | `chose_templated_slot`  | `single_param_string`    |
| 7  | `single_param_string`     | `set_hero_name`         | `single_param_int`       |
| 8  | `single_param_int`        | `set_hero_age`          | `single_param_enum`      |
| 9  | `single_param_enum`       | `set_hero_class`        | `multi_basic`            |
| 10 | `multi_basic`             | `chose_traits`          | `multi_min_zero`         |
| 11 | `multi_min_zero`          | `chose_optional_extras` | `multi_no_max`           |
| 12 | `multi_no_max`            | `chose_all_perks`       | `multi_per_item_when`    |
| 13 | `multi_per_item_when`     | `chose_gated_traits`    | `multi_templated`        |
| 14 | `multi_templated`         | `chose_templated_traits`| `form_basic`             |
| 15 | `form_basic`              | `submit_basic_form`     | `form_bool`              |
| 16 | `form_bool`               | `submit_bool_form`      | `form_float`             |
| 17 | `form_float`              | `submit_float_form`     | `form_enum`              |
| 18 | `form_enum`               | `submit_enum_form`      | `form_placeholder`       |
| 19 | `form_placeholder`        | `submit_placeholder_form`| `form_required`         |
| 20 | `form_required`           | `submit_required_form`  | `form_bounds`            |
| 21 | `form_bounds`             | `submit_bounds_form`    | `form_per_field_when`    |
| 22 | `form_per_field_when`     | `submit_gated_form`     | `form_readonly_expr`     |
| 23 | `form_readonly_expr`      | `submit_readonly_form`  | `complete`               |
| 24 | `complete`                | `restart`               | `intro` (loop)           |
| 24 | `complete`                | `goto_menu`             | `menu`                   |

Several arcs along the chain also seed world keys for the spoke they
route into — `single_no_slots`'s commit flips `per_item_gate` to
`true` so the next spoke's gated row is visible; `multi_no_max`'s
commit bumps `world.day` so the next spoke's "rash" gated row clears
its `when:`; etc. The seeding is performed by `effects:` on the arc
that ENTERS the spoke needing the state, so the demo state has the
world it needs the moment the user lands on it. Flow tests that drive
a single spoke in isolation use `initial_world:` to set up the same
preconditions.

### Escape hatch — the `menu` state

```
                ┌─────────────────────────────────────┐
                │              menu (hub)             │
                │  feature picker over 22 spokes      │
                │  goto_<spoke> jumps direct          │
                └────────────┬────────────────────────┘
                             │  goto_<spoke>
                             ▼  (or goto_intro / goto_complete)
   ┌───────────────────────────────────────────────────────────┐
   │                  any spoke (incl. intro / complete)        │
   └────────────────────────┬──────────────────────────────────┘
                            │  goto_menu
                            ▼
                          menu
```

The `menu` items list every spoke (plus `goto_intro` and
`goto_complete`) so a reader can re-enter the chain at any point.
Picking, say, `goto_multi_basic` from the menu lands you in
`multi_basic`; committing `chose_traits` from there still routes to
`multi_min_zero` (the linear successor) — i.e. jumping in via the
menu doesn't break the chain. To start over from the top, pick
**← Back to the intro** in `menu`.

## How to run

```sh
cd <repo-root>
go build -o ./kitsoki ./cmd/kitsoki

# Flow tests (all 32 fixtures, including the full-walkthrough chain).
./kitsoki test flows testdata/apps/choice_smoke/app.yaml

# Interactive walkthrough — starts at intro, walks linearly.
./kitsoki run testdata/apps/choice_smoke/app.yaml

# To jump straight to the feature picker, pick "Jump straight to the
# feature menu" on the intro screen, or dispatch `goto_menu` from
# any spoke. From the menu, pick any goto_<spoke> item.

# Static-render dump (covers every spoke's view at once).
./kitsoki render testdata/apps/choice_smoke/app.yaml
```

## Spokes

Each subsection below lists what the spoke demos, the relevant YAML
shape, and which flow fixture(s) cover it. The "Successor" row names
the next spoke the commit intent routes to in the linear chain.

### Single mode

#### `single_basic`
Three literal items dispatching `chose_color` with pre-bound
`slots: { color: ... }`. The baseline single-mode picker. Proposal §3.1.

- YAML: `items: [{ label, intent, slots }, ...]`
- Flow: `flows/single_basic.yaml` — fires `chose_color` with `color: red`, asserts `picked_color == red`.
- Successor: `single_no_slots`.

#### `single_no_slots`
Items with neither `slots:` nor `param:` — the intent name alone
encodes the choice. Mirrors `stories/robbery/app.yaml`'s
pay/fight/flee.

- YAML: `items: [{ label, intent }, ...]`
- Flow: `flows/single_no_slots.yaml` — dispatches `chose_bare` with `{}` slots.
- Successor: `single_per_item_when` (commit arc also seeds `per_item_gate: true`).

#### `single_per_item_when`
Items filtered by per-item `when:`. Two rows on the same intent with
mutually exclusive guards: one shows when the gate is open, the
"blocked" row replaces it when the gate is closed. Mirrors robbery's
affordability split. Proposal §3.1, §4.4.

- YAML: `items[].when: "world.flag"` / `"!world.flag"`
- Flows: `flows/single_per_item_when.yaml` (gate true, "opened" row),
  `flows/single_per_item_when_blocked.yaml` (gate false, "blocked" row).
- Successor: `single_templated` (commit arc seeds `day: 2, money: 250`).

#### `single_templated`
Item `label:` and `hint:` strings with `{{ world.x }}` substitutions
that render through pongo at render time.

- YAML: `items[].label: "Dawn — day {{ world.day }}"`
- Flow: `flows/single_templated.yaml` — seeds `world.day`, dispatches `chose_templated`.
- Successor: `single_templated_slots` (commit arc bumps `money` to 300).

#### `single_templated_slots`
A `slots:` entry whose VALUE is templated. The substituted string is
what gets dispatched. Loader skips literal enum-membership checks for
templated values (§4.4(2)).

- YAML: `slots: { money_amount: "{{ world.money }}" }`
- Flow: `flows/single_templated_slots.yaml` — dispatches the substituted "300".
- Successor: `single_param_string`.

#### `single_param_string`
Item with a `param:` that captures one free-form string slot at pick
time. Proposal §3.1.

- YAML: `param: { slot, type: string, placeholder, required: true }`
- Flow: `flows/single_param_string.yaml` — captures `name: "Aria"`.
- Successor: `single_param_int`.

#### `single_param_int`
Same as above but `type: int`; the buffer is coerced to an int at
commit.

- YAML: `param: { slot, type: int, ... }`
- Flow: `flows/single_param_int.yaml` — captures `age: 27`.
- Successor: `single_param_enum`.

#### `single_param_enum`
`param.type: enum` with `values:` — Space cycles through the values
inline.

- YAML: `param: { slot, type: enum, values: [...] }`
- Flows: `flows/single_param_enum.yaml` (happy), `flows/single_param_enum_invalid.yaml` (negative — orchestrator rejects out-of-list value with `INVALID_SLOT_VALUE`).
- Successor: `multi_basic`.

### Multi mode

#### `multi_basic`
The baseline multi-mode picker — `min: 1`, `max: 3`, hint column.
Proposal §3.2.

- YAML: `mode: multi`, `intent`, `slot`, `min`, `max`, `items: [{ value, label, hint }]`
- Flow: `flows/multi_basic.yaml` — dispatches `traits: [brave, clever]`.
- Successor: `multi_min_zero`.

#### `multi_min_zero`
`min: 0` — empty list is a valid submit. Proposal §3.2.

- YAML: `min: 0`
- Flow: `flows/multi_min_zero.yaml` — dispatches `extras: []`.
- Successor: `multi_no_max`.

#### `multi_no_max`
`max:` unset → defaults to len(visible items). Paired with `min: 1` so
"at least one" is enforced but there's no upper bound.

- YAML: omit `max:`
- Flow: `flows/multi_no_max.yaml` — dispatches all four perks.
- Successor: `multi_per_item_when` (commit arc seeds `day: 5`).

#### `multi_per_item_when`
Per-item `when:` filters one row by `world.day > 3` (the "rash after
day 3" pattern from the proposal §3.2 example). Proposal §4.4.

- YAML: `items[].when: "world.day > 3"`
- Flow: `flows/multi_per_item_when.yaml` — seeds `day: 5`, dispatches with the gated value included.
- Successor: `multi_templated` (commit arc seeds `day: 4, money: 80`).

#### `multi_templated`
Labels and hints with `{{ world.x }}` substitutions. The `value:` itself
must remain literal (§4.4(5) — templated values are rejected at load
time in multi mode).

- YAML: `items[].label: "Brave"`, `items[].hint: "free on day {{ world.day }}"`
- Flow: `flows/multi_templated.yaml` — seeds day + money, dispatches literal trait values.
- Successor: `form_basic`.

### Form mode

#### `form_basic`
The baseline form: a readonly `expr:`-backed field plus a writable
string and a writable int. Readonly fields are NOT submitted as slots.
Proposal §3.3.

- YAML: `template: "A {color} hero named {name} with {count} traits."`, `fields: { color: {readonly, expr}, name: {string, required}, count: {int, default, min, max} }`
- Flow: `flows/form_basic.yaml` — seeds `picked_color: blue`, submits `name + count`.
- Successor: `form_bool`.

#### `form_bool`
A `type: bool` field — Space toggles true/false.

- YAML: `fields: { active: { type: bool, default: false } }`
- Flow: `flows/form_bool.yaml` — dispatches `active: "true"`.
- Successor: `form_float`.

#### `form_float`
A `type: float` field with `min:` / `max:` bounds.

- YAML: `fields: { ratio: { type: float, min: 0.0, max: 1.0, default: 0.5 } }`
- Flow: `flows/form_float.yaml` — submits `ratio: 0.75`.
- Successor: `form_enum`.

#### `form_enum`
A `type: enum` field with `values:` — Space cycles. Proposal §3.3.

- YAML: `fields: { method: { type: enum, values: [...], default: ford } }`
- Flows: `flows/form_enum.yaml` (happy), `flows/form_enum_invalid.yaml` (negative — out-of-list value rejected at orchestrator).
- Successor: `form_placeholder`.

#### `form_placeholder`
A field with `placeholder:` shown only when the buffer is empty.

- YAML: `fields: { note: { type: string, placeholder: "..." } }`
- Flow: `flows/form_placeholder.yaml`.
- Successor: `form_required`.

#### `form_required`
A `required: true` field. The widget refuses to commit an empty
buffer; the orchestrator independently rejects an empty slot with
`MISSING_SLOTS` because the intent declares the slot required.

- YAML: `fields: { value: { type: string, required: true } }`
- Flows: `flows/form_required.yaml` (happy), `flows/form_required_missing.yaml` (negative — `MISSING_SLOTS`).
- Successor: `form_bounds`.

#### `form_bounds`
An int field with `min:` / `max:`. Bounds enforcement lives in the
widget — see "What this fixture does NOT cover" below.

- YAML: `fields: { bounded: { type: int, min: 1, max: 10, default: 5 } }`
- Flow: `flows/form_bounds.yaml` — submits `bounded: 7`.
- Successor: `form_per_field_when` (commit arc seeds `show_secret_field: true`).

#### `form_per_field_when`
A field hidden by per-field `when:`. When the gate is false, the
field is not rendered AND not submitted. Proposal §4.4.

- YAML: `fields: { secret: { type: string, when: "world.show_secret_field" } }`
- Flows: `flows/form_per_field_when_hidden.yaml` (gate false → slot absent),
  `flows/form_per_field_when_shown.yaml` (gate true → slot present).
- Successor: `form_readonly_expr` (commit arc seeds `money: 100, day: 3`).

#### `form_readonly_expr`
A readonly field whose `expr:` is non-trivial arithmetic:
`world.money + world.day * 10`. Demonstrates a real expression
(internal/expr, not pongo) and that the result is propagated through
the dispatched slot.

- YAML: `fields: { total: { type: int, readonly: true, expr: "world.money + world.day * 10" } }`
- Flow: `flows/form_readonly_expr.yaml` — seeds money + day, asserts the computed total.
- Successor: `complete` (terminal state).

### Endpoints — `intro` and `complete`

`intro` is the root state — narrative prose plus a single-mode
two-item choice (Begin / Jump to menu). `complete` is the terminal
state — congratulatory prose plus a single-mode two-item choice
(Restart / Open the feature menu). Both have a `goto_menu` arc.

- Flows: `flows/intro_begin.yaml`, `flows/intro_jump_to_menu.yaml`,
  `flows/complete_restart.yaml`.

### `full_walkthrough.yaml`

End-to-end integration fixture starting on `intro` and dispatching
every spoke's commit intent in canonical order, asserting the final
world snapshot at the `complete` state. If you add or reorder a
spoke, update this fixture.

## Feature × mode coverage

| YAML knob                       | Single                                 | Multi                          | Form                                  |
|---------------------------------|----------------------------------------|--------------------------------|---------------------------------------|
| Literal items, no slots/param   | `single_no_slots`                      | —                              | —                                     |
| Literal pre-bound slots         | `single_basic`                         | `multi_basic`                  | `form_basic` (per-field defaults)     |
| Templated labels & hints        | `single_templated`                     | `multi_templated`              | (templates render in prompts)         |
| Templated slot values           | `single_templated_slots`               | (forbidden — §4.4(5))          | (per-field defaults can template)     |
| `param:` string capture         | `single_param_string`                  | n/a                            | use form mode instead                 |
| `param:` int capture            | `single_param_int`                     | n/a                            | use form mode instead                 |
| `param:` enum cycle             | `single_param_enum`                    | n/a                            | `form_enum`                           |
| `when:` per-item / per-field    | `single_per_item_when`                 | `multi_per_item_when`          | `form_per_field_when`                 |
| `min:` lower bound              | n/a                                    | `multi_basic`, `multi_no_max`  | `form_float`, `form_bounds`           |
| `max:` upper bound              | n/a                                    | `multi_basic`                  | `form_float`, `form_bounds`           |
| `max:` unset (defaults)         | n/a                                    | `multi_no_max`                 | n/a                                   |
| `min: 0` empty allowed          | n/a                                    | `multi_min_zero`               | n/a                                   |
| `placeholder:`                  | (on param) `single_param_string/int`   | n/a                            | `form_placeholder`                    |
| `required:`                     | (on param) `single_param_string`       | n/a                            | `form_required`                       |
| `readonly:` + `expr:`           | n/a                                    | n/a                            | `form_basic`, `form_readonly_expr`    |
| `type: bool`                    | n/a                                    | (use multi for lists)          | `form_bool`                           |
| `type: enum` field              | n/a                                    | n/a                            | `form_enum`                           |
| `type: float`                   | n/a                                    | n/a                            | `form_float`                          |

## What this fixture deliberately does NOT cover

- **`choice` inside `blocks:` (`extends:` views).** The loader rejects
  this at load time (proposal §4.5, §8) — typed metadata is lost
  through the inheritance pipeline. Any spoke here would fail to load.
  Authors who want a shared chrome around a choice put the `extends:`
  at the wrapping view and the `choice:` as a sibling element in the
  same `view:`, not inside a block.
- **More than one `choice` per view.** Loader enforces exactly one
  (§4.5). Adding a second `choice:` element to any spoke would error
  out at load time.
- **Dynamic items.** Item lists materialized from world state (e.g.
  `items: "{{ world.ticket_list }}"`) are deferred per the proposal's
  migration map §6 — the v1 widget only supports literal item arrays.
- **Widget-only validation paths.** The proposal puts several
  validations inside the widget (Phase C), not the orchestrator:
    - `min:` / `max:` on `multi` selections.
    - `min:` / `max:` on `form` int and float fields.
    - The widget's own empty-buffer rejection for `required:` fields.
    - paramMode buffer parsing (e.g. non-numeric input on a `type: int`
      param).
  A flow test fires `intent: { name, slots }` directly, bypassing the
  widget — so the orchestrator only sees `required:` and enum
  membership. Out-of-range ints, oversized multi selections, and
  malformed param buffers cannot be exercised by flow fixtures alone.
  Those negative paths are covered by `internal/tui/choice_widget_test.go`
  (Phase F, per the proposal). Negative paths reachable through the
  orchestrator (missing required slot, invalid enum value) DO have
  flow fixtures here: `form_required_missing.yaml`,
  `form_enum_invalid.yaml`, `single_param_enum_invalid.yaml`.

## Subtleties worth knowing

- **Readonly form fields ARE submitted as slots when the intent
  declares the slot.** Looking at `internal/tui/choice_widget.go` and
  `internal/render/elements/choice.go`, the readonly value is computed
  via `expr.Compile` + `expr.EvalAny` against the open-time env
  (world + slots only — there is no live `form.<other_field>` key
  yet). For static-render purposes the value is presentation-only,
  but if the intent has a matching slot declared and the widget's
  commit path includes the field, the computed value gets dispatched
  too. This fixture's `form_readonly_expr` declares `total` as a
  required slot precisely so the readonly value participates in the
  dispatch — read the flow fixture to see the expected shape.
- **`form.<other_field>` is NOT live in `expr:` today.** The readonly
  field's `expr:` is evaluated at widget open with `world.*` and
  `slots.*`; sibling buffer changes do NOT re-trigger the eval. A
  future enhancement could plumb a live `form` snapshot; the
  proposal's §3.3 example references it but the current
  implementation skips it.
- **`when:` on items is evaluated by the renderer, not the
  orchestrator.** A guard-false item is hidden from the widget but
  the on: arc still fires if the underlying intent is dispatched
  (e.g. by a flow test). Use `when:` for display filtering; use
  transition `when:` on the on: arc for behavioral gating.
- **Multi-mode `items[].value` must be a string literal.** Templated
  values are rejected at load time (`internal/app/choice.go`'s
  `validateChoiceCrossRefs`) because materializing a list of templated
  strings would defeat downstream type checks.
- **Seeding is performed by arc effects, not by separate seed
  states.** The previous version of this fixture had `seed_day` /
  `seed_money` / `seed_show_secret_field` intents that wrote world
  keys from the menu. Those intents still exist as test rigging
  (firable by flow fixtures that drive a spoke in isolation), but
  the walkthrough itself does its seeding via `effects:` on the arc
  that ENTERS the spoke needing seeded state. This avoids a state
  proliferation problem (one seed state per seeded spoke) and keeps
  the chain at exactly 24 states.
