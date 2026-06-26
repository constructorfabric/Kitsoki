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
import json
import os
import shutil
import subprocess
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


def test_summarize_empty_results_fails_loudly_by_default():
    with tempfile.TemporaryDirectory() as td:
        manifest = {"project": {"id": "demo"}, "bugs": [], "_dir": Path(td)}
        rel = os.path.relpath(Path(td) / "empty-results", bench.HERE)
        out = io.StringIO()
        with redirect_stdout(out):
            rc = bench.summarize(manifest, rel)
        assert rc == 1
        text = out.getvalue()
        assert '"error": "no scored cells"' in text
        assert "drive_cell.sh --score" in text


def test_drive_plan_renders_exact_matrix_commands():
    manifest = {
        "project": {"id": "demo"},
        "bugs": [
            {"id": "bug1", "baseline_sha": "abc", "oracle_test": "oracles/bug1"},
            {"id": "bug2", "baseline_sha": "def", "oracle_test": "oracles/bug2"},
        ],
    }
    out = io.StringIO()
    with redirect_stdout(out):
        rc = bench.drive_plan(manifest, bug_ids="bug1,bug2", candidate="cheap,strong", repo_dir="/tmp/demo")
    assert rc == 0
    text = out.getvalue()
    assert "--bug bug1 --candidate cheap --repo-dir /tmp/demo --score" in text
    assert "--bug bug2 --candidate strong --repo-dir /tmp/demo --score" in text
    assert '"commands": [' in text


def test_pending_cell_rolls_up_separately_from_failures():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        manifest = {
            "project": {"id": "demo"},
            "bugs": [{"id": "bug1", "baseline_sha": "abc", "oracle_test": "oracles/bug1"}],
            "_dir": root,
        }
        results = root / "results"
        out_cell = results / "cells" / "demo-bug1-cheap-kitsoki.json"
        out = io.StringIO()
        with redirect_stdout(out):
            rc = bench.pending_cell(manifest, "bug1", "cheap", "provider rate limited", str(out_cell))
        assert rc == 0
        assert out_cell.exists()
        summary_out = io.StringIO()
        rel = os.path.relpath(results, bench.HERE)
        markdown = root / "report.md"
        with redirect_stdout(summary_out):
            rc = bench.summarize(manifest, rel, markdown=str(markdown))
        assert rc == 0
        summary = json_load(summary_out.getvalue())
        bucket = summary["rollup"]["by_candidate"]["cheap"]
        assert bucket["pending"] == 1
        assert bucket["failed"] == 0
        assert bucket["attempted"] == 0
        assert bucket["solve_rate"] == 0.0
        text = markdown.read_text()
        assert "demo bake-off: 0/0 attempted solved; 1 pending" in text
        assert "| cheap | 1 | 0 | 0 | 0 | 1 | 0% |" in text
        assert "| bug1 | cheap | pending | pending |" in text


def test_readiness_reports_missing_and_scored_cells():
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
        (oracle_dir / "bug2.test").write_text("oracle")
        manifest = {
            "project": {"id": "demo"},
            "bugs": [
                {"id": "bug1", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/bug1.test"},
                {"id": "bug2", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/bug2.test"},
            ],
            "_dir": manifest_dir,
        }
        results = root / "results"
        out_cell = results / "cells" / "demo-bug1-ready-kitsoki.json"
        with redirect_stdout(io.StringIO()):
            bench.pending_cell(manifest, "bug1", "ready", "provider blocked", str(out_cell))
        prepared = root / "prepared" / "demo-bug2-ready.json"
        prepared.parent.mkdir(parents=True)
        (root / "cells" / "demo-bug2-ready").mkdir(parents=True)
        (root / "prompts").mkdir()
        (root / "prompts" / "demo-bug2-ready.md").write_text("prompt")
        (root / "preflight").mkdir()
        (root / "preflight" / "demo-bug2-ready.json").write_text("{}")
        prepared.write_text(json.dumps({
            "project": "demo",
            "bug": "bug2",
            "candidate": "ready",
            "worktree": str(root / "cells" / "demo-bug2-ready"),
            "prompt": str(root / "prompts" / "demo-bug2-ready.md"),
            "preflight": str(root / "preflight" / "demo-bug2-ready.json"),
            "trace": str(root / "traces" / "demo-bug2-ready.jsonl"),
        }))
        stale = root / "prepared" / "demo-bug1-ready.json"
        stale.write_text(json.dumps({
            "project": "demo",
            "bug": "bug1",
            "candidate": "ready",
            "worktree": str(root / "cells" / "missing"),
            "prompt": str(root / "prompts" / "missing.md"),
            "preflight": str(root / "preflight" / "missing.json"),
        }))
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            out = io.StringIO()
            markdown = root / "ready.md"
            rel = os.path.relpath(results, bench.HERE)
            with redirect_stdout(out):
                rc = bench.readiness(
                    manifest,
                    candidate="ready",
                    candidates_path=str(candidates),
                    bug_ids="bug1,bug2",
                    results_dir=rel,
                    markdown=str(markdown),
                    armed=True,
                )
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 0
        report = json_load(out.getvalue())
        assert report["results"]["selected_cells"] == 2
        assert report["results"]["scored_cells"] == 1
        assert report["results"]["missing_cells"] == 1
        assert report["results"]["prepared_cells"] == 1
        assert report["results"]["stale_prepared_cells"] == 1
        assert report["results"]["unprepared_cells"] == 1
        assert report["arming"]["verified"] is True
        assert report["prepared"]["cells"][0]["bug"] == "bug2"
        assert report["unprepared"][0]["bug"] == "bug1"
        assert "--no-drive" in report["unprepared"][0]["command"]
        assert "--no-drive" in report["prepared"]["stale"][0]["command"]
        assert "/../" not in report["prepared"]["cells"][0]["_path"]
        assert report["missing"][0]["bug"] == "bug2"
        assert "bench.py pending" in report["missing"][0]["pending_command"]
        text = markdown.read_text()
        assert "Preflight: ready" in text
        assert "Arming: verified" in text
        assert "Prepared cells: 1" in text
        assert "Stale prepared cells: 1" in text
        assert "Unprepared cells: 1" in text
        assert "## What This Proves" in text
        assert "verified RED at the historical baseline and GREEN at the real fix" in text
        assert "not attempted or not recorded, not failed" in text
        assert "## Prepared Cells" in text
        assert "metadata `" in text
        assert "demo-bug2-ready.json" in text
        assert "## Stale Prepared Metadata" in text
        assert "demo-bug1-ready.json" in text
        assert "refresh with `" in text
        assert "## Unprepared Cells" in text
        assert "--no-drive" in text
        assert "`bug2` x `ready`" in text
        assert "## Pending Alternatives" in text
        assert "--reason \"<reason>\"" in text


def test_readiness_markdown_uses_singular_missing_cell_wording():
    report = {
        "project": "demo",
        "preflight": {"ok": True, "errors": [], "warnings": []},
        "drive_plan": {"markdown": "- `bug1` x `ready`: `drive`"},
        "results": {
            "selected_cells": 1,
            "scored_cells": 0,
            "missing_cells": 1,
            "prepared_cells": 0,
            "stale_prepared_cells": 0,
            "unprepared_cells": 1,
        },
        "arming": {"verified": True},
        "missing": [{
            "bug": "bug1",
            "candidate": "ready",
            "command": "drive",
            "pending_command": "pending",
        }],
        "prepared": {"cells": []},
        "unprepared": [{
            "bug": "bug1",
            "candidate": "ready",
            "command": "drive --no-drive",
        }],
        "next_actions": ["drive it"],
    }
    with tempfile.TemporaryDirectory() as td:
        markdown = Path(td) / "ready.md"
        bench.write_readiness_markdown(report, markdown)
        text = markdown.read_text()
        assert "1 of 1 selected cell has no result artifact yet" in text
        assert "1 of 1 selected cells have no result artifacts yet" not in text


def test_drive_cell_preflight_scopes_to_requested_bug():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        repo = root / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
        (repo / "lib.txt").write_text("baseline\n")
        subprocess.run(["git", "add", "lib.txt"], cwd=repo, check=True)
        subprocess.run(
            ["git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "baseline"],
            cwd=repo,
            check=True,
        )
        baseline = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()

        project = "drive-scope-demo"
        manifest_dir = Path(HERE) / "projects" / project
        cache = root / "cache"
        try:
            (manifest_dir / "oracles").mkdir(parents=True)
            (manifest_dir / "oracles" / "bug1.test").write_text("oracle")
            (manifest_dir / "manifest.yaml").write_text(
                "project:\n"
                f"  id: {project}\n"
                "  repo: local\n"
                "  local_only: true\n"
                "bugs:\n"
                "  - id: bug1\n"
                f"    baseline_sha: {baseline}\n"
                f"    fix_sha: {baseline}\n"
                "    oracle_test: oracles/bug1.test\n"
                "  - id: bug2\n"
                f"    baseline_sha: {baseline}\n"
                f"    fix_sha: {baseline}\n"
                "    oracle_test: oracles/missing.test\n"
            )
            env = {**os.environ, "EXTERNAL_BAKEOFF_CACHE": str(cache)}
            r = subprocess.run(
                [
                    str(Path(HERE) / "drive_cell.sh"),
                    "--project", project,
                    "--bug", "bug1",
                    "--candidate", "opus-4.8",
                    "--repo-dir", str(repo),
                    "--no-drive",
                ],
                cwd=Path(HERE).parents[2],
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            assert r.returncode == 0, r.stdout + r.stderr
            preflight = json_load((cache / "preflight" / f"{project}-bug1-opus-4.8.json").read_text())
            assert preflight["bugs"] == ["bug1"]
            assert preflight["errors"] == []
            prepared = json_load((cache / "prepared" / f"{project}-bug1-opus-4.8.json").read_text())
            assert prepared["project"] == project
            assert prepared["bug"] == "bug1"
            assert prepared["candidate"] == "opus-4.8"
            assert Path(prepared["worktree"]).exists()
            assert Path(prepared["prompt"]).exists()
            assert prepared["preflight"].endswith(f"{project}-bug1-opus-4.8.json")
            old_here = bench.HERE
            bench.HERE = Path(HERE)
            try:
                out = io.StringIO()
                rel = os.path.relpath(cache / "results", bench.HERE)
                with redirect_stdout(out):
                    rc = bench.audit_handoffs(
                        bench.load(project),
                        results_dir=rel,
                        candidate="opus-4.8",
                        bug_ids="bug1",
                    )
            finally:
                bench.HERE = old_here
            assert rc == 0
            audit = json_load(out.getvalue())
            assert audit["ok"] is True
            assert audit["prepared_cells"] == 1
        finally:
            shutil.rmtree(manifest_dir, ignore_errors=True)


def test_audit_handoffs_rejects_oracle_and_real_fix_leaks():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        manifest_dir = root / "projects" / "demo"
        oracle_dir = manifest_dir / "oracles"
        oracle_dir.mkdir(parents=True)
        oracle_line = "assert_eq!(loaded_config.modules.len(), 1, \"duplicate module was rejected\");"
        (oracle_dir / "bug1.rs").write_text(f"#[test]\nfn oracle() {{\n    {oracle_line}\n}}\n")
        worktree = root / "cells" / "demo-bug1-ready"
        worktree.mkdir(parents=True)
        preflight = root / "preflight" / "demo-bug1-ready.json"
        preflight.parent.mkdir()
        preflight.write_text("{}")
        prompt = root / "prompts" / "demo-bug1-ready.md"
        prompt.parent.mkdir()
        prompt.write_text(
            "Drive ONE kitsoki bug-fix pipeline cell\n"
            'profile: "ready-profile"\n'
            'ticket_id: "bug1"\n'
            'ticket_title: "Leaky bug — reproduce it"\n'
            'workdir: "' + str(worktree) + '"\n'
            'workspace_id: ""\n'
            "Do not use shell\n"
            "Hidden hints: oracles/bug1.rs abcdef12 src/fix.rs cargo test oracle_bug1\n"
            f"{oracle_line}\n"
        )
        prepared_dir = root / "prepared"
        prepared_dir.mkdir()
        (prepared_dir / "demo-bug1-ready.json").write_text(json.dumps({
            "project": "demo",
            "bug": "bug1",
            "candidate": "ready",
            "profile": "ready-profile",
            "worktree": str(worktree),
            "branch": "bench-demo-bug1",
            "baseline_sha": "1234567",
            "trace": str(root / "traces" / "demo-bug1-ready.jsonl"),
            "thread": str(root / "threads" / "demo-bug1-ready.md"),
            "prompt": str(prompt),
            "preflight": str(preflight),
            "score_result": str(root / "results" / "cells" / "demo-bug1-ready-kitsoki.json"),
        }))
        manifest = {
            "project": {"id": "demo"},
            "bugs": [{
                "id": "bug1",
                "title": "Leaky bug",
                "ticket": "reproduce it",
                "baseline_sha": "1234567",
                "fix_sha": "abcdef12",
                "fix_source": "src/fix.rs",
                "oracle_test": "oracles/bug1.rs",
                "oracle": {"target": "tests/oracle_bug1.rs", "run": "cargo test oracle_bug1"},
            }],
            "_dir": manifest_dir,
        }
        out = io.StringIO()
        rel = os.path.relpath(root / "results", bench.HERE)
        with redirect_stdout(out):
            rc = bench.audit_handoffs(manifest, results_dir=rel, candidate="ready", bug_ids="bug1")
        assert rc == 1
        report = json_load(out.getvalue())
        assert report["ok"] is False
        joined = "\n".join(report["errors"])
        assert "prompt leaks hidden oracle_test" in joined
        assert "prompt leaks hidden fix_sha" in joined
        assert "prompt leaks hidden fix_source" in joined
        assert "prompt leaks hidden oracle.run" in joined
        assert "prompt leaks hidden oracle content" in joined


def json_load(raw):
    import json
    return json.loads(raw)


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
