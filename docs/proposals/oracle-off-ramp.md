# Runtime: the oracle off-ramp (no-match → converse)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   — standalone

## Why

A free-text utterance that no routing tier and no LLM can map to a
declared intent dead-ends today in a rejection: the no-match path returns
`ModeRejected` with `UNKNOWN_INTENT` / `INTENT_UNKNOWN`
(`internal/orchestrator/orchestrator.go:1769`, `semantic.go:335`,
`helpers.go:367`; codes at `internal/intent/intent.go:34,46`), and the
TUI re-prompts with "I didn't catch that" plus the menu. For a tightly
scripted room that is exactly right.

But many rooms want a softer floor. An intake / discovery room, a
"describe your idea" room (the dev-story `idea` flow), an exploratory
main menu — when the user says something the graph can't *act* on, the
useful response is often a free-form answer, not a bounce back to the
menu. Today the only free-form escape is `off_path:`, and it is
**explicit-trigger only**: the user has to type the declared trigger
string (`OffPathDef.Trigger`, `internal/app/types.go:926`). There is no
way for a *no-match* to fall through into it. Authors who want "answer
anything the menu doesn't cover" are forced to either over-broaden their
intent vocabulary (defeating the declared-alphabet moat) or accept the
dead-end.

## What changes

A per-room opt-in — `oracle_off_ramp:` on a `State` — that intercepts the
no-match rejection. When routing + the LLM resolve to **no declared
intent** in a room that declared the off-ramp, the orchestrator hands the
user's original free text to an oracle `converse` turn (reusing the
off-path machinery, `Orchestrator.AskOffPath`,
`internal/orchestrator/offpath.go:48`) and returns the free-form answer as
a `ModeOffPath` outcome — **without advancing the state machine or
mutating world**. The room stays put; the user gets an answer; the same
menu is there next turn.

One sentence: **the off-ramp is automatic, room-scoped off-path entry,
triggered by a no-match instead of a typed trigger.**

The two halves stay on the right side of the moat: the *decision* to
off-ramp is **deterministic** (a room flag × which error code came back);
the converse *answer* is **interpretive** and already recorded.

### The scope guard (what does NOT off-ramp)

The off-ramp fires **only** on genuine no-match codes — `UNKNOWN_INTENT`
(name maps to nothing) and `INTENT_UNKNOWN` (the LLM couldn't map the
utterance). A *recognized-but-blocked* intent still rejects or clarifies
exactly as today, because those are meaningful signals the author wants
surfaced, not chat fodder:

| Code | Today | With off-ramp |
|---|---|---|
| `UNKNOWN_INTENT` / `INTENT_UNKNOWN` | reject ("didn't catch that") | **→ converse** |
| `GUARD_FAILED` | reject (+ guard hint) | unchanged |
| `INTENT_NOT_ALLOWED_IN_STATE` | reject | unchanged |
| `MISSING_SLOTS` | clarify | unchanged |
| `INVALID_SLOT_VALUE` | reject | unchanged |
| `AMBIGUOUS_INTENT` | disambiguation card | unchanged |

## Impact

- **Code seams:** `internal/app/types.go` (`State` struct, ~`:618`);
  `internal/orchestrator/orchestrator.go` no-match rejection sites
  (`:1769`, ~`:1947`) + `semantic.go:335`; reuses
  `internal/orchestrator/offpath.go:48` (`AskOffPath`).
- **Vocabulary:** one new `State` field, `oracle_off_ramp:` (table below).
- **Stories affected:** none change behavior; opt-in. Natural first
  adopter is the dev-story `idea`/intake room.
- **Backward compat:** default **off**. No field → byte-identical
  behavior. The one new trace field is additive, so existing cassettes
  replay unchanged.
- **Docs on ship:** `docs/stories/architecture.md` §9 (the forward-looking
  note added alongside this proposal becomes the real description),
  `docs/stories/state-machine.md` §11 (off-path), `docs/stories/meta-mode.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| state field | `oracle_off_ramp` | `true` \| `{ agent?, persona?, banner? }` | scalar-or-struct (custom unmarshal, like `View`); struct fields mirror `OffPathDef` (`types.go:925`) |

```yaml
idea_intake:
  view: "Tell me about the idea you want to explore."
  oracle_off_ramp: true                 # bare form: use the off-path agent/persona
  on:
    submit: [{ target: brief }]
    cancel: [{ target: main }]

# or the struct form, to style the off-ramp voice independently of /freeform:
discovery:
  view: "..."
  oracle_off_ramp:
    agent:  discovery-guide             # an entry in the top-level agents: map
    banner: "(thinking it through)"
```

## The model

```
free text ─▶ routing (4 tiers) ─▶ LLM translate
                                       │
                  resolved ────────────┼──────────── no-match (UNKNOWN_INTENT / INTENT_UNKNOWN)
                     │                                       │
               machine.Turn                       oracle_off_ramp on this room?
               (deterministic)                  ┌────────────┴────────────┐
                                                no                        yes
                                                 │                         │
                                          ModeRejected            AskOffPath(input) ─▶ converse
                                          ("didn't catch")         (no advance, no world write)
                                                                           │
                                                                    ModeOffPath answer
```

- **Deterministic:** the routing tiers, the no-match determination, and
  the `room-flag × error-code` branch that decides to off-ramp.
- **Interpretive (recorded):** the `converse` answer — same
  `host.oracle.converse` call off-path already makes.
- **Inviolate:** world and state. Like off-path, the off-ramp fires no
  `Turn()`, emits no `TransitionApplied`, and leaves the journey state
  exactly where it was. The user can still pick a real intent next turn.

## Decision recording

The interpretive call already records `OffPathQuestion` / `OffPathAnswer`
(`offpath.go:116,158`). The one gap is *why* the turn went free-form — a
typed `/freeform` trigger and an automatic no-match must be
distinguishable in the trace. Add a `reason` (and `error_code`,
`confidence`) field to the `OffPathEntered` event so an off-ramp entry is
labeled `reason: "off_ramp"` against the no-match code that triggered it.
Additive on an existing event → replay-compatible. (If a reviewer prefers
a distinct `OffRampEngaged` event, that becomes a small `tracing.md`
slice; lean is the additive field.)

## Engine seams & invariants

- **Field + load-time check** (`internal/app/types.go`,
  `internal/app/loader.go`): add `OracleOffRamp` to `State`. If the struct
  names an `agent:`, that agent must resolve against the top-level
  `agents:` map — the same check `OffPathDef.Agent` gets. **Reject at load
  time** an off-ramp declared on a `terminal: true` state or a
  `mode: conversational` state (the latter is already free-form, so the
  flag is meaningless there) with a clear error.
- **Interception point:** centralize a helper
  `maybeOffRamp(state, input, code) (*TurnOutcome, bool)` consulted
  immediately before each no-match `ModeRejected` is returned (the
  main-turn LLM path `orchestrator.go:1769`/~`:1947` and the semantic
  no-match at `semantic.go`). It checks the resting state's flag and the
  code, and on a hit delegates to `AskOffPath` and returns a `ModeOffPath`
  outcome. Routing through one helper keeps the three rejection sites from
  drifting.
- **Lock + persistence:** off-ramp runs under the same per-session lock
  off-path already uses; no new concurrency surface.

## Backward compatibility / migration

Default off; opt-in per room. Stories and cassettes keep working
unchanged. No mechanical migration — a room adopts the off-ramp by adding
one field. The `OffPathEntered` field addition is backward-compatible for
replay (older cassettes simply lack `reason`).

## Relationship to off-path and the meta convergence

The off-ramp is the **no-match door** into the same free-form `converse`
mechanism `off_path:` reaches through its **typed-trigger door** — one
voice, two entrances. It rides the convergence already noted in
architecture.md §9 ("off-path becomes `/meta default-oracle` with a
tool-restricted read-only agent"): once that lands, the off-ramp is simply
"auto-enter the default-oracle agent on a no-match." Agent/persona
precedence should mirror off-path's (`oracle_off_ramp.agent:` >
`off_path.agent:` > app default).

## Tasks

```
## 1. Engine
- [ ] 1.1 Add `oracle_off_ramp` to State (scalar-or-struct unmarshal); OffRampDef mirrors OffPathDef
- [ ] 1.2 Load-time invariants: agent resolves; reject on terminal / conversational states (clear error)
- [ ] 1.3 maybeOffRamp helper; wire it before the three no-match ModeRejected sites
- [ ] 1.4 AskOffPath fed the original free text; ModeOffPath outcome returned
- [ ] 1.5 OffPathEntered gains reason/error_code/confidence; off-ramp entries labeled

## 2. Verification
- [ ] 2.1 Flow fixture: no-match in an off-ramp room routes to AskOffPath (converse stubbed by id) → ModeOffPath; world/state unchanged
- [ ] 2.2 Flow fixture: GUARD_FAILED / MISSING_SLOTS in an off-ramp room still reject/clarify (scope guard)
- [ ] 2.3 Load-time: off-ramp on a terminal/conversational state fails to load
- [ ] 2.4 Legacy path: a no-off-ramp room still returns ModeRejected unchanged

## 3. Adopt + document
- [ ] 3.1 Turn on the off-ramp in the dev-story idea/intake room; confirm in a real run
- [ ] 3.2 Promote the §9 forward-looking note to a real description; update state-machine.md §11; delete this proposal
```

## Verification

The deterministic half is testable without an LLM: a flow fixture stubs
the `converse` host call **by id** (per the oracle-stub-by-id rule) and
asserts that a no-match input in an off-ramp room yields `ModeOffPath`
with world/state unchanged, while the same input in a non-off-ramp room
yields `ModeRejected`, and that a `GUARD_FAILED` / `MISSING_SLOTS` in an
off-ramp room is untouched by the off-ramp (the scope guard). Load-time
invariants are pure unit tests. The only step that genuinely needs an LLM
is the end-to-end "the no-match is *correctly* classified as a no-match
and the converse answer is sensible" — exercised once by hand in a real
dev-story run (Task 3.1), not in CI.

## Open questions

1. **Confidence floor vs. hard no-match.** Should a *low-confidence* LLM
   match (below `routing.semantic_mid_bar`, `types.go:326`) also off-ramp,
   or only a hard `UNKNOWN_INTENT` / `INTENT_UNKNOWN`? *Lean: hard no-match
   only for v1; add a confidence floor once dogfood shows real misroutes.*
2. **Chat-thread scoping.** Reuse the session off-path thread (continuity
   with an explicit `/freeform`) or open a per-room thread? *Lean: reuse
   the off-path thread — one free-form voice per session.*
3. **Field shape.** Bare `oracle_off_ramp: true` vs. struct. *Lean: accept
   both via a `View`-style scalar-or-struct unmarshal; bare form defaults
   to the off-path agent/persona.*

## Non-goals

- **Converse-then-route** — letting the off-ramp answer *suggest or emit*
  a transition. That overlaps the execution-modes gate deciders and is its
  own proposal; v1 is pure free-form, no advance.
- **An app-level global off-ramp default.** Start per-room; a top-level
  default can come later if every room ends up opting in.
- **Changing explicit `off_path:` behavior.** The typed-trigger door is
  unchanged; the off-ramp only adds a second, automatic door.
