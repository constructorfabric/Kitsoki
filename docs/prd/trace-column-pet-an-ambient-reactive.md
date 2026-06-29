### Trace-Column Pet — an ambient, reactive sign of life

# PRD — Trace-Column Pet (an ambient, reactive sign of life)

A tiny SVG "pet" anchored at the bottom of the kitsoki trace column that gives the timeline a glanceable, friendly sense of life. It **rests** when nothing runs, **perks up** as new datapoints arrive, and shows **concern** on errors.

**Core stance:** the pet *reacts to trace activity* but only by **reading** the existing trace snapshot + live SSE feed — zero new application state, zero writes, zero fabricated info. This is a conscious revision of the prior design deck (which scoped it as "purely decorative, reads no trace data"); all other deck constraints (additive leaf, absolutely-docked over the footer, opt-in/off-by-default, zero deps, reduced-motion) are preserved.

**Mood → existing EventKind mapping:**
- Resting (default): quiescent SSE stream
- Perked up: `agent.call.complete`, `artifact.emitted`, `machine.transition`/`state_entered`, `harness.returned`, `turn.end`
- Concerned: `agent.call.error`, `harness.error`, `machine.validation_failed`, `machine.guard_rejected`

**Key requirements:** anchored in `.iv__trace`, absolutely positioned over the footer, never displaces/clips a row; `data-testid` (`trace-pet`); opt-in 🐾 toggle persisted client-side; tour-spotlightable; brand-tone abstract geometry (no sacred imagery); inline SVG + CSS only (zero deps/assets); reduced-motion + `aria-hidden` + silent; deterministic/no-LLM Playwright testable; coarse mood (no counts), terminal-state reflection on static traces.

**Sections:** Summary · Problem & context · Target users · Goals & non-goals · Functional requirements · Non-functional requirements · Success metrics · Open questions · Relationship to prior design pass.

**Honesty note:** The clarification round returned **no answers**, so audience, problem-vs-delight framing, the v1 mood set, success criteria, and character (mascot vs. minimal) are author assumptions, flagged inline and surfaced as follow-ups. The PRD stands as a solid draft but warrants one more clarification round before build.

Full document written to `.artifacts/prd/add-a-tiny-svg-pet-anchored-2/004-prd.md`.
