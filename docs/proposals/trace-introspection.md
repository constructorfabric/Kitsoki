# Epic: Trace introspection — decision-first, multi-view, annotatable run viewing

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 6 (0/6 shipped)

## Why

`runstatus` already shows a kitsoki run as a state diagram + an event
timeline + a detail drawer (`docs/tracing/run-status-ui.md`), and the trace
it reads is *richer than what comparable tools capture*: every interpretive
decision lands as a labeled datapoint — `machine.gate_decided` carries the
decider, the available intents, the chosen one, and confidence
(`internal/orchestrator/decider.go:260`); `turn.start` carries the routing
tier and match confidence; `turn.end.view` carries the literal text the
operator saw. A survey against Langfuse's trace viewer
(`.context/langfuse-trace-viewer-comparison.md`) confirms the data model is a
genuine moat — Langfuse has no decision-provenance equivalent, and our
embedded-story deterministic replay is strictly stronger than its session
replay.

But the *presentation* under-sells the data. The same survey found five
gaps where a mainstream viewer is simply nicer to look at, plus two places
where we capture less than we should:

- One projection only. We have `duration_ms` on every agent/host call but
  render a vertical list — no latency **waterfall**, no co-equal **view
  modes** (Langfuse keeps Tree/Timeline/Log/Graph at parity).
- Event rendering is keyed off ad-hoc per-component matching, not a
  **semantic kind taxonomy**, so badging/coloring/collapsing is bespoke.
- The detail drawer **leads with the prompt**, burying the decision — the
  one thing we have and Langfuse doesn't.
- We discard the decide agent's **runner-up scores** (only the winner is
  kept), so a confidence number has nothing to be relative to.
- We capture **no scores / human annotation**, so traces can't become a
  labeled eval/training set — directly against the "every decision is a
  labeled datapoint → self-improvement" commitment.
- No "**replay this decision**" — Langfuse's Open-in-Playground re-runs a
  generation; our trace is deterministic *and* carries the story, so we
  could re-run one call against a different operator and diff.

This epic closes those gaps, leaning into the decision-provenance moat
rather than copying Langfuse feature-for-feature.

## What changes

Once every slice ships, a `runstatus` viewer (live or exported artifact)
will let an operator:

- **See one trace four ways** — the existing grouped timeline (Tree), a
  duration-keyed **waterfall** (Timeline), and the existing Mermaid diagram
  promoted to a co-equal **Graph** tab, plus a sortable/filterable **trace
  list** on Home to triage many runs — all pure projections of the
  immutable event stream.
- **Read every event through a semantic kind** (decision / agent-call /
  host-call / narration / world-mutation / routing) so rows badge, color,
  and collapse consistently and a future graph lays out by kind.
- **Land on the decision** for a gate/routing event — available → chosen →
  confidence-vs-threshold → reason → bailed-to-human — with prompt/response
  demoted to a "show evidence" drawer, and the confidence bar made legible
  by the decide agent now emitting **ranked alternatives**.
- **Annotate** a turn/gate with a score + label + comment, recorded as a
  new read-only operator-metadata event kind — closing the loop from trace
  to labeled dataset.
- **Replay a single recorded decision** against a different operator (Claude
  / local-model / human) or an edited prompt, and diff the verdict — the
  natural UI for the pluggable-operator moat.

## Impact

- **Spans:** tracing (2 slices), tui (2 slices), runtime (2 slices).
- **Net surface:** one semantic-kind map shared by Go + the SPA; one new
  read-only event kind (`trace.annotation`); a ranked-alternatives field on
  the decide verdict + `gate_decided`; new `runstatus.*` RPC + write path
  for annotations and single-call replay; multiple new Vue view-mode and
  detail components. No change to the deterministic-replay border for the
  *story* itself — annotation and replay are operator metadata / sandboxed
  re-dispatch, not story mutation (keeps meta-mode read-only,
  `feedback_meta_mode_readonly`).
- **Docs on ship:** `docs/tracing/run-status-ui.md` (view modes, decision
  detail, annotation, replay), `docs/tracing/trace-format.md` (kind
  taxonomy, `trace.annotation`, alternatives field),
  `docs/stories/state-machine.md` (decide alternatives).

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Observation kinds | tracing | A semantic kind taxonomy over `EventKind` so consumers badge/color/collapse/lay-out by category | — | Shipped; see `internal/store/observation.go`, `tools/runstatus/src/lib/observation.ts`, and `docs/tracing/trace-format.md` | — |
| 2 | Decision-first detail | tui | Hero the gate/routing detail with the decision; demote prompt/response to an evidence drawer | 1 (soft), 4 (soft) | Draft | [`trace-decision-detail.md`](trace-decision-detail.md) |
| 3 | Multi-view + waterfall | tui | Co-equal view modes (Tree / Timeline-waterfall / Graph) + a filterable trace-list triage table | 1 (soft) | Draft | [`trace-view-modes.md`](trace-view-modes.md) |
| 4 | Decide alternatives | runtime | The decide agent emits ranked runner-up scores; recorded in the verdict + `gate_decided` | — | Draft | [`decision-alternatives.md`](decision-alternatives.md) |
| 5 | Scores & annotation | tracing | A read-only `trace.annotation` event kind + an annotate surface; traces become a labeled dataset | — | Draft | [`trace-annotation.md`](trace-annotation.md) |
| 6 | Replay a decision | runtime | Re-run one recorded agent call against a different operator / edited prompt and diff the verdict | 4 (soft) | Draft | [`replay-decision.md`](replay-decision.md) |

## Sequencing

```
#1 (taxonomy) ──▶ #2 (decision detail) ──▶ #3 (view modes)
                       ▲
#4 (alternatives) ─────┘  (runtime, parallel to #1; makes #2's bar legible)

#5 (annotation) ── independent, any time (strategically highest value)
#6 (replay) ────── last; builds on deterministic replay + #4
```

- **#1 first** — it's the substrate #2 and #3 lean on, and pure projection
  over existing data (least new code, lowest risk).
- **#4 in parallel** with #1 (a runtime/decider change, no UI dependency);
  it feeds #2's confidence bar but #2 can ship with chosen+confidence alone
  and gain the runner-ups when #4 lands.
- **#2 then #3** — both are SPA presentation over data we already capture;
  the survey rates #2 (decision-first) + #1 the biggest fast demo win, then
  #3 (waterfall).
- **#5 any time** — independent capability; the survey rates it
  strategically highest because it operationalizes decision-as-labeled-datapoint.
- **#6 last** — most complex; depends on the deterministic-replay substrate
  (shipped) and reads best with #4's alternatives.

## Shared decisions

1. **Taxonomy lives once, derived not stored.** The semantic kind is a pure
   function of the existing `Event.Kind` string
   (`internal/store/event.go:220`) — no new per-event field, consistent with
   "the UI is a projection of the immutable event stream." Slice #1 owns the
   one canonical map (Go side for docs/trace-format, mirrored in the SPA);
   every other slice consumes it, none re-derives it.
2. **Annotation and replay are operator metadata, never story mutation.**
   Annotations write to a trace-adjacent sidecar (a new event kind in its
   own stream), and replay re-dispatches a *single* agent call in
   isolation — neither advances the machine or writes world/story. This
   keeps the read-only-viewer and read-only-meta-mode invariants
   (`feedback_meta_mode_readonly`) intact; each child defers here rather
   than re-arguing it.
3. **Confidence is always shown against its threshold.** Wherever confidence
   appears (#2 detail, #3 timeline badges), it renders relative to the
   decider's configured `Threshold` (`internal/orchestrator/decider.go:60`,
   default `0.8` at `:41`), not as a bare float. #4's alternatives extend
   this, they don't replace it.

## Cross-cutting open questions

1. **Do view modes (#3) and decision detail (#2) ship as one runstatus
   release or two?** They're separate slices for review, but a demo wants
   both. *Lean: separate PRs, sequenced #2 then #3; the epic is the demo
   story.*
2. **Where does annotation persistence live for the artifact (offline)
   mode?** Live mode has a server to POST to; a `file://` artifact does
   not. *Lean: annotations are a live-mode capability in v1; the artifact
   export bakes in any annotations already recorded but is read-only — see
   #5 Open questions.*

## Non-goals

- **Richer plain I/O rendering** (markdown-render `turn.end.view`,
  collapse/pretty-print large JSON) — survey idea #7. This belongs to the
  in-flight **`view-rendering-readability`** epic, specifically
  [`view-trace-and-web-typed.md`](view-trace-and-web-typed.md) (record the
  typed view tree; web renders every turn through `ViewElement`). This epic
  consumes that rendering, it does not re-do it.
- **Token/cost-by-subsystem breakdown UI.** The header already shows total
  tokens + cost per agent call (`AgentDetail.vue`); a per-phase cost
  rollup is a follow-on, out of scope here.
- **Multi-session hosting / cross-run aggregation.** The trace-list (#3)
  triages whatever sessions a single server exposes; aggregating annotation
  scores across many runs into an eval dataset is a downstream consumer of
  #5, not part of it.

<!--
  Lifecycle: as each slice ships, update its row's Status and migrate its
  detail into docs/ per that child's plan, then delete the child file. When
  every slice has shipped, delete this epic. Git history preserves the
  decomposition.
-->
