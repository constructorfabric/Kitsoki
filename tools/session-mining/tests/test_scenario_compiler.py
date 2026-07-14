#!/usr/bin/env python3
"""Unit tests for scenario_compiler.py (tasks 2.1-2.2), over SYNTHETIC mining
output (no LLM, no real corpus) — exercises the goal-bounded grouping algorithm,
correction folding, expected_effects derivation, persona heuristic, abandonment,
and the compiler's determinism (pure function of its inputs).

Run:  python3 tools/session-mining/tests/test_scenario_compiler.py
(exits 0 on success, non-zero with a diagnostic on failure)
"""
import copy
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import scenario_compiler as sc


def _intent(session, idx, user_text, tags=None):
    return {
        "instance_id": "%s#%d" % (session, idx),
        "user_text": user_text,
        "session": session,
        "span": [idx * 2 + 1, idx * 2 + 2],
        "tags": tags or {},
        "analysis_ref": "analysis.json#%s#%d" % (session, idx),
    }


def _instance(session, idx, actions=None, satisfaction=None, tags=None):
    inst = {
        "instance_id": "%s#%d" % (session, idx),
        "tags": tags or {},
        "determinism": "deterministic",
        "actions": actions or [],
        "measured": {"tool_calls": len(actions or []), "edit_rerun_cycles": 0, "retries": 0},
        "grounding": {"actions_cited": len(actions or []), "actions_validated": len(actions or [])},
    }
    if satisfaction is not None:
        inst["satisfaction"] = satisfaction
    return inst


def build_fixture():
    """A hand-built, synthetic (never-real) mining job spanning two sessions:

    sess-git:
      #0 "rebase this onto main"           corrected=true  (folds in #1)
      #1 "ok stash my changes then rebase" corrected=false (scenario resolves)
      #2 "write docs for foo"              no satisfaction key at all (single turn)
      #3 "fix the flaky test"              corrected=true, session ends -> abandoned

    sess-two:
      #0 ""  (empty user_text -> not calibration-worthy, must be skipped silently)
      #1 "look into the weird crash"       tags with an unmapped action -> default persona
    """
    intents = [
        _intent("sess-git", 0, "rebase this onto main",
                {"action": ["rebase-or-resolve-conflicts"]}),
        _intent("sess-git", 1, "ok stash my changes then rebase",
                {"action": ["rebase-or-resolve-conflicts"]}),
        _intent("sess-git", 2, "write docs for foo", {"action": ["write-docs"]}),
        _intent("sess-git", 3, "fix the flaky test", {"action": ["fix-failing-tests"]}),
        _intent("sess-two", 0, "", {}),
        _intent("sess-two", 1, "look into the weird crash", {"action": ["some-unmapped-action"]}),
    ]
    instances = [
        _instance("sess-git", 0,
                  actions=[{"tool": "Bash", "signature": "git rebase --abort",
                            "parameters": {}, "cite": {"line": 1}, "grounded": True}],
                  satisfaction={"followup_text_head": "no wait, stash first",
                                "corrected": True, "corrective_ops": ["git rebase --abort"]}),
        _instance("sess-git", 1,
                  actions=[{"tool": "Bash", "signature": "git rebase main",
                            "parameters": {}, "cite": {"line": 3}, "grounded": True},
                           {"tool": "Bash", "signature": "git rebase main",
                            "parameters": {}, "cite": {"line": 4}, "grounded": True}],
                  satisfaction={"followup_text_head": "", "corrected": False, "corrective_ops": []}),
        _instance("sess-git", 2,
                  actions=[{"tool": "Write", "signature": "Write <file>",
                            "parameters": {}, "cite": {"line": 5}, "grounded": False}]),
        _instance("sess-git", 3,
                  actions=[{"tool": "Bash", "signature": "go test ./...",
                            "parameters": {}, "cite": {"line": 7}, "grounded": True}],
                  satisfaction={"followup_text_head": "hmm still failing",
                                "corrected": True, "corrective_ops": []}),
        _instance("sess-two", 0, actions=[]),
        _instance("sess-two", 1,
                  actions=[{"tool": "Bash", "signature": "go run ./cmd/debug",
                            "parameters": {}, "cite": {"line": 1}, "grounded": True}],
                  satisfaction={"followup_text_head": "", "corrected": False, "corrective_ops": []}),
    ]
    intents_doc = {"schema_version": "1.0", "job": "fixture-job", "total_intents": len(intents),
                   "intents": intents, "tags": {}}
    analysis_doc = {"schema_version": "1.0", "job": "fixture-job", "instances": instances}
    return intents_doc, analysis_doc


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    intents_doc, analysis_doc = build_fixture()
    scenarios = sc.compile_scenarios(intents_doc, analysis_doc, corpus="claude-code")
    by_id = {s["id"]: s for s in scenarios}

    # --- calibration-worthy filtering: the empty-user_text span must be skipped,
    # and must NOT produce a phantom scenario or break the next one's grouping.
    check("scn-sess-two-0000" not in by_id, "empty user_text span should not compile a scenario")
    check(len(scenarios) == 4, "expected 4 scenarios (2 in sess-git-groups + 1 solo + 1 sess-two), got %d: %r"
          % (len(scenarios), sorted(by_id)))

    # --- correction folding: span 0 (corrected) folds in span 1 (resolved) as one scenario
    rebase = by_id.get("scn-sess-git-0000")
    check(rebase is not None, "rebase scenario missing")
    if rebase:
        check(len(rebase["turns"]) == 2, "rebase scenario should have 2 folded turns, got %d" % len(rebase["turns"]))
        check(rebase["turns"][0]["text"] == "rebase this onto main", "turn0 text wrong")
        check(rebase["turns"][0]["corrected"] is True, "turn0 must be corrected")
        check(rebase["turns"][0]["corrective_ops"] == ["git rebase --abort"], "corrective_ops not folded")
        check(rebase["turns"][0]["followup_text_head"] == "no wait, stash first", "followup_text_head not folded")
        check(rebase["turns"][1]["text"] == "ok stash my changes then rebase", "turn1 text wrong")
        check(rebase["turns"][1]["corrected"] is False, "turn1 (resolving turn) must be corrected:false")
        check(rebase["goal"] == "rebase this onto main", "goal must be the first turn's verbatim text")
        check(rebase["persona"] == "core-maintainer", "persona heuristic wrong: %r" % rebase["persona"])
        check(rebase["abandoned"] is False, "resolved scenario must not be abandoned")
        check(rebase["source"] == "mined", "source must be mined")
        check(rebase["provenance"] == {"corpus": "claude-code", "session_id": "sess-git", "span_idx": 0},
              "provenance wrong: %r" % rebase["provenance"])
        # expected_effects: grounded actions across BOTH folded turns, deduped+sorted
        check(rebase["expected_effects"] == ["git rebase --abort completed", "git rebase main completed"],
              "expected_effects wrong: %r" % rebase["expected_effects"])

    # span 1 must NOT also start its own scenario (it was consumed by span 0's fold)
    check("scn-sess-git-0001" not in by_id, "span 1 must be folded into span 0's scenario, not standalone")

    # --- single-turn scenario with no satisfaction key at all (no --outcomes data)
    docs = by_id.get("scn-sess-git-0002")
    check(docs is not None, "docs scenario missing")
    if docs:
        check(len(docs["turns"]) == 1, "docs scenario should be single-turn")
        check("corrected" not in docs["turns"][0], "turn should have no corrected key when satisfaction absent")
        check(docs["expected_effects"] == [], "ungrounded action must yield NO expected_effects")
        check(docs["abandoned"] is False, "single resolved turn is not abandoned")

    # --- abandonment: last turn corrected=true, session runs out -> abandoned
    flaky = by_id.get("scn-sess-git-0003")
    check(flaky is not None, "flaky-test scenario missing")
    if flaky:
        check(len(flaky["turns"]) == 1, "flaky scenario should have exactly 1 turn (nothing left to fold in)")
        check(flaky["turns"][0]["corrected"] is True, "flaky turn must be corrected")
        check("corrective_ops" not in flaky["turns"][0], "empty corrective_ops list must not be emitted")
        check(flaky["abandoned"] is True, "scenario ending on an unresolved correction must be abandoned")

    # --- persona default for an unmapped action tag
    crash = by_id.get("scn-sess-two-0001")
    check(crash is not None, "crash scenario missing")
    if crash:
        check(crash["persona"] == "developer", "unmapped action tag should default to 'developer'")

    # --- determinism: re-running on a deep-copied, key-order-shuffled input yields
    # byte-identical json output (compile_scenarios is a pure function; sorted by
    # instance_id numeric suffix, not input list order)
    shuffled_intents = copy.deepcopy(intents_doc)
    shuffled_intents["intents"] = list(reversed(shuffled_intents["intents"]))
    shuffled_analysis = copy.deepcopy(analysis_doc)
    shuffled_analysis["instances"] = list(reversed(shuffled_analysis["instances"]))
    scenarios_again = sc.compile_scenarios(intents_doc, analysis_doc, corpus="claude-code")
    scenarios_shuffled = sc.compile_scenarios(shuffled_intents, shuffled_analysis, corpus="claude-code")
    check(json.dumps(scenarios, sort_keys=True) == json.dumps(scenarios_again, sort_keys=True),
          "compile_scenarios is not repeatable on identical input")
    check(json.dumps(scenarios, sort_keys=True) == json.dumps(scenarios_shuffled, sort_keys=True),
          "compile_scenarios output depends on input list order (must sort by span_idx)")
    check("timestamp" not in json.dumps(scenarios), "output must carry no timestamps")

    # --- schema conformance (optional strict gate; skip gracefully if jsonschema absent)
    try:
        import jsonschema
        from jsonschema import Draft202012Validator
        schema_path = os.path.join(TOOL, "schema", "scenario_ir.schema.json")
        with open(schema_path) as fh:
            schema = json.load(fh)
        validator = Draft202012Validator(schema)
        for scn in scenarios:
            errs = sorted(validator.iter_errors(scn), key=lambda e: list(e.path))
            check(not errs, "scenario %s failed schema validation: %s" % (
                scn["id"], "; ".join(e.message for e in errs)))
        # the documented example must also validate
        example_path = os.path.join(TOOL, "examples", "scenario-ir.example.json")
        with open(example_path) as fh:
            example = json.load(fh)
        errs = sorted(validator.iter_errors(example), key=lambda e: list(e.path))
        check(not errs, "documented example failed schema validation: %s" % "; ".join(e.message for e in errs))
    except ImportError:
        sys.stderr.write("(jsonschema not installed; skipping strict schema-conformance check)\n")

    # --- CLI main(): writes one file per scenario id under --out-dir
    with tempfile.TemporaryDirectory() as work:
        intents_path = os.path.join(work, "intents.json")
        analysis_path = os.path.join(work, "analysis.json")
        out_dir = os.path.join(work, "ir")
        with open(intents_path, "w") as fh:
            json.dump(intents_doc, fh)
        with open(analysis_path, "w") as fh:
            json.dump(analysis_doc, fh)
        rc = sc.main(["--intents", intents_path, "--analysis", analysis_path,
                      "--corpus", "claude-code", "--out-dir", out_dir])
        check(rc == 0, "main() should return 0")
        written = sorted(os.listdir(out_dir))
        check(written == sorted(s["id"] + ".json" for s in scenarios),
              "CLI did not write the expected scenario files: %r" % written)

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: scenario_compiler (kind:conversation IR, synthetic mining output, no LLM)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
