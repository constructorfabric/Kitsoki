# Story style guide

How a kitsoki story should look. Short by design — when a new room
doesn't fit a section below, copy the closest Oregon Trail room and
edit. That's the style guide in one sentence.

The gold standard is [`stories/oregon-trail/`](../../stories/oregon-trail/).
[`stories/robbery/`](../../stories/robbery/) shows the same shape in a
small sub-story. The typed-element schema reference lives in
[`embedded/app-schema.md`](../embedded/app-schema.md).

---

## 1. Where color comes from

Authors don't write color. They pick elements; the renderer paints.
The palette lives in
[`internal/tui/styles.go`](../../internal/tui/styles.go) and the
element renderers under
[`internal/render/elements/`](../../internal/render/elements/).

| Visual | Means | Where it lands |
|---|---|---|
| Emerald, **bold** | Section header / available action | `heading:` elements; `list:` items whose guards pass |
| Red, *italic* | Blocked action | `list:` items whose `available(...)` is false |
| Violet, **bold** | Chrome — location bar, prompt cursor, turn header | `status` block, the input line |
| Amber, ***bold italic*** | Off-path mode, guard-failure hint | `guard_hint:` text, off-path banner |
| Blue, **bold** | Slash-command echo | `/meta`, `/onpath`, `/trace` output |
| Gray, *italic* | Soft "didn't catch that" | Clarification card |

If you want emphasis, **choose the right element**. There is no
inline bold or color in author strings — that's a feature, not a gap.

---

## 2. The shape of a room

Every room extends `views/base.pongo`. The base defines five blocks;
override only the ones you need:

```
status   — top status line (cash, day, picked ticket, …)
heading  — room title (default: state.description)
body     — main narration / status content
choices  — action menu
footer   — optional bottom matter
```

Inside `body` and `choices`, prefer **typed elements** over a string
`view: |`. The kinds, in order of how often you'll reach for them:

| Kind | Use for |
|---|---|
| `prose:` | One paragraph of narration. Reflows. |
| `heading:` | Section break. Not bulleted. No trailing colon. |
| `list:` | Bulleted actions or enumerations. Optional aligned `hint:` column. |
| `kv:` | Short key/value status. Key column auto-aligns. |
| `code:` | Layout-preserved content (tables, ASCII art, `{% include %}`). |
| `template:` | Escape hatch — raw pongo, today's pipeline. |

The canonical pattern (from `stories/oregon-trail/rooms/intro.yaml`):

```yaml
view:
  extends: "base"
  blocks:
    body:
      - prose: >
          It is {{ world.year }}. You are in Independence, Missouri,
          preparing to lead a wagon party of five to Oregon.
      - heading: "Party of {{ world.party_size }}"
      - kv:
          pairs:
            Profession: '{{ world.profession|default:"(not yet chosen)" }}'
            Departure:  '{{ world.month|default:"(not yet chosen)" }}'
    choices:
      - heading: "Choose"
      - list:
          items:
            - "pick a profession (banker / carpenter / farmer)"
            - label: "start the journey"
              when:  "available('start_journey')"
            - label: "✗ start_journey — {{ blocked_reason('start_journey') }}"
              when:  "!available('start_journey')"
```

Rules of thumb:

- One paragraph = one `prose:`. Blank lines between are automatic.
- `heading:` for section breaks. Never bullet them. No trailing colon.
- If you're hand-aligning columns inside a `prose:` or string view,
  you picked the wrong element — use `kv:` or `list:` with a hint.
- Shared content (an inventory readout used in three rooms) goes in
  `views/partials/*.pongo` and is `{% include %}`d from a `code:`
  element. See [`stories/oregon-trail/views/partials/inventory.pongo`](../../stories/oregon-trail/views/partials/inventory.pongo).

---

## 3. Action menus

The `choices` block is almost always:

```yaml
choices:
  - heading: "Actions"
  - list:
      items: [...]
```

Conventions:

- **The intent name is the label.** Use the bare name (`pay`, `ford`,
  `accept_purchase`) — don't wrap it in backticks, don't paraphrase
  it as a sentence. The TUI styles it.
- **Hint = cost or consequence**, not a restatement. "buy them off
  (\${{ world.threat_level * 50 }})", not "pay them money".
- **Availability has two standard shapes — pick one:**

  *Affordability* — the action is always offered, the hint shows
  the cost. Use mutually-exclusive `when:` guards on two copies:

  ```yaml
  - label: "pay"
    hint:  "buy them off (${{ world.threat_level * 50 }})"
    when:  "world.party_money >= world.threat_level * 50"
  - label: "pay"
    hint:  "not enough money (${{ world.threat_level * 50 }} needed)"
    when:  "world.party_money < world.threat_level * 50"
  ```

  *Prerequisite* — the action is named-but-not-yet-doable, and the
  label should grey out. Use the `available()` / `blocked_reason()`
  helpers and the `✗` prefix:

  ```yaml
  - label: "start the journey"
    when:  "available('start_journey')"
  - label: "✗ start_journey — {{ blocked_reason('start_journey') }}"
    when:  "!available('start_journey')"
  ```

  Use room-level `prerequisites:` instead when the missing thing is setup or
  readiness for the whole room, not just one action. A prerequisite is declared
  beside the state, evaluates deterministically from `world.*`, and the runtime
  renders the warning/help block consistently in TUI, web, screenshots, and flow
  output:

  ```yaml
  prerequisites:
    - id: project-onboarding
      title: "Project onboarding"
      satisfied_when: "world.init_profile_status == 'applied'"
      summary: "Local setup has not been applied."
      action:
        label: "onboard"
        intent: go_init
  ```

- **`look` is always last** and always `target: .`. It needs no hint.
- Don't number the items. Selection highlight is the renderer's job.

---

## 3.4 User messaging

Messaging is part of the story contract. A room must keep the operator oriented
from the first visible turn: what Kitsoki understood, which path it selected,
what mode/consequence applies, and what the next action is. The standard is
terse and informative, not chatty.

This applies especially to free-form captures, contextual routers, imported
story handoffs, workbench rooms, authoring/builder stories, long-running host
calls, and any room that moves from read-only exploration to write-capable work.

Required signals:

- **Interpreted input** — the captured request, ticket, document path, proposal,
  or resolved slots the story will use.
- **Selected path** — the story/import/state/workbench the request is entering.
  If the story inferred a route, name it before it starts work.
- **Mode and consequence** — read-only vs write-capable, branch/workspace, cost,
  external post, or destructive action.
- **Next action** — the thing now happening, or the operator action that is
  actually required.

Use `view` for durable orientation and `say:` for one-turn acknowledgements:

- Put persistent facts in typed elements, usually `kv:`:

  ```yaml
  - kv:
      pairs:
        Request: '{{ world.landing_request|default:"(none)" }}'
        Story: "story_authoring"
        Story root: '{{ world.working_dir|default:"(not yet chosen)" }}'
        Mode: "read-only until edit approval"
        Next: '{{ world.next_action|default:"(pending)" }}'
  ```

- Use a short `say:` when a capture or handoff immediately dispatches another
  room/agent, so the transcript contains an acknowledgement before the operator
  loses control:

  ```yaml
  - set: { landing_request: "{{ slots.request }}" }
  - say: "Request captured for story_authoring; starting read-only intake."
  ```

- Use `say:` for outcomes, route changes, and recoverable failures. Do not use
  it as the only place a durable value appears; a later `look` must still show
  the state.

Only interrupt when the operator must decide or supply data. Stop for missing
required inputs, ambiguous routes with materially different outcomes, write or
external side effects, destructive actions, high-cost calls, or policy/security
gates. Do not stop just to confirm values that are already clear and safe; show
the interpretation and continue. A confirmation turn with one safe forward path
is ceremony (see [`authoring.md` §3.1](authoring.md#31-avoid-ceremony-steps)).

Good operator-facing messages are concrete:

| Weak | Better |
|---|---|
| `Processing...` | `Request captured for bugfix; searching local tickets.` |
| `Starting agent.` | `Dispatching read-only landing agent for: {{ slots.request }}` |
| `Done.` | `Story authoring result saved; review, refine, or clear.` |
| `Failed.` | `Ticket search failed: {{ world.last_error }}. Retry or update provider setup.` |

Do not narrate implementation mechanics unless the operator can act on them.
Provider names, route ids, call ids, and trace internals belong in the view only
when they explain the next action or a failure. Keep messages to one sentence
unless the room is a review surface.

Flow fixtures should prove the messaging, not just the state transition:

```yaml
turns:
  - intent: { name: landing_capture, slots: { request: "implement .context/proposal.md" } }
    expect_events:
      - kind: machine.say
        effect: { text: "Request captured for story_authoring; starting read-only intake." }
    expect_view_matches: "Story root[\\s\\S]*read-only[\\s\\S]*implement \\.context/proposal\\.md"
```

For generated or imported rooms, test both the child story directly and the
imported/root-prefixed surface so aliasing does not erase the user's context.

---

## 3.5 The view MUST always render to something visible

The single worst failure mode in a kitsoki story is a room whose
view renders to **zero bytes**. The user is dropped into a blank
screen with no narration, no status, no action menu, no prompt — no
way to tell what state the session is in or what to type next. They
quit, restart, or grep the trace. This has happened. Defend against
it.

Why blanks happen:

- A pongo2 expression inside `view: |` errors (missing key, wrong
  type, malformed `|default:` chain, `{% for %}` over a non-iterable).
  The orchestrator's render-after-bind path silently swallows the
  error and leaves `res.View` empty — there is no diagnostic in the
  trace beyond `view_bytes: 0`.
- A typed `view: extends: base` whose every block evaluates to empty
  (every `when:` guard false, every `prose:` body empty) renders the
  base template with empty slots — also unrecoverable from the user's
  side.
- A host call binds `nil` or `null` into a world key the view then
  iterates over.

**Rules to keep a view from blanking:**

1. **Prefer typed `view: extends: "base"` over `view: |` strings.**
   A single bad `{{ … }}` in a legacy string kills the entire render.
   Typed elements isolate failures to one element at a time and the
   surrounding chrome (location bar, prompt, action menu) keeps the
   user oriented.

2. **Never make the action menu conditional on world state.** Even
   if the room can't show its body, the user must still see
   `Reply: \`continue\` · \`restart_from stage=…\` · \`quit\``. Put
   the menu in a `choices:` block (always rendered) or as a
   non-guarded `prose:` line at the bottom of `body:`. If the user
   doesn't know what to type, the session is unrecoverable.

3. **Pongo2 `{% for %}` over a possibly-absent key needs an
   `{% if %}` guard, OR use a typed `list:` element with a `from:`
   reference.** The `list:` form tolerates `nil` / missing keys —
   the legacy `{% for x in world.maybe_missing %}` form will render
   to empty under common world shapes.

4. **`{% if world.foo == false %}` distinguishes "missing", "null"
   and "false" differently in pongo2. Prefer `{% if !world.foo %}`
   or move the check into a typed element's `when:`.** Equality
   against literal booleans is a frequent silent-blanker source.

5. **Every room's `view:` must produce ≥ 1 line of text against an
   empty world (`{}`).** If your view depends on a host call having
   already returned, write a *pending-state* version that renders
   before the bind happens (see §4 placeholders). Tests:
   `internal/machine/view_elements_test.go` exercises the typed
   path; add an analogous case if you're writing a legacy-string
   view that you can't migrate today.

6. **Treat `view_bytes: 0` in the trace as a P0 bug.** It is never
   correct for a turn that lands in a non-exit state. If you see it,
   the view template errored — find it by re-rendering against the
   logged world. (Authors: prefer `extends: base` so this can't
   happen silently.)

The implementing-room regression of 2026-05-19 — user typed
`continue` on a fix-proposal, the agent ran for 50 seconds and
returned a valid artifact, the room transitioned successfully — but
the view template hit a pongo2 error and the orchestrator silently
zeroed it. The user described it as "dumped into nothingness."
Don't ship a room whose view can do that. Use `extends: base` and
let the chrome carry the floor.

---

## 3.6 Interactive `choice:` widgets

A `choice:` element is an interactive in-transcript picker. Three
modes — `single`, `multi`, `form` — share one envelope and dispatch
an intent directly, short-circuiting the semantic router. Schema
reference: `kitsoki docs app-schema` §`choice:`. Author cookbook:
[`choice-widget.md`](choice-widget.md).

Prescriptive guidance for when you reach for one:

- **Prefer `choice:` over a prose hint + free-text routing whenever
  the room's `on:` arc set is enumerable and stable.** A `list:` of
  inert labels paired with a "type the action" prose hint is the
  legacy pattern; convert it to a `choice:` and you save an LLM
  call per turn on the cold-route path AND give the user a
  first-class affordance. If the action set changes on every entry
  (a dynamic ticket list, a free-form composition), keep the prose
  hint.
- **`prompt:` is sentence-case, action-oriented, no trailing
  punctuation.** "Choose a profession", "Select symptoms",
  "Compose your purchase". Not "Choose a profession:" and not
  "What is your profession?".
- **`label:` is terse, one phrase, and mirrors the underlying
  intent's name where possible.** `"pay"` rather than `"pay them
  off"`; `"accept"` rather than `"yes, accept the proposal"`. The
  intent name is the label by convention; if you paraphrase, the
  user has to translate.
- **`hint:` carries cost / availability / consequence — not a
  restatement of the label.** Good: `"${{ world.threat_level * 50 }}
  starting cash — easy"`. Bad: `"pick the banker option"`.
- **Per-item `when:` — pick one of two patterns:**

  *Hide-when-unavailable* — drop the row entirely. Use when showing
  the option would mislead (e.g. an action that depends on a flag
  the user hasn't seen yet).

  ```yaml
  items:
    - { label: "drink", intent: drink, when: "world.canteen > 0" }
  ```

  *Show-disabled-with-reason* — keep the row visible but greyed,
  with the reason in the label. Use when the user benefits from
  knowing the option exists (affordability splits, unmet
  prerequisites). Follow the `✗ <intent> — <reason>` convention
  from §3 above.

  ```yaml
  items:
    - { label: "pay", intent: pay, when: "world.money >= cost" }
    - label: "✗ pay — not enough money (${{ cost }} needed)"
      intent: pay
      when: "world.money < cost"
  ```

- **Multi-mode `min:` / `max:`** — set them when the dispatched
  intent has hard requirements. Omit them when "any selection is
  fine" (the widget defaults to `min: 0`, `max: len(visible)`). Use
  `min: 1` when the intent's slot is required; `min: 0` when an
  empty list is a meaningful submission ("none of these apply").
- **`param: { required: false }` for optional free-form args.**
  Common on `refine` rows where an LLM judge has already filled in
  a reason — Enter accepts the judge's text, type-then-Enter
  overrides. Use the `(optional — …)` placeholder convention so the
  user sees the affordance.
- **Form-mode field type:**
  - `string` for free text and identifiers.
  - `enum` when the value comes from a short fixed list — gives
    the user a cycle-on-Space picker rather than a typo-prone text
    field.
  - `int` / `float` for numeric input — also gives the user
    `min:` / `max:` bounds.
  - `bool` for toggles — Space flips. (Note: a `bool` world key
    set from `"{{ slots.x }}"` will fail because template
    substitution always produces strings — store as `string` or
    coerce in an effect.)
- **Form-mode `default:`** — set when there is a sensible starting
  value (a recommendation, the last-used value, a world-derived
  number). Omit when 0 / "" should look distinct from "the user
  typed 0 / ""." Combine with `required: true` to mean "we suggest
  X, but you must confirm or change it."
- **`readonly:` form fields** — use sparingly and only when the
  value is *genuinely derived* from world state (a running total,
  a computed cost). If you find yourself writing `readonly: true,
  expr: "'a literal label'"`, you want a `prose:` element above
  the choice instead. Readonly fields ARE submitted when the
  intent declares a matching slot — confirm this is what you want
  before adding one.

Authoring checklist (additive to the §7 list):

- [ ] The room's `on:` arc set is enumerable. (If not, use a
      prose hint + router.)
- [ ] `prompt:` is sentence case, action-oriented, no trailing
      `:` or `?`.
- [ ] Each `label:` mirrors the intent name or is a one-phrase
      action verb.
- [ ] Each `hint:` adds cost / consequence info — never restates
      the label.
- [ ] Disabled rows use the `✗ <intent> — <reason>` shape from §3.
- [ ] Multi-mode `min:` matches the intent's slot-required-ness.
- [ ] Form-mode enums prefer `type: enum` over `type: string +
      validator`.
- [ ] No `readonly:` field whose `expr:` is a literal — use
      `prose:` instead.

Worked end-to-end reference: [`testdata/apps/choice_smoke/`](../../testdata/apps/choice_smoke/)
covers every feature combo across 23 spokes.

---

## 3.7 `media:` — showing a recorded artifact

A `media:` element is how a room says "here is the file this step
produced." It is display-only — no input bar, no intent dispatch. The
TUI renders a labeled pointer block (path to the `.artifacts/` file);
the web UI renders the artifact inline (`<video>`, `<img>`, PDF/HTML
embed). Full schema reference: `kitsoki docs app-schema` §`media:`.

**Canonical usage pattern:**

1. A step invokes `host.artifacts_dir` with `src_path` set to the file
   the step produced. The handler copies the file under `.artifacts/`,
   records an `artifact.emitted` trace event, and returns a stable
   `handle`. Bind the handle into world:

   ```yaml
   - invoke: host.artifacts_dir
     with:
       src_path: "{{ world.output_path }}"
       kind:     video
       label:    "Session walkthrough"
       thread:   walkthrough
     bind: { walkthrough_handle: handle }
   ```

2. Reference the handle in a `media:` element. Guard the element so it
   stays invisible before the artifact is ready:

   ```yaml
   view:
     extends: "base"
     blocks:
       body:
         - prose: >
             The walkthrough recording is ready.
         - media:
             handle:  "{{ world.walkthrough_handle }}"
             caption: "Session walkthrough"
             kind:    video
             when:    "world.walkthrough_handle != ''"
   ```

`handle` is required. `caption` (optional) overrides the default label.
`kind` (optional) picks the TUI pointer icon and the web embed strategy;
values: `video`, `image`, `pdf`, `html`, `slideshow`.

Add a `when:` guard to suppress the element when the artifact is not yet
ready (a world var bound by the preceding host call), following the same
pattern as §4 placeholders.

Cross-references:

- [`embedded/app-schema.md`](../embedded/app-schema.md) §`media:` — full
  field reference and emit-then-reference example.
- [`docs/architecture/hosts.md`](../architecture/hosts.md#hostartifacts_dir)
  §`host.artifacts_dir` — how to emit an artifact and obtain a handle.
- [`docs/tracing/trace-format.md`](../tracing/trace-format.md) — the
  `artifact.emitted` trace event shape.

---

## 3.8 The view must read as plain text

A story runs on more surfaces than the TUI. Most sessions are carried
by plain-text comment threads — Jira, Bitbucket, Slack/Teams, email —
where there is no color, no alignment guarantee, and no interactive
widget. The view you author is *projected* onto each surface: the TUI
gets the polish, a text transport gets a Markdown serialization of the
same element tree. **Author for the text projection and let the TUI add
polish on top** — never the reverse. (Why this is a hard requirement,
not a nicety: [`../architecture/transports.md` §7](../architecture/transports.md#7-every-story-must-work-text-only).)

Concretely:

- **Never put essential meaning in color.** The emerald/red action
  palette (§1) and source-color (§8) are *redundant* cues — a blocked
  action is already prefixed `✗` and names its reason in text. If "it's
  red" is the only signal, the comment-thread reader misses it.
- **Typed elements over `view: |` strings** (§2, §3.5): they serialize
  to clean Markdown. Layout you hand-align inside a `code:` block or
  ASCII table survives only in a monospace surface — keep
  layout-critical status in `kv:` / `list:`, which reflow anywhere.
- **`choice:` (§3.6) and `media:` (§3.7) are enhancements, not the only
  path.** A `choice:` widget collapses to nothing on a comment thread,
  so the same intents must stay reachable by typing — keep the `list:`
  of action labels (or a prose hint) so the text reader still sees the
  menu; the router/classifier resolves the typed reply. A `media:`
  embed degrades to a path pointer, so put the artifact's *meaning* in
  a neighboring `prose:`, not only in the embed.
- **The reply prompt is always text.** Per §3.5 rule 2 the action menu
  / reply line renders unconditionally — on a comment thread that line
  is the user's entire interface.

---

## 4. Placeholders for empty / pending values

Lowercase, in parentheses. The pongo `|default` filter is the standard
splice:

| What | Render as |
|---|---|
| Unset configurable value | `(not yet chosen)` |
| Empty list | `(empty — type \`tickets\` to search)` |
| Awaiting host result | `(pending)` |
| Not applicable here | `(n/a)` |
| Nothing / null / absent | `(none)` |

```yaml
- kv:
    pairs:
      "Picked ticket":  '{{ world.ticket_id|default:"(none — pick one below)" }}'
      "Workspace":      '{{ world.workdir|default:"(none — created when you drive)" }}'
```

Parentheses + lowercase signals "this is metadata about the missing
value, not part of it".

---

## 5. Narration voice

Two voices, one per story. **Don't mix them inside a room.**

- **In-character** (Oregon Trail's Matt, Robbery's masked rider).
  Full sentences. Quoted dialog gets its own `prose:` so the blank
  line above the quote falls naturally.
- **Operator-facing** (dev-story, bugfix, implementation, kitsoki-dev).
  Terse, declarative, no in-world voice. "Bug-fix pipeline parked.
  Waiting for `start`." — not "The bug glares at you menacingly."

`say:` effects follow the same split. Oregon Trail says
`"Matt licks his pencil and writes it down."`; bugfix says
`"Cycle 1 ✓ reviewing passed."`

---

## 6. Cases without a standard yet

These shapes recur across stories but have no canonical presentation
today. Each deserves a one-time decision. **Listed for discussion —
not yet adopted.**

1. **Awaiting an external result** (LLM, host invoke, background job).
   Today bugfix's `proposing_executing` shows
   `Summary: (pending)` plus a freeform "(artifact pending — agent
   has not returned yet)" paragraph. Other stories say nothing.
   *Proposed:* a `kv:` entry with value `(pending — <what's running>)`,
   or a future `pending: true` flag on `prose:`/`kv:` that the
   renderer styles in muted gray.

2. **Live result lists from `iface.X` calls** (dev-story `my_tickets`,
   `ticket_results`). Today: hand-rolled `code: |` with a `{% for %}`
   loop and inline status badges.
   *Proposed:* a `list:` element variant that consumes a world array
   directly — `list: { from: world.my_tickets, label_field: title, hint_field: status }`.
   Until then, copy the dev-story `code:` shape verbatim instead of
   reinventing it.

3. **Status bars on operator stories.** Oregon Trail and Robbery have
   one (`Day · miles · cash · party · landmark`); dev-story, bugfix,
   implementation don't.
   *Proposed:* every story's `base.pongo` provides a `status` block.
   Dev-story shows `Picked · Workspace · Branch · PR`; bugfix shows
   `Ticket · Stage · Cycle · Mode`. Separator is ` · ` (middle dot)
   for content, `  |  ` (pipe with spaces) only for the wide marquee
   shape.

4. **Multi-field verdicts.** Bugfix `proposing_awaiting_reply` packs
   verdict + confidence + reason into one prose line with `{% if %}`
   gymnastics.
   *Proposed:* whenever you have ≥ 3 named fields, use a `kv:` block.

5. **Confidence / progress indicators.** Stored as floats; today
   rendered as floats (`0.83`).
   *Proposed:* always render as `83%` (`{{ int(world.confidence * 100) }}%`),
   or a `★★★★☆` glyph bar — pick one across all stories and stick
   with it.

6. **Outcome / end-state rooms** (`done`, `@exit:*`). Each story
   styles its own.
   *Proposed:* one `heading:` ("Done" / "Killed" / "Resolved"), one
   `prose:` paragraph, one `kv:` with the lifted outcome fields, one
   `list:` of next-step choices (usually just `leave`/`go_main`).

7. **In-character vs system lines in the same transcript.** Stories
   that import a narrative sub-story into an operator host (kitsoki-dev
   importing dev-story which imports bugfix) currently let both
   voices land in the same scrollback.
   *Proposed:* multi-voice stories pick a prefix glyph (e.g. `▸` for
   system) or route system lines to a separate transport. Single-voice
   stories don't need anything.

8. **Per-pipeline infrastructure plumbed through every room.** The
   bugfix story carries `workspace_id`, `workdir`, `feature_branch`
   through every room view AND threads them into every
   `iface.workspace.*` / `iface.vcs.*` call as `with:` args — plus a
   `when: world.workspace_id != ''` guard on every host call to
   "degrade gracefully" if the workspace wasn't set up. This is
   leaky: workspaces should be a pipeline-level concept (set up
   once when the pipeline enters its first room, torn down at
   `@exit:*`), not a per-room obligation. Authors end up re-stating
   the same plumbing in five rooms; a missed guard silently bounces
   to idle; the `view:` shows infrastructure detail the user has no
   say in.
   *Proposed:* a pipeline-scoped `setup:` block on the parent state
   (or on the import) that runs once on entry and exposes the
   resulting context via implicit keys handlers can read without the
   room having to pass them. Until that exists: keep all workspace
   plumbing in one room (`idle.yaml` for bugfix today), and treat
   the per-room re-creation as a workaround for a missing
   abstraction, not a pattern to copy.

---

## 7. Quick author checklist

Before you ship a new room:

- [ ] `view: extends: "base"` — not a string `view: |`.
- [ ] Each paragraph is its own `prose:`.
- [ ] Section breaks are `heading:`, no trailing colon.
- [ ] Status data is in a `kv:`, not hand-aligned in prose.
- [ ] Actions are a `list:` with `hint:` for cost / consequence.
- [ ] Empty / pending values use the parenthetical-lowercase
      placeholders from §4.
- [ ] `look` is the last action and `target: .`.
- [ ] You haven't reached for ANSI escapes or backticks-around-intent-names.
- [ ] The view renders to ≥ 1 visible line against an empty world
      (`{}`). Action-menu / reply prompt is unconditional.
- [ ] No `{% for %}` over a world key that might be absent / nil.
      Either guard with `{% if %}` or use a typed `list: from: …`.
- [ ] The view reads on a plain-text surface (§3.8): no meaning
      carried only by color or a `choice:`/`media:` widget; every
      intent reachable by typing.

---

## 8. Source-color: template vs LLM

When a view mixes templated text with LLM-generated text, the two are
distinguished by **terminal background color**, not punctuation or
quoting. **Authors write nothing special** — the LLM operator labels
its own output at the result boundary, the labels survive pongo
rendering and transcript flush, and the final paint lands at the TUI
write seam.

| Source | Background | Why this hue |
|---|---|---|
| Templated / deterministic | Cool slate `#2a3550` | The scaffolding. Predictable. |
| LLM-generated / interpretive | Warm bronze `#5c3e28` | Generative. "Examine before trusting." |
| Chrome / outside the view | Terminal default | Frames, status bars, prompt. |

Two render modes, picked automatically by the renderer:

- **Inline.** An LLM value substituted into a single field (`{{
  world.ticket.title }}` where `title` came from an LLM call) switches
  bg only around the LLM bytes; the rest of the line stays cool.
- **Block.** An LLM value containing newlines gets each contained
  line padded to the transcript's wrap width so the warm band reads
  as a solid rectangle. A "shoulder" row pads above and below the
  block so the boundary is unmistakable.

Nesting works through a background-color stack. Entering a span pushes
its bg; exiting pops and restores the parent's bg — never a bare
reset. So an LLM tool-call that quotes earlier LLM output stays warm
through the inner exit.

**Wire mechanism (engine-internal).** The pipeline has four seams:

| Seam | What happens | Lives in |
|---|---|---|
| Operator | `cr.Stdout` and `cr.Answer` are wrapped before being stored as `Result.Data["stdout"]` / `["answer"]`. Structured payloads (`Result.Data["submitted"]` from the MCP validator, `Result.Data["stdout_json"]` from `output_format=json`) get every string leaf wrapped recursively via `WrapTree` — bugfix-style flows bind individual fields (`world.x.summary_markdown`) and need them tagged too. | `internal/host/agent_ask.go`, `agent.go`, `agent_ask_with_mcp.go` |
| Render | Pongo substitutes the wrapped value into the view template. Sentinels are zero-width Unicode runes (`U+2063 U+2061 U+2061 U+2063` open, `U+2063 U+2062 U+2062 U+2063` close), so pongo's HTML auto-escape leaves them alone. | `internal/render/pongo.go` |
| Outbound prompt | Sentinels are stripped immediately after the prompt template is rendered, before the prompt crosses back into claude. Bound LLM values keep their tags for the display path; claude doesn't see them. | `internal/host/agent_ask.go`, `agent_ask_with_mcp.go` |
| Hardwrap | The transcript pre-wraps each entry to the viewport width before queueing. Sentinels add zero visible width, so width-based wrapping cannot bisect one. | `internal/tui/transcript.go queue()` |
| Paint | `FlushPending` runs the joined buffer through `sourcecolor.Colorize` immediately before `tea.Println`. Sentinels become ANSI bg switches with stack-aware nesting and block padding. | `internal/tui/transcript.go FlushPending()` |

Plain-text consumers that must not see sentinels strip them with
`sourcecolor.Strip` (current strip points: the `chat`/`chat queue`
CLI JSON output, the on-complete notification truncate path, and the
meta-mode adapter that feeds replies back to claude).

**Preview the rendering:**

    go run ./cmd/source-color-demo
    go run ./cmd/source-color-demo -theme=high-contrast
    go run ./cmd/source-color-demo -theme=light
    go run ./cmd/source-color-demo -all
    go run ./cmd/source-color-demo -fill-template

The demo wraps hand-fed strings with the same `sourcecolor.Wrap` the
LLM operators use in production, then runs them through the same
`Colorize` the TUI calls at flush — there is no second implementation
to drift.

**API surface for new code paths:**

- `sourcecolor.Wrap(s)` — call when emitting any string whose
  provenance is "an LLM produced this." Empty strings are a no-op.
- `sourcecolor.Strip(s)` — call before sending the string to a
  non-terminal consumer (shell JSON, an outbound prompt to claude,
  any byte/rune-based truncate).
- `sourcecolor.Colorize(s, theme, opts)` — call at the final
  terminal-write seam. The package wires this in for the transcript;
  other write paths can opt in.
