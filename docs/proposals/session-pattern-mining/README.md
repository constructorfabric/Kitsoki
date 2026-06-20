# Session pattern mining → kitsoki stories (downstream consumer)

**Status:** PoC / method validation (reference run 2026-05-31, 12 of 245 sessions).

The general method, pipeline, redaction, report schema, and aggregation live in the
standalone kit at [`tools/session-mining/`](../../../tools/session-mining/) — that
is tool-agnostic and shareable. **This doc is the thin kitsoki-specific layer:** why
kitsoki cares, how a mined pattern becomes a kitsoki story, and what the reference
run found.

## Why this feeds kitsoki

A mined pattern is exactly the shape kitsoki exists to capture: a deterministic
skeleton with a few interpretive decision points. That is the kitsoki moat
restated — separate interpretation from execution, put a pluggable operator at each
decision, record every decision (see [[feedback_kitsoki_moat_is_architecture]]).

The mapping is direct:

| Mining concept | kitsoki concept |
|---|---|
| pattern's mechanical skeleton | story rooms / deterministic effects + host calls |
| pattern's `decision_points` | intent gates resolved by a **decider** (default / LLM / human) |
| the determinism ladder L0→L4 | progressively replacing LLM/human deciders with default deciders as recorded decisions accumulate |

So "progressive determinism" and the engine's gate-decider model
([[project_execution_modes_gate_deciders]]) are the same idea seen from two ends:
mining finds the gate worth installing; recorded gate decisions are the labels that
later let a default decider take it over.

## From pattern to story

1. Pick a high-`determinism_priority` pattern with **few, crisp** decision points.
2. Encode its skeleton as a story (rooms + effects + host calls) via
   `/kitsoki-story-authoring`.
3. Turn each `decision_point` into an intent gate with an LLM or human decider.
4. As the story runs, recorded decisions become the dataset to fit a default
   decider and climb the ladder.

## Brief → existing-story enrichment map (real-data run, 3 contributors)

The 3-contributor brief (`.artifacts/session-mining/BRIEF.md`, gitignored) ranks
patterns we *already have pipelines for*. So the actionable output is usually not
"build a new story" but "**install the gate the brief surfaced**" — most rooms today
model a fork only as a generic `accept / refine / restart` checkpoint, not as the
named, recorded decision the moat is built on.

| Brief pattern (priority) | Verdict | Closest existing story | Action |
|---|---|---|---|
| `implement-from-spec` (0.71) | BUILD (judgment) | `implementation` + `dev-story` + `cypilot` | **enrich** |
| `fan-out-agents-and-reconcile` (0.67) | BUILD (judgment) | — none — | **gap → new story** |
| `explore-codebase` (0.54) | BUILD NOW | off-path/meta read-only agent only | enrich (`agent`/meta room) |
| `verify-by-running` (0.51) | BUILD NOW | `bugfix.validating`, `implementation.test` | **enrich** |
| `debug-from-error-or-trace` (0.50) | LATER | `bugfix.reproducing` | enrich (later) |
| `build-compile-fix-loop` / `fix-failing-tests` | LATER | `bugfix.testing`, `implementation.test` | already modeled |
| `commit-or-pr` (0.24) | SOLVED | `pr-refinement`, `code-review` | leave as-is |

Three concrete enrichments and one gap (decision deferred — captured here, not yet built):

1. **`implement-from-spec` → `implementation/review_task`.** Add a `host.agent.decide`
   gate that classifies the incoming spec before `write_code`: *complete enough to
   one-shot vs needs a clarification round*, and *engine-level feature vs app-level
   YAML*. The "needs clarification" half is already solved in `prd` — the enrichment
   is partly wiring `prd`'s clarify loop in front of `implementation`. (The brief's
   7-gate count is itself a signal to consolidate.)
2. **`verify-by-running` → the verify step.** `implementation/test.yaml` only runs
   `iface.ci.run_tests`; it never decides *how* to verify. Add a modality decider —
   *trace live-tail vs flow test vs manual TUI interaction* — matching the existing
   `/run` + `/verify` skills. High pain, 77% mechanical, crisp.
3. **`debug-from-error-or-trace` → `bugfix/reproducing`** (later): the *flaky-vs-real*
   and *trace-vs-rerun* forks.

**Gap:** `fan-out-agents-and-reconcile` (3/3 contributors, also the strongest novel
signal from the 6-agent run) has no story, yet ranks second. Its gates —
*file-disjoint partitioning vs collision*, *merge partial vs wait serial*, *stale WIP
vs committed* — are exactly the reconcile decisions kitsoki should record, and tie to
the orchestrator. Candidate for a **new story**, not an enrichment.

## Reference-run findings

Full scored registry: [`pattern-catalog.yaml`](pattern-catalog.yaml). Top story
candidates by `determinism_priority`:

1. **build-compile-fix-loop** — most universal (6/6 sessions), highly mechanical → L3.
2. **fix-failing-tests** — cleanest *single-gate* story (loop is rote; the one
   judgment is "test wrong vs code wrong") → L2.
3. **verify-by-running** — high pain (slow restarts, stale binary, visual-only bugs).
4. **fan-out-agents-and-reconcile** — *novel*, corroborated by 4/6 agents, fully ad
   hoc today; maps cleanly onto kitsoki's recorded-decision model.
5. **debug-from-error-or-trace** — kitsoki's own recorded-trace superpower; a natural
   dogfood story.

## Method-validation verdict

The method reliably **ranks known workflows** and **surfaces unexpected ones**; the
promotion gate in `aggregate.py` keeps novel-pattern noise out of the shared counts.
Before treating numbers as load-bearing for a wide rollout: broaden the session
sample beyond biggest-by-size, and run the open-coding pass as a measured experiment
(see the kit README "Does it find novel patterns").

## Next steps

- Enrich `implementation/review_task` with the spec-triage gate (highest-priority
  enrichment; see the map above).
- Enrich the verify step with the verification-modality gate.
- Draft a proposal for a new `fan-out-agents-and-reconcile` story (the one real gap).
- Add `overlay-js` / `overlay-python` so non-Go contributors' reports stay clean.
- Broaden + recency-stratify the session sample for a deeper run.
