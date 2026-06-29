# Proposal — Execution modes & gate deciders (one-shot / staged)

**Status:** Partially implemented (2026-05-29). The engine core + the
docs-review migration + the CLI/flow surface have landed; the items in
§8 remain. Keep this proposal until they do, then fold the model into
narrative docs (`docs/stories/state-machine.md`, `docs/stories/authoring.md`) and delete.

**Landed:**
- `ExecutionMode` (one-shot / staged) on the orchestrator
  (`WithExecutionMode`, `internal/orchestrator/orchestrator.go`); zero
  value is one-shot so all existing callers/tests are unaffected.
- Gate-stop in the **post-bind** emit chain
  (`machine.dispatchEmittedIntents` via `DispatchPostBindEmits`):
  `isDecisionGate` (a gate has a forward intent that is NOT an
  `emit_intent` target — an operator-only path; `@exit`/self excluded) +
  `isStagedGate` (stops when staged, or `decider: human`; `decider: llm`
  forces advance). Emits a `GateDecided` store event
  (`internal/store/event.go`).
- Per-gate `decider:` override field on `State` + load-time validation
  (`internal/app/types.go`, `internal/app/loader.go`).
- docs-review: `reviewed` now lists `fix_docs` in its choice menu (shown
  on needs_update) so a staged operator can apply fixes; the `on_enter`
  emit stays as the one-shot conditional default.
- CLI: `kitsoki run --mode staged|one-shot` (default **staged**) and
  `kitsoki session continue --mode …` (default **one-shot**, like
  `kitsoki turn`); flow fixtures accept `mode:` (`internal/testrunner`).
- **Engine-driven LLM decider** (§3.2): when a one-shot (or `decider: llm`)
  run rests at a multi-way gate with no firing conditional-default emit, the
  engine invokes a configured judge agent via `host.agent.decide` (passing
  the gate's candidate intents — `machine.DecisionCandidates`), parses the
  verdict (`internal/orchestrator/decider.go`), and either fires the chosen
  intent (`judges.ShouldAutoFire` + candidate-membership check) or **bails to
  human** (low confidence / uncertain / invalid / error → rest at the gate).
  Loops for chained gates, depth-capped. Configured app-level via a
  `decider:` block (`agent`, `schema`, optional `prompt`, `threshold`;
  load-validated) and wired through `kitsoki run`/`session`. Tests:
  `internal/orchestrator/decider_test.go` (fire / bail / not-configured).
- **Decision recording**: every gate resolution emits `GateDecided` —
  `decider: human` (staged stop), `default` (a conditional-default emit
  resolved a real gate), or `llm` (engine decider), with chosen intent +
  confidence + bail flag.
- **One-shot live progress**: each synthetic hop's `say:` text streams
  live per-room through the `RoomEnterSink` (an `onEnter` callback
  threaded into `DispatchPostBindEmits`/`dispatchEmittedIntents`), so a
  one-shot chain narrates what it's doing instead of jumping silently to
  the final room. When a sink is present the say is streamed (not merged
  into the final view); headless callers keep the merged prepend.
  docs-review's `reviewed`/`fixing`/`fixed` now carry `say:`
  breadcrumbs (verdict → "applying fixes…" before the slow agent.task →
  "docs patched").
- Tests: `internal/orchestrator/execution_mode_gate_test.go` (engine
  unit — staged stop incl. fails-without-fix check, plus per-room say
  streaming) and `cmd/kitsoki/docs_review_execution_mode_test.go` +
  `stories/docs-review/flows/staged_needs_update.yaml` (end-to-end:
  one-shot → fixed, staged → stops at reviewed → fix_docs → fixed).

**Original spec follows.** The Tldr and §§1–7 describe the full intent;
§8 is the authoritative remaining-work list.

**Tldr.** Every room (== phase) is a container for a turn that ends in
an **intent gate**: the set of `on:` intents whose guards currently
pass. Today the engine resolves a gate implicitly — a room's `on_enter`
fires `emit_intent: X when <cond>` and the orchestrator chains the
synthetic transition *within the same turn*. With multiple chained
emits, a whole pipeline (`reviewing → reviewed → fixing → fixed`) runs
in one turn, the TUI (append-only: one turn = one transcript entry from
the *final* state) shows nothing in between, and write-capable agents
edit the tree with no operator visibility. This proposal makes gate
resolution a **first-class engine concern** driven by a run-level
**execution mode**, so a phase boundary can *end the turn* and the
decision at each gate is made by an explicit, recorded **decider**
(default intent, LLM agent, or human).

---

## 1. The gate model

A **gate** is evaluated after a room's `on_enter` effects and any host
calls have settled. Its members are the room's available intents —
computed today by `allowedNamesFromMachine(machine, state, world)` in
`internal/orchestrator`. Gate cardinality decides what happens:

| Available intents | Behaviour |
|---|---|
| **0** | Terminal / rest. The room is a display or exit state. |
| **1** | **Always auto-advance.** No decision exists; fire it. (This is today's single-`emit_intent` case.) |
| **>1** | A **decision** is required — resolved by a *decider* (§3). |

A room may **recycle**: an arm whose target is the room itself (a
self-loop) is a legal gate outcome and spends another turn in the room
(e.g. bugfix's `refine` arc bumping `cycle`).

This unifies "room" and "phase": a phase template
(`internal/app/phases.go`) expands to states that are just rooms with
gates; `checkpoint: true` becomes one expression of a per-gate decider
override (§3.3).

## 2. Execution mode

A run carries an **execution mode**, a first-class orchestrator setting
(not a per-story world-var convention):

- **`staged`** — a **human** decides every gate with >1 available
  intent. The turn *completes* at the gate: the room renders its own
  view and the operator picks the next intent. This is the safe default
  (never auto-fires a multi-way decision unattended).
- **`one-shot`** — the engine advances autonomously: at each gate it
  takes the **default intent** if one is set (§3.1); where none is set,
  it consults an **LLM agent decider** (§3.2). The run only stops for a
  human when the LLM *bails* (§3.2).

The mode is supplied per session/run (`--mode` on `kitsoki run` /
`session create`; a `TurnOption` override per turn) and defaults to
**`staged`**. `kitsoki turn` (the stateless diagnosis path) defaults to
**`one-shot`** so it keeps walking pipelines end-to-end.

Existing `judge_mode` (`human|llm|llm_then_human`) +
`judge_confidence_threshold` world vars are *kept and mapped* onto the
engine mode for the first slice (`human` → staged-ish, `llm` /
`llm_then_human` → one-shot), so bugfix/dev-story flows do not churn.

## 3. The decider

A **decider** selects one intent from a >1 gate. Exactly one decider is
responsible for each gate decision, and that decision is recorded (§4).

### 3.1 Default intent (deterministic, both modes)

A gate may declare a **default intent**, and the default may be
**conditional** — an intent becomes the default once room conditions are
met. Mechanically this is the existing guarded `emit_intent: X when
<expr>` (a matched guarded emit *is* a conditional default). In
`one-shot` mode a present default short-circuits the decision; in
`staged` mode a default does **not** override the human (unless the gate
pins an LLM/auto decider — §3.3).

### 3.2 LLM agent decider (one-shot, no default)

When a one-shot gate has >1 intent and no default, the engine invokes an
LLM decider, reusing the existing contract:

- `host.agent.decide` against a `judge` agent + `judge_verdict.json`
  schema → a `judges.Verdict{verdict, intent, reason, confidence}`.
- `judges.Parse` validates; `Verdict.ShouldAutoFire(threshold)` is the
  single source of truth for "is this auto-fireable?"
  (confidence ≥ threshold AND not `uncertain`). `AutoFireIntent()`
  returns the chosen intent.
- **Bail-out to human** (turn completes, room renders, awaits operator)
  when any of:
  - retry budget exhausted without a schema-valid verdict
    (`ErrMalformedJSON` / `ErrSchemaViolation`),
  - `verdict == uncertain` or `intent == uncertain`,
  - `confidence < threshold`.

The decider must choose only among the gate's *available* intents; a
verdict naming an unavailable intent is treated as a bail.

### 3.3 Per-gate decider override (the "mix")

A gate may pin its decider regardless of run mode:

- pin **LLM** → decided by the agent decider even inside a `staged`
  run (autonomous step inside an otherwise-supervised pipeline);
- pin **human** → always stops for a human even inside a `one-shot`
  run (a mandatory checkpoint).

Expressed as a per-state/transition annotation (a new `decider:`
field, generalizing the phase-template `checkpoint:` flag). Load-time
validation lives in `internal/app/loader.go`.

## 4. Recording every decision

Each gate resolution emits a `gate.decided` store + trace event:

```
{ state, available_intents, decider: default|llm|human,
  chosen_intent, confidence?, bailed_to_human }
```

This is the architecture moat made literal — one pluggable interpretive
operator per decision, every decision a labeled datapoint — and lets the
TUI / runstatus explain *why* a one-shot run advanced (rather than the
silent jump that motivated this proposal).

## 5. Implementation seams

- **Gate resolution** replaces the unconditional auto-chain in
  `internal/orchestrator/orchestrator.go::settlePostBindEmits`
  (called from `helpers.go` RunIntent success path). "Stop the turn" =
  return without further advancing so RunIntent renders the resting
  room; the TUI appends it as a transcript entry → *a phase boundary
  ends the turn.*
- Auto-advance / conditional-default reuse the existing
  `machine.go::dispatchEmittedIntents` / `DispatchPostBindEmits` with
  depth caps `EmitIntentMaxDepth` / `OrchestratorPostBindMaxDepth`.
- Decision logic reuses `internal/judges/judges.go` verbatim.
- Mode is an `Option func(*Orchestrator)` (+ `TurnOption`).

## 6. First slice (scope) — DONE

Landed as described in the Status block above. Note one refinement vs.
the original plan: docs-review's `reviewed.on_enter` emit was **kept**
(not removed) — it is the one-shot conditional default, and staged mode
suppresses it at the gate. The story change was adding `fix_docs` to the
room's choice menu so a staged operator can pick it.

## 8. Remaining work

1. **Pre-bind / Turn-path staging.** `machine.Turn` and
   `RunEffectsAndState` dispatch emit chains with `staged=false` /
   `onEnter=nil` because threading the run mode through those core
   interface methods is wide churn (~15 callers). So a decision emit that
   fires *pre-bind* (unconditional / non-host-gated) does not staged-stop
   or stream its say. This is a **narrow, currently-unexercised** hole:
   every real decision gate in docs-review and bugfix does work (a host
   call) then decides → post-bind, which is fully covered. Close it when
   a story actually needs a pre-bind auto-advance into a gate.
2. **bugfix migration.** Now that the engine decider (§3.2) exists, the
   §6 per-room "auto-fire if confident" guard can be collapsed onto an
   app-level `decider:` block, mapping `judge_mode` → `ExecutionMode`
   (`human` → staged, `llm`/`llm_then_human` → one-shot). High blast
   radius (bugfix is the dogfood story) — do it deliberately with the
   flow suite as the guard.
3. **Narrative docs.** Fold §§1–4 into `docs/stories/state-machine.md` /
   `docs/stories/authoring.md` (execution modes, the gate rule, the `decider:`
   state field + app block, `--mode`) and delete this proposal.

## 7. Verification

- Unit (orchestrator dogfood/smoke): a 2-intent gate **stops** in
  staged (turn rests at the room, view rendered) and **auto-fires** the
  default/LLM intent in one-shot; LLM low-confidence verdict **bails to
  human**. Each test must fail without the change (today it auto-chains).
- Story flows mirroring `.kitsoki/stories/kitsoki-dev/flows/pickup_autonomous_then_bail.yaml`
  and `stories/dev-story/flows/bf_llm_auto_advance.yaml`.
- End-to-end: replay the docs-review world at `reviewed`; staged renders
  at `reviewed` instead of jumping to `fixed`; trace shows `gate.decided`.
- No-regression: bugfix/dev-story flow suites stay green; `kitsoki turn`
  one-shot still walks pipelines.
