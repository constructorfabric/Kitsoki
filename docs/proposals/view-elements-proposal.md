# Proposal — Typed view elements (TUI renderer)

**Status:** Draft v1. Not implemented. Lifted from `ideas.md` §
"View rendering: unify structured + prose content". Not blocking
anyone today — cloak's prose-narrow foyer is cosmetic and the
structured views in dev-story and oregon-trail already render
correctly. Pick up when (a) a new app needs column-aligned lists
that survive resize, or (b) someone tries to add richer UI
(diffs, panels, inline images).

**Tldr:** Two coupled changes.

1. **Typed elements.** Replace the single `view: <markdown string>`
   field with an optional typed array of elements (`prose`,
   `heading`, `list`, `kv`, `code`, `template`). The renderer
   picks a strategy per element kind: prose reflows to the
   viewport, lists / kv get aligned via lipgloss, code preserves
   layout exactly, `template` is the escape hatch into today's
   Glamour pipe.
2. **Pongo2 for all templated strings.** Every YAML leaf string
   that contains template syntax — view bodies, element leaf
   strings (`kv.pairs[*]`, `list.items[*].label/hint`, `prose`,
   `code`, `heading`), `say:`, `guard_hint:`, dynamic `target:`
   paths, `set:` RHS, prompt-file args — is rendered through
   pongo2 (`github.com/flosch/pongo2`). Standalone `.pongo`
   template files referenced via `template_file:` / `extends:`
   add `{% extends %}` / `{% block %}` inheritance,
   `{% include %}`, `{% macro %}`, and filters. **The YAML
   structure itself is never templated** — YAML composition stays
   on the existing `internal/app/loader` import/imports surface.

Expr-lang continues to be the **expression evaluator** for pure-
expression fields — `when:` guards on transitions, effect-level
`when:`, initial-child selectors, parallel emit guards. These
fields are not `{{ }}`-delimited templates; their entire value
is an expression. The AST whitelist stays load-bearing there.

The split is clean: anywhere `{{ }}` or `{% %}` appears in a
string value → pongo2 renders it. Anywhere the entire field is
an expression with no delimiters → expr-lang evaluates it.

Existing string-form `view:` blocks need a one-time mechanical
syntax migration (see §3.1) — `{{ if x }}…{{ end }}` becomes
`{% if x %}…{% endif %}`, ternaries become `x if … else y`, the
nullish-coalesce `??` becomes the `|default(…)` filter, etc. A
codemod handles it. Glamour stays only inside the `template`
element.

---

## 1. Problem

`internal/tui/transcript.go` runs every state's `view:` through
Glamour with `glamour.WithPreservedNewLines()`
(`transcript.go:99,245`). One setting has to satisfy two
incompatible needs.

**Prose narration** wants to reflow to the viewport. The cloak
foyer view (`testdata/apps/cloak/app.yaml`) is hand-wrapped at
~65 chars/line:

```yaml
view: |
  You are in a spacious hall, splendidly decorated in red and gold,
  with glittering chandeliers overhead. The entrance from the street
  is to the north, and there are doorways south and west.
```

On a 150-column terminal this sits in a 65-column column. Glamour
will re-wrap lines longer than the panel, but it cannot grow past
the author's hand-wrap.

**Structured content** wants every authored newline preserved.
The Terminal Room's example block in dev-story
(`testdata/apps/dev-story/rooms/terminal.yaml:13-17`) —

```
Propose a command to run, e.g.:
  propose "list files in /tmp"
  propose "git status"
  propose "go test ./..."
```

— and the Oregon Trail general-store stock readout
(`stories/oregon-trail/rooms/general_store.yaml:58-64`) —

```
Cash:      ${{ world.money }}
Oxen:      {{ world.oxen }}    ($40 / yoke)
Food:      {{ world.food_lbs }} lbs  ($0.20 / lb)
Bullets:   {{ world.bullets }}    ($2.00 / box of 20)
```

— rely on every newline staying put and on hand-spaced columns
surviving the renderer. Without `WithPreservedNewLines` Glamour
would collapse the stack into one paragraph.

Today the renderer picks "preserve" because structured views are
the common case across dev-story (main menu, terminal room,
incident loading stub, workspace list) and oregon-trail (every
room view, from `intro` through every `river_crossing` substate
to the inbox). Cloak's prose is the odd case out and loses.

The deeper issue is that authors encode rendering intent through
whitespace and Markdown punctuation. Dev-story's main menu
(`testdata/apps/dev-story/rooms/main.yaml:10-17`) hand-aligns the
hint column:

```
Available areas:
  - Start a new task          (jira search)
  - Continue existing task    (workspace manager)
  - Consult the Oracle        (general Q&A with Claude)
```

The alignment survives only because CommonMark with
`WithPreservedNewLines` happens not to collapse runs of spaces
inside a list item (and `preserveLeadingIndent` in
`transcript.go:267` swaps leading runs to U+00A0 to keep them).
Shrink the terminal narrower than the hand-wrap and the right
column visually unpairs from its label; rename a longer item and
the author has to retune the spacing across every row.

Oregon Trail's intro view stacks five distinct rendering jobs
into a single Markdown blob
(`stories/oregon-trail/rooms/intro.yaml:74-87`):

```
Party of {{ world.party_size }}:
  1. {{ if world.party_member_1 != "" }}{{ world.party_member_1 }} (leader){{ else }}(unnamed){{ end }}
  2. {{ if world.party_member_2 != "" }}{{ world.party_member_2 }}{{ else }}(unnamed){{ end }}
  ...
Profession: {{ if world.profession != nil }}{{ world.profession }}{{ else }}(not yet chosen){{ end }}
Departure:  {{ if world.month != nil }}{{ world.month }}{{ else }}(not yet chosen){{ end }}

Choose:
  - name the party (or: name member N, or: generate names <theme>)
  - pick a profession (banker / carpenter / farmer)
  ...
  {{ if available("start_journey") }}- start the journey{{ else }}- ✗ start_journey — {{ blocked_reason("start_journey") }}{{ end }}
```

There's an instructional paragraph, a numbered roster with
per-row conditional fallback, a two-column status block, a
section heading, and an action list with a conditionally-disabled
item — all sharing one styling pass and one wrap policy.

## 2. Proposed element model

Replace the single string-typed `view:` field with an optional
typed array of elements. Each element declares **what kind of
content** it is; the renderer chooses a strategy per kind.

### 2.1 Element kinds

| Kind        | Reflows? | Use for                                                          |
| ----------- | -------- | ---------------------------------------------------------------- |
| `prose`     | yes      | Narration; reflows to viewport width.                            |
| `heading`   | n/a      | Section break (e.g. "Available areas:"); styled, not bulleted.   |
| `list`      | yes¹     | Bullet/numbered list, optional aligned hint column.              |
| `kv`        | yes¹     | Key/value pairs with the key column auto-aligned.                |
| `code`      | no       | Indented examples, terminal output; monospace, layout preserved. |
| `template`  | yes²     | Escape hatch — runs through today's Glamour pipe verbatim.       |

¹ The variable-width content reflows inside its column; the
column itself is sized once at the viewport width.
² Existing behaviour: `WithPreservedNewLines` so structure
survives, with whatever caveats Glamour has today.

There is deliberately no `paragraph` vs `text` distinction — `prose` is
one element per logical paragraph. Multiple consecutive `prose`
elements render as separate paragraphs with one blank line between.

### 2.2 Element schema

```yaml
view:
  # Reflows. One element per paragraph.
  - prose: |
      You are in a spacious hall, splendidly decorated in red and
      gold, with glittering chandeliers overhead.

  - heading: "Available areas"

  # Two-column list with right-column hints. Renderer measures
  # the longest label, pads, and re-pads on resize.
  - list:
      items:
        - { label: "Start a new task",     hint: "jira search" }
        - { label: "Continue existing",    hint: "workspace manager" }
        - { label: "Consult the Oracle",   hint: "general Q&A" }
      # Optional. Default: "-".
      marker: "-"
      # Optional. Per-item `when:` guards items in or out at render time.

  # Plain list (no hint column). Items can be strings or {label,when}.
  - list:
      items:
        - "ford   — drive the team straight through."
        - "caulk  — seal and float the wagon."
        - "ferry  — pay the ferryman."
        - "wait   — make camp until the water drops."

  # Aligned key/value block. Keys are right-trimmed; the colon
  # column is set once per element.
  - kv:
      pairs:
        Cash:      "${{ world.money }}"
        Oxen:      "{{ world.oxen }}    ($40 / yoke)"
        Food:      "{{ world.food_lbs }} lbs"

  # Preserve layout exactly. Monospace; never reflowed.
  - code: |
      propose "list files in /tmp"
      propose "git status"
      propose "go test ./..."

  # Conditional inclusion at the element level — sugar for the
  # `{{ if … }}{{ end }}` blocks that today live inside view: strings.
  - prose: "Hunt result: {{ world.last_hunt_lbs }} lbs of {{ world.last_hunt_target }} ({{ world.last_hunt_outcome }})."
    when: "world.last_hunt_outcome != ''"

  # Escape hatch: today's pipeline, unmodified.
  - template: |
      Anything Glamour-shaped that doesn't fit above goes here.
```

`when:` is evaluated against the same expr-lang environment as
templated views (the world / slots / room intent helpers) and
suppresses the element entirely if false. This subsumes most of
today's inline `{{ if … }}{{ end }}` gymnastics — see §4 worked
examples.

### 2.3 Two-form `view:` — string OR array

The existing string form keeps working unchanged.
`internal/app/types.go` declares `View string \`yaml:"view,omitempty"\``;
we change it to a `View` wrapper that custom-unmarshals YAML, then
normalizes both forms to a `[]ViewElement` slice at load time:

- `view: "<string>"` → `[]ViewElement{{Template: "<string>"}}`
- `view: |\n  multiline string` → same as above.
- `view: [<element>, …]` → parsed straight into `[]ViewElement`.

The loader runs validation per kind (e.g. `kv.pairs` must be a
map of strings; `list.items` entries must be string-or-object).

No existing app file changes. The migration is opt-in: when an
author wants reflowable prose or column-aligned lists, they switch
that one view to the array form. Mixing within a state is fine
(some authors might use `template` for legacy substates and
`prose`/`list` for new ones in the same room).

## 3. Templating: pongo2 for templated strings, expr-lang for guards

`internal/expr/expr.go` today plays two roles. It's both a
**template renderer** (the `Render` function — handles
`{{ expr }}`, `{{ if … }}{{ end }}`, `{{ else }}`,
`{{ range … }}{{ end }}` against the env) and an **expression
evaluator** (the `EvalAny` function — runs a single expr-lang
AST against the env, returning a typed value).

These two roles run on the same engine today, but they have
different contracts:

- **Templates** produce strings for further use (view text,
  rendered command lines, dynamic state paths, `say` output).
  They're delimited (`{{ }}`, `{% %}` after this proposal) and
  contain a mix of literal text and substitution.
- **Expressions** produce values that drive the FSM (a `when:`
  predicate returns bool; an effect-level `when:` returns
  bool; an initial-child selector returns a string). They're
  not delimited — the entire field value is the expression.

This proposal splits the two engines along that contract:

| Role                  | Engine     | Where it runs                                                                                                                       |
| --------------------- | ---------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| Template rendering    | **pongo2** | String-output templated leaves: view bodies, element leaves, `say:`, `guard_hint:`, dynamic `target:`, templated `initial:`, prompt args. |
| Expression evaluation | expr-lang  | `when:` guards, `set:` RHS, host-invoke `with:` args, bare-expression `initial:`, parallel emit guards, MCP / proposal predicates.   |
| File inheritance      | **pongo2** | Standalone `.pongo` template files referenced from YAML via `template_file:` or `extends:`. Only place `{% extends %}` is useful.   |

### Pragmatic split: text-output vs typed-value

The boundary is **string output vs typed value**, not "delimited vs
not". `set:` and host-invoke `with:` args today render via
`expr.RenderValue`, which preserves typed return values (bool,
int64, …) when the entire field is a single `{{ }}` expression — a
contract pongo2 cannot honour because pongo2 always returns
strings. Pushing those fields onto pongo2 would silently turn
`set: { x: "{{ world.y > 5 }}" }` from the bool `true` into the
string `"true"`, breaking every guard that reads `world.x`
downstream.

Worse, `set:` RHS in real apps uses expr-lang builtins
(`int(slots.x ?? 0)`, `min(a, b)`, `len(slots.items)`) that have
no pongo2 equivalent. Migrating those would require both a
codemod-level function-to-filter rewrite AND a per-builtin pongo2
filter registration — significant churn for no author-visible
improvement (these fields are never displayed).

So the split is: **string-output leaves move to pongo2 (views,
say, guard_hint, target, prompt args, templated initial);
typed-value leaves stay on expr-lang (set, with, when, bare
initial)**. Both engines coexist; authors don't notice — they
just keep writing `{{ … }}` and the loader routes to whichever
renderer matches the field. The codemod's allowlist enforces
this routing (see `cmd/kitsoki-migrate-templates/walk.go`
`isTemplatedField`).

### What changes (template rendering)

Every `expr.Render` call site (string-output template renderer)
becomes `render.Pongo`. `expr.RenderValue` call sites do NOT
move — those are the typed-value renderer and stay on expr-lang.
Concretely (`grep -n 'expr.Render(' internal/`):

- View bodies — `machine.go:1167,1179,1194,1275,1284`,
  `parallel.go:742,750`.
- Dynamic target paths — `machine.go:579`, `parallel.go:379,620`.
- `say:` strings — `machine.go:880,1101`.
- Initial-child selectors — `machine.go:1055`.
- Prompt file bodies — `host/oracle_ask.go:67`,
  `host/oracle_ask_with_mcp.go:324,398`.
- Flow-fixture interpolation — `testrunner/flows.go:1575`.
- Phase-id interpolation — `app/phases.go:523`.
- Metamode adapter — `metamode/adapter.go:236`.
- Harness prompt body — `harness/prompt.go` callers.

Untouched (`grep -n 'expr.RenderValue(' internal/`):
- `set:` RHS — `orchestrator/orchestrator.go:1077,1148,1151`,
  `machine.go:1226`, `orchestrator/oncomplete.go:478`.

The Render → Pongo swap is one-for-one: same signature
(`(string, Env) (string, error)`), same env shape, same call
sites. Only the syntax inside `{{ }}` / `{% %}` changes.

### What stays (expression evaluation)

`expr.EvalAny` and its `Compile` / `Program` cache remain
exactly as they are. Every `when:` clause, every initial-child
selector that's a bare expression (no `{{ }}`), every effect
guard — all stay on expr-lang. The AST whitelist
(`internal/expr/expr.go:27-37`) keeps its role as the trust
boundary for values that move FSM state. Pongo2 isn't designed
as a sandbox; keeping it out of pure-expression contexts keeps
the whitelist load-bearing.

### What pongo2 brings to templates

- **Block inheritance** in standalone `.pongo` files.
  `{% extends "base" %}` + `{% block name %}…{% endblock %}`.
  An app declares a base view layout (status bar, choice-list
  framing) once; each room overrides only the blocks that vary.
- **Includes** between `.pongo` files. `{% include "partial" %}`
  shares repeated fragments (e.g. an inventory readout used
  across forts and the general store) without copy-paste.
- **Macros** for parameterized snippets.
  `{% macro roster_line(name, role) %}…{% endmacro %}`.
- **Filters** in every templated string.
  `{{ value|default:"—" }}`, `{{ items|join:", " }}`,
  `{{ name|upper }}`. Tidies the `?? '(not set)'` patterns
  scattered through today's views. (Django colon-syntax for
  filter args — see §3.1.)
- **For/if syntax via `{% %}`.** Nests cleanly without the
  `{{ end }}` ambiguity of the current engine. Works in any
  templated string, including inline YAML leaves.

Note that `{% extends %}` and `{% include %}` are useful only
inside `.pongo` files (pongo2 needs a template loader to
resolve names). Inline templated strings in YAML can use
`{% if %}`, `{% for %}`, filters, and ternaries — but if you
want extends/include, factor the template into a
`<app>/views/*.pongo` file.

The YAML structure itself is never templated. The existing
YAML composition story (`internal/app/loader.go` imports,
namespaced cross-app composition — see commit
`7331630 feat: story imports`) is the mechanism for sharing
YAML; pongo2 plays no role there.

### 3.1 Template syntax — translation table

Applies to every YAML leaf string that currently contains `{{ }}`
template syntax (view bodies, element leaves, `say:`,
`guard_hint:`, dynamic `target:`, `set:` RHS, prompt args), and
to standalone `.pongo` files.

**Important — pongo2/v6 is Django-syntax, not Jinja2.** Filter
arguments use colon syntax (`|default:"x"`), there is no
expression-level ternary (must expand to `{% if %}`/`{% else %}`),
and the for-loop counter is `forloop.Counter` / `forloop.First` —
not `loop.index`.

| Today (expr-lang)                                   | After (pongo2/Django)                                                |
| --------------------------------------------------- | -------------------------------------------------------------------- |
| `{{ world.foo }}`                                   | `{{ world.foo }}` (identical)                                        |
| `{{ world.foo.bar }}`                               | `{{ world.foo.bar }}` (identical)                                    |
| `{{ world.x ? 'a' : 'b' }}`                         | `{% if world.x %}a{% else %}b{% endif %}` (no inline ternary)        |
| `{{ slots.foo ?? '(unset)' }}`                      | `{{ slots.foo\|default:"(unset)" }}` (colon, not parens)             |
| `{{ if world.foo != '' }}…{{ end }}`                | `{% if world.foo != '' %}…{% endif %}`                               |
| `{{ if x }}…{{ else }}…{{ end }}`                   | `{% if x %}…{% else %}…{% endif %}`                                  |
| `{{ range world.members }}{{ .name }}{{ end }}`     | `{% for m in world.members %}{{ m.name }}{% endfor %}`               |
| Inside range body: `{{ .name }}`                    | `{{ m.name }}` (loop var name, not bare dot)                         |
| Loop index / first inside `{% for %}`               | `{{ forloop.Counter }}` / `{% if forloop.First %}` (capital C / F)   |
| `{{ available('start_journey') }}`                  | `{{ available('start_journey') }}` (helpers stay exposed as callables) |
| `{{ blocked_reason('start_journey') }}`             | `{{ blocked_reason('start_journey') }}`                              |
| Helper filter form: `{{ x \| available }}`          | Not supported — use the call form `{{ available(x) }}` (pongo2 can't bind per-render filters) |

Member access via `.` works the same; map and struct fields look
identical to today. Note that struct fields reflect by **Go name**,
not by struct tag — `RunCtx{ID, Turn}` is exposed in the context
as `{"id": …, "turn": …}` by the render bridge so `{{ run.id }}`
keeps working.

The migration is mechanical. A `kitsoki migrate-templates`
codemod walks `testdata/apps/` and `stories/` and rewrites every
shape in the table above — every translation is deterministic.
Run it once, review the diff, commit. This catches the bulk of
view bodies, embedded `{{ if }}` conditionals (oregon-trail
intro, inbox), and the handful of nullish-coalesce shapes
(`{{ slots.foo ?? '…' }}` appearing in oregon-trail
`general_store.yaml:84-90`, etc.).

### 3.2 Where pongo2 renders

Pongo2 runs on every templated string field — not just views.
Concretely:

- **View bodies** — `view: <string>` (legacy form) and every
  leaf string inside a typed view element (`prose`, `heading`,
  `list.items[].label`, `list.items[].hint`, `kv.pairs[*]`,
  `code`, `template`).
- **`say:` strings** on effects.
- **`guard_hint:` strings** on transitions.
- **Dynamic `target:` paths** — e.g.
  `target: "{{ world.last_job_originating_state }}"` in
  inbox-style stackless teleports.
- **`set:` RHS** — e.g.
  `money: "{{ world.money - world.proposal_total_cost }}"`.
- **Initial-child selectors** when they're `{{ }}`-templated —
  e.g. river_crossing's
  `initial: "{{ if world.river_depth_ft < 3 }}shallow{{ else }}…{{ end }}"`.
- **Prompt-file bodies** read by `host.oracle.ask` and
  `host.oracle.ask_with_mcp`.
- **Standalone `.pongo` files** referenced via
  `template_file:` or `extends:` (§3.3).

Expression-evaluation fields keep using expr-lang and do NOT
go through pongo2:

- `when:` on transitions and on effects (their entire value is
  an expression, no `{{ }}` delimiters).
- Pure-expression initial selectors (when the value is a bare
  expression, not a template — though most authors use the
  template form, both are supported).
- Internal predicates in `internal/proposal/`, `internal/menu/`,
  `internal/mcp/` that consume compiled `expr.Program` values
  directly.

### 3.3 Block inheritance — apps and areas

The pongo2 loader resolves template names against a per-app
template root, conventionally `<app>/views/`:

```
stories/oregon-trail/
  app.yaml
  views/
    base.pongo            ← app-wide layout
    forts/
      base.pongo          ← area-level (extends base)
      store.pongo         ← per-state body (extends forts/base)
    partials/
      inventory.pongo     ← included by any room that wants it
```

An app-wide base view:

```jinja
{# stories/oregon-trail/views/base.pongo #}
{% block heading %}{{ state.description }}{% endblock %}

{% block status %}
Day {{ world.days_on_trail }}  |  {{ world.miles_traveled }} mi  |  ${{ world.money }}
{% endblock %}

{% block body %}{% endblock %}

{% block choices %}{% endblock %}
```

A state references it from YAML by adding an `extends:` line,
with element arrays slotted into named blocks:

```yaml
states:
  general_store:
    view:
      extends: "forts/store"
      blocks:
        body:
          - prose: >
              Matt's General Store. Stock up before you head out…
          - kv:
              pairs:
                Cash: "${{ world.money }}"
                Oxen: "{{ world.oxen }}    ($40 / yoke)"
        choices:
          - heading: "Actions"
          - list:
              items:
                - { label: "propose_purchase items=… total_cost=…" }
                - { label: "leave the store" }
```

Two equivalent author surfaces are supported:

1. **Pure element array with `extends:` + `blocks:` map**
   (above). Authors stay in YAML; the loader emits a pongo2
   template that fills the named blocks with rendered elements.
2. **Raw pongo2 file** referenced by
   `view: { template_file: "forts/store.pongo" }`. The file
   uses pongo2's full syntax (`{% extends %}`, `{% block %}`,
   `{% for %}`, `{% include %}`). Used when the author wants
   to author outside YAML.

Either form goes through the same render pipeline; the array
form is syntactic sugar over the raw form.

### 3.4 Helper functions and pongo2 context

The expr-lang Env exposes `available`, `blocked`,
`blocked_reason`, `intent_status` as bound helper closures
(`internal/expr/expr.go:80-90`). A new
`internal/render/pongoenv.go` builds a pongo2 `Context` from the
same `expr.Env`, registering the helpers as filters AND globals
so both call shapes work:

```jinja
{% if available('start_journey') %}…{% endif %}
{{ blocked_reason('start_journey') }}
```

Pongo2 context shape:

```go
pongo2.Context{
  "world":  env.World,
  "slots":  env.Slots,
  "event":  env.Event,
  "run":    env.Run,
  "menu":   env.Menu,
  "state":  map[string]any{"path": ..., "description": ...},
  "available":      env.Available,
  "blocked":        env.Blocked,
  "blocked_reason": env.BlockedReason,
  "intent_status":  env.IntentStatus,
}
```

Pongo2 reflects into Go maps and struct fields, so
`world.party_member_1`, `slots.cmd`, `run.id` all resolve the
same way they do under expr-lang.

### 3.5 Why expr-lang stays for guards and predicates

A pure-pongo2 world would let us delete `internal/expr`
entirely. Tempting, but rejected:

- **`when:` clauses are pure expressions, not templates.**
  Pongo2's expression sub-grammar (the part inside `{{ }}` /
  `{% if %}`) isn't a drop-in replacement for expr-lang. We'd
  be swapping one parser for another, in a context where
  templating doesn't apply, without simplification.
- **The AST whitelist is the security story.** `internal/expr`
  enforces a restricted expression language (see the package
  doc at `expr.go:1-37` — allowed builtins enumerated, lambdas
  / map literals / let-bindings forbidden, function calls
  restricted to the builtin set). Pongo2 isn't designed as a
  sandbox; re-implementing whitelisting on top of its parser
  is a project unto itself.
- **`when:` values move FSM state.** A guard that evaluates to
  true picks the next state. View text, by contrast, only
  displays. Keeping different engines for "moves state" vs
  "shows text" reflects the different blast radii honestly.

The line is clean: `{{ }}`-delimited interpolation → pongo2.
Bare-expression fields → expr-lang.

---

## 4. Renderer architecture

A new `internal/render/elements/` package, one file per element
kind. (The package lives at the render level rather than nested
under TUI so non-TUI callers — `internal/machine.renderViewBody`
and the orchestrator's pre-render seam — can dispatch typed
elements without inverting the dependency graph.)

```
internal/render/elements/
  element.go    // ViewElement interface + Render(width int) string
  prose.go      // word-wrap via lipgloss
  heading.go    // bold/colour styling
  list.go       // two-column lipgloss table, marker prefix
  kv.go         // key-column width = max(len(keys)); colon-aligned
  code.go       // monospace, no reflow, optional border
  template.go   // delegates to current Glamour renderer
```

`transcriptModel.renderMarkdown` (`transcript.go:245`) is renamed
`renderView` and dispatches: for a single `template` element it
calls today's Glamour path verbatim; otherwise it asks each
element to render at `viewportWidth - 2` and joins results with
one blank line.

Templating happens **before** element layout. Each leaf string
inside an element (`prose` body, `list.items[].label`,
`list.items[].hint`, `kv.pairs[*]`, `code`, `heading`, `template`)
is rendered through `render.View` — the new pongo2 bridge layer
(see §3). The element renderer then sees concrete text and lays
it out at the viewport width. The `when:` evaluator runs on the
raw expression against the same Env through `expr.EvalAny`
(unchanged path).

The pongo2 bridge owns one renderer per app, holding a per-app
template loader rooted at `<app>/views/`. The loader is invoked
both for `extends:` / `include:` references and for ad-hoc leaf
templates (which receive a synthetic name like
`inline:<state-path>:<element-index>` so error messages locate
the failure). In tests the loader is the cached variant; in
`kitsoki` interactive mode it's uncached so app-file changes
take effect on the next render without restart (ideas.md L37).

On resize (`tea.WindowSizeMsg` at `transcript.go:125-130`), the
renderer rebuilds at the new width by re-rendering each entry's
elements. Today the existing path stores the raw markdown source
and re-runs Glamour on resize; the typed path stores the parsed
element slice and re-renders that. Both coexist (an entry produced
by a `template`-only view rebuilds via Glamour; a typed entry
rebuilds via lipgloss).

Glamour stays in the dependency tree only for `template`. Prose,
list, kv, and code are pure lipgloss.

## 5. Worked examples

### 5.1 Cloak foyer — reflowing prose

Today (`testdata/apps/cloak/app.yaml`, hand-wrapped to 65ch):

```yaml
view: |
  You are in a spacious hall, splendidly decorated in red and gold,
  with glittering chandeliers overhead. The entrance from the street
  is to the north, and there are doorways south and west.
```

Migrated:

```yaml
view:
  - prose: >
      You are in a spacious hall, splendidly decorated in red and
      gold, with glittering chandeliers overhead. The entrance from
      the street is to the north, and there are doorways south and
      west.
```

(YAML `>` folds the wrapped lines into one logical paragraph.)
The renderer reflows to whatever the panel width is.

### 5.2 dev-story main — column-aligned action list

Today (`testdata/apps/dev-story/rooms/main.yaml:10-17`):

```yaml
view: |
  Welcome to dev-story. What would you like to do today?

  Available areas:
    - Start a new task          (jira search)
    - Continue existing task    (workspace manager)
    - Consult the Oracle        (general Q&A with Claude)
    ...

  Inbox: {{ world.inbox_unread }} unread.
```

Migrated:

```yaml
view:
  - prose: "Welcome to dev-story. What would you like to do today?"
  - heading: "Available areas"
  - list:
      items:
        - { label: "Start a new task",          hint: "jira search" }
        - { label: "Continue existing task",    hint: "workspace manager" }
        - { label: "Consult the Oracle",        hint: "general Q&A with Claude" }
        - { label: "Review a teammate's PR",    hint: "code review" }
        - { label: "Check my inbox",            hint: "notifications, background jobs" }
        - { label: "Prep for standup",          hint: "daily summary" }
        - { label: "Triage a production issue", hint: "incident room" }
  - kv:
      pairs:
        Inbox: "{{ world.inbox_unread }} unread"
```

The hint column auto-aligns at render time. Renaming an entry or
resizing the terminal does not require retuning spaces.

### 5.3 Terminal Room — code examples + KV status

Today (`testdata/apps/dev-story/rooms/terminal.yaml:9-18`):

```yaml
view: |
  Terminal Room.
  Workspace: {{ world.current_workspace }}

  Propose a command to run, e.g.:
    propose "list files in /tmp"
    propose "git status"
    propose "go test ./..."

  Last result: {{ world.proposal_result }}
```

Migrated:

```yaml
view:
  - heading: "Terminal Room"
  - kv:
      pairs:
        Workspace: "{{ world.current_workspace }}"
  - prose: "Propose a command to run, e.g.:"
  - code: |
      propose "list files in /tmp"
      propose "git status"
      propose "go test ./..."
  - kv:
      pairs:
        Last result: "{{ world.proposal_result }}"
```

`code` guarantees the example block survives in monospace at any
width. The KV elements coalesce visually because the renderer can
choose to leave only one blank line between same-kind neighbors;
authors get the layout they expect without hand-counting newlines.

### 5.4 Oregon-trail general store — stock readout + actions

Today (`stories/oregon-trail/rooms/general_store.yaml:53-69`):

```yaml
view: |
  Matt's General Store. Stock up before you head out — once you
  leave Independence there's no road back, only forts with markup.

  Cash:      ${{ world.money }}
  Oxen:      {{ world.oxen }}    ($40 / yoke)
  Food:      {{ world.food_lbs }} lbs  ($0.20 / lb)
  ...

  Actions:
    - propose_purchase items=... total_cost=...
      (auto-accepts when total_cost < $5; otherwise review/refine)
    - leave the store          (requires >= 2 oxen and >= 200 lbs food)
```

Migrated:

```yaml
view:
  - prose: >
      Matt's General Store. Stock up before you head out — once you
      leave Independence there's no road back, only forts with markup.
  - kv:
      pairs:
        Cash:           "${{ world.money }}"
        Oxen:           "{{ world.oxen }}    ($40 / yoke)"
        Food:           "{{ world.food_lbs }} lbs  ($0.20 / lb)"
        Bullets:        "{{ world.bullets }}    ($2.00 / box of 20)"
        Clothing:       "{{ world.clothing_sets }}  ($10.00 / set)"
        Spare wheels:   "{{ world.spare_wheels }}  ($10.00 each)"
        Spare axles:    "{{ world.spare_axles }}   ($10.00 each)"
        Spare tongues:  "{{ world.spare_tongues }} ($10.00 each)"
  - heading: "Actions"
  - list:
      items:
        - label: "propose_purchase items=… total_cost=…"
          hint:  "auto-accepts when total_cost < $5"
        - label: "leave the store"
          hint:  "requires ≥ 2 oxen and ≥ 200 lbs food"
```

The narration reflows; the stock readout aligns its colon column
to the longest key automatically; the action list pairs each
intent with its constraint hint.

### 5.5 Oregon-trail intro — conditional roster (pongo2 `for`)

Today (`stories/oregon-trail/rooms/intro.yaml:74-87`):

```yaml
Party of {{ world.party_size }}:
  1. {{ if world.party_member_1 != "" }}{{ world.party_member_1 }} (leader){{ else }}(unnamed){{ end }}
  2. {{ if world.party_member_2 != "" }}{{ world.party_member_2 }}{{ else }}(unnamed){{ end }}
  ...
Profession: {{ if world.profession != nil }}{{ world.profession }}{{ else }}(not yet chosen){{ end }}
Departure:  {{ if world.month != nil }}{{ world.month }}{{ else }}(not yet chosen){{ end }}

Choose:
  - name the party (or: name member N, or: generate names <theme>)
  ...
  {{ if available("start_journey") }}- start the journey{{ else }}- ✗ start_journey — {{ blocked_reason("start_journey") }}{{ end }}
```

Migrated (pongo2 templating in leaf strings):

```yaml
view:
  - heading: "Party of {{ world.party_size }}"

  # `code` element body is a pongo2 template — using {% for %}
  # collapses the five copy-pasted roster lines to one loop.
  # forloop.Counter / forloop.First are pongo2's loop helpers
  # (Django syntax — capital letter); filter arg uses colon.
  - code: |
      {% for m in world.party_members %}
        {{ forloop.Counter }}. {{ m.name|default:"(unnamed)" }}{% if forloop.First %} (leader){% endif %}
      {% endfor %}

  - kv:
      pairs:
        Profession: '{{ world.profession|default:"(not yet chosen)" }}'
        Departure:  '{{ world.month|default:"(not yet chosen)" }}'

  - heading: "Choose"
  - list:
      items:
        - "name the party (or: name member N, or: generate names <theme>)"
        - "pick a profession (banker / carpenter / farmer)"
        - "pick a departure month (march / april / may / june / july)"
        - label: "start the journey"
          when:  "available('start_journey')"   # expr-lang (guard)
        - label: "✗ start_journey — {{ blocked_reason('start_journey') }}"
          when:  "!available('start_journey')"  # expr-lang (guard)
```

The `code` element's body is a pongo2 template; `{% for %}`
plus `forloop.Counter` / `forloop.First` collapses five
hand-copied roster lines into one block that scales to any
party size. `|default:"(unnamed)"` replaces the
`?? '(unnamed)'` shape.

The per-item `when:` stays on expr-lang (it's a guard, same
contract as effect-level `when:`) and replaces the inline
`{{ if … }}{{ else }}{{ end }}` block. The renderer omits items
whose guard fails, which composes cleanly with the list's
alignment pass (no blank rows or stray hyphens).

Note: this assumes a small refactor of the world schema —
`party_members` as a list rather than `party_member_1..5` as
flat keys. The migration is independent of this proposal but
the `for`-loop benefit only materializes with a list-shaped
value. Until then, an `if`-chain is the pongo2 equivalent of
today's flat-key fan-out.

### 5.6 Block inheritance — every room sharing a status bar

The Oregon Trail rooms (intro, general_store, leg_a_executing,
fort, river_crossing, hunt, snow_blocked, …) all want a common
"day / miles / cash" status line. Today every room view repeats
the rendering by hand or skips it.

A base view declared once:

```jinja
{# stories/oregon-trail/views/base.pongo #}
{% block status %}
Day {{ world.days_on_trail }}  |  {{ world.miles_traveled }} mi  |
${{ world.money }}  |  {{ world.party_size }} on the wagon
{% endblock %}

{% block heading %}{{ state.description }}{% endblock %}

{% block body %}{% endblock %}

{% block choices %}{% endblock %}
```

A room then only declares what changes:

```yaml
states:
  intro:
    view:
      extends: "base"
      blocks:
        body:
          - prose: >
              It is {{ world.year }}. You are in Independence,
              Missouri, preparing to lead a wagon party of
              {{ world.party_size }} to the Willamette Valley.
        choices:
          - list:
              items:
                - "name the party"
                - "pick a profession"
                - "pick a departure month"
                - label: "start the journey"
                  when:  "available('start_journey')"
```

Renaming the status bar (add weather, drop the cash column,
change the separator) touches one file. A new room gets the
status bar for free with `extends: "base"`.

The dev-story equivalent is a shared "Workspace: x / Last
result: y / Inbox: N unread" footer that the Terminal Room, the
Workspace Manager, the Incident Room, and the Inbox all want and
today maintain by hand.

## 6. Adjacent issues this unblocks

- **Off-path banner** (`transcript.go:549`) and proposal-diff
  display can become their own element kinds (`banner`, `diff`)
  with proper styling instead of being shoved through the same
  Glamour pipe with ad-hoc lipgloss decoration.
- **Apply-proposal proposals** (the LLM-authored proposals
  pattern in `docs/proposals/ai-collaboration-proposal.md`) get a
  natural authoring surface: emit an element array, not a
  Markdown blob the LLM has to keep aligned by hand.
- **World display panel** (ideas.md L28) — once views are typed
  element arrays, the same `kv` / `list` renderers serve the
  world panel without a second styling layer.
- **`preserveLeadingIndent` hack** in `transcript.go:267-293` can
  retire entirely once Glamour only runs inside `template`. The
  hack exists because CommonMark collapses leading spaces;
  removing it removes a class of "why did my whitespace get
  eaten" authoring bugs.
- **Story extension / composition** (ideas.md L8-L9). Pongo2
  `{% extends %}` is the natural surface for a reusable dev
  story whose company- or project-specific variants override
  only the rooms / view blocks that differ. A `dev-story` base
  app declares `views/base.pongo`; a company fork
  `acme/dev-story` extends the rooms it wants to re-skin
  without forking the whole view tree.
- **Macros for repeated view shapes.** The "intent action with
  hint column" pattern recurs across nearly every room
  (dev-story main, terminal, oregon-trail every store and
  fort). A `{% macro intent_list(items) %}…{% endmacro %}` in
  `views/partials/` keeps the styling decision in one place.

## 7. Phased delivery

1. **Phase A — schema + loader.** Add `ViewElement` types in
   `internal/app/types.go`; custom `UnmarshalYAML` on the `View`
   field that accepts string-or-array (and the
   `{extends, blocks}` object shape). Load-time validation per
   element kind. No renderer changes yet — array form falls
   through to a `template` element internally.
2. **Phase B — pongo2 bridge.** New `internal/render/` package
   wrapping pongo2: env→context shim, helper registration
   (`available`, `blocked`, `blocked_reason`, `intent_status`),
   per-app template loader (`<app>/views/`). Swap **every**
   `expr.Render` call site to `render.Pongo` — view bodies in
   `machine.go:1167-1194` and `parallel.go:742-758`, dynamic
   targets in `machine.go:579` and `parallel.go:379,620`,
   `say:` and initial-child in `machine.go:880,1055,1101`,
   prompt-file bodies in `host/oracle_ask*.go:67,324,398`,
   flow-fixture interpolation in `testrunner/flows.go:1575`,
   phase-id in `app/phases.go:523`, metamode adapter in
   `metamode/adapter.go:236`. `expr.EvalAny` / `Compile`
   stay untouched — they keep serving `when:` guards and pure-
   expression fields.
3. **Phase C — template syntax codemod.** Ship
   `kitsoki migrate-templates`: walks every YAML under
   `testdata/apps/` and `stories/`, rewrites every leaf string
   that contains `{{ }}` per the §3.1 table. Scope covers
   view bodies, `kv.pairs[*]`, `list.items[*]`, `say:`,
   `guard_hint:`, dynamic `target:`, `set:` RHS, prompt args.
   Pure-expression fields (`when:`, bare-expression
   `initial:`) are left alone. Review the diff; commit.
   Existing apps render identically afterward.
4. **Phase D — element renderers.** New `internal/tui/elements/`
   package with `prose`, `heading`, `list`, `kv`, `code`,
   `template`. `transcriptModel.renderView` dispatches per
   element. Each leaf string goes through `render.View` before
   element-specific layout.
5. **Phase E — migrate one structured view.** Pick dev-story's
   `main` room — short, high-value, exercises `prose` + `heading`
   + `list` + `kv`. Verify column alignment survives resize.
6. **Phase F — migrate cloak foyer.** Prove the reflow story end
   to end. Delete the "cloak prose is narrow" caveat from
   `transcript.go:237-244` and from ideas.md.
7. **Phase G — element-level `when:`.** Wire the per-element
   guard for the common conditional-row pattern.
8. **Phase H — block inheritance.** Ship `extends:` + `blocks:`
   parsing in the view loader. Author the oregon-trail
   `views/base.pongo` status bar and migrate two rooms onto it
   as the validating case.
9. **Phase I — opportunistic migration.** Convert Terminal Room,
   river crossings, general store, intro on demand as other
   work touches those files. No big-bang sweep.

Each phase is independently shippable. A → B → C is the
prerequisite chain for pongo2 view rendering; D+ stack the
typed-element story on top. Phases C and F are the visible-to-
authors moments; everything else is invisible if it works.

## 8. Open questions

- **Markdown inside elements?** A `prose` value of `"This is
  **bold**."` — does the renderer parse inline Markdown? Cheapest
  answer: yes, via a thin lipgloss-based inline renderer (not
  Glamour). Defer until the first author asks for it.
- **Default element when a `view:` string contains list-shaped
  Markdown?** The string form maps to `template` and stays on
  Glamour — no auto-detect heuristics. Authors opt in to typed
  elements explicitly.
- **Per-state `relevant_world` vs `kv` elements.** Today
  `relevant_world` declares which world keys appear in the
  location indicator. Once `kv` elements exist, there's a
  tempting overlap. v1 keeps them separate: `relevant_world`
  drives the location indicator (a sidebar); `kv` drives the
  body of the view. Unifying them is a follow-up.
- **`code` element styling.** Bordered box (like Glamour's code
  fence) or bare-monospace indent? Prefer bare-monospace for the
  Terminal Room's example shape; bordered as opt-in.
- **Author-side YAML ergonomics.** The migrated examples are
  more verbose than the originals. Acceptable for views that pay
  for the verbosity in resize-correctness; the string form
  remains the default for short or stable views.
- **Pongo2 sandbox posture.** Pongo2 isn't a sandbox — `{% include %}`
  reads files, custom filters execute Go, and a hostile template
  has the same blast radius as any code in the repo. Since
  templates ship from the same source tree as the app code, this
  is acceptable for v1. If imported stories from untrusted sources
  ever land (see ideas.md L8-L9 — extensible / composable
  stories), we'll need a per-app template-root jail at minimum
  and probably a pongo2-tag whitelist on top.
- **Template hot-reload.** ideas.md L37 wants reload of app
  changes without quit/restart. Pongo2 has a `MustCacheLoader` /
  uncached loader split — phase B picks uncached in dev, cached
  in tests / production. Worth confirming the perf hit is
  negligible (views render once per turn).
- **Error UX for template failures.** Expr-lang errors today
  surface as `expr eval %q: %w`. Pongo2 errors include line /
  column. The bridge layer should wrap pongo2 errors with the
  app-relative template path so authors see
  `views/forts/store.pongo:14:8: unknown filter "wat"` instead
  of a raw stack trace.
- **`for` loops vs flat-key world fan-out.** §5.5 assumes
  `world.party_members` is a list. Migrating world shape is out
  of scope here but enabled by the new rendering: the
  conditional-row pattern (today's
  `{{ if world.party_member_N != "" }}…{{ end }}` × 5) is the
  motivating case for switching to list-shaped world keys.

## 9. Non-goals

- Replacing Glamour wholesale. `template` keeps it available.
- Templating the YAML structure itself. Pongo2 renders string
  values; it never sees the YAML document tree. YAML
  composition stays on `internal/app/loader` imports.
- Pongo2-flavored guards. Per-element `when:`, transition
  `when:`, and effect `when:` all stay on expr-lang. Pongo2
  takes over `{{ }}`-delimited templating; expr-lang keeps
  bare-expression evaluation. See §3.5.
- Retiring `internal/expr`. `EvalAny`, `Compile`, the AST
  whitelist, and the env shape all stay. Only the `Render`
  template engine and its tmplTree helpers retire.
- Touching the off-path / proposal / oracle entries' rendering
  outside of `template`. Migrating those is in scope for a later
  proposal once this one ships.
- Auto-detecting Markdown structure inside `template`. No magic
  inference; if you want typed layout, use typed elements.
- A custom DSL for layout. Pongo2's `{% extends %}` /
  `{% block %}` is the layout DSL.

---

## Implementation notes

Concrete touchpoints when the phases land:

- **Phase A (types + loader).**
  `internal/app/types.go:255-285` — replace `View string` with a
  `View` wrapper holding a parsed `[]ViewElement`, an optional
  `Extends string` / `Blocks map[string][]ViewElement`, and the
  original source string (for serialization round-trips and
  trace). `internal/app/loader*.go` — wire the new
  unmarshaller; add per-kind validation to
  `loader_test.go`-adjacent tests.
- **Phase B (pongo2 bridge).** Add `github.com/flosch/pongo2/v6`
  to `go.mod`. New `internal/render/` package: `Pongo(src
  string, env expr.Env) (string, error)` and
  `PongoFile(templatePath string, env expr.Env) (string, error)`,
  plus an app-scoped `Renderer` constructor that owns the
  per-app loader rooted at `<app>/views/`. Swap **every**
  `expr.Render` call site to `render.Pongo` (full list in §7
  phase B). `internal/expr/expr.go` keeps `EvalAny`, `Compile`,
  `Program`, and the AST whitelist — only `Render` and its
  tmplTree helpers retire.
- **Phase C (codemod).** Standalone tool (e.g.
  `cmd/kitsoki-migrate-templates/`) that walks every YAML under
  `testdata/apps/` and `stories/`, rewrites the §3.1 patterns in
  every leaf string that contains `{{ }}`. Pure-expression
  fields (`when:`, bare-expression initial selectors) are left
  alone — the tool needs an allowlist of "templated string"
  field paths to know what to rewrite. Run, review the diff,
  commit.
- **Phase D (element renderers).**
  `internal/tui/transcript.go:93-103,125-130,198-208,245-258` —
  rename `renderMarkdown` to `renderView`; dispatch on element
  kind; keep Glamour construction behind a `template`-only
  branch. New `internal/tui/elements/` package with `prose`,
  `heading`, `list`, `kv`, `code`, `template`. Each leaf string
  passes through `render.View` before layout.
- **Phase F cleanup.**
  `internal/tui/transcript.go:267-293` — `preserveLeadingIndent`
  retires once all non-`template` content stops going through
  Glamour. Move under the `template` branch in the interim.
- **Docs.** Update `docs/stories/authoring.md` once phase D ships with
  at least one migrated view. The string form stays
  documented; the array form and the pongo2 syntax cheatsheet
  (§3.1) are documented alongside. Add a `docs/views.md`
  topic doc once block inheritance lands in phase H.
