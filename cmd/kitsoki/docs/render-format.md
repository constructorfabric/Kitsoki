# Kitsoki — Rendered-Docs Format

`kitsoki render <app.yaml>` produces a Markdown document describing the app.
The output is a **one-way work product**: the engine never reads it back.
`app.yaml` stays the source of truth. This doc describes what's in the
output so humans reading it and LLMs acting on proposals about it know
what to expect.

Companion docs:

- `apply-proposal` — how to use rendered docs + a prose proposal to drive
  a YAML edit with an LLM
- `app-schema` — the authoritative YAML schema
- `llm-guide` — top-level CLI operator guide

---

## 1. Sections (in order)

1. **Title block** — app title, version, author, license.
2. **Overview** — app id, entry room, counts of rooms/intents/world vars,
   host allow-list.
3. **State Diagram** — Mermaid `flowchart LR` with every state as a node
   and every transition as a labelled edge. Guards appear in brackets;
   `(default)` branches are annotated.
4. **World Variables** — table of `name | type | default | values`.
5. **Intents** — one H3 per intent (alphabetical), with description,
   priority, hidden flag, examples, and a slot table if any.
6. **Rooms** — one H3 per room (alphabetical, nested rooms written as
   `bar.dark`), with description, initial child (compound), shows, view,
   on-enter effects, and a transition table.
7. **Off-path** — trigger/banner/return (if the app declares `off_path:`).
8. **Generated-by footer** — reminder that this file is derived.

Anchor ids are stable: `#room-<slug>` for rooms, `#intent-<slug>` for
intents, where `<slug>` is the engine name with `_`/`.`/`/` all mapped to
`-`. Transition cells link to target rooms by anchor.

---

## 2. Transition table shape

Each room's transitions render as:

```
| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`go`](#intent-go) | `slots.direction == 'south'` | [`bar`](#room-bar) | |
| 2 | [`go`](#intent-go) | _default_ | [`foyer`](#room-foyer) | say "You can't go that way." |
```

Columns:

- **#** — 1-based display index. Not an id; numbering may shift if the
  author reorders transitions. For stable references in a proposal, cite
  the intent name and the target rather than the number.
- **Intent** — linked to the intent's H3 anchor.
- **Guard** — the `when:` expression, or `_default_` for catch-alls, or
  blank if unguarded.
- **→** — the `target:` state, linked to its room anchor when resolvable
  (templates and relative paths aren't linked).
- **Effects** — comma-separated effect clauses; `_hint: …_` for
  `guard_hint`, `_(no-history)_` when `push_history: false`.

---

## 3. Effect clauses as rendered

| YAML                                                   | Rendered                                  |
|--------------------------------------------------------|-------------------------------------------|
| `set: {wearing_cloak: false}`                          | `` set `wearing_cloak = false` ``         |
| `increment: {disturbance: 1}`                          | `` increment `disturbance += 1` ``        |
| `say: "You hang the cloak."`                           | `say "You hang the cloak."`               |
| `emit: tick`                                           | `` emit `tick` ``                         |
| `invoke: host.run` + `with: {cmd: "ls"}`               | `` invoke `host.run` with `cmd = "ls"` `` |
| `bind: {result: stdout}` on the same invoke            | `` ..., bind `result ← stdout` ``         |
| `on_error: error_state`                                | `` ..., on_error → `error_state` ``       |
| `guard_hint: "…"` on the transition                    | `_hint: …_` in the Effects cell           |
| `push_history: false` on the transition                | `_(no-history)_`                          |

Template expressions (`{{ slots.direction }}`, etc.) are rendered verbatim.

---

## 4. Naming conventions in the rendered doc

The rendered doc uses the app's **engine-facing names** (from `app.yaml`):

- Rooms are referenced by their state name (`foyer`, `bar.dark`).
- Intents by their intent name (`go`, `hang_cloak`).
- World variables by their world key (`wearing_cloak`, `disturbance`).

There are no invented identifiers. When writing a proposal, refer to these
same names.

---

## 5. What is NOT in the rendered doc

- Implementation status markers. YAML is authoritative; if it's in the
  YAML it exists. There's no `[x]`/`[ ]` convention.
- Phase / release tokens.
- Parseable grammar constraints. The rendered Markdown is free to contain
  tables, Mermaid, bold/italic prose, and HTML anchors — none of which
  must round-trip to YAML.
- Comments from the YAML source. Comments live in YAML only.

---

## 6. Output conventions

- Default output: stdout. Redirect with `-o <path>`.
- Common convention in a repo: place the rendered doc next to `app.yaml`
  as `APP.md` or `README.md`.
- Treat the rendered file as a build artifact: regenerate on every YAML
  change (pre-commit hook, `go generate`, or CI). Do not hand-edit it.

---

## 7. Regeneration recipe

```sh
# Pre-commit hook (shell):
kitsoki render path/to/app.yaml -o path/to/APP.md
git add path/to/APP.md

# Or via go:generate in a Go package that hosts the app:
//go:generate kitsoki render app.yaml -o APP.md
```

---

## 8. Diffs

Because the output is deterministic (alphabetical ordering, stable
transition numbering within a room), diffs reflect real content changes:

- Added transition → one new row in the affected room's table.
- Renamed state → renames appear in every cross-reference.
- Removed intent → deletion of the intent's H3 and every reference row.

Review rendered-doc diffs alongside YAML diffs on PRs.
