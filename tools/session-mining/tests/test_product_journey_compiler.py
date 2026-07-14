#!/usr/bin/env python3
"""Unit tests for product_journey_compiler.py (task 3.3), over SYNTHETIC
scenario IR documents (no LLM, no real corpus, no dependence on the real
tools/product-journey/*.json files). Exercises: mcp-tool extraction, persona
clustering + dedup-against-existing, scenario-entry projection, merge
idempotence, and never-mutates-existing-entries.

Run:  python3 tools/session-mining/tests/test_product_journey_compiler.py
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

import product_journey_compiler as pjc

FAILURES = []


def check(name, got, want):
    if got != want:
        FAILURES.append("%s: got %r, want %r" % (name, got, want))


def _scenario(id_, persona, goal, **over):
    base = {
        "schema_version": "1.0",
        "kind": "conversation",
        "id": id_,
        "source": "mined",
        "provenance": {"corpus": "claude-code", "session_id": id_, "span_idx": 0},
        "persona": persona,
        "goal": goal,
        "turns": [{"role": "user", "text": goal, "corrected": False}],
        "expected_effects": [],
        "abandoned": False,
    }
    base.update(over)
    return base


def test_extract_mcp_tools():
    effects = [
        "mcp__kitsoki__story_graph: {} completed",
        "mcp__kitsoki__story_read: {} completed",
        "Bash: git status completed",
        "mcp__kitsoki__story_graph: {} completed",  # duplicate
    ]
    tools = pjc.extract_mcp_tools(effects)
    check("dedups and sorts mcp tool names", tools, ["mcp__kitsoki__story_graph", "mcp__kitsoki__story_read"])


def test_persona_label():
    check("hyphenated persona id -> Title Case label", pjc.persona_label("bugfix-contributor"), "Bugfix Contributor")


def test_compile_personas_dedup_against_existing():
    scenarios = [
        _scenario("scn-a-0000", "explorer", "goal a"),
        _scenario("scn-b-0000", "explorer", "goal b"),
        _scenario("scn-c-0000", "core-maintainer", "goal c"),  # collides with hand-authored
    ]
    new_personas = pjc.compile_personas(scenarios, existing_ids={"core-maintainer", "dependency-debugger"})
    check("only the non-colliding persona is emitted", [p["id"] for p in new_personas], ["explorer"])
    check("emitted persona is tagged source: mined", new_personas[0]["source"], "mined")
    check("emitted persona's sample_count reflects cluster size", new_personas[0]["sample_count"], 2)


def test_compile_scenario_entries():
    scenarios = [
        _scenario(
            "scn-x-0000",
            "bugfix-contributor",
            "fix the flaky test",
            turns=[
                {"role": "user", "text": "fix the flaky test", "corrected": True, "corrective_ops": ["git stash"]},
                {"role": "user", "text": "ok try again", "corrected": False},
            ],
            expected_effects=["mcp__kitsoki__story_graph: {} completed"],
        ),
        _scenario("scn-y-0000", "explorer", "look around", abandoned=True),
    ]
    entries = pjc.compile_scenario_entries(scenarios, existing_ids=set())
    check("one entry per scenario", len(entries), 2)
    by_id = {e["id"]: e for e in entries}
    check("derived id is mined-<scenario id>", "mined-scn-x-0000" in by_id, True)
    check("task carries the verbatim goal", by_id["mined-scn-x-0000"]["task"], "fix the flaky test")
    check(
        "required_mcp reflects grounded expected_effects",
        by_id["mined-scn-x-0000"]["required_mcp"],
        ["mcp__kitsoki__story_graph"],
    )
    check("stage is the mined sentinel (not a real project stage)", by_id["mined-scn-x-0000"]["stage"], "mined_from_sessions")
    check(
        "correction is reflected in success_criteria",
        any("git stash" in c for c in by_id["mined-scn-x-0000"]["success_criteria"]),
        True,
    )
    check(
        "abandoned scenario's success_criteria warns against silent success",
        any("unresolved" in c for c in by_id["mined-scn-y-0000"]["success_criteria"]),
        True,
    )
    check("every entry is tagged source: mined", all(e["source"] == "mined" for e in entries), True)


def test_compile_scenario_entries_skips_existing():
    scenarios = [_scenario("scn-x-0000", "explorer", "goal")]
    entries = pjc.compile_scenario_entries(scenarios, existing_ids={"mined-scn-x-0000"})
    check("already-present derived id is skipped", entries, [])


def test_merge_into_appends_and_is_idempotent():
    scenarios = [_scenario("scn-a-0000", "explorer", "goal a")]
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, "scenarios.json")
        original = {
            "scenarios": [
                {"id": "hand-authored-1", "label": "Existing", "stage": "discover_product", "task": "t",
                 "primary_story": "s", "required_mcp": [], "evidence": [], "success_criteria": []}
            ]
        }
        with open(path, "w", encoding="utf-8") as f:
            json.dump(original, f)

        new_entries = pjc.compile_scenario_entries(scenarios, existing_ids={"hand-authored-1"})
        appended = pjc.merge_into(path, "scenarios", new_entries)
        check("first merge appends exactly the new entries", appended, 1)

        with open(path, "r", encoding="utf-8") as f:
            after_first = json.load(f)
        check("hand-authored entry is untouched, in original position", after_first["scenarios"][0], original["scenarios"][0])
        check("mined entry was appended after it", after_first["scenarios"][1]["id"], "mined-scn-a-0000")

        # Rerun with the SAME scenario set + the file's OWN ids now current.
        existing_ids_second_run = {e["id"] for e in after_first["scenarios"]}
        new_entries_2 = pjc.compile_scenario_entries(scenarios, existing_ids=existing_ids_second_run)
        appended_2 = pjc.merge_into(path, "scenarios", new_entries_2)
        check("second run with the same input is a no-op", appended_2, 0)

        with open(path, "r", encoding="utf-8") as f:
            after_second = json.load(f)
        check("file is byte-identical (as JSON) after the idempotent rerun", after_second, after_first)


def test_never_mutates_input_scenarios():
    scenarios = [_scenario("scn-a-0000", "explorer", "goal a")]
    before = copy.deepcopy(scenarios)
    pjc.compile_personas(scenarios, existing_ids=set())
    pjc.compile_scenario_entries(scenarios, existing_ids=set())
    check("compiler does not mutate its input scenario list", scenarios, before)


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
