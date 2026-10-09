#!/usr/bin/env python3
"""Unit tests for flow_fixture_compiler.py (task 3.1), over SYNTHETIC scenario
IR documents (no LLM, no real corpus). Exercises: turn/episode count parity,
display_input fidelity, canned-answer derivation (corrective_ops /
expected_effects / generic), determinism, and the kind:conversation guard.

Run:  python3 tools/session-mining/tests/test_flow_fixture_compiler.py
(exits 0 on success, non-zero with a diagnostic on failure)
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import flow_fixture_compiler as ffc

FAILURES = []


def check(name, cond, detail=""):
    if not cond:
        FAILURES.append("%s: %s" % (name, detail))


def _scenario(**over):
    base = {
        "schema_version": "1.0",
        "kind": "conversation",
        "id": "scn-synthetic-0000",
        "source": "mined",
        "provenance": {"corpus": "claude-code", "session_id": "synthetic", "span_idx": 0},
        "persona": "explorer",
        "goal": "do the thing",
        "turns": [{"role": "user", "text": "do the thing", "corrected": False}],
        "expected_effects": [],
        "abandoned": False,
    }
    base.update(over)
    return base


def test_single_turn_no_correction():
    scenario = _scenario()
    flow_yaml, cassette_yaml = ffc.compile_scenario(scenario)
    flow_text = flow_yaml.decode("utf-8")
    cas_text = cassette_yaml.decode("utf-8")
    check("single_turn: one 'ask' turn emitted", flow_text.count("name: ask") == 1, flow_text)
    check("single_turn: display_input carries verbatim text", '"do the thing"' in flow_text)
    check("single_turn: one cassette episode", cas_text.count("converse_") == 1, cas_text)
    check("single_turn: generic canned answer (no correction, no effects)", "acknowledged" in cas_text and "expected effects" not in cas_text)


def test_correction_turn_and_final_effects():
    scenario = _scenario(
        turns=[
            {"role": "user", "text": "rebase this onto main", "corrected": True,
             "corrective_ops": ["git rebase --abort"], "followup_text_head": "no wait, stash first"},
            {"role": "user", "text": "ok stash my changes then rebase", "corrected": False},
        ],
        expected_effects=["git.rebase completed", "no uncommitted changes lost"],
    )
    flow_yaml, cassette_yaml = ffc.compile_scenario(scenario)
    flow_text = flow_yaml.decode("utf-8")
    check("two turns -> two 'ask' entries", flow_text.count("name: ask") == 2, flow_text)
    check("two turns -> two cassette episodes", cassette_yaml.decode("utf-8").count("converse_") == 2)
    turn0_answer = ffc.canned_answer(scenario, 0)
    turn1_answer = ffc.canned_answer(scenario, 1)
    check("corrected turn cites its corrective_ops", "git rebase --abort" in turn0_answer, turn0_answer)
    check("final turn cites expected_effects", "git.rebase completed" in turn1_answer, turn1_answer)
    check("corrected turn and final turn answers differ", turn0_answer != turn1_answer)


def test_abandoned_scenario_last_turn():
    scenario = _scenario(
        turns=[{"role": "user", "text": "do the risky thing", "corrected": True,
                "corrective_ops": [], "followup_text_head": "[Request interrupted by user]"}],
        expected_effects=[],
        abandoned=True,
    )
    answer = ffc.canned_answer(scenario, 0)
    # corrected=True but no corrective_ops -> falls through past the
    # correction branch; abandoned+last -> the abandonment branch.
    check("abandoned last turn with no ops -> abandonment phrasing", "unresolved" in answer, answer)


def test_determinism():
    scenario = _scenario()
    a = ffc.compile_scenario(scenario)
    b = ffc.compile_scenario(scenario)
    check("compile_scenario is a pure function (byte-identical reruns)", a == b)


def test_rejects_non_conversation_kind():
    scenario = _scenario(kind="trace")
    try:
        ffc.compile_scenario(scenario)
        check("rejects kind != conversation", False, "expected ValueError, got none")
    except ValueError:
        pass


def test_rejects_empty_turns():
    scenario = _scenario(turns=[])
    try:
        ffc.compile_scenario(scenario)
        check("rejects empty turns", False, "expected ValueError, got none")
    except ValueError:
        pass


def test_yaml_string_escaping_is_safe():
    # A turn containing a double quote and a newline must not break the
    # generated YAML's double-quoted scalar (json.dumps produces valid YAML
    # double-quoted-scalar escaping for both).
    scenario = _scenario(turns=[{"role": "user", "text": 'say "hi"\nplease', "corrected": False}])
    flow_yaml, _ = ffc.compile_scenario(scenario)
    text = flow_yaml.decode("utf-8")
    check("embedded quote/newline round-trips via json.dumps escaping", '\\"hi\\"' in text and "\\n" in text, text)


def _import_yaml():
    try:
        import yaml  # type: ignore
        return yaml
    except ImportError:
        return None


# --- compile_scenario_onto_workbench (S6 "workbench-target") ---------------

def test_workbench_target_unknown_raises():
    scenario = _scenario()
    try:
        ffc.compile_scenario_onto_workbench(scenario, "not-a-real-target")
        check("unknown workbench target raises", False, "expected ValueError, got none")
    except ValueError:
        pass


def test_workbench_target_all_three_registered():
    check(
        "WORKBENCH_TARGETS carries dev-story + pets-dev + slidey-dev",
        set(ffc.WORKBENCH_TARGETS) == {"dev-story", "pets-dev", "slidey-dev"},
        sorted(ffc.WORKBENCH_TARGETS),
    )


def test_workbench_target_dev_story_single_turn():
    scenario = _scenario(expected_effects=["filed the ticket"])
    flow_yaml, cassette_yaml = ffc.compile_scenario_onto_workbench(scenario, "dev-story")
    flow_text = flow_yaml.decode("utf-8")
    cas_text = cassette_yaml.decode("utf-8")
    check("dev-story: drives the real landing_capture intent, not 'ask'",
          "name: landing_capture" in flow_text, flow_text)
    check("dev-story: initial_state is the real room", "initial_state: landing" in flow_text, flow_text)
    check("dev-story: single turn -> one world_override (last turn)",
          flow_text.count("world_override:") == 1, flow_text)
    check("dev-story: world_override seeds the real expected_effects world var",
          "landing_expected_effects" in flow_text and "filed the ticket" in flow_text, flow_text)
    check("dev-story: cassette stubs host.agent.task, not host.agent.converse",
          "handler: host.agent.task" in cas_text and "host.agent.converse" not in cas_text, cas_text)
    check("dev-story: cassette app_id matches the real story",
          "app_id: dev-story" in cas_text, cas_text)


def test_workbench_target_pets_dev_and_slidey_dev_use_folded_names():
    scenario = _scenario(expected_effects=["shipped the feature"])
    for target in ("pets-dev", "slidey-dev"):
        flow_yaml, cassette_yaml = ffc.compile_scenario_onto_workbench(scenario, target)
        flow_text = flow_yaml.decode("utf-8")
        check("%s: drives the import-folded capture intent name" % target,
              "name: core__landing_capture" in flow_text, flow_text)
        check("%s: initial_state is the nested compound path" % target,
              "initial_state: core.landing" in flow_text, flow_text)
        check("%s: world_override seeds the folded (core__-prefixed) world var" % target,
              "core__landing_expected_effects" in flow_text, flow_text)
        check("%s: expect_world asserts the folded capture-slot key" % target,
              "core__landing_request" in flow_text, flow_text)


def test_workbench_target_only_last_turn_carries_world_override():
    scenario = _scenario(
        turns=[
            {"role": "user", "text": "start the migration", "corrected": False},
            {"role": "user", "text": "now finish it up", "corrected": False},
        ],
        expected_effects=["migration completed"],
    )
    flow_yaml, _ = ffc.compile_scenario_onto_workbench(scenario, "dev-story")
    flow_text = flow_yaml.decode("utf-8")
    check("two turns -> exactly one world_override (on the LAST turn only)",
          flow_text.count("world_override:") == 1, flow_text)
    # The override line must appear after the second turn's intent line, not the first's.
    first_intent_idx = flow_text.index('"start the migration"')
    second_intent_idx = flow_text.index('"now finish it up"')
    override_idx = flow_text.index("world_override:")
    check("world_override sits after the final turn's intent, not the first's",
          second_intent_idx < override_idx < len(flow_text) and override_idx > first_intent_idx,
          flow_text)


def test_workbench_target_non_abandoned_note_states_expected_effects():
    scenario = _scenario(expected_effects=["ran the migration", "verified output"], abandoned=False)
    _, cassette_yaml = ffc.compile_scenario_onto_workbench(scenario, "dev-story")
    cas_text = cassette_yaml.decode("utf-8")
    check("non-abandoned scenario's stub note states its expected_effects (join should pass)",
          "ran the migration" in cas_text and "verified output" in cas_text, cas_text)


def test_workbench_target_abandoned_note_omits_expected_effects():
    scenario = _scenario(
        turns=[{"role": "user", "text": "start the risky migration", "corrected": True,
                "corrective_ops": [], "followup_text_head": "[Request interrupted by user]"}],
        expected_effects=["ran the migration", "verified output"],
        abandoned=True,
    )
    _, cassette_yaml = ffc.compile_scenario_onto_workbench(scenario, "dev-story")
    cas_text = cassette_yaml.decode("utf-8")
    check("abandoned scenario's stub note honestly omits its expected_effects (join should fail)",
          "ran the migration" not in cas_text and "verified output" not in cas_text, cas_text)


def test_workbench_target_determinism():
    scenario = _scenario(expected_effects=["did the thing"])
    a = ffc.compile_scenario_onto_workbench(scenario, "dev-story")
    b = ffc.compile_scenario_onto_workbench(scenario, "dev-story")
    check("compile_scenario_onto_workbench is a pure function (byte-identical reruns)", a == b)


def test_workbench_target_rejects_empty_turns():
    scenario = _scenario(turns=[])
    try:
        ffc.compile_scenario_onto_workbench(scenario, "dev-story")
        check("workbench target rejects empty turns", False, "expected ValueError, got none")
    except ValueError:
        pass


def test_workbench_target_flow_yaml_is_well_formed():
    yaml = _import_yaml()
    if yaml is None:
        return  # pyyaml not installed in this environment; skip the parse check
    scenario = _scenario(expected_effects=["did the thing"])
    for target in ffc.WORKBENCH_TARGETS:
        flow_yaml, cassette_yaml = ffc.compile_scenario_onto_workbench(scenario, target)
        parsed_flow = yaml.safe_load(flow_yaml.decode("utf-8"))
        parsed_cas = yaml.safe_load(cassette_yaml.decode("utf-8"))
        check("%s: flow YAML parses" % target, isinstance(parsed_flow, dict) and "turns" in parsed_flow)
        check("%s: cassette YAML parses" % target, isinstance(parsed_cas, dict) and "episodes" in parsed_cas)
        check("%s: exactly one turn compiled" % target, len(parsed_flow["turns"]) == 1)
        turn = parsed_flow["turns"][0]
        check("%s: world_override present on the (only, last) turn" % target, "world_override" in turn)
        check(
            "%s: world_override value is the scenario's own expected_effects list" % target,
            turn["world_override"].get(ffc.WORKBENCH_TARGETS[target]["expected_effects_key"]) == ["did the thing"],
            turn,
        )


def main():
    tests = [v for k, v in sorted(globals().items()) if k.startswith("test_") and callable(v)]
    for t in tests:
        t()
    if FAILURES:
        print("FAILED (%d):" % len(FAILURES))
        for f in FAILURES:
            print(" -", f)
        return 1
    print("OK: %d test functions passed" % len(tests))
    return 0


if __name__ == "__main__":
    sys.exit(main())
