# TUI: decision-first detail — lead with the choice, not the prompt

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   ../trace-introspection.md

<!-- "TUI" here means the runstatus web SPA (the operator inspection
     surface), per docs/tracing/run-status-ui.md — not the chat TUI. The
     rendering-through-typed-elements rule is a Vue-component concern: data
     in, no hand-built HTML strings. -->

## Why

When an operator clicks a gate or a routed turn in `runstatus`, the detail
pane leads with the **prompt and response** — the same thing every LLM tool
shows. But the most valuable thing in a kitsoki trace is the *decision*: a
`machine.gate_decided` event already carries `available_intents`,
`chosen_intent`, `confidence`, `decider`, `bailed_to_human`, and `reason`
(`internal/orchestrator/decider.go:260-273`), and `turn.start` carries the
routing tier + match confidence. This is precisely what Langfuse's generic
spans *cannot* show (`.context/langfuse-trace-viewer-comparison.md`, idea
#3 — "where we beat Langfuse"). Burying it under the prompt sells the moat
short and makes confidence — a bare float today — illegible.

## What changes

One sentence: **for any `decision`- or `routing`-category event (slice #1),
hero the detail pane with the decision — available intents → chosen intent →
confidence rendered as a bar against the configured threshold → reason →
bailed-to-human? — and demote the prompt/response to a collapsed "show
evidence" drawer.**

The operator should be able to answer "what did the engine decide, how sure
was it, and why" without expanding anything, and "what evidence did it see"
with one click.

## Impact

- **Code:** `tools/runstatus/src/components/agent/DecideDetail.vue` (today
  shows choices + decision + prompt/response tabs — reorder + add the
  confidence bar); a new `RoutingDetail.vue` for `turn.start` routing
  provenance; `EventDetail.vue` dispatch (route `decision`/`routing`
  categories to these); the existing `CollapsibleText.vue` for the evidence
  drawer.
- **Rendering:** new typed Vue components, data-in only — no hand-built
  markup strings. The confidence bar is a styled element bound to
  `confidence` and `threshold`, not a printf.
- **Input:** none (read-only surface).
- **Docs on ship:** `docs/tracing/run-status-ui.md` (decision-first detail).

## Mental model

A decision row reads like a verdict card: *"`proposing` → chose **accept**
(0.92, threshold 0.80) — proposer reasoning held; did not bail."* The prompt
that produced it is evidence you can unfold, not the headline.

## Layout

```
Before (DecideDetail today):        After:
┌─ decide · proposer ───────────┐   ┌─ decide · proposing ───────────────┐
│ [Prompt] [Response] [Object]  │   │ accept  ▓▓▓▓▓▓▓▓░░ 0.92  (thr 0.80) │
│ ┌───────────────────────────┐ │   │   chose: accept   decider: llm      │
│ │ <prompt text, long>       │ │   │   reason: proposer reasoning held…  │
│ │                           │ │   │   available: accept · refine · cancel│
│ └───────────────────────────┘ │   │   bailed to human? no               │
│ choices: …                    │   ├─────────────────────────────────────┤
│ decision: { … }               │   │ ▸ show evidence (prompt · response) │
└───────────────────────────────┘   └─────────────────────────────────────┘
```

## Rendering changes

- **`DecideDetail.vue`** — reorder so the verdict block is first. Render
  `confidence` as a horizontal bar filled to `confidence`, with a tick at
  `threshold`; color the bar pass/fail relative to threshold (the
  semantics, not a per-row "execution level" — Langfuse's success/error
  coloring was *refuted* in the survey, so we anchor to threshold, which we
  actually have). `available_intents` render as chips with the chosen one
  emphasized; once slice #4 lands, each chip shows its runner-up score and
  the bar becomes a ranked ladder. `bailed_to_human` renders an explicit
  badge. Prompt/response move into a `CollapsibleText` "show evidence"
  drawer, collapsed by default.
- **`RoutingDetail.vue`** (new) — for `turn.start`: a tier badge
  (`routed_by`), `match_type`, and a confidence bar (same component as
  above); `direct` shown when routing was bypassed.
- **Threshold source.** The bar needs the decider's `Threshold`
  (`decider.go:60`, default `0.8` at `:41`). It is already in the
  `gate_decided` payload only implicitly (confidence vs. an unstated floor) —
  see Open question 1.

## Input & commands

None — read-only. No slash commands or keybindings.

| Command / key | Does | Notes |
|---|---|---|
| (click a `decision`/`routing` row) | opens decision-first detail | replaces the prompt-first pane |
| (click "show evidence") | unfolds prompt/response | collapsed by default |

## Rendering tests

This surface is the runstatus SPA (no concurrent stdout/stderr + View()
interleaving), so the CLAUDE.md combined-I/O rule doesn't bite; the relevant
guard is **Playwright + Vitest** snapshot/DOM assertions, which the
runstatus suite already uses.

- `decision-detail.spec.ts` (Playwright, artifact mode) — load the bugfix
  fixture, click a `gate_decided`/decide row: assert the verdict block
  renders **above** the evidence drawer, the confidence bar's fill width and
  threshold tick match the recorded values, and prompt text is hidden until
  "show evidence" is clicked. Verified to fail against the current
  prompt-first `DecideDetail`.
- `DecideDetail`/`RoutingDetail` Vitest unit tests — confidence-bar
  geometry (fill %, threshold position) as a pure function of
  `(confidence, threshold)`; pass/fail color classification at the boundary.

## Tasks

```
## 1. Render
- [ ] 1.1 ConfidenceBar element (fill = confidence, tick = threshold, pass/fail color) + Vitest geometry test
- [ ] 1.2 Reorder DecideDetail: verdict block first (available→chosen→reason→bailed), prompt/response into a collapsed evidence drawer
- [ ] 1.3 New RoutingDetail for turn.start (tier badge + match_type + confidence bar)
- [ ] 1.4 EventDetail dispatch routes `decision`/`routing` categories (slice #1) to these components

## 2. Drive
- [ ] 2.1 (none — read-only surface)

## 3. Prove + document
- [ ] 3.1 decision-detail.spec.ts (Playwright) — verdict-first layout, bar geometry, evidence collapsed; verified to fail without the change
- [ ] 3.2 Re-render the bugfix artifact; eyeball a decide row leads with the decision
- [ ] 3.3 Update docs/tracing/run-status-ui.md; trim/delete this slice
```

## What we lose, honestly

The prompt — which power users *do* read first when debugging a bad
generation — now takes one extra click. We accept this: the decision is the
differentiated value, the evidence drawer is one keystroke, and a debugger
chasing a specific prompt can still filter to `agent-call` rows where the
prompt leads.

## Open questions

1. **Is `threshold` in the `gate_decided` payload?** The bar needs it. Today
   the payload (`decider.go:261-271`) carries `confidence` but the threshold
   is configured separately (`DeciderConfig.Threshold`, `:60`). *Lean: add
   `threshold` to the `gate_decided` payload at emit time — a tiny producer
   addition; if it's deferred, the SPA falls back to the documented default
   `0.8` and renders the tick as "default".*
2. **Routing confidence field name.** `turn.start` carries routing
   provenance per the survey; confirm the exact attr keys (`routed_by`,
   `match_type`, `confidence`, `direct`) against the live emitter before
   binding the component.

## Non-goals

- **Emitting ranked alternatives** — that's slice #4 (runtime). This slice
  renders whatever the verdict carries; the chips upgrade to a ranked ladder
  automatically when #4 lands.
- **The kind taxonomy** — slice #1 supplies the `decision`/`routing`
  categories this dispatches on.
- **Replaying the decision** — slice #6.
