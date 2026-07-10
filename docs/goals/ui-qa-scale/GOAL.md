# GOAL — Persona QA, arena, and swarm UI testing are one scaled system

**Status:** Active target for the `stabilize/ui-qa-scale` line of work. Read by
`/goal` (the goal-seeker) from this location. Provenance: the integration + scale
review `.context/persona-qa-arena-ui-scale-review.md` (2026-07-04) and the arena
proposal `docs/proposals/arena-comparison-runner.md`.

**One line:** Persona-driven QA journeys run *through* arena as a first-class
workload (shared verdict contract, shared corpora, shared rollup), and a swarm
harness simulates dozens of concurrent users actively using the kitsoki web UI —
for regression QA and for discovery — with everything no-LLM by default and every
finding reproduction-ready.

## The shape of done

Today product-journey (the persona/scenario brain, fully serial) and arena (the
container/placement body, bugfix-only) share concepts but zero code: two matrix
brains, two verdict vocabularies, two rollups, no browser in the cell image, and
no harness anywhere that puts N concurrent users on one `kitsoki web` server.
Done means one system: arena is the single front door for comparison sweeps
including persona QA, and the swarm is how the web UI gets soaked and explored
at dozens-of-users scale.

## Done-when (criteria — each falsifiable; `/goal` verdict=done requires all)

- **G1 — One contract.** Arena and product-journey share a single
  completion-state/verdict schema (plugins score from a structured file, never
  stdout regex), arena loads product-journey's corpora (`github-targets.json`,
  `personas.json`) as targets/axes, and one rollup/deck implementation serves
  both. *Gate: schema-conformance + corpus-loading + shared-rollup tests, no LLM.*
- **G2 — Persona journeys run through arena.** A `persona-qa` arena job spec
  emits run bundles, drives them in cells, and scores each cell from the bundle's
  19-check `review.json` — with real concurrency across cells. The
  `plan/persona-qa-arena-ui-scale` branch's existing plugin/bridge is landed, not
  reimplemented. *Gate: plugin tests over FakeBackend + a no-LLM replay-smoke
  sweep through `arena run`.*
- **G3 — Dozens of users on one server.** A swarm harness drives N ≥ 24
  concurrent simulated users (Playwright contexts, scripted persona journeys)
  against ONE shared `kitsoki web` server: every journey completes, sessions
  stay isolated (no cross-talk), zero console errors, geometry/axe audit clean
  on every state reached, and the server stays within a session cap with
  eviction. *Gate: a no-LLM swarm Playwright spec + a registry cap/eviction Go
  test.*
- **G4 — Findings arrive reproduction-ready.** Any swarm assertion failure or
  explorer finding auto-captures rrweb + console + scrubbed HAR under a capture
  id via the existing bug-report path, so a finding can be replayed without
  re-running the swarm. *Gate: a spec that forces a failure and asserts the
  capture bundle exists and replays.*
- **G5 — No-LLM by default, live gated, arena-scalable (cross-cutting).** Swarm
  tiers 1–2 (replay users, cassette-agent users) spend zero LLM tokens; live
  explorer users are few, explicitly gated, and every live capability has a
  standing no-LLM replay gate in CI. The swarm runs as an arena job type in a
  browser-capable cell image so placement/VMs scale it out. *Gate: every other
  gate is no-LLM; the live path's standing CI check is its replay_cmd.*

## Non-goals (parked — must not gate this goal)

TUI swarm (the TUI shares the orchestrator; a frame-assert tier can follow
later), VM host pooling / completion-state polling (arena P1 infra — only needed
past one VM), retiring `escalate.sh`/`--emit-matrix` (arena P3 consolidation),
per-host docker resource limits, load testing beyond ~dozens of users
(performance benchmarking is a different goal than QA/discovery).

## Aggregate

Done when a single command sequence — `arena run` with a persona-qa spec, and
`arena run` (or the tier-1 spec directly) with a swarm spec — exercises persona
journeys concurrently and puts 24+ simulated users on one kitsoki web server,
all gates green RED→GREEN with traces on record, zero LLM spend outside the
gated explorer tier. Highest-ROI first step: **unify-contract** (the shared
completion-state contract) — it lands the already-written bridge work and stops
the arena/product-journey drift everything else builds on.
