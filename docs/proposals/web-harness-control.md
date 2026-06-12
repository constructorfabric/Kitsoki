# Web: harness profile & model control

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui (web operator surface)
**Epic:**   [`dynamic-model-and-provider-control.md`](dynamic-model-and-provider-control.md)
**Depends on:** [`harness-profiles.md`](harness-profiles.md) — the
`Selection/SetSelection/Profiles` API; parity target is
[`model-provider-commands.md`](model-provider-commands.md).

## Why

The web UI has **no notion of slash commands** — input flows over JSON-RPC
(`runstatus.session.{turn,submit,offpath}`,
`internal/runstatus/server/server.go:719,735,790`) and there is no config
control at all (`internal/webconfig/webconfig.go:40` carries only
`story_dirs`). So the TUI's `/model` / `/provider` have no web counterpart:
a web operator cannot see or change which harness/model is answering. This
slice gives the web UI the same control through its own idiom — a header
picker backed by a new RPC method — so the two surfaces reach parity.

## What changes

A new RPC method `runstatus.session.set_selection {session_id, profile,
model?}` writes the active selection via the substrate API; a read method
(or an additive field on the existing session-state payload) exposes the
available profiles + current selection. The SPA
(`tools/runstatus/src/`) renders a **header picker**: a provider dropdown
listing profiles and a dependent model dropdown listing the active
profile's catalog, with the live selection shown beside the transcript.
Switching takes effect next-turn (the substrate's semantics) and the chosen
profile shows on each subsequent call's trace row.

One sentence: **the web UI gets a header profile/model dropdown backed by a
`set_selection` RPC, reaching parity with the TUI's `/model` and
`/provider`.**

## Impact

- **Code:**
  - Server: a `case "runstatus.session.set_selection"` arm in `dispatch`
    (`internal/runstatus/server/server.go:578`+), routed to the session
    entry's substrate API; profiles + current selection added to the
    session-state RPC payload.
  - SPA: a header control in `tools/runstatus/src/` (the profile/model
    dropdowns) and the RPC call in the data layer
    (`tools/runstatus/src/data/live-source.ts`).
  - Embed: rebuild + re-embed the web bundle (the go:embed staging step —
    `make web-dev` for HMR; the binary serves `internal/runstatus/web/assets`,
    so `dist` must be copied there for `kitsoki web`).
- **Rendering:** standard SPA dropdowns; no Go-side typed-view work (web is
  the TS SPA, not the pongo2 TUI path).
- **Input:** new RPC method; the dropdowns call it on change. No effect on
  `turn`/`submit`/`offpath`.
- **Docs on ship:** the web surface doc (alongside `docs/web/` or the web-ui
  narrative) + a row in the harness-profiles architecture doc noting the
  two surfaces.

## Mental model

Same two knobs as the TUI, rendered as the web's native control: a provider
dropdown and a model dropdown in the header, always showing the live
selection. The operator never types a command — they pick from a menu.

## Layout

```
┌─ kitsoki web ─────────────────────────────────────────────┐
│  story: dev-story      [ provider: synthetic-codex ▾ ]     │
│                        [ model:    Qwen2.5-Coder-32B ▾ ]   │
├───────────────────────────────────────────────────────────┤
│  …transcript…                                             │
│   • oracle.decide · profile=synthetic-codex · model=…      │  ← trace row shows it
│  > input                                                   │
└───────────────────────────────────────────────────────────┘
```

## Rendering changes

The header gains two `<select>` controls populated from the session-state
payload's new `profiles` + `selection` fields. Changing the provider
dropdown repopulates the model dropdown from that profile's catalog (or
disables it / shows "backend default" when the profile declares none — epic
Q2) and fires `set_selection`. The active selection renders beside the story
name. Secret `env` values are never sent to the client — the payload carries
only profile name, backend, model, and catalog.

## Input & commands

| Control | Does | Notes |
|---|---|---|
| provider dropdown | `set_selection {profile}` | next-turn; repopulates model dropdown |
| model dropdown | `set_selection {profile, model}` | disabled/“default” when no catalog |

| RPC method | Shape | Notes |
|---|---|---|
| `runstatus.session.set_selection` | `{session_id, profile, model?}` → `{selection}` | validates via substrate; error on unknown profile/model |
| session-state payload | `+ {profiles[], selection}` | additive; existing fields unchanged |

## Rendering tests

The web surface is exercised by the existing web spec harness, not the
TUI's combined-I/O analyzer. A spec asserts: the header renders the
profiles from a seeded `.kitsoki.yaml`; selecting `synthetic-codex` fires
`set_selection` and the model dropdown repopulates; the active selection
text updates. Mind the known web-spec timing seams (observer rows lag the
trace RPC by an SSE tick; click `.trace-timeline__row-main`, not the
expanded row — see memory). No real LLM: a cassette session backs the spec.

## Migration plan

Additive — the header control appears only when `harness_profiles:` is
declared (otherwise the single default-profile row, matching the TUI). The
`set_selection` RPC is new, so no existing client call changes. Ship behind
the same bundle rebuild every web change needs.

## Tasks

```
## 1. Server
- [ ] 1.1 set_selection dispatch arm → substrate SetSelection; validation errors as RPC errors
- [ ] 1.2 Add profiles[] + selection to the session-state payload (no env values)

## 2. SPA
- [ ] 2.1 Header provider + model dropdowns; dependent repopulation; live selection display
- [ ] 2.2 live-source.ts set_selection call on change
- [ ] 2.3 Rebuild + re-embed bundle (web staging)

## 3. Prove + document
- [ ] 3.1 Web spec: render profiles, switch, model repopulate (cassette-backed, no LLM)
- [ ] 3.2 Update web surface docs; trim this proposal
```

## What we lose, honestly

A second control to keep in sync with the TUI command (two surfaces, one
substrate — drift risk if the API grows). The header gets busier; on narrow
viewports the two dropdowns compete with the story name for space (a
responsive concern the UI review should catch).

## Open questions

1. **Read path: new RPC vs payload field.** Fold profiles/selection into
   the existing session-state payload, or add a dedicated
   `session.get_selection`? *Lean:* additive payload field (one round-trip,
   already streamed).
2. **Raw-axis override on web.** The TUI exposes `/provider backend=codex`;
   does the web need an "advanced" disclosure for raw axes, or is the
   curated dropdown enough for v1? *Lean:* curated only on web in v1; raw
   axes stay TUI-only until asked for.

## Non-goals

- The TUI commands — slice
  [`model-provider-commands.md`](model-provider-commands.md).
- Editing/creating profiles from the UI; profiles are authored in
  `.kitsoki.yaml` (substrate non-goal).
