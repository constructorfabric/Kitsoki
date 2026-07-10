#!/usr/bin/env python3
"""scenario_compiler.py — compile mined intents+outcomes into the scenario IR.

Reads a single session-mining job's `intents.json` + `analysis.json` (as
produced by `emit.py --outcomes` — see schema/{intents,analysis}.schema.json)
and emits one `kind: conversation` scenario IR document
(schema/scenario_ir.schema.json) per calibration-worthy, goal-bounded span.

## Grouping algorithm (goal-bounded spans, corrections folded in)

Per session, intent instances are already emitted in encounter order (span
index ascending, recovered from the `<session>#<idx>` instance_id suffix so
compilation does not depend on input list order). Walking that ordered list:

  - start a new scenario at the next not-yet-consumed instance; its verbatim
    `user_text` becomes the scenario's first turn and its `goal`.
  - if that instance's `analysis.json` satisfaction says `corrected: true`,
    the NEXT instance in the same session is folded into the SAME scenario
    as a second turn (the user's own corrective follow-up) — this repeats
    for as long as each folded-in turn is itself flagged `corrected`.
  - the scenario ends (successfully) the first time a turn's satisfaction is
    `corrected: false` (or absent — no --outcomes data to say otherwise).
  - the scenario ends (unresolved) when the session simply runs out of
    instances while the last turn folded in is still `corrected: true` —
    that scenario is marked `abandoned: true` per the IR's contract: it
    should assert the workbench does NOT silently claim success.

This mirrors emit.py's own satisfaction docstring: corrected/corrective_ops/
followup_text_head are a recall-biased REVIEW FLAG, not a verdict, so the
compiler folds them in structurally rather than re-deriving new judgment.

## expected_effects

Deterministically derived, not invented: the sorted, de-duplicated list of
"<signature> completed" for every GROUNDED action (grounding.py's verdict,
carried through untouched) across every turn folded into the scenario. A
scenario with no grounded action at all gets an empty list — a real,
honest signal (nothing here was confirmed reproducible), not padding.

## persona

Mined analysis carries tags, not personas — synthesizing personas from
intent clusters is the product-journey compiler's job (task 3.3), out of
scope here. This compiler applies a small, documented, deterministic
lookup from the scenario's first turn's primary action tag to a coarse
persona label, so mined scenarios are usable as -is and easy to hand-check
at calibration (task 2.4); it is not meant to be the final word on persona
taxonomy.

## Determinism

compile_scenarios() is a pure function of its two input dicts (+ the
`corpus` label passed by the caller) — no timestamps, no randomness, no
filesystem/network access, no reliance on input list ordering (spans are
re-sorted by their instance_id's numeric suffix). Same inputs, same corpus
label -> byte-identical output (after json.dumps with sort_keys=False but a
stable field order, since callers use ic.dump_json's stable formatting).

Stdlib only.
"""
import argparse
import json
import os
import re
import sys

import intent_common as ic


SCHEMA_VERSION = "1.0"

# Coarse, documented action-tag -> persona lookup (task 2.2 scope: heuristic,
# hand-checked at calibration, not a final taxonomy — see module docstring).
_PERSONA_BY_ACTION = {
    "rebase-or-resolve-conflicts": "core-maintainer",
    "branch-or-worktree-setup": "core-maintainer",
    "commit-or-pr": "core-maintainer",
    "review-feedback": "reviewer",
    "fix-failing-tests": "bugfix-contributor",
    "build-compile-fix-loop": "bugfix-contributor",
    "debug-from-error-or-trace": "bugfix-contributor",
    "implement-from-spec": "feature-implementer",
    "add-test-coverage": "feature-implementer",
    "refactor-rename-move": "feature-implementer",
    "write-docs": "docs-writer",
    "update-config-or-deps": "maintainer",
    "verify-by-running": "verifier",
    "visual-verification-loop": "qa-reviewer",
    "explore-codebase": "explorer",
    "fan-out-agents-and-reconcile": "orchestrator",
}
_DEFAULT_PERSONA = "developer"

_INSTANCE_ID_RE = re.compile(r"^(?P<session>.+)#(?P<idx>[0-9]+)$")


def _span_idx(instance_id):
    m = _INSTANCE_ID_RE.match(instance_id or "")
    return int(m.group("idx")) if m else 0


def _persona_for(tags):
    for action in (tags or {}).get("action", []):
        if action in _PERSONA_BY_ACTION:
            return _PERSONA_BY_ACTION[action]
    return _DEFAULT_PERSONA


def _slug(text):
    s = re.sub(r"[^a-z0-9]+", "-", (text or "").lower()).strip("-")
    return s or "session"


def _grounded_effects(instances):
    """Sorted, de-duplicated '<signature> completed' for every grounded action
    across the given analysis instances (dict order irrelevant; output sorted
    for determinism)."""
    effects = set()
    for inst in instances:
        for a in inst.get("actions", []):
            if a.get("grounded") and a.get("signature"):
                effects.add(a["signature"] + " completed")
    return sorted(effects)


def compile_scenarios(intents_doc, analysis_doc, corpus):
    """Pure function: intents.json + analysis.json dicts -> list of scenario IR
    dicts (kind: conversation), one per calibration-worthy goal-bounded span.

    `corpus` is the label stamped into provenance.corpus (e.g. "claude-code",
    "codex") — the compiler itself is corpus-agnostic; it only reads the
    already-uniform intents/analysis shape both adapters emit.
    """
    intents_by_id = {i["instance_id"]: i for i in intents_doc.get("intents", [])}
    analysis_by_id = {i["instance_id"]: i for i in analysis_doc.get("instances", [])}

    by_session = {}
    for iid, intent in intents_by_id.items():
        by_session.setdefault(intent.get("session"), []).append(iid)

    scenarios = []
    for session in sorted(by_session):
        ordered = sorted(by_session[session], key=_span_idx)
        consumed = set()
        for start_i, start_id in enumerate(ordered):
            if start_id in consumed:
                continue
            turns = []
            instances_in_scenario = []
            k = start_i
            while k < len(ordered):
                iid = ordered[k]
                intent = intents_by_id[iid]
                inst = analysis_by_id.get(iid, {})
                consumed.add(iid)
                instances_in_scenario.append(inst)

                user_text = intent.get("user_text") or ""
                turn = {"role": "user", "text": user_text}
                sat = inst.get("satisfaction")
                corrected = bool(sat and sat.get("corrected"))
                if sat is not None:
                    turn["corrected"] = corrected
                    if corrected:
                        if sat.get("corrective_ops"):
                            turn["corrective_ops"] = sat["corrective_ops"]
                        if sat.get("followup_text_head"):
                            turn["followup_text_head"] = sat["followup_text_head"]
                turns.append(turn)

                if not corrected:
                    break  # goal resolved (or no outcomes data) — end scenario here
                k += 1  # fold in the corrective follow-up turn and re-check it too

            if not turns or not turns[0]["text"].strip():
                continue  # not calibration-worthy: no recoverable user text

            first_intent = intents_by_id[start_id]
            span_idx = _span_idx(start_id)
            abandoned = bool(turns[-1].get("corrected", False))

            scenarios.append({
                "schema_version": SCHEMA_VERSION,
                "kind": "conversation",
                "id": "scn-%s-%04d" % (_slug(session), span_idx),
                "source": "mined",
                "provenance": {
                    "corpus": corpus,
                    "session_id": session,
                    "span_idx": span_idx,
                },
                "persona": _persona_for(first_intent.get("tags")),
                "goal": turns[0]["text"],
                "turns": turns,
                "expected_effects": _grounded_effects(instances_in_scenario),
                "abandoned": abandoned,
            })
    return scenarios


def main(argv=None):
    ap = argparse.ArgumentParser(
        description="Compile emit.py --outcomes' intents.json+analysis.json into scenario IR "
                    "documents (kind: conversation). Deterministic; no LLM.")
    ap.add_argument("--intents", required=True, help="intents.json path")
    ap.add_argument("--analysis", required=True, help="analysis.json path")
    ap.add_argument("--corpus", default="claude-code",
                    help="provenance.corpus label stamped on every emitted scenario")
    ap.add_argument("--out-dir", required=True,
                    help="directory to write one <id>.json scenario file per scenario "
                         "(create it under .artifacts/ — unredacted IR must stay local, "
                         "never committed, per the redaction gate in task 2.3)")
    args = ap.parse_args(argv)

    intents_doc = ic.load_json(args.intents)
    analysis_doc = ic.load_json(args.analysis)
    scenarios = compile_scenarios(intents_doc, analysis_doc, args.corpus)

    os.makedirs(args.out_dir, exist_ok=True)
    for scn in scenarios:
        ic.dump_json(scn, os.path.join(args.out_dir, scn["id"] + ".json"))

    print("compiled %d scenario(s) -> %s/<id>.json" % (len(scenarios), args.out_dir),
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
