---
title: "Complete Product Tour: close the demo-asset gaps that block the differentiating beats"
target: kitsoki
feature: true
status: open
severity: P1
assignee: ""
component: features/demo
filed_at: "2026-06-15T09:49:41Z"
kitsoki_rev: 55aa25a
trace_ref: ""
proposal: ".context/complete-product-tour-proposal.md"
review: ".context/complete-product-tour-skeptic-review.md"
external: {}
---

> **Note:** `issues/` is a deprecated, frozen archive — new tickets are meant to
> live as GitHub Issues on `constructorfabric/Kitsoki`. Filed here on explicit
> request; mirror to GitHub (`kitsoki bug create --github` / migrate) if this
> should be tracked live.

## Body

Two adversarial reviews of the **Complete Product Tour** proposal
(`.context/complete-product-tour-proposal.md`, full critique in
`.context/complete-product-tour-skeptic-review.md`) verified its gap analysis
against source. The proposal's differentiating beats — the ones that defeat a
"kitsoki is just a YAML wrapper over things my coding agent already does"
skeptic — depend on **demo assets that do not exist or are broken today.** This
ticket enumerates the missing/broken features so the persuasive cut is buildable.

### Progress (2026-06-19, branch `feat/complete-product-tour`) — ALL SECTION ASSETS DONE

Every section-level gap and framing win is closed and validated green
(no-LLM, deterministic). Only the master STITCH remains (Phase 3, §8-gated
infrastructure) and G5 (explicitly v2 per §8.5). Commits:

- **G4 — DONE** (`5f69dbf`): multi-story spec repaired to the two-turn
  clarifying flow; persistence/active-sessions beat green (95.5s, 12 chapters).
- **U2 — DONE** (`2e0ca42`): host-rejects-the-model guardrail arc LEADS
  `agent-actions` (step 6/17); drawer helpers made backdrop-proof.
- **U5 — DONE** (`e2d2377`): story-editor hook framed as the host allow-list
  security boundary.
- **U1 + G3 — DONE** (`8718f16`): trace-features gains `trace-routing` (expands
  an explicit-intent turn.start, spotlights `Direct: yes` / `direct:true` — the
  ~78% zero-agent proof) and `trace-world-diff` (expands a world.update row,
  spotlights the WorldDiffViewer before/after). Frontend testids
  `world-diff-viewer` + `subsystem-chip-<sys>`; targeted backdrop-proof spec
  hooks. **G3 premise corrected:** `WorldDiffViewer.vue` was NEVER orphaned —
  it has been wired into `TraceTimeline` effect-group rows since `c841391`. The
  ticket's "imported nowhere, grep-verified" was a stale false negative. G3 was
  a manifest+testid+hook task, not frontend wiring.
- **U3 — DONE** (`0efe7e1`): operator-ask leads with the silent-auto-resolve
  landmine (headless `AskUserQuestion` → empty answer), not "the agent asks".
- **G1 + G2 — DONE** (`9f76493`): `features/meta-mode.yaml` (P3 hero, promo
  highlight) + `features/harness-picker.yaml` as **demo-only** catalog entries
  (no `tour:` block). **G1/G2 re-scope corrected:** `tour` is OPTIONAL in the
  schema (only `tour ⇒ demo`); generate.ts skips tour-less features and the live
  SPA imports no per-feature tours — so wrapping the scene-driven specs is the
  lightweight "wrap the built spec" the ticket originally specified, NOT a spec
  rewrite. Both specs are no-LLM (meta stub / agent_probe cassette).
- **U4 — INTENTIONALLY NOT CONVERTED**: the category chips are multi-select,
  all-on by default, so a single chip click *toggles a category OFF* — there is
  no clean one-click "filter down to X" beat, and adding the action introduces
  filter-state cleanup risk to the otherwise-clean trace-features bundle. Left
  as `kind: explain` (it already narrates the taxonomy correctly). Low value;
  not worth the awkward semantics.
- **G5 — v2** (per §8.5): the cross-operator replay-diff fixture is the most
  fixture-heavy beat and explicitly deferred to v2. `aa-diff` honestly shows
  "byte-identical under replay" today.

**Remaining:** the master stitch only — see the rewritten proposal
`.context/complete-product-tour-proposal.md` (now a focused master-stitch spec
with the §8 decisions made) and `.context/complete-product-tour-progress.md`.

Severity rationale: the four irrefutable on-screen proofs (host-rejects-the-model,
zero-agent-call routing, live FSM self-edit, film-is-a-CI-test) each have **no
analog in a general coding agent**, and three of them are currently un-filmable.
That makes these P1 for the demo, not cosmetic.

---

### Missing / broken assets (verified against source on 2026-06-15)

**G1 — `features/meta-mode.yaml` does not exist (P1).**
`tools/runstatus/tests/playwright/meta-mode.spec.ts` (~381 lines, built
end-to-end, hot-reloads `prd/flows/happy_path.yaml`) has **no feature manifest**,
so the single most viscerally-differentiating beat — a running FSM editing its
own YAML and reloading, with the edit recorded as a `story.changed` trace event
(`docs/tracing/trace-format.md:139–143`) — has zero promo presence.
*Fix:* NEW `features/meta-mode.yaml` wrapping the built spec; record + QA.

**G2 — `features/harness-picker.yaml` does not exist (P1).**
`harness-picker-video.spec.ts` is built and byte-deterministic but uncatalogued,
so live provider/model/effort switching with per-call provenance
(`agent.call.complete.meta`) never reaches the promo grid.
*Fix:* NEW `features/harness-picker.yaml`. Open question: it films on
`testdata/apps/agent_probe` (synthetic) — consider a first-party story that
declares `harness_profiles` so the differentiator isn't shown on a throwaway.

**G3 — `WorldDiffViewer.vue` is orphaned + `world-diff` step exists in no
manifest (P1, real frontend work — NOT a caption).**
`tools/runstatus/src/components/WorldDiffViewer.vue` renders Before/Diff/After
but is **imported nowhere** (grep-verified, zero importers); the `world-diff`
step id appears in **no** `features/*.yaml`. The proposal files this under
"extend" / "only incidental today" — it is actually unwired. The "how the turn
mutated the world" beat (a no-agent-log-analog proof) requires wiring the
component to a `world.update`/`machine.transition` event first, then adding the
manifest step.
*Fix:* wire `WorldDiffViewer` into the observer detail surface; add a `world-diff`
step to `features/trace-features.yaml`.

**G4 — `multi-story` recording is broken; spec↔flow desync (P1, blocks
persistence beat).**
On-disk `.artifacts/multi-story/ERROR.txt` confirms 34× "Expected: brief,
Received: clarifying". Root cause verified: `multi-story.spec.ts:293` sends
`submit_answers` with answer text in **one** turn, but
`stories/prd/flows/happy_path.yaml:104–122` requires **two** turns —
`answer{text}` (stays clarifying, `answered_count→2`) then `submit_answers{}`
with empty slots → `brief`. The spec skips the `answer` turn.
*Fix:* correct the **spec** to the intentional two-turn flow (per
`stories/AGENTS.md`: never paper over by loosening the flow). Re-record; capture
reload-survival + active-sessions roll-up.

**G5 — `aa-diff` has no real cross-operator drift to show (P2).**
`TranscriptDiff.vue:18–26` honestly renders "No live run to compare — replay is
byte-identical." On screen this reads as *empty feature*. The differentiating
"two operators, same prompt, here's the drift" beat needs a NEW two-cassette
fixture.
*Fix:* author a two-cassette fixture so `aa-diff` renders a real verdict diff;
pull from Phase 4 into v1 if the diff beat stays in the cut.

---

### Under-exploited capabilities that already exist (no code work, framing only)

These are NOT missing — they ship today and are the sharpest weapons, but the
proposal buries them. Captured here so the demo work surfaces them:

- **U1 — Zero-agent-call routing / "the LLM was never called."** `turn.start`
  carries `direct:true` / `routed_by: deterministic|semantic|turncache`
  (`trace-format.md:90–99`), surfaced by `RoutingDetail.vue`'s `routed_by` badge.
  ~78% of turns never call the model (`README.md:44–50`). This defeats the
  "structured output already does this" conflation; the proposal exiles it to a
  Phase-4 maybe.
- **U2 — Host rejects the model mid-call (decide guardrail arc).** The host
  injects synthetic `_kitsoki` lines for validator-rejection → nudge → re-submit
  → accept (`trace-format.md:300–306`), visible via `aa-decide-guardrail` /
  `aa-nudge` in `features/agent-actions.yaml`. No coding agent surfaces a host
  overruling the model. Buried at bullet 5/6 of `pt-actions`.
- **U3 — operator-ask exists because headless `AskUserQuestion` silently
  auto-resolves *empty*** (`docs/architecture/operator-ask.md:11–20`; hard-denied
  at `internal/host/agents.go:392–406`). The differentiator is the silent-failure
  it routes around, not "the agent asks a question."
- **U4 — `trace-category-chips` is `kind: explain` today** — convert to action
  (click a chip) for the `pt-inside` filter beat. Small, correctly-scoped extend.
- **U5 — host allow-list caption** — the allow-list is real and load-bearing
  (`internal/host/agents.go:392–406`, `internal/host/host.go:142–157`) but
  `HookDetail.vue` has no security-boundary caption today; add one in
  `features/story-editor.yaml`.

---

### Suggested order (cheapest gap closures first)

1. G4 repair (unblocks the persistence beat; pure spec fix).
2. G1, G2 (promote built specs — manifest + record only).
3. G3 (real frontend wiring — largest single item).
4. U1–U5 framing/extends folded into the relevant section re-records.
5. G5 (new fixture) — only if the cross-operator diff beat stays in v1.

## Source

Filed from the Complete Product Tour proposal review. The proposal carries the
full per-section production plan; the skeptic review carries the
verified-against-source evidence (file:line refs) for every gap above. Read both
before starting.
