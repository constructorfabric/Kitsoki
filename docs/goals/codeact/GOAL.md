# GOAL — `agent.codeact` fully implemented, tested, pipeline improvements landed

**Status:** New goal, seeded 2026-07-05.
Read by `/goal` (the goal-seeker) from this location. Provenance:
`.context/codeact-implementation-brief.md` (the slice plan) and
`.context/kitsoki-vs-codeact.md` §"Reframing: an `agent.codeact` verb over the
embedded Starlark runtime" (the design).

**One line:** kitsoki gains a new agent verb, `agent.codeact` — the LLM
iteratively emits Starlark snippets against an author-declared capability
allowlist, each step traced/bounded/sandboxed, observations feed back, a final
`done(payload)` is schema-gated — slotting between the finite intent alphabet
and `task` in the verb taxonomy. Equal-rank second deliverable: every slice is
built BY kitsoki (studio-MCP-driven dogfood), so every pipeline friction hit
along the way is filed and fixed, never worked around outside the engine.

## Done-when (criteria — each falsifiable; `/goal` verdict=done requires all)

- **G1 — Engine.** The bounded codeact loop exists in Go: step budget,
  per-step Starlark execution over a capability-scoped builtin set, structured
  error envelope (same contract as the validation-feedback retry loop),
  `done(payload)` schema gating, deterministic termination.
  *Gate: Go unit tests with a scripted agent stub — happy path, error-envelope
  path, budget exhaustion, schema-reject on `done`.*
- **G2 — Verb wiring.** `verb: codeact` in the invoke schema (`capabilities:`,
  `budget:`, `goal:`, `schema:`, `bind:`); loader validates capabilities
  against the registered builtin set at load time; taxonomy docs
  (`prior-art.md §6.4`) gain the row.
  *Gate: loader-validation unit tests (unknown capability → load error) + a
  story flow fixture exercising a codeact room with a stubbed agent.*
- **G3 — Tracing + replay (the differentiator).** Every snippet + observation
  journaled; a recorded codeact trajectory replays with zero LLM and zero live
  side effects through `test flows`.
  *Gate: a flow fixture replaying a committed codeact cassette, asserting the
  bound payload; a trace-shape test asserting each step is journaled.*
- **G4 — Live proof (dogfood the feature itself).** A codeact room driven LIVE
  over the studio MCP on a real target room; the trajectory recorded; trace
  shows N snippet/observation steps; `done` payload schema-passes; replaying
  the recording reproduces the run.
  *Gate: the recorded cassette + replay fixture from this run, committed as
  the regression artifact.*
- **G5 — Promotion ratchet.** A successful trajectory's final Starlark can be
  frozen into a committed `.star` file + sidecar, swapping the room's
  `agent.codeact` for a deterministic `host.starlark.run`.
  *Gate: an extraction unit test (given the slice-4 trace, emits a `.star`
  that `starcheck` passes and reproduces the payload deterministically) + a
  flow proving the promoted room runs with no agent dispatch.*
- **G6 — Pipeline-as-SUT findings closed.** Every slice built via
  `kitsoki-mcp-driver` agents over the studio MCP (no CLI, no direct git from
  drivers); every blocker hit while building G1-G5 filed as a GitHub issue or
  fixed in its own commit; `.context/codeact-dogfood-findings.md` complete.
  *Gate: findings file exists and every `blocker`-classified entry has a
  linked issue or a landed fix commit.*
- **G7 — Docs landed, proposal retired.** `prior-art.md` gains the CodeAct
  section (harvested from `.context/kitsoki-vs-codeact.md`, which is then
  deleted); taxonomy table updated; authoring docs cover the verb;
  `.context/codeact-implementation-brief.md` trimmed to a pointer or deleted
  per the proposal lifecycle once G1-G6 land.
  *Gate: doc-presence lint (`prior-art.md` contains a CodeAct heading) +
  absence of the two `.context/*.md` source files (folded, not orphaned).*

## Non-goals (parked — must not gate this goal)

Fully-automatic promotion (G5 is a manual/tool-assisted extraction, not an
autonomous ratchet); a second target room beyond the one chosen in the design
spike; non-Starlark capability substrates.

## Aggregate

Done when G1-G7 are all green and every change's deterministic gate went
RED→GREEN with its trace on record — mirroring the `generalized-usage` goal's
discipline (see `[[goal-seeker-generalized-usage]]` memory), applied to a
narrower, single-feature scope.
