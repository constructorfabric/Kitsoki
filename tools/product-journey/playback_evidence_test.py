#!/usr/bin/env python3
"""Regression test for WS-F F2: playback-capable evidence slots.

Run directly:  python3 tools/product-journey/playback_evidence_test.py

Every natural-use scenario must declare exactly one playback-capable evidence
kind (rrweb | trace-replay | flow-fixture | png-sequence), and that slot only
counts as proof when it is backed by a real LOCAL file — a cassette://,
http(s)://, retained://, or other opaque/indirect URI is never accepted, even
though those same URIs can count as general proof evidence elsewhere. This
guards the contract gap called out in
.context/dev-workflows-surface-matrix-plan.md (WS-F F2): cassette:// URIs are
unbacked/fake proof for REPLAY purposes.
"""

import importlib.util
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def main():
    personas = run.load_personas(run.PERSONAS)
    scenarios = run.load_scenarios(run.SCENARIOS)
    github_targets = run.load_github_targets(run.GITHUB_TARGETS)
    active_scenarios = run.active_scenarios(scenarios)

    # 1. Every active (non-mined) scenario declares exactly one playback kind.
    for scenario in active_scenarios:
        kind = run.scenario_playback_kind(scenario)
        _check(f"{scenario['id']} declares one playback-capable kind", kind in run.PLAYBACK_EVIDENCE_KINDS)

    # 2. scenario_playback_kind is None with zero or multiple declared kinds.
    _check("no playback kind declared -> None",
           run.scenario_playback_kind({"evidence": ["session_trace"]}) is None)
    _check("two playback kinds declared -> None (ambiguous)",
           run.scenario_playback_kind({"evidence": ["rrweb", "trace-replay"]}) is None)
    _check("exactly one playback kind declared -> that kind",
           run.scenario_playback_kind({"evidence": ["session_trace", "flow-fixture"]}) == "flow-fixture")

    # 3. The four workflow personas WS-F wires are present and active.
    required_personas = {"core-maintainer", "dependency-debugger", "docs-minded-contributor", "ide-first-engineer"}
    active_persona_ids = {persona["id"] for persona in run.active_personas(personas)}
    _check("required workflow personas are active", required_personas <= active_persona_ids)

    # 4. Full corpus validation still passes with the new contract wired in.
    result = run.validate_journey_corpus(personas, scenarios, github_targets)
    _check("corpus validation passes with playback contract wired", result["status"] == "valid" and result["errors"] == 0)

    # 5. A scenario dropping its playback kind fails corpus validation.
    broken_scenarios = [dict(scenario) for scenario in scenarios]
    for scenario in broken_scenarios:
        if scenario["id"] == "bugfix":
            scenario["evidence"] = [kind for kind in scenario["evidence"] if kind not in run.PLAYBACK_EVIDENCE_KINDS]
    broken_result = run.validate_journey_corpus(personas, broken_scenarios, github_targets)
    _check("dropping the playback kind fails corpus validation", broken_result["status"] == "invalid")
    _check("failure names the scenario-playback-evidence check",
           any(issue["id"] == "scenario-playback-evidence" for issue in broken_result["issues"]))

    with tempfile.TemporaryDirectory() as tmp:
        rd = Path(tmp)
        (rd / "playback").mkdir()
        real_file = rd / "playback" / "bugfix.trace-replay.jsonl"
        real_file.write_text('{"replay": true}\n', encoding="utf-8")

        # 6. is_playback_evidence rejects cassette:// even when it would
        # resolve as general proof evidence.
        cassette_item = {"kind": "trace-replay", "status": "captured", "path": "cassette://product-journey/run-x/playback/bugfix.trace-replay.jsonl"}
        (rd / "cassettes").mkdir()
        (rd / "cassettes" / "run-x").mkdir()
        _check("cassette:// path is not playback evidence even if it would resolve as proof",
               run.is_playback_evidence(cassette_item, rd) is False)
        _check("...but the same ref DOES count as general proof evidence (contrast)",
               run.is_proof_evidence({**cassette_item, "source": "cassette"}) is True)

        # 7. is_playback_evidence rejects other opaque/remote URIs.
        for bad_path in ["https://example.com/x.rrweb.json", "retained://abc123", "mcp://trace/1"]:
            _check(f"opaque ref rejected as playback evidence: {bad_path}",
                   run.is_playback_evidence({"kind": "rrweb", "status": "captured", "path": bad_path}, rd) is False)

        # 8. A wrong evidence kind is never playback evidence, regardless of path.
        _check("non-playback kind is never playback evidence",
               run.is_playback_evidence({"kind": "session_trace", "status": "captured", "path": str(real_file)}, rd) is False)

        # 9. A genuine local file (relative to run_dir) IS playback evidence.
        local_item = {"kind": "trace-replay", "status": "captured", "path": "playback/bugfix.trace-replay.jsonl"}
        _check("real local relative path is playback evidence", run.is_playback_evidence(local_item, rd) is True)

        # 10. A dangling local path is not playback evidence.
        dangling_item = {"kind": "trace-replay", "status": "captured", "path": "playback/missing.jsonl"}
        _check("dangling local path is not playback evidence", run.is_playback_evidence(dangling_item, rd) is False)

        # 11. missing_playback_evidence reports unbacked and missing slots,
        # and clears once a real local file backs the declared kind.
        run_json = {
            "scenarios": [
                {"id": "bugfix", "source": "natural-use", "evidence": ["session_trace", "trace-replay"]},
                {"id": "mined-scn-x", "source": "mined", "evidence": ["session_trace"]},
            ]
        }
        unbacked_items = [
            {"scenario": "bugfix", "kind": "trace-replay", "status": "captured", "path": "cassette://product-journey/run-x/nothing.jsonl"},
        ]
        missing = run.missing_playback_evidence(run_json, unbacked_items, rd)
        _check("unbacked playback evidence is reported missing", any("bugfix/trace-replay" in row for row in missing))
        _check("mined scenarios are exempt from the playback contract",
               not any(row.startswith("mined-scn-x") for row in missing))

        backed_items = [
            {"scenario": "bugfix", "kind": "trace-replay", "status": "captured", "path": "playback/bugfix.trace-replay.jsonl"},
        ]
        backed = run.missing_playback_evidence(run_json, backed_items, rd)
        _check("a real local file clears the playback gap", backed == [])

        # 12. blocked scenarios are exempt.
        blocked = run.missing_playback_evidence(run_json, [], rd, blocked_scenarios={"bugfix"})
        _check("blocked scenarios are exempt from the playback contract", blocked == [])

    print("\nPASS: playback evidence contract")


if __name__ == "__main__":
    main()
