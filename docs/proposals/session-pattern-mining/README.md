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

- Draft `fix-failing-tests` as a real story to prove the pattern→story→gate handoff.
- Add `overlay-js` / `overlay-python` so non-Go contributors' reports stay clean.
- Broaden + recency-stratify the session sample for a deeper run.
