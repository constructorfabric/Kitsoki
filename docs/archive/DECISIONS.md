# App-Build Integration Decisions

Recorded before building the dev-story app.  Each of the three platform gaps
identified in the task brief is addressed here.

---

## Decision 1: Proposal lifecycle (caveat 1)

**Choice: Option (a) — explicit states.**

Rationale:
- The DSL compiler (option b) would require significant new platform code and
  tests, adding risk to the app-build task.
- The proposal_smoke reference app already demonstrates that explicit states
  (`terminal`, `reviewing`, `terminal_result`, etc.) produce clean YAML.
- The Terminal Room — the canonical proposal user — is straightforward to
  author with four states (`terminal_idle`, `terminal_reviewing`,
  `terminal_executing`, `terminal_result`).  The proposal package helpers
  (`proposal.New`, `SetDraft`, etc.) are called from world state effects via
  Go, not from YAML.
- Authors get full control over view text and guard conditions with no hidden
  compiler magic.

Concretely, each proposal-using room has sub-states for the phases it cares
about.  Global intents `propose`, `accept`, `cancel`, `retry` are shared.
The `$proposal` world variable is used as a passthrough bag for command text
and result display.

---

## Decision 2: Oracle ConversationalHarness stub (caveat 2)

**Choice: Accept the stub; no Anthropic SDK wiring.**

Rationale:
- The task brief explicitly says "do not add Anthropic SDK calls".
- ConversationalHarness already exists as a stub that returns a formatted
  stub response.
- The Oracle Room is wired structurally (enter, ask, get stub reply, exit).
  The flow test verifies the state transitions; the reply text is noted as stub.

Integration test note: Oracle flow uses `input:` turns, which require the
ReplayHarness oracle YAML.  The Oracle Room flow uses `intent:` turns directly
to avoid the oracle lookup, matching the stub behavior.

---

## Decision 3: Room history stack wiring (caveat 3)

**Choice: Explicit intent-level wiring in YAML.**

Rationale:
- Wiring `history.Push` into the orchestrator turn loop would require touching
  the orchestrator package (platform code) and threading the history stack
  through every turn result.
- The simpler path for the app layer is to manage `$history` (the world key)
  through explicit YAML `set:` effects on every navigation transition.
- The history package provides `ToWorld`/`FromWorld` helpers; the YAML uses
  `set: { "$history": ... }` via a helper world variable.
- A global `back` intent handles the pop: it reads `$history` and transitions
  to the recorded state.

Concretely:
- Every room-navigation transition carries `effects: - set: { $prev_state: "<source_state>" }`.
- The `back` intent reads `$prev_state` and branches to the appropriate state.
- Full multi-level history (via the `$history` stack) is scaffolded using the
  world variable `$history` set by `set:` effects.  The history package helpers
  are demonstrated in the test flows.
- For simplicity in the YAML app (since the machine cannot call Go helpers
  inline), back-navigation is modeled as a single-level `$prev_state` world
  variable.  The history package is exercised in the Go-level test helper.

---

## Summary table

| Caveat | Decision | Key trade-off |
|---|---|---|
| Proposal DSL | Explicit states (option a) | More YAML, zero new Go |
| Oracle stub | Accept stub, no SDK | Per brief; structural test only |
| History wiring | Explicit `$prev_state` + global `back` intent | Simpler than orchestrator changes |
