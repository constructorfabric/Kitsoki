# TUI: `/model` and `/provider` slash commands

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [`dynamic-model-and-provider-control.md`](dynamic-model-and-provider-control.md)
**Depends on:** [`harness-profiles.md`](harness-profiles.md) — the selection
state + `Selection/SetSelection/Profiles` API this surface drives.

## Why

An operator running a session in the TUI cannot see or change which
backend/model is answering. The choice is made once via `--oracle` /
`--claude-model` at launch and is invisible thereafter — there's no way to
say "switch to codex for the next turn" or even "which model am I on?"
without killing the session and re-reading the launch command.

## What changes

Two slash commands, mirroring the established pattern: `/provider` lists the
declared harness profiles and selects one; `/model` lists the active
profile's model catalog and selects one. Both echo the current selection
when run bare, take an argument (name or 1-based index) to switch, and
accept a raw-axis form (`/provider backend=codex`) for power users. The
change lands on the **next** turn (the substrate's next-turn semantics);
the active selection is shown in the help/status output and reflected in the
trace.

One sentence: **`/provider` and `/model` make the harness selection a
first-class, list-and-pick slash command alongside `/intents` and `/world`.**

## Impact

- **Code:** `internal/tui/` — a new `commands_harness.go` implementing the
  command(s); a `case "/model"` / `case "/provider"` arm in
  `handleSlashCommand` (`tui.go:1919`+); a help row in `commands_help.go:28`.
- **Rendering:** reuse the typed `blocks.MenuAction` + `r.Menu(rows)` list
  element (`internal/tui/blocks/render.go:353`) exactly as `/intents` does
  (`commands_actions.go:53`) — numbered, with the active row marked
  available/selected. No hand-rolled strings.
- **Input:** these are `ChatBlockCommand`s (`internal/tui/commands.go:33`) —
  synchronous, preemptive, bypass the LLM queue (`tui.go:1736`), same as the
  other read/echo commands. Selecting calls `SetSelection` then re-renders
  the block.
- **Docs on ship:** `docs/tui/README.md` (the two commands + the active
  selection display).

## Mental model

The operator thinks in two knobs — *provider/harness* and *model* — and
each is a list they can see and pick from, just like the action menu. The
profile names (`synthetic-codex`, `llama-local`) are the headline; backend
jargon stays hidden unless they ask for it via the raw form.

## Layout

```
> /provider

  Harness profiles:                       > /model
  1. claude-native        (active)
  2. synthetic-claude                        Models for synthetic-codex:
  3. synthetic-codex                         1. hf:Qwen/Qwen2.5-Coder-32B  (active)
  4. codex-native                            2. hf:meta-llama/Llama-3.3-70B
  5. llama-local
                                             pick:  /model 2  ·  /model hf:meta…
  pick:  /provider 3  ·  /provider synthetic-codex
  raw:   /provider backend=codex
```

## Rendering changes

`renderHarnessBlock()` builds `[]blocks.MenuAction` from
`orchestrator.Profiles()` (name → `Label`, `Available=true`, the active one
flagged in its label) and calls `r.Menu(rows)` — identical shape to
`renderActionsBlock` (`commands_actions.go:53`). `/model` builds its rows
from the active profile's `models` catalog (or a single
"(backend default)" row when the profile declares none — epic Q2). Nothing
new in the rendering layer; this is a new consumer of the existing typed
menu element.

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| `/provider` | list profiles, mark active | bare = echo current selection |
| `/provider <name\|N>` | `SetSelection(profile)` | next-turn; rejects unknown names with an error block |
| `/provider <axis>=<val>` | raw override (e.g. `backend=codex`) | synthesizes a `(custom)` selection (substrate Q2) |
| `/model` | list active profile's models, mark active | bare = echo current model |
| `/model <id\|N>` | `SetSelection(profile, model)` | rejects a model not in the profile's catalog |

The dispatch arm sits in `handleSlashCommand` (`tui.go:1919`+) next to the
existing `case "/intents"`; the help text gains two rows in
`commands_help.go:28`. Selecting is synchronous: validate → `SetSelection`
→ re-render the block showing the new active row. No queue interaction
(these never wait on an LLM).

## Rendering tests

These commands don't touch concurrent I/O (no slog-vs-render interleave —
they render a single block synchronously), so the non-negotiable
combined-I/O test doesn't apply. A standard block-render assertion suffices:

- `commands_harness_test.go` — given a fake orchestrator exposing three
  profiles with #2 active, assert `/provider` renders the typed menu with
  row 2 marked active, and `/provider 3` flips the active marker and calls
  `SetSelection("…3…")`. Pure render + API-call assertion, no LLM, no I/O
  race surface.

## Migration plan

Additive — no existing surface is replaced. The commands appear in `/help`
once shipped; sessions launched without `harness_profiles:` show a single
default-profile row (the substrate's compat default), so the commands are
never empty or broken.

## Tasks

```
## 1. Render
- [ ] 1.1 renderHarnessBlock() / renderModelBlock() building []blocks.MenuAction
- [ ] 1.2 (reuse r.Menu — no new typed element)

## 2. Drive
- [ ] 2.1 commands_harness.go (ChatBlockCommand); /model + /provider arms in handleSlashCommand
- [ ] 2.2 Argument parse: name | 1-based index | axis=val raw form; validation errors as a block
- [ ] 2.3 Help rows in commands_help.go

## 3. Prove + document
- [ ] 3.1 commands_harness_test.go (render + SetSelection assertion; no LLM)
- [ ] 3.2 Manual run; screenshot both blocks
- [ ] 3.3 Update docs/tui/README.md; trim this proposal
```

## What we lose, honestly

Two more commands to learn and keep in `/help`. The raw-axis form
(`/provider backend=codex`) leaks the internal taxonomy the profiles exist
to hide — acceptable because it's opt-in and power-user-only, but it is a
second mental model living next to the curated one.

## Open questions

1. **Echo vs picker for `/model` when the profile has no catalog.** Show
   "(backend default)" + a hint, or hide `/model` entirely for catalog-less
   profiles? *Lean:* show the single row + hint (discoverable, honest).
2. **Confirmation on switch.** Switch silently, or print "→ next turn uses
   synthetic-codex"? *Lean:* print the one-line confirmation (next-turn
   semantics are non-obvious; say so).

## Non-goals

- The web equivalent — that's slice [`web-harness-control.md`](web-harness-control.md).
- A full-pane `DedicatedViewCommand` picker; the inline menu is enough.
