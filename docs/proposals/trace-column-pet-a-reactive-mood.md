### Trace-Column Pet — a reactive mood projection of the trace

# Tracing: Trace-Column Pet — a reactive mood projection of the trace

**Status:** Draft v1. Nothing implemented yet. **Kind:** tracing. **Epic:** — standalone.

A *consumer* proposal (no new events/fields/RPC): revise the already-shipped decorative `TracePet.vue` into a reactive **mood projection of the trace**. It **rests** when nothing runs, **perks up** as datapoints land, shows **concern** on errors — driven only by reading `store.events` (snapshot + the SSE `/rpc/events` feed the SPA already consumes). Zero new state, zero writes.

**Template choice:** `tracing` over `tui` — the cited references route "run-status surfaces" to the tracing template, and this proposal fills its consumer/determinism/fixtures sections (the surface *consumes* already-traced EventKinds rather than recording new ones). Spillover (Vue SPA, not Go) noted under Impact.

**Mood map (trace-format §4 kinds):** Perked = `agent.call.complete`, `artifact.emitted`, `machine.transition`/`state_entered`, `harness.returned`, `turn.end`; Concerned = `agent.call.error`, `harness.error`, `machine.validation_failed`, `machine.guard_rejected`; else Resting. Concerned outranks Perked; both decay back to Resting; static/replayed traces settle into the run's terminal mood.

**Key grounding:** dock seam + opt-in toggle unchanged (`InteractiveView.vue:306`, `:377-393`); keep pure-CSS `pet-bob`/`pet-blink` idle loop (`TracePet.vue:174-238`); **remove** the non-deterministic `Math.random()`/`setTimeout` wander/💩/🍎 loop (`TracePet.vue:79-158`) to honor live ≡ replay. Pure `deriveMood(events)` modelled on `diagram/horizon.ts`; tests are Vitest (pure fold) + a no-LLM Playwright spec on the `bugfix.spec.ts` pattern (mood class + no row obstruction, must fail against the decorative component first).

**needs_clarification: true** — six open questions, the two material ones being supersede-vs-coexist (which PRD is canonical) and reconciliation with the sibling four-state proposal over the same component. Full document written to `docs/proposals/.workspace/realize-trace-pet-prd-as-web-2/004-proposal.md`.
