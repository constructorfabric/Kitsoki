# Proposal — Interactive choice / multi-select / form widget

**Status:** Draft v1. Not implemented. Sibling to
[`view-elements-proposal.md`](view-elements-proposal.md): that proposal
gives authors *typed* content elements (`prose` / `heading` / `list` /
`kv` / `code` / `template`); this one adds *interactive* elements
(`choice`) that participate in intent dispatch.

**Tldr.** One new view element kind, `choice`, with three modes:

- `single` — ▸-cursor picker. Enter fires one item's `intent:` + `slots:`.
  Optional one-shot `param:` captures a single free-form slot.
- `multi`  — `[ ]` / `[x]` togglable picker. Space toggles; Enter
  submits a *single* intent with a list-valued slot bound to the
  selected items.
- `form`   — mad-lib template (`"Buy {items} for ${total_cost}"`) with
  inline underlined fields. Tab cycles fields. Enter fires one intent
  with one slot per field.

Picking always dispatches through the existing
`asyncSubmitDirect(orch, sid, intent, slots)` path the right-pane
action menu already uses (`internal/tui/tui.go:2004`). The widget is
pure TUI presentation + an intent constructor — the orchestrator, the
state machine, flow tests, `kitsoki turn`, and the kitsoki-debugging
skill see no new code path. Definition of done: every existing story
uses the widget wherever it applies, with green flow tests and
smoke-tested intent dispatch.

---

## 1. Problem

Today, almost every kitsoki room communicates the available actions
through one of two surfaces:

1. **Right-pane auto-action-menu.** Derived from the state's `on:` map
   — one row per declared intent, ordered by intent `priority:`.
   Universal but unaware of room-specific context. Doesn't carry the
   author's chosen labels, hints, or per-item availability narrative.
2. **Prose hints in `view:`.** Authors write things like
   `"pick a profession (banker / carpenter / farmer)"`
   (`stories/oregon-trail/rooms/intro.yaml:102`) and rely on the
   four-tier semantic router (synonyms → templates → turncache → LLM)
   to translate the user's free text into an intent + slots. This is
   how *every* enumerated decision in oregon-trail, robbery,
   bugfix, dev-story, code-review currently routes.

The second surface has three structural problems:

- **Expense.** Cold routes burn an LLM judge call (`internal/intent`,
  `internal/semroute`). On the documented Oregon Trail trace ~78% of
  turns route deterministically, but the remaining 22% include almost
  every choice the user has to actually think about — paying ~1s of
  latency per decision.
- **Fragility.** Typo the prose hint and the user's typed echo of
  the hint becomes unrouteable. The author has to maintain the prose
  AND the semroute synonyms AND the slot parser examples in three
  places to stay deterministic.
- **No first-class affordance.** The user has to *read* the prose,
  *type* the right phrase, and *trust* the LLM. There is no in-context
  picker showing "here are your options; choose one." The right-pane
  menu is the closest thing, but it's keyboard-modal, lives outside
  the transcript, and shows the global intent set rather than the
  *room's* curated list.

Authors already reach for typed `list:` elements as a fake menu
(oregon-trail's intro at `rooms/intro.yaml:100-108`, every
`river_crossing` substate, `general_store`, `fort`, `snow_blocked`,
`trail_guide`, `rest_room` — see Migration map §6). The lists are
already structured (label / hint / `when:` guards), but they remain
inert — selection still flows through the prompt + router.

We need a first-class interactive **transcript widget** that:

- Renders the room author's curated decision set inline in the
  transcript next to its narrative context.
- Dispatches an intent + slots directly when the user picks, with
  zero semroute / LLM cost.
- Supports the patterns stories actually use today: enum picks
  (profession, month, method), enum + free-form parameter (`name
  member <N> <name>`, `generate names <theme>`), multi-pick
  (selecting items to keep / discard), and structured submission
  forms (`propose_purchase items=… total_cost=…`).
- Degrades gracefully on non-TUI surfaces (`kitsoki render`,
  Jira/Bitbucket transports) to a static numbered list so existing
  flow fixtures and the same `app.yaml` work across every transport.

## 2. Proposed widget model

One new element kind, `choice`, with a `mode:` discriminator. The
modes share a common envelope (prompt, items/fields, dispatch target)
but diverge in interaction.

### 2.1 Mode summary

| Mode               | Interaction                                                            | Dispatches                                                              |
|--------------------|------------------------------------------------------------------------|-------------------------------------------------------------------------|
| `single` (default) | ▸ cursor. ↑/↓ moves; Enter picks ONE item. Optional `param:` captures one free-form slot via inline mini-prompt. | One intent (per item) with item's `slots:` ∪ `{param.slot: captured}`. |
| `multi`            | `[ ]` checkboxes. Space toggles; Enter submits the *whole* selection. `min:` / `max:` enforced. | One intent (per choice) with one list-valued slot holding the selected values. |
| `form`             | Mad-lib `template:` with `{field}` blanks rendered as inline underlined regions. Tab cycles editable fields; Enter submits. | One intent (per choice) with one slot per editable field. `readonly:` fields are not submitted. |

All three modes share:

- An optional `prompt:` heading line displayed above the widget.
- Per-item / per-field `when:` guards (expr-lang) evaluated at
  render time using the same env as transition guards.
- A graceful **static rendering** for non-TUI surfaces — numbered
  list with intent name in parens for `single`/`multi`, plain
  template echo with placeholder text for `form`.
- A `coexists` policy: the right-pane auto-action-menu remains
  populated from the state's `on:` map. The choice widget adds
  authoritative in-context UX; it does not replace the menu.

### 2.2 Interaction model (TUI)

A new `ModeChoosing` joins the existing
`ModeOnPath / ModeOffPath / ModeSlotFilling / ModeDisambiguating /
ModeMenu / ModeMeta / ModeMetaSessions` set in
`internal/tui/tui.go`. When a state's view contains a `choice`
element, focus auto-transfers to the inline widget; the prompt
textarea is dimmed (still visible — typing a printable letter
defocuses the widget back to the textarea, so the user always has
the free-text escape hatch).

| Key                     | `single`                | `multi`                          | `form`                                              |
|-------------------------|-------------------------|----------------------------------|-----------------------------------------------------|
| ↑ / ↓                   | move cursor             | move cursor                      | (no-op)                                             |
| Space                   | (no-op)                 | toggle current item              | enum field: cycle next value; text field: insert " "|
| Tab / Shift-Tab         | (no-op)                 | (no-op)                          | next / previous editable field                      |
| Enter                   | pick current item       | submit selection                 | submit form                                         |
| Esc                     | cancel → focus prompt   | cancel → focus prompt            | cancel → focus prompt                               |
| Backspace               | (param mode only — edits buffer) | (no-op)              | edit current text field                             |
| Printable letter        | item with `param:` picked but buffer empty → switch to param mode and start typing. Otherwise → defocus widget, forward key to prompt textarea. | defocus widget, forward to prompt textarea. | edit current text field. (Outside form mode the form *is* the input.) |

Off-path is mutually exclusive: entering off-path closes any active
choice widget (the help banner takes over); the widget re-opens on
the next room entry that declares one.

### 2.3 Dispatch parity

Every commit funnels into:

```go
asyncSubmitDirect(m.orch, m.sid, intent, slots)
```

which is the same call `dispatchMenuEntry` uses for the right-pane
action menu (`internal/tui/tui.go:2004`). The orchestrator sees a
direct intent submission identical to:

- a menu pick,
- a `/warp` scenario step,
- a flow-test turn (`intent: { name, slots: {...} }`),
- a `kitsoki turn --intent X --slots @f.json` invocation (the
  surface kitsoki-debugging uses).

The widget never reaches the orchestrator and never affects the
state machine. **Flow tests, `kitsoki turn`, kitsoki-debugging,
and the Jira/Bitbucket transports require zero code changes** to
benefit from the migration.

## 3. Schema

### 3.1 `single`

```yaml
view:
  - choice:
      prompt: "Choose a profession"
      items:
        - label:  "Banker"
          hint:   "$1,600 starting cash — easy"
          intent: pick_profession
          slots:  { profession: banker }
        - label:  "Carpenter"
          hint:   "$800 — medium"
          intent: pick_profession
          slots:  { profession: carpenter }
        - label:  "Farmer"
          hint:   "$400 — hard, highest score multiplier"
          intent: pick_profession
          slots:  { profession: farmer }
        - label:  "Generate names from a theme"
          hint:   "Western / Norse / Star Wars / Lord of the Rings"
          intent: generate_names
          param:                                  # one free-form slot
            slot:        theme
            type:        string
            placeholder: "e.g. norse mythology"
            required:    true
          when: "world.party_names == ''"
        - label:  "✗ start_journey — {{ blocked_reason('start_journey') }}"
          intent: start_journey
          when:   "!available('start_journey')"   # disabled-row display
```

Rendered TUI form (single mode):

```
Choose a profession:

  ▸ Banker                    $1,600 starting cash — easy
    Carpenter                 $800 — medium
    Farmer                    $400 — hard, highest score multiplier

    Generate names from a theme    Western / Norse / Star Wars / LOTR
    ✗ start_journey — name the party first

[↑/↓ move • Enter pick • Esc cancel]
```

### 3.2 `multi`

```yaml
view:
  - choice:
      mode:   multi
      prompt: "Select symptoms"
      intent: report_symptoms
      slot:   symptoms
      min:    1
      max:    5
      items:
        - { value: fever,    label: "Fever",    hint: ">100.4°F" }
        - { value: cough,    label: "Cough" }
        - { value: fatigue,  label: "Fatigue" }
        - { value: rash,     label: "Rash", when: "world.day > 3" }
```

Rendered TUI form (multi mode):

```
Select symptoms (1–5):

    [x] Fever     >100.4°F
    [ ] Cough
  ▸ [x] Fatigue
    [ ] Rash

[↑/↓ move • Space toggle • Enter submit • Esc cancel]
```

Dispatches `intent: report_symptoms, slots: { symptoms: [fever,
fatigue] }`.

### 3.3 `form` (mad-lib)

```yaml
view:
  - choice:
      mode:     form
      prompt:   "Compose your purchase"
      intent:   propose_purchase
      template: "Buy {items} for ${total_cost}, leaving ${remaining}."
      fields:
        items:
          type:        string
          placeholder: "oxen=4, food=1500, bullets=10"
          required:    true
        total_cost:
          type:    int
          min:     1
          max:     "{{ world.money }}"
          default: 0
        remaining:
          # Read-only computed display; not submitted as a slot.
          type:     int
          expr:     "world.money - form.total_cost"
          readonly: true
```

Rendered TUI form (form mode):

```
Compose your purchase:

  Buy ▸_oxen=4, food=1500_________________ for $_1200__,
  leaving $_400_.

[Tab next • Shift-Tab prev • Enter submit • Esc cancel]
```

Dispatches `intent: propose_purchase, slots: { items: "oxen=4,
food=1500", total_cost: 1200 }`. The `remaining` field is presentation
only.

Enum fields render as cycle-on-Space inline pickers:

```yaml
fields:
  method:
    type:   enum
    values: [ford, caulk, ferry, wait]
    default: ford
```

renders as `▸[ford ▾]` and Space cycles to `[caulk ▾]`, etc.

## 4. Element schema reference

Validation is **layered**: a JSON Schema covers the structural shape,
the expr-lang compiler covers expression fields, and a small loader
walk covers cross-references that need the surrounding `intents:`
map. This matches existing kitsoki patterns (see §4.4).

### 4.1 Authoritative YAML shape

```yaml
- choice:
    mode:     "single" | "multi" | "form"      # default: single
    prompt:   <string>                          # optional heading
    when:     <expr>                            # element-level guard

    # ---- single mode ----
    items:
      - label:  <string>           # required for single
        hint:   <string>           # optional right-column hint
        intent: <intent-name>      # required
        slots:  { <key>: <value> } # optional pre-bound slots
        param:                     # optional one-shot slot capture
          slot:        <slot-name>
          type:        string | int | enum
          placeholder: <string>
          values:      [<v>, ...]  # required when type: enum
          required:    bool
        when:   <expr>             # per-item guard

    # ---- multi mode ----
    intent: <intent-name>          # required
    slot:   <slot-name>            # required — list-valued slot
    min:    <int>                  # optional, default 0
    max:    <int>                  # optional, default len(items)
    items:
      - value: <string>            # required for multi
        label: <string>            # display label (default = value)
        hint:  <string>
        when:  <expr>

    # ---- form mode ----
    intent:   <intent-name>        # required
    template: <string>             # required, includes {field} blanks
    fields:
      <name>:
        type:        string | int | float | bool | enum
        hint:        <string>      # one-line description
        placeholder: <string>      # shown when buffer is empty
        values:      [<v>, ...]    # required when type: enum
        default:     <value>       # initial buffer
        min:         <value>       # int/float bounds
        max:         <value>
        required:    bool
        readonly:    bool          # display-only; not submitted
        expr:        <expr>        # required iff readonly: true
        when:        <expr>        # per-field guard
```

### 4.2 Structural validation — JSON Schema

The shape above is published as a draft-2020-12 JSON Schema at
`internal/app/schemas/choice.schema.json` (embedded via `//go:embed`)
and validated by `github.com/santhosh-tekuri/jsonschema/v6` — the
same library `internal/mcp/validator.go` already uses for MCP draft
artifacts. One canonical schema covers all three modes via `oneOf`
mode-discrimination plus `additionalProperties: false` to reject
typos.

Schema sketch (full file lives at the embedded path; this is the
shape the validator sees):

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://kitsoki.dev/schemas/view/choice.json",
  "type": "object",
  "additionalProperties": false,
  "required": ["mode"],
  "properties": {
    "mode":   { "enum": ["single", "multi", "form"] },
    "prompt": { "type": "string" },
    "when":   { "type": "string" }
  },
  "oneOf": [
    { "$ref": "#/$defs/single" },
    { "$ref": "#/$defs/multi"  },
    { "$ref": "#/$defs/form"   }
  ],
  "$defs": {
    "single": {
      "properties": {
        "mode":  { "const": "single" },
        "items": {
          "type": "array", "minItems": 1,
          "items": {
            "type": "object", "additionalProperties": false,
            "required": ["label", "intent"],
            "properties": {
              "label":  { "type": "string", "minLength": 1 },
              "hint":   { "type": "string" },
              "intent": { "type": "string", "pattern": "^[A-Za-z_][A-Za-z0-9_]*$" },
              "slots":  { "type": "object" },
              "when":   { "type": "string" },
              "param":  { "$ref": "#/$defs/param" }
            }
          }
        }
      },
      "required": ["items"]
    },
    "multi": {
      "properties": {
        "mode":   { "const": "multi" },
        "intent": { "type": "string", "pattern": "^[A-Za-z_][A-Za-z0-9_]*$" },
        "slot":   { "type": "string", "minLength": 1 },
        "min":    { "type": "integer", "minimum": 0 },
        "max":    { "type": "integer", "minimum": 1 },
        "items": {
          "type": "array", "minItems": 1,
          "items": {
            "type": "object", "additionalProperties": false,
            "required": ["value"],
            "properties": {
              "value": { "type": "string", "minLength": 1 },
              "label": { "type": "string" },
              "hint":  { "type": "string" },
              "when":  { "type": "string" }
            }
          }
        }
      },
      "required": ["intent", "slot", "items"]
    },
    "form": {
      "properties": {
        "mode":     { "const": "form" },
        "intent":   { "type": "string", "pattern": "^[A-Za-z_][A-Za-z0-9_]*$" },
        "template": { "type": "string", "minLength": 1 },
        "fields": {
          "type": "object", "minProperties": 1,
          "additionalProperties": { "$ref": "#/$defs/field" }
        }
      },
      "required": ["intent", "template", "fields"]
    },
    "param": {
      "type": "object", "additionalProperties": false,
      "required": ["slot", "type"],
      "properties": {
        "slot":        { "type": "string", "minLength": 1 },
        "type":        { "enum": ["string", "int", "enum"] },
        "placeholder": { "type": "string" },
        "values":      { "type": "array", "items": { "type": "string" } },
        "required":    { "type": "boolean" }
      },
      "if":   { "properties": { "type": { "const": "enum" } } },
      "then": { "required": ["values"] }
    },
    "field": {
      "type": "object", "additionalProperties": false,
      "required": ["type"],
      "properties": {
        "type":        { "enum": ["string", "int", "float", "bool", "enum"] },
        "hint":        { "type": "string" },
        "placeholder": { "type": "string" },
        "values":      { "type": "array", "items": { "type": "string" } },
        "default":     { },
        "min":         { },
        "max":         { },
        "required":    { "type": "boolean" },
        "readonly":    { "type": "boolean" },
        "expr":        { "type": "string" },
        "when":        { "type": "string" }
      },
      "allOf": [
        { "if":   { "properties": { "type":     { "const": "enum" } } },
          "then": { "required":   ["values"] } },
        { "if":   { "properties": { "readonly": { "const": true  } } },
          "then": { "required":   ["expr"] } }
      ]
    }
  }
}
```

Validation runs after goyaml strict-unmarshal: the loader marshals
the parsed `choice:` subtree back to JSON and hands it to the
embedded schema. JSON Schema errors are returned with the source
path (state → view → element index) the loader already prefixes onto
structural errors, so the diagnostic location stays consistent with
today's load-error format.

The schema doubles as IDE tooling: published under
`docs/embedded/schemas/choice.schema.json` (alongside
`docs/embedded/app-schema.md`), it can be consumed by
yaml-language-server in VS Code / Helix / Neovim for inline
autocomplete + validation as authors edit room YAML.

### 4.3 Expression validation — expr-lang compile-pass

JSON Schema reports `when:` / `expr:` fields as strings; it cannot
verify that those strings are valid expr-lang. After structural
validation passes, the loader walks every expression-bearing field
and compiles it through the existing `internal/expr` surface:

- `when:` (element / per-item / per-field) → `expr.CompileBool`
  (same call already used for transition `when:` and effect `when:`
  guards, see `internal/expr/expr.go:344`). Failure surfaces as a
  load error with the offending source snippet — identical
  treatment to today's `loader.go` `when:` compilation errors.
- `expr:` on `readonly` form fields → `expr.Compile`. The compiled
  program is cached by source (matches the existing global program
  cache pattern in `internal/expr` and the `whenCache` in
  `internal/render/elements/element.go:235`).
- Templated leaves (`slots: { x: "{{ world.y }}" }`,
  `placeholder: "{{ ... }}"`, `template:` body) → pongo2
  compile-pass via the per-app `*render.AppRenderer` already
  available at load time (no new compiler needed; same path
  used for view bodies).
- `min:` / `max:` / `default:` on form fields, when they are
  templated, go through pongo compile-pass; literal values are
  type-checked by JSON Schema only (the schema deliberately leaves
  these untyped because they can be either literal or templated).

The compile-pass is identical in spirit to what
`loader.go` already does for transition `when:` and effect
expression fields — the choice widget plugs into the same pre-load
expression-validation walk rather than introducing a parallel
mechanism.

### 4.4 Cross-reference validation — loader walk

JSON Schema cannot express "this intent name must exist in the
surrounding state's `on:` map OR in the global `intents:` map" —
that's a graph-level invariant. After structural + expression
validation pass, a small loader walk at the existing state-view
(`loader.go:858`) and transition-view (`loader.go:898`) sites
resolves:

1. Each `item.intent` (single) / element-level `intent` (multi,
   form) against the global / local intents map.
2. Each `item.slots` key against the chosen intent's declared slot
   set. Literal enum slot values get membership-checked against
   the slot's `values:`. Templated values (containing `{{`) skip
   literal checks because their type is only knowable at submit
   time.
3. `param.slot` (single) / `slot` (multi) / each `fields.<name>`
   (form) is a declared slot of the chosen intent.
4. For `mode: form`, every `{name}` placeholder in `template:` has
   a matching `fields:` entry. (Pure-string check; lives here
   rather than the JSON Schema because the cross-reference
   "placeholder ↔ field" doesn't fit JSON Schema's per-property
   semantics cleanly.)
5. For `mode: multi`, every `items[].value` is a string literal
   (no `{{` template), because the choice widget materializes the
   selection as a literal list and a templated value would defeat
   downstream type checks.

These walks reuse the existing loader infrastructure — the same
`(*loader).resolveIntent` / `(*loader).validateSlotRef` helpers
that today check `effects:` slot references. No new
cross-reference machinery.

### 4.5 Other rules

- Exactly one `choice` element per view (enforced by the schema's
  `oneOf` over the parent view's element array? No — that's
  outside the choice schema's scope. Enforced by `(View).Validate`
  in `internal/app/view_element.go` with a one-line count check.)
- A `choice` element cannot live inside a `blocks:` (`extends:`-form)
  view — typed metadata is lost through the inheritance pipeline.
  Loader rejects this at load time. Authors who want a templated
  shell + a choice put the `extends:` at the wrapping view and the
  `choice:` as a sibling element in the same `view:`, not inside a
  block. See §8 Limitations.

### 4.6 Why this split

JSON Schema is the right tool for declarative *shape* — required
properties, type constraints, enum members, `oneOf` discrimination.
It is already in kitsoki's dependency tree
(`github.com/santhosh-tekuri/jsonschema/v6`) and already in use
for MCP artifact validation (`internal/mcp/validator.go`).

expr-lang's `CompileBool` / `Compile` are the right tools for
*expression validation* — they catch typos, undefined identifiers,
AST-whitelist violations at load time rather than at runtime, and
they're already wired into the loader for every other expression
field.

The 4-line cross-reference loader walk covers the remaining
*graph-level* invariants (intent name resolution, slot key
existence) that neither tool can express.

Hand-rolled Go validation in `(ViewElement).validate()` is reserved
for things JSON Schema and expr can't do — currently nothing, so
the function reduces to "kind == choice → run the JSON Schema
validator and return its error." This is materially less code than
the alternative of replicating the JSON Schema's `oneOf` /
required-field logic in Go.

## 5. Phases

### Phase 0 — Proposal landed

This document. Linked from README.md's docs index.

### Phase A — Schema (load-time only)

`internal/app/schemas/choice.schema.json`: NEW. Draft-2020-12 JSON
Schema covering all three modes (full shape in §4.2). Embedded
into the binary via `//go:embed`. Mirrors the publication pattern
of MCP artifact schemas and gets a sibling copy at
`docs/embedded/schemas/choice.schema.json` for IDE consumption.

`internal/app/view_element.go`: extend `ViewElement` with
`ChoiceMode` / `ChoicePrompt` / `ChoiceItems` / `ChoiceIntent` /
`ChoiceSlot` / `ChoiceMin` / `ChoiceMax` / `ChoiceTemplate` /
`ChoiceFields`. Add `ChoiceItem`, `ChoiceField`, `ChoiceParam`
types. Extend `rawViewElementYAML` with a
`Choice goyaml.MapSlice` (or `json.RawMessage` after the YAML→JSON
re-marshal step — implementation detail) so the raw subtree is
preserved for the JSON Schema validator. Extend
`(ViewElement).validate()` to: kind == "choice" → marshal the
preserved subtree to JSON, run it through the embedded schema via
`jsonschema.Validate`, then walk expression-bearing fields and
compile each via `expr.CompileBool` / `expr.Compile` /
`render.Pongo` compile-pass (§4.3). Extend `(View).Validate()` to
forbid `choice` inside `Blocks` and >1 `choice` per view (§4.5).

`internal/app/loader.go`: at the existing state-view and
transition-view validation sites (~loader.go:858 / ~898), walk
choice items and resolve `intent` + `slots` + `param.slot` /
`slot` / `fields.*` against the surrounding intents map per §4.4.
Reuse the existing `resolveIntent` / `validateSlotRef` helpers
already used for `effects:` slot references. Surface errors with
the same path-prefix format the loader already uses (state name →
view → element index).

Phase A is schema-only. Loading an app with `choice` elements
succeeds; rendering and TUI behavior are unchanged because no
renderer or TUI handler is wired yet. Existing apps continue to
load identically.

Library reuse:

- `github.com/santhosh-tekuri/jsonschema/v6` — already a direct
  dep (`internal/mcp/validator.go` uses it).
- `internal/expr.CompileBool` / `expr.Compile` — already invoked
  by the loader for transition / effect / slot `when:` and
  `validator:` fields.
- `internal/render.Pongo` — used as compile-pass for templated
  leaves; already invoked at load time for typed view elements'
  leaf strings.

### Phase B — Static renderer

`internal/render/elements/choice.go`: new `Choice{}` implementing
the existing `Renderer` interface
(`internal/render/elements/element.go:112`). Layout mirrors
`list.go` for label/hint column alignment. Reuses the global
`evalWhen` cache in `element.go` for per-item / per-field guards.
Per-mode rendering:

- `single`: numbered, hint-aligned. Item with `param:` appends
  the placeholder to the label in brackets.
- `multi`: `[ ]` / `[x]` brackets, footer of selection bounds.
- `form`: template body with `{name}` substituted by underlined
  buffer content; readonly fields rendered with their computed
  `expr:` value.

`internal/render/elements/element.go:182`: add `case "choice":`
to `renderOne()`'s dispatch.

After Phase B, `kitsoki render` and the Jira/Bitbucket transports
get the static form; the TUI still renders the static form (the
interactive widget arrives in Phase C). The static form is
sufficient for flow tests and for any non-interactive transport.

### Phase C — TUI interactive widget

`internal/tui/choice_widget.go`: new `choiceWidgetModel` —
Bubble Tea model holding active items / fields / cursor / buffers
/ paramMode flag. `Open(el app.ViewElement, env expr.Env)`
populates from the typed view element captured in the transcript
entry. `Update(msg)` handles keyboard interaction. `View(width)`
renders the widget with cursor / `[x]` / underlined-field
overlays. `Commit()` returns `(intent, slots, err)`.

`internal/tui/tui.go`: add `ModeChoosing` to the Mode enum
(~tui.go:49); add `m.choice choiceWidgetModel`; add a
`case ModeChoosing` branch in `Update()` (~tui.go:800), below
Clarify/Disambig and above Menu; forward
`tea.WindowSizeMsg` / `turnOutcomeMsg` / routing observer
messages / meta-stream messages through to the default branch
(mirror `updateSlotFilling` at tui.go:1045-1054). On `Commit()`,
call `asyncSubmitDirect` and transition to `ModeBusy`. In
`handleTurnOutcome` (~tui.go:2057) after `AppendAgentBodyTyped`,
inspect `out.TypedView` for a `choice` element and auto-focus the
widget. Apply the same check in `NewRootModel`'s welcome path so
the initial frame's choice is interactive.

`internal/tui/transcript.go`: use the existing `liveLine` plumbing
(~transcript.go:264-292) to re-render the widget in place on
every cursor/buffer change. On commit, finalize the live line so
the picked / submitted form becomes a permanent transcript entry.

After Phase C, `kitsoki run` renders interactive widgets;
everything else (flow tests, `kitsoki turn`, transports) continues
to use the static Phase B rendering.

### Phase D — Param mode + clarify fallback

For `single` items with a `param:`, the widget enters paramMode
on Enter when the param buffer is empty. paramMode renders an
inline `<slot>? ` prompt with the placeholder/type hint;
Backspace and printable chars edit the buffer; Enter commits;
Esc returns to the item list (without dispatching).

`clarifyModel` continues to fire post-dispatch when the
orchestrator declares missing required slots beyond what the
widget supplied — unchanged behavior, just a fallback for items
whose `intent:` declares multiple required slots that the choice
author didn't fully cover.

### Phase E — Migrate stories

See §6 Migration map. One PR per small story; oregon-trail split
into 2–3 PRs by room cluster.

### Phase F — Tests + smoke

- Per-mode static-render tests in `internal/render/elements/
  choice_test.go`.
- Per-mode keyboard tests in `internal/tui/choice_widget_test.go`
  using `teatest` (existing pattern from `clarify_test.go`).
- Per-story flow-fixture coverage in each story's `flows/*.yaml`.
- A new `testdata/apps/choice_smoke/` story exercising all three
  modes against deterministic intents — `kitsoki test flows
  testdata/apps/choice_smoke/app.yaml`.
- Smoke testing via the kitsoki-debugging skill: for each
  migrated story, run `./kitsoki turn <app> --state X --intent Y
  --slots @f.json` against the converted room and confirm
  `next_state` + `host_calls` are bit-identical to the
  pre-migration result.

`go test ./...` must remain under the 10s budget (memory: tests
must be fast).

## 6. Migration map

Categories from the survey (`A`: pure enumerable, `B`: enum + free-form
slot, `C`: typed list already used as menu, `D`: dynamic-list ticket
picker, `E`: yes/no / verdict).

Migration order (smallest stories first; surfaces engine bugs cheaply):

| # | Story                  | Rooms                                                                                           | Modes                          |
|---|------------------------|-------------------------------------------------------------------------------------------------|--------------------------------|
| 1 | robbery                | encounter (A,C)                                                                                  | single                          |
| 2 | frontier_event         | scouting (A,C)                                                                                   | single                          |
| 3 | bugfix                 | proposing / reviewing / testing / validating / implementing (E)                                  | single + param (restart_from, jump_to) |
| 4 | code-review            | decide (E); list_pending (D)                                                                     | single + dynamic items          |
| 5 | pr-refinement          | diagnose (E)                                                                                     | single                          |
| 6 | cypilot                | code (E)                                                                                         | single                          |
| 7 | dev-story              | inbox pick_review (D); main menu (C)                                                             | single + dynamic items          |
| 8 | implementation         | write_code / test / handoff / review (E)                                                         | single                          |
| 9 | kitsoki-dev            | landing (A,C,D — tickets / pick / bugfix / feature / cypilot)                                    | single + dynamic items          |
| 10| oregon-trail           | snow_blocked / robbery_aftermath / rest_room (A,C)                                               | single                          |
| 10| oregon-trail           | intro (A,B,C) — profession + month + name_member + generate_names + start_journey                | single + param                  |
| 10| oregon-trail           | general_store / fort (B,C) — propose_purchase (items + total_cost)                               | form                            |
| 10| oregon-trail           | river_crossing × 3 substates (B,C) — propose_crossing (method + confidence)                      | form (or single + param)        |
| 10| oregon-trail           | hunt (B,C) — shoot (bullets)                                                                     | single + param (or form)        |
| 10| oregon-trail           | trail_guide_list (C) — ask / open / rename / archive / fork / back                                | single + param (chat ID)        |
| 10| oregon-trail           | inbox (D) — open_job / answer_clarification                                                       | single + dynamic items + param  |

Total: ~35 rooms across 10 stories.

### Rooms explicitly NOT converted

- **Oracle / conversational rooms.** trail_guide_active, dev-story
  oracle, anything with `mode: conversational` — these are
  open-ended chat surfaces; a finite picker is the wrong tool.
- **Free-form composition rooms.** code-review comment composers,
  oregon-trail freeform, anywhere the user is meant to *write* prose
  rather than choose from options.
- **Background-job execution states.** Rooms like
  `hunt_running` / `validating_executing` — the user has no
  decision to make while the job runs; the inbox surfaces the
  re-entry point.
- **Hub / navigation rooms.** bugfix idle, dev-story main, where
  the actions are orthogonal operations rather than a ranked
  choice menu. (Some of these *will* convert; case-by-case during
  Phase E.)

The proposal doc will be updated with the final not-converted list
as Phase E progresses.

## 7. Coexistence with existing surfaces

| Surface                                  | Effect of `choice` element                                                                                       |
|------------------------------------------|------------------------------------------------------------------------------------------------------------------|
| Right-pane action menu                   | Continues to populate from `on:` intents. The choice widget is *additional* UX, never authoritative replacement. |
| Prompt textarea (free text)              | Always available. A printable letter while ModeChoosing defocuses the widget back to the textarea.               |
| Clarify model (post-dispatch slot fill)  | Unchanged. Fires after a choice commit if the orchestrator declares additional missing required slots.           |
| Disambig model                           | Unchanged. Disambig and Choice are mutually exclusive: Choice fires deterministic intents (no ambiguity).        |
| Sessions panel / inbox panel             | Unchanged modal overlays.                                                                                         |
| Off-path                                 | Closes any active choice widget; the help banner takes over. Widget re-opens on next room entry that declares one.|
| `/warp` scenarios                        | Auto-firing intents bypass the widget entirely (intents go through the same `asyncSubmitDirect` path).            |
| Flow tests (`kitsoki test flows`)        | Continue to use `intent: { name, slots }` literals. Widget is pure presentation — fixtures need no changes.       |
| `kitsoki render`                         | Renders the static Phase B form (numbered list / static template echo).                                          |
| `kitsoki turn` / kitsoki-debugging       | Bit-identical to today. Confirmed: `OneShot` (turn.go:143) and `SubmitDirect` (tui.go:1593) share the same intent-handling path. |
| Jira / Bitbucket transports              | Render the static Phase B form in their comment body. Selection happens via the transport's native action verbs. |

## 8. Limitations (v1)

- **No `choice` inside `blocks:` (extends-form views).** The typed
  view metadata is lost through `AppRenderer.RenderExtended` —
  the TUI receives only the post-extends rendered text. Loader
  rejects the combination. Workaround: put the `choice` element
  in the same `view:` as the `extends:`, as a sibling.
- **One `choice` element per view.** Loader-enforced. Avoid
  authoring complexity around focus cycling through multiple
  widgets. Can be revisited if a real story needs two.
- **Mode is fixed per element.** A single element cannot blend
  modes (e.g. "pick one of these enums, OR fill this form"). To
  offer both, the author writes two states or uses two top-level
  intents on one form's items.
- **`param:` captures exactly one free-form slot.** Items needing
  multiple parameters should use `form` mode.
- **`form` field types are flat scalars only** — `string`, `int`,
  `float`, `bool`, `enum`. List-valued and nested-map slots need
  `multi` mode or post-dispatch clarify.
- **Multi-select dispatches a single intent.** It is NOT a
  shorthand for "fire one intent per selected item." Stories
  needing per-item dispatch should chain through a background
  job or compound state.

## 9. Smoke testing (kitsoki-debugging skill)

Per [`kitsoki-debugging` skill](../skills/kitsoki-debugging/SKILL.md),
each migrated story is smoke-tested by replaying the choice's
underlying intent + slots through `kitsoki turn` and comparing
`next_state` / `host_calls` / `error_message` to the pre-migration
baseline.

Example for oregon-trail/intro after migration:

```sh
./kitsoki turn stories/oregon-trail/app.yaml \
    --state intro \
    --intent pick_profession \
    --slots '{"profession":"banker"}' \
  | python3 -c "import sys,json; d=json.load(sys.stdin); \
                print(d.get('next_state'), d.get('error_message')); \
                [print(c) for c in d.get('host_calls', [])]"
```

Expected: `intro None` (intent applied, stayed in intro, no host
calls, no error). Compare against the pre-migration baseline; both
must match. Repeat for every choice intent in every migrated room.

Migration is complete when:

1. `go test ./...` is green and under 10s.
2. `./kitsoki test flows stories/<story>/app.yaml` is green for
   every migrated story.
3. `./kitsoki test flows testdata/apps/choice_smoke/app.yaml` is
   green.
4. Every migrated story's choice intents pass the smoke comparison
   above against the pre-migration baseline.
5. Manual `./kitsoki run stories/oregon-trail/app.yaml` confirms
   cursor moves, Enter dispatches, Esc cancels, Space toggles in
   multi mode, Tab cycles in form mode, and a printable key
   defocuses the widget.

## 10. Related proposals

- [`view-elements-proposal.md`](view-elements-proposal.md) — the
  typed view-element foundation this proposal extends. Phase A of
  the choice widget *requires* the typed view-element schema (the
  `ViewElement` discriminated union, `rawViewElementYAML`
  decoder, per-element `when:` guards). Already landed.
- [`docs/proposals/dev-story-bugfix-unify-proposal.md`](dev-story-bugfix-unify-proposal.md)
  — verdict patterns in bugfix / dev-story / cypilot use the
  `accept / refine / restart_from / jump_to / quit` arc that this
  proposal migrates to `single` mode.
- [`docs/architecture/semantic-routing.md`](../architecture/semantic-routing.md) — the
  routing stack the choice widget short-circuits. After migration,
  any text typed *while a choice is active* still flows through
  semroute as today; choice is only authoritative when the user
  actually uses it.
