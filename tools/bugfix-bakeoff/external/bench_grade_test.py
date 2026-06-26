#!/usr/bin/env python3
"""Free, no-LLM regression tests for the deterministic grade + the cell→deck seam.

Run: python3 bench_grade_test.py   (exit 0 = pass). Guards two dogfood finds:
  1. decide_quality: a suite-DISABLED project (kitsoki/gears-rust) reaches `solved`
     on the oracle alone — otherwise the escalation ladder never stops.
  2. aggregate.py consumes a bench.py-format cell (None metrics / unmeasured
     compliance) without KeyError.
"""
import importlib.util
import io
import os
import sys
import tempfile
from contextlib import redirect_stdout
from pathlib import Path

HERE = os.path.dirname(os.path.abspath(__file__))


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


bench = _load("bench", os.path.join(HERE, "bench.py"))
aggregate = _load("aggregate", os.path.join(HERE, "..", "aggregate.py"))


def test_decide_quality():
    dq = bench.decide_quality
    # suite DISABLED: oracle alone decides solved (the escalation-ladder fix).
    assert dq(True, None, False) == "solved", "suite-disabled + oracle GREEN must be solved"
    assert dq(False, None, False) == "failed"
    # suite ENABLED: oracle GREEN but suite RED ⇒ partial; both GREEN ⇒ solved.
    assert dq(True, False, True) == "partial"
    assert dq(True, True, True) == "solved"
    assert dq(False, True, True) == "failed"


def test_aggregate_tolerates_bench_cell():
    # A bench.py-shaped cell: None metrics, unmeasured compliance.
    cell = {
        "project": "kitsoki", "bug": "bug9", "candidate": "glm-5.2", "treatment": "kitsoki",
        "model": "GLM-5.2", "effort": "medium", "provider": "synthetic.new",
        "outcome": {"quality": "solved"},
        "compliance": {"rate": None},
        "metrics": {"cost_usd": 0.6, "total_tokens": 2900, "wall_time_s": None,
                    "guidance_turns": 0, "agent_calls": 2},
    }
    manifest = {"project": {"id": "kitsoki"}, "bugs": [], "candidates": [], "treatments": ["kitsoki"]}
    summary = aggregate.build_summary(manifest, [cell], "2026-06-26T00:00:00Z")
    bucket = summary["rollup"]["by_candidate"]["glm-5.2"]
    assert bucket["solved"] == 1 and bucket["solve_rate"] == 1.0
    assert bucket["avg_cost_usd"] == 0.6
    # also the agenteval report path (the deck source) must not KeyError
    aggregate.build_agenteval_reports(manifest, [cell], "2026-06-26T00:00:00Z")


def test_preflight_candidate_profile_and_local_repo_checks():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        candidates = root / "candidates.yaml"
        candidates.write_text(
            "candidates:\n"
            "  - key: cheap\n"
            "    profile: missing-profile\n"
            "    model: test-model\n"
            "    effort: low\n"
        )
        manifest_dir = root / "projects" / "demo"
        oracle_dir = manifest_dir / "oracles"
        oracle_dir.mkdir(parents=True)
        oracle = oracle_dir / "bug1.test"
        oracle.write_text("oracle")
        manifest = {
            "project": {"id": "demo", "local_only": True, "repo": "local"},
            "bugs": [{
                "id": "bug1",
                "baseline_sha": "abc123",
                "fix_sha": "def456",
                "oracle_test": "oracles/bug1.test",
            }],
            "_dir": manifest_dir,
        }
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            out = io.StringIO()
            with redirect_stdout(out):
                rc = bench.preflight(manifest, candidate="cheap", candidates_path=str(candidates))
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 1
        text = out.getvalue()
        assert "missing-profile" in text
        assert "DEMO_REPO" in text


def test_preflight_scopes_to_selected_bugs():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        candidates = root / "candidates.yaml"
        candidates.write_text(
            "candidates:\n"
            "  - key: ready\n"
            "    profile: ready-profile\n"
        )
        (root / ".kitsoki.yaml").write_text(
            "harness_profiles:\n"
            "  ready-profile:\n"
            "    backend: replay\n"
        )
        manifest_dir = root / "projects" / "demo"
        oracle_dir = manifest_dir / "oracles"
        oracle_dir.mkdir(parents=True)
        (oracle_dir / "bug1.test").write_text("oracle")
        manifest = {
            "project": {"id": "demo"},
            "bugs": [
                {"id": "bug1", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/bug1.test"},
                {"id": "bug2", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/missing.test"},
            ],
            "_dir": manifest_dir,
        }
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            out = io.StringIO()
            with redirect_stdout(out):
                rc = bench.preflight(manifest, candidate="ready", candidates_path=str(candidates), bug_ids="bug1")
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 0
        text = out.getvalue()
        assert '"bugs": [\n    "bug1"\n  ]' in text
        assert "missing.test" not in text


def main():
    fails = 0
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            try:
                fn()
                print(f"PASS {name}")
            except AssertionError as e:
                fails += 1
                print(f"FAIL {name}: {e}")
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
