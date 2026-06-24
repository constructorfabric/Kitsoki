#!/usr/bin/env python3
"""Offline unit tests for score.py — no LLM, no provider, no real toolchain.

The oracle runner and (where needed) the cost extractor are dependency-injected
stubs, so these run with no go/pnpm and no network. The transcript is a small,
clearly-synthetic JSONL fixture under testdata/ carrying realistic message.usage
shapes only — no real content.

Runs two ways (matching the session-mining suites, which are stdlib-only):
    python3 tools/bugfix-bakeoff/score_test.py        # stdlib runner, CI/Makefile
    python3 -m pytest tools/bugfix-bakeoff/score_test.py -v   # when pytest present
The plain `def test_*` + `assert` functions are pytest-discoverable; the bottom
runner invokes them directly when pytest isn't installed.
"""

from __future__ import annotations

import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import score  # noqa: E402

FIXTURE = os.path.join(HERE, "testdata", "fake_session.jsonl")

MANIFEST = {
    "bugs": [{
        "id": "bug1",
        "title": "demo bug",
        "severity": "P2",
        "component": "tui",
        "fix_sha": "deadbeef",
        "baseline_sha": "cafebabe",
        "oracle_test": "internal/foo/repro_test.go",
        "oracle_kind": "go",
        "affected_test_pkgs": ["./internal/foo/..."],
    }],
    "candidates": [{
        "key": "opus-4.8", "profile": "claude-native", "model": "opus",
        "effort": "medium", "provider": "anthropic",
    }],
    "treatments": ["kitsoki", "single"],
}


class StubOracle:
    """Injectable oracle runner with no subprocess work."""

    def __init__(self, oracle="pass", build=True, suite=True):
        self._oracle, self._build, self._suite = oracle, build, suite

    def run_oracle(self, bug, worktree):
        return self._oracle, f"stub oracle={self._oracle}"

    def run_build(self, worktree):
        return self._build

    def run_suite(self, pkgs, worktree, kind):
        return self._suite


def _score(oracle="pass", build=True, suite=True, transcript=FIXTURE,
           treatment="kitsoki", worktree="/nonexistent/wt"):
    return score.score_cell(
        MANIFEST, "bug1", "opus-4.8", treatment, worktree, transcript,
        wall_time_s=412.5, guidance_turns=2,
        oracle_runner=StubOracle(oracle, build, suite),
        # cost_fn left as default so we exercise real extraction off the fixture;
        # compliance git scans hit a nonexistent worktree and degrade gracefully.
        trace_found=True)


def test_cell_has_all_schema_keys():
    cell = _score()
    assert set(cell) >= {
        "bug", "candidate", "treatment", "profile", "model", "effort",
        "provider", "outcome", "compliance", "metrics", "transcript_path",
        "trace_found", "notes"}
    assert set(cell["outcome"]) == {
        "oracle_pass", "oracle_status", "build_pass", "suite_pass", "quality"}
    assert set(cell["compliance"]) == {
        "reproduced_red", "added_regression_test", "suite_green", "in_scope",
        "stage_order", "rate"}
    assert set(cell["metrics"]) == {
        "input_tokens", "output_tokens", "cache_read_tokens",
        "cache_write_tokens", "total_tokens", "cost_usd", "cost_exact",
        "wall_time_s", "guidance_turns"}


def test_quality_solved():
    cell = _score(oracle="pass", build=True, suite=True)
    assert cell["outcome"]["oracle_pass"] is True
    assert cell["outcome"]["quality"] == "solved"


def test_quality_partial_on_regression():
    cell = _score(oracle="pass", build=True, suite=False)
    assert cell["outcome"]["quality"] == "partial"


def test_quality_partial_on_noncompile():
    cell = _score(oracle="noncompile", build=False, suite=False)
    assert cell["outcome"]["quality"] == "partial"
    assert "noncompile" in cell["notes"]


def test_quality_failed_on_oracle_fail():
    cell = _score(oracle="fail")
    assert cell["outcome"]["quality"] == "failed"


def test_quality_failed_on_absent():
    cell = _score(oracle="absent")
    assert cell["outcome"]["quality"] == "failed"


def test_cost_extracted_from_fixture():
    """Real extraction off the synthetic fixture: opus-priced, exact, tokens
    summed across the two assistant messages."""
    cell = _score()
    m = cell["metrics"]
    assert m["input_tokens"] == 1800        # 1000 + 800
    assert m["output_tokens"] == 350        # 200 + 150
    assert m["cache_read_tokens"] == 10000  # 4000 + 6000
    assert m["cache_write_tokens"] == 800   # 500 + 300
    assert m["total_tokens"] == 1800 + 350 + 10000 + 800
    assert m["cost_exact"] is True
    assert m["cost_usd"] > 0
    assert m["wall_time_s"] == 412.5
    assert m["guidance_turns"] == 2


def test_missing_transcript_zeros_metrics():
    cell = _score(transcript="")
    m = cell["metrics"]
    assert m["total_tokens"] == 0
    assert m["cost_usd"] == 0.0
    assert "no transcript" in cell["notes"]


def test_injected_cost_fn_used():
    """cost_fn is a DI seam — a stub fully replaces extraction."""
    def fake_cost(_transcript):
        return dict(input_tokens=11, output_tokens=22, cache_read_tokens=33,
                    cache_write_tokens=44, cost_usd=1.25, cost_exact=False,
                    note="")
    cell = score.score_cell(
        MANIFEST, "bug1", "opus-4.8", "single", "/nonexistent/wt", FIXTURE,
        wall_time_s=1.0, guidance_turns=0,
        oracle_runner=StubOracle(), cost_fn=fake_cost)
    assert cell["metrics"]["cost_usd"] == 1.25
    assert cell["metrics"]["cost_exact"] is False
    assert cell["metrics"]["total_tokens"] == 110


def test_compliance_rate_is_mean_of_five():
    cell = _score()
    comp = cell["compliance"]
    flags = [comp[k] for k in ("reproduced_red", "added_regression_test",
                               "suite_green", "in_scope", "stage_order")]
    assert comp["rate"] == round(sum(1 for f in flags if f) / 5, 4)


def test_transcript_drives_reproduced_red_and_stage_order():
    """The fixture shows a FAIL then 'implement the fix' — both heuristics fire."""
    cell = _score()
    assert cell["compliance"]["reproduced_red"] is True
    assert cell["compliance"]["stage_order"] is True


def test_quality_mapping_unit():
    assert score.map_quality("pass", True, True) == "solved"
    assert score.map_quality("pass", None, True) == "solved"
    assert score.map_quality("pass", False, True) == "partial"
    assert score.map_quality("pass", True, False) == "partial"
    assert score.map_quality("noncompile", None, None) == "partial"
    assert score.map_quality("fail", True, True) == "failed"
    assert score.map_quality("absent", None, None) == "failed"


def test_main_writes_cell_file():
    """End-to-end through main(): a stubbed OracleRunner (monkeypatched without
    pytest), real fixture cost, a written SCHEMA cell file."""
    import yaml
    orig = (score.OracleRunner.run_oracle, score.OracleRunner.run_build,
            score.OracleRunner.run_suite)
    score.OracleRunner.run_oracle = lambda self, bug, wt: ("pass", "stub")
    score.OracleRunner.run_build = lambda self, wt: True
    score.OracleRunner.run_suite = lambda self, pkgs, wt, kind: True
    try:
        with tempfile.TemporaryDirectory() as td:
            manifest_path = os.path.join(td, "bakeoff.yaml")
            with open(manifest_path, "w") as fh:
                yaml.safe_dump(MANIFEST, fh)
            out = os.path.join(td, "cells", "bug1-opus-4.8-kitsoki.json")
            score.main([
                "--manifest", manifest_path, "--bug", "bug1",
                "--candidate", "opus-4.8", "--treatment", "kitsoki",
                "--worktree", os.path.join(td, "wt"), "--transcript", FIXTURE,
                "--wall-time-s", "10", "--guidance-turns", "1", "--out", out])
            with open(out) as fh:
                cell = json.load(fh)
        assert cell["bug"] == "bug1"
        assert cell["outcome"]["quality"] == "solved"
        assert cell["metrics"]["total_tokens"] > 0
    finally:
        (score.OracleRunner.run_oracle, score.OracleRunner.run_build,
         score.OracleRunner.run_suite) = orig


def _run_stdlib():
    """Run every `test_*` in this module with no pytest dependency."""
    tests = [(n, f) for n, f in sorted(globals().items())
             if n.startswith("test_") and callable(f)]
    failures = []
    for name, fn in tests:
        try:
            fn()
            print("  ok  %s" % name)
        except AssertionError as exc:
            failures.append((name, exc))
            print("  FAIL %s: %s" % (name, exc))
        except Exception as exc:  # noqa: BLE001
            failures.append((name, exc))
            print("  ERROR %s: %r" % (name, exc))
    if failures:
        print("FAIL (%d/%d)" % (len(failures), len(tests)))
        return 1
    print("PASS: %d score.py tests (no LLM, no provider, no network)" % len(tests))
    return 0


if __name__ == "__main__":
    sys.exit(_run_stdlib())
